package fw

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"strings"
	"sync"
	"time"
)

// Event 事件回调载荷（WS events topic / 审计联动）。
type Event struct {
	Type      string    `json:"type"` // apply_ok|apply_fail|drift|danger_pending|danger_applied|rollback
	Token     string    `json:"token,omitempty"`
	Message   string    `json:"message"`
	Detail    string    `json:"detail,omitempty"`
	Timestamp time.Time `json:"timestamp"`
}

// DNSResolver 域名解析函数（可注入测试桩）。
type DNSResolver func(domain string) ([]net.IP, error)

func defaultResolve(domain string) ([]net.IP, error) {
	return net.LookupIP(domain)
}

// Reconciler 把期望状态同步到内核：应用、漂移检测、回滚、危险变更守护。
type Reconciler struct {
	backend FirewallBackend

	sshPorts      []int
	dangerDelay   time.Duration
	confirmWindow time.Duration
	resolve       DNSResolver

	mu        sync.Mutex
	lastGood  *DesiredState // 上一份成功应用的期望状态
	prevGood  *DesiredState // 危险变更生效前的上一份成功状态（超时回滚目标）
	pending   *DangerOp
	watchdogs map[string]*time.Timer

	Events func(Event) // 事件回调（可为空）
}

// DangerOp 危险变更：延时生效 + 应用后确认 + 超时回滚。
type DangerOp struct {
	Token     string    `json:"token"`
	Reason    string    `json:"reason"`
	Staged    []byte    `json:"-"` // 序列化的新期望状态
	CreatedAt time.Time `json:"created_at"`
	ApplyAt   time.Time `json:"apply_at"`
	Applied   bool      `json:"applied"`
}

func NewReconciler(backend FirewallBackend, sshPorts []int, dangerDelay, confirmWindow time.Duration) *Reconciler {
	if dangerDelay <= 0 {
		dangerDelay = 60 * time.Second
	}
	if confirmWindow <= 0 {
		confirmWindow = 90 * time.Second
	}
	return &Reconciler{
		backend:       backend,
		sshPorts:      sshPorts,
		dangerDelay:   dangerDelay,
		confirmWindow: confirmWindow,
		resolve:       defaultResolve,
		watchdogs:     map[string]*time.Timer{},
	}
}

func (rc *Reconciler) emit(e Event) {
	e.Timestamp = time.Now()
	if rc.Events != nil {
		rc.Events(e)
	}
}

// ResolveDomains 把域名目标解析为 IP，返回可直接翻译规则的期望状态。
func (rc *Reconciler) ResolveDomains(ctx context.Context, st *DesiredState) (*DesiredState, error) {
	out := *st
	out.Forwards = make([]ForwardSpec, len(st.Forwards))
	copy(out.Forwards, st.Forwards)
	for i := range out.Forwards {
		f := &out.Forwards[i]
		if f.DstIP != "" {
			continue
		}
		if f.DstDomain == "" {
			return nil, fmt.Errorf("转发规则 %d 缺少目标 IP 或域名", f.ID)
		}
		ips, err := rc.resolve(f.DstDomain)
		if err != nil || len(ips) == 0 {
			return nil, fmt.Errorf("解析域名 %s 失败: %v", f.DstDomain, err)
		}
		f.DstIP = ips[0].String()
	}
	return &out, nil
}

// SyncResult 同步结果：danger=true 表示进入延时生效队列。
type SyncResult struct {
	Applied    bool   `json:"applied"`
	Danger     bool   `json:"danger"`
	Token      string `json:"token,omitempty"`
	Reason     string `json:"reason,omitempty"`
	DelaySec   int    `json:"delay_sec,omitempty"`
	ConfirmSec int    `json:"confirm_sec,omitempty"`
}

// Sync 应用期望状态。clientIP 非空时做危险判定（空=系统发起，如启动重放）。
func (rc *Reconciler) Sync(ctx context.Context, st *DesiredState, clientIP string) (*SyncResult, error) {
	rc.mu.Lock()
	defer rc.mu.Unlock()

	resolved, err := rc.ResolveDomains(ctx, st)
	if err != nil {
		return nil, err
	}
	rules := BuildRules(resolved)

	// 危险变更：仅针对「相对上一份成功状态的新增/变更规则」判定（入队延时生效）
	if clientIP != "" {
		var baseline []Rule
		if rc.lastGood != nil {
			baseline = BuildRules(rc.lastGood)
		}
		if reason := classifyDanger(deltaRules(rules, baseline), clientIP, rc.sshPorts); reason != "" {
			op := &DangerOp{
				Token:     randomToken(),
				Reason:    reason,
				Staged:    mustJSON(resolved), // 存已解析状态，避免生效时域名仍未解析
				CreatedAt: time.Now(),
				ApplyAt:   time.Now().Add(rc.dangerDelay),
			}
			rc.pending = op
			delay := rc.dangerDelay
			go func(op *DangerOp, delay time.Duration) {
				t := time.AfterFunc(delay, func() { _ = rc.applyPending(op.Token) })
				rc.mu.Lock()
				// 挂起可能已被确认/撤销，只有仍在队列时才登记定时器
				if rc.pending != nil && rc.pending.Token == op.Token {
					rc.watchdogs[op.Token+":delay"] = t
				}
				rc.mu.Unlock()
			}(op, delay)
			rc.emit(Event{Type: "danger_pending", Token: op.Token, Message: "检测到高危规则变更，已进入延时生效队列", Detail: reason})
			return &SyncResult{
				Danger: true, Token: op.Token, Reason: reason,
				DelaySec: int(rc.dangerDelay.Seconds()), ConfirmSec: int(rc.confirmWindow.Seconds()),
			}, nil
		}
	}

	if err := rc.backend.Apply(ctx, rules); err != nil {
		rc.emit(Event{Type: "apply_fail", Message: "规则应用失败", Detail: err.Error()})
		return nil, err
	}
	rc.lastGood = resolved
	rc.emit(Event{Type: "apply_ok", Message: "规则已同步到内核"})
	return &SyncResult{Applied: true}, nil
}

// applyPending 到期执行暂存的危险变更。
// 失败时清空挂起状态，避免面板永久卡在「存在待确认的高危变更」。
func (rc *Reconciler) applyPending(token string) error {
	rc.mu.Lock()
	defer rc.mu.Unlock()
	if rc.pending == nil || rc.pending.Token != token {
		return errors.New("确认令牌无效或已过期")
	}
	op := rc.pending
	rc.cancelWatchdogLocked(token + ":delay")
	var st DesiredState
	if err := json.Unmarshal(op.Staged, &st); err != nil {
		rc.clearPendingLocked()
		rc.emit(Event{Type: "apply_fail", Token: token, Message: "危险变更反序列化失败", Detail: err.Error()})
		return fmt.Errorf("危险变更反序列化失败: %w", err)
	}
	rules := BuildRules(&st)
	if err := rc.backend.Apply(context.Background(), rules); err != nil {
		rc.clearPendingLocked()
		rc.emit(Event{Type: "apply_fail", Token: token, Message: "危险变更应用失败", Detail: err.Error()})
		return fmt.Errorf("危险变更应用失败: %w", err)
	}
	// 记录生效前的上一份成功状态，作为超时回滚的目标（必须在覆盖 lastGood 之前保存）
	rc.prevGood = rc.lastGood
	rc.lastGood = &st
	op.Applied = true
	rc.emit(Event{Type: "danger_applied", Token: token, Message: "高危规则已生效", Detail: op.Reason})

	// 确认窗口：超时未确认 → 自动回滚到生效前的状态
	t := time.AfterFunc(rc.confirmWindow, func() {
		rc.mu.Lock()
		var ev Event
		if rc.pending != nil && rc.pending.Token == token && rc.pending.Applied {
			ev = rc.rollbackLocked("确认窗口超时未确认")
		}
		rc.mu.Unlock()
		// 锁外广播：事件回调（app 层恢复数据库快照）会重入 Sync，持锁会死锁
		if ev.Type != "" {
			rc.emit(ev)
		}
	})
	rc.watchdogs[token+":confirm"] = t
	return nil
}

// PendingExists 是否存在未完成的危险变更。
func (rc *Reconciler) PendingExists() bool {
	rc.mu.Lock()
	defer rc.mu.Unlock()
	return rc.pending != nil
}

// ConfirmDanger 确认危险变更：阶段1（生效前）立即应用；阶段2（生效后）取消回滚。
func (rc *Reconciler) ConfirmDanger(token string, stage int) (*SyncResult, error) {
	rc.mu.Lock()
	if rc.pending == nil || rc.pending.Token != token {
		rc.mu.Unlock()
		return nil, errors.New("确认令牌无效或已过期")
	}
	op := rc.pending
	rc.mu.Unlock()

	switch stage {
	case 1:
		if !op.Applied {
			if err := rc.applyPending(token); err != nil {
				return nil, err
			}
			return &SyncResult{Applied: true, ConfirmSec: int(rc.confirmWindow.Seconds())}, nil
		}
		return &SyncResult{Applied: true}, nil
	case 2:
		rc.mu.Lock()
		defer rc.mu.Unlock()
		if rc.pending == nil || rc.pending.Token != token {
			return nil, errors.New("确认令牌无效或已过期")
		}
		if !rc.pending.Applied {
			return nil, errors.New("规则尚未生效，无法进行第二阶段确认")
		}
		rc.clearPendingLocked()
		rc.emit(Event{Type: "apply_ok", Token: token, Message: "高危规则已确认，取消自动回滚"})
		return &SyncResult{Applied: true}, nil
	}
	return nil, errors.New("未知确认阶段")
}

// CancelDanger 撤销危险变更：生效前直接丢弃；已生效（确认窗口内）则立即回滚。
func (rc *Reconciler) CancelDanger(token string) bool {
	rc.mu.Lock()
	if rc.pending == nil || rc.pending.Token != token {
		rc.mu.Unlock()
		return false
	}
	var ev Event
	if rc.pending.Applied {
		ev = rc.rollbackLocked("用户主动回滚")
	} else {
		rc.clearPendingLocked()
		ev = Event{Type: "apply_ok", Token: token, Message: "高危规则已撤销"}
	}
	rc.mu.Unlock()
	// 锁外 emit：rollback 事件会让 app 层恢复数据库并重入 Sync（需要 rc.mu）
	rc.emit(ev)
	return true
}

// clearPendingLocked 清空挂起状态与全部定时器（调用方需持锁）。
func (rc *Reconciler) clearPendingLocked() {
	if rc.pending != nil {
		rc.clearWatchdogsLocked(rc.pending.Token)
	}
	rc.pending = nil
	rc.prevGood = nil
}

// rollbackLocked 回滚到危险变更生效前的上一份成功状态（调用方需持锁）。
// 返回待广播事件；调用方必须在**释放锁之后**再 emit，因为 app 层收到
// rollback 事件会恢复数据库快照并重入 Sync，持锁 emit 会死锁。
func (rc *Reconciler) rollbackLocked(reason string) Event {
	token := ""
	if rc.pending != nil {
		token = rc.pending.Token
	}
	target := rc.prevGood
	if target == nil {
		target = rc.lastGood
	}
	if target == nil {
		rc.clearPendingLocked()
		return Event{Type: "rollback_fail", Token: token, Message: "无法回滚：没有可用的上一份快照", Detail: reason}
	}
	rules := BuildRules(target)
	if err := rc.backend.Apply(context.Background(), rules); err != nil {
		return Event{Type: "rollback_fail", Token: token, Message: "回滚失败", Detail: err.Error()}
	}
	rc.lastGood = target
	rc.clearPendingLocked()
	return Event{Type: "rollback", Token: token, Message: "已自动回滚到上一份规则", Detail: reason}
}

func (rc *Reconciler) cancelWatchdogLocked(key string) {
	if t, ok := rc.watchdogs[key]; ok {
		t.Stop()
		delete(rc.watchdogs, key)
	}
}

// clearWatchdogsLocked 停掉某个变更的全部定时器（延时 + 确认窗口）。
func (rc *Reconciler) clearWatchdogsLocked(token string) {
	rc.cancelWatchdogLocked(token + ":delay")
	rc.cancelWatchdogLocked(token + ":confirm")
}

// Drift 对比内核实际规则与期望状态。
func (rc *Reconciler) Drift(ctx context.Context, st *DesiredState) (*DriftReport, error) {
	resolved, err := rc.ResolveDomains(ctx, st)
	if err != nil {
		return nil, err
	}
	expected := BuildRules(resolved)
	snap, err := rc.backend.Snapshot(ctx)
	if err != nil {
		return nil, err
	}
	return diffRules(expected, snap), nil
}

// iptChainToAbstract iptables 内核链名 → 抽象链名（diffRules 对比用）。
var iptChainToAbstract = map[string]string{
	"FPANEL_INPUT":   ChainInput,
	"FPANEL_FORWARD": ChainFwd,
	"FPANEL_DNAT":    ChainDstNat,
	"FPANEL_SNAT":    ChainSrcNat,
}

// normChain 链名归一化：iptables 后端的内核链名映射回抽象名，nft 后端本就是抽象名。
func normChain(c string) string {
	if a, ok := iptChainToAbstract[c]; ok {
		return a
	}
	return c
}

// diffRules 对比期望规则序列与内核快照（按后端语法规范化后比较）。
func diffRules(expected []Rule, snap *Snapshot) *DriftReport {
	rep := &DriftReport{}
	byChain := map[string][]KernelRule{}
	for _, kr := range snap.Rules {
		byChain[normChain(kr.Chain)] = append(byChain[normChain(kr.Chain)], kr)
	}
	expByChain := map[string][]Rule{}
	for _, r := range expected {
		expByChain[r.Chain] = append(expByChain[r.Chain], r)
	}
	for chain, exps := range expByChain {
		// iptables 双栈时同一规则在 v4/v6 各渲染一次、tag 相同，按 tag 去重后对比
		seenTag := map[string]bool{}
		var kerns []KernelRule
		for _, k := range byChain[chain] {
			if k.Tag == "" || seenTag[k.Tag] {
				continue
			}
			seenTag[k.Tag] = true
			kerns = append(kerns, k)
		}
		for i, e := range exps {
			if i >= len(kerns) {
				rep.Missing = append(rep.Missing, KernelRule{Chain: chain, Tag: e.Tag, Spec: ruleSummary(e)})
				continue
			}
			k := kerns[i]
			if k.Tag != e.Tag || !ruleEquivalent(k, e) {
				rep.Modified = append(rep.Modified, DriftModify{
					Tag: e.Tag, Expected: ruleSummary(e), Actual: k.Spec,
				})
			}
		}
		for i := len(exps); i < len(kerns); i++ {
			rep.Extra = append(rep.Extra, kerns[i])
		}
	}
	// 内核存在、期望不存在的链上的面板标签规则
	for chain, entries := range byChain {
		if _, ok := expByChain[chain]; ok {
			continue
		}
		for _, k := range entries {
			if _, _, _, ok := ParseTag(k.Tag); ok {
				rep.Extra = append(rep.Extra, k)
			}
		}
	}
	rep.Drifted = len(rep.Missing)+len(rep.Extra)+len(rep.Modified) > 0
	return rep
}

// ruleSem 规则的语义签名（与后端语法无关）。
type ruleSem struct {
	Src     string
	Dst     string
	Proto   string
	DstPort string
	Action  string
	To      string
}

func normAddr(v string) string {
	if strings.HasSuffix(v, "/32") || strings.HasSuffix(v, "/128") {
		return strings.SplitN(v, "/", 2)[0]
	}
	return strings.ToLower(v)
}

func semOfExpected(e Rule) ruleSem {
	return ruleSem{
		Src: normAddr(e.Src), Dst: normAddr(e.Dst), Proto: e.Proto,
		DstPort: normalizePortRange(e.DstPort), Action: e.Action, To: normalizeDNATTo(e.To),
	}
}

func normalizePortRange(p string) string {
	if p == "" {
		return ""
	}
	if strings.Contains(p, "-") {
		parts := strings.SplitN(p, "-", 2)
		a := strings.TrimSpace(parts[0])
		b := strings.TrimSpace(parts[1])
		return a + "-" + b
	}
	return strings.TrimSpace(p)
}

// normalizeDNATTo 归一化 DNAT 目标用于比较：去掉 IPv6 方括号并小写。
// iptables 存 "[::1]:9000"，nft JSON 摘要存 "::1:9000"，两者应视为同一语义。
func normalizeDNATTo(to string) string {
	return strings.ToLower(strings.NewReplacer("[", "", "]", "").Replace(strings.TrimSpace(to)))
}

// stripToBrackets 仅供 toEquivalent 复用。
func stripToBrackets(s string) string {
	return strings.NewReplacer("[", "", "]", "").Replace(s)
}

// semOfKernelSpec 从内核规则摘要提取语义（iptables-save 或 nft JSON 摘要）。
func semOfKernelSpec(spec string) ruleSem {
	sem := ruleSem{}
	fields := splitSpec(spec)
	for i := 0; i < len(fields); i++ {
		f := fields[i]
		switch {
		case f == "-s" || f == "saddr":
			sem.Src = normAddr(nextField(fields, &i, f))
		case f == "-d" || f == "daddr":
			sem.Dst = normAddr(nextField(fields, &i, f))
		case f == "-p":
			sem.Proto = nextField(fields, &i, f)
		case f == "proto":
			// nft JSON 摘要: "tcp dport == 80"
			continue
		case f == "--dport" || f == "dport":
			v := nextField(fields, &i, f)
			if v == "==" {
				v = nextField(fields, &i, f)
			}
			sem.DstPort = normalizePortRange(strings.ReplaceAll(v, ":", "-"))
		case f == "-j":
			v := nextField(fields, &i, f)
			switch v {
			case "ACCEPT":
				sem.Action = "accept"
			case "DROP":
				sem.Action = "drop"
			case "DNAT", "SNAT", "MASQUERADE":
				sem.Action = lower(v)
			}
		case f == "--to-destination":
			sem.To = lower(nextField(fields, &i, f))
		case f == "dnat":
			sem.Action = "dnat"
			if nextField(fields, &i, f) == "to" {
				sem.To = lower(nextField(fields, &i, f))
			}
		case f == "accept":
			sem.Action = "accept"
		case f == "drop":
			sem.Action = "drop"
		}
	}
	return sem
}

func splitSpec(spec string) []string {
	// 处理 nft JSON 摘要中的引号包裹项；iptables spec 无引号
	var out []string
	cur := strings.Builder{}
	inQuote := false
	for _, r := range spec {
		switch {
		case r == '"':
			inQuote = !inQuote
		case r == ' ' && !inQuote:
			if cur.Len() > 0 {
				out = append(out, cur.String())
				cur.Reset()
			}
		default:
			cur.WriteRune(r)
		}
	}
	if cur.Len() > 0 {
		out = append(out, cur.String())
	}
	return out
}

func nextField(fields []string, i *int, cur string) string {
	*i++
	if *i >= len(fields) {
		return ""
	}
	// 跳过 nft JSON 摘要中的比较运算符
	switch fields[*i] {
	case "==", "!=", ">=", "<=", ">", "<":
		return nextField(fields, i, cur)
	}
	return fields[*i]
}

func lower(s string) string { return strings.ToLower(s) }

// ruleEquivalent 语义级比较（容忍后端语法差异）。
func ruleEquivalent(k KernelRule, e Rule) bool {
	a := semOfExpected(e)
	b := semOfKernelSpec(k.Spec)
	if a.Src != "" && b.Src != "" && a.Src != b.Src {
		return false
	}
	if a.Dst != "" && b.Dst != "" && a.Dst != b.Dst {
		return false
	}
	if a.Proto != "" && b.Proto != "" && a.Proto != b.Proto {
		return false
	}
	if a.DstPort != "" && b.DstPort != "" && a.DstPort != b.DstPort {
		return false
	}
	if a.To != "" && b.To != "" && !toEquivalent(a.To, b.To) {
		return false
	}
	if a.Action != "" && b.Action != "" && a.Action != b.Action {
		return false
	}
	return true
}

// toEquivalent 对比 DNAT 目标（容忍 ip:port / ip 两种形态、IPv6 方括号差异与 nft JSON 拆分形式）。
func toEquivalent(a, b string) bool {
	a = strings.ToLower(stripToBrackets(a))
	b = strings.ToLower(stripToBrackets(b))
	if a == b {
		return true
	}
	return strings.HasSuffix(b, a) || strings.HasSuffix(a, b)
}

func ruleSummary(e Rule) string {
	return fmt.Sprintf("%s src=%s dst=%s proto=%s dport=%s action=%s to=%s",
		e.Chain, e.Src, e.Dst, e.Proto, e.DstPort, e.Action, e.To)
}

// deltaRules 提取相对基线的新增/变更规则（按 tag 对齐 + 语义比较）。
// tag 在基线中不存在，或同 tag 语义不同，视为变更。
func deltaRules(current, baseline []Rule) []Rule {
	if len(baseline) == 0 {
		return current
	}
	baseByTag := map[string]Rule{}
	for _, b := range baseline {
		baseByTag[b.Tag] = b
	}
	curTags := map[string]bool{}
	var out []Rule
	for _, r := range current {
		curTags[r.Tag] = true
		b, ok := baseByTag[r.Tag]
		if !ok {
			out = append(out, r)
			continue
		}
		// 同 tag 语义比较（渲染摘要粗比对即可）
		if ruleSummary(b) != ruleSummary(r) {
			out = append(out, r)
		}
	}
	// 基线有而当前没有的删除动作本身不判危（恢复路径）
	_ = curTags
	return out
}

// classifyDanger 判定高危规则：涉及 SSH 端口、封禁当前管理 IP 或全封。
func classifyDanger(rules []Rule, clientIP string, sshPorts []int) string {
	for _, r := range rules {
		if r.Chain == ChainDstNat && r.Action == "dnat" {
			// 监听端口与目标端口都算（DNAT 的目标端口在 To 字段）
			ports := []string{r.DstPort, portOfTo(r.To)}
			for _, p := range ports {
				if p != "" && portOverlapAny(p, sshPorts) {
					return fmt.Sprintf("转发规则涉及 SSH 端口 %s，可能导致管理连接中断", p)
				}
			}
		}
		if r.Chain == ChainInput && r.Action == "drop" && r.Src != "" {
			if coversAll(r.Src) {
				return "黑名单包含全网段（0.0.0.0/0），将阻断所有入站连接"
			}
			if clientIP != "" && cidrCovers(r.Src, clientIP) {
				return fmt.Sprintf("黑名单包含当前管理地址 %s，可能导致面板失联", clientIP)
			}
		}
		// 限速/限连规则（INPUT 链带端口、无来源限制）同样可能把管理员挡在 SSH 外
		if r.Chain == ChainInput && r.Action == "drop" && r.Src == "" && r.DstPort != "" {
			if portOverlapAny(r.DstPort, sshPorts) {
				return fmt.Sprintf("限速/限连规则作用于 SSH 端口 %s，可能导致管理连接中断", r.DstPort)
			}
		}
	}
	return ""
}

// portOfTo 从 DNAT 目标 "ip:port"/"ip:start-end"/"[IPv6]:port"/"ip" 中提取端口部分。
func portOfTo(to string) string {
	to = strings.TrimSpace(to)
	// 带方括号的 IPv6：[::1]:9000 / [::1]:8000-8010 / [::1]（无端口）
	if strings.HasPrefix(to, "[") {
		j := strings.LastIndex(to, "]")
		if j < 0 || j == len(to)-1 {
			return ""
		}
		return strings.TrimPrefix(to[j+1:], ":")
	}
	// 不带方括号的裸地址（IPv4 或 IPv6）没有端口
	if net.ParseIP(to) != nil {
		return ""
	}
	i := strings.LastIndex(to, ":")
	if i < 0 {
		return "" // 无端口 = 保持原端口
	}
	return to[i+1:]
}

func portOverlapAny(portSpec string, ports []int) bool {
	for _, p := range ports {
		if portContains(portSpec, p) {
			return true
		}
	}
	return false
}

func portContains(spec string, p int) bool {
	if strings.Contains(spec, "-") {
		parts := strings.SplitN(spec, "-", 2)
		a, e1 := atoi(parts[0])
		b, e2 := atoi(parts[1])
		return e1 == nil && e2 == nil && p >= a && p <= b
	}
	n, err := atoi(spec)
	return err == nil && n == p
}

func coversAll(cidr string) bool {
	return cidr == "0.0.0.0/0" || cidr == "::/0"
}

func cidrCovers(cidr, ip string) bool {
	_, network, err := net.ParseCIDR(cidr)
	if err != nil {
		return false
	}
	parsed := net.ParseIP(ip)
	if parsed == nil {
		return false
	}
	return network.Contains(parsed)
}

func mustJSON(v any) []byte {
	b, _ := json.Marshal(v)
	return b
}

func randomToken() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		// crypto/rand 不可用时退化为时间熵（不应用于认证场景）
		return fmt.Sprintf("%x", time.Now().UnixNano())
	}
	return hex.EncodeToString(b)
}

func atoi(s string) (int, error) {
	n := 0
	for _, c := range s {
		if c < '0' || c > '9' {
			return 0, fmt.Errorf("非数字")
		}
		n = n*10 + int(c-'0')
	}
	return n, nil
}
