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

	sshPorts     []int
	dangerDelay  time.Duration
	confirmWindow time.Duration
	resolve      DNSResolver

	mu        sync.Mutex
	lastGood  *DesiredState // 上一份成功应用的期望状态
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

// Sync 应用期望状态。dangerCtx 非 nil 时做危险判定（clientIP 可为空）。
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
				Staged:    mustJSON(st),
				CreatedAt: time.Now(),
				ApplyAt:   time.Now().Add(rc.dangerDelay),
			}
			rc.cancelWatchdogLocked(op.Token)
			rc.pending = op
			delay := rc.dangerDelay
			go func(op *DangerOp, delay time.Duration) {
				t := time.AfterFunc(delay, func() { rc.applyPending(op.Token) })
				rc.mu.Lock()
				rc.watchdogs[op.Token+":delay"] = t
				rc.mu.Unlock()
			}(op, delay)
			rc.emit(Event{Type: "danger_pending", Message: "检测到高危规则变更，已进入延时生效队列", Detail: reason})
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
func (rc *Reconciler) applyPending(token string) {
	rc.mu.Lock()
	defer rc.mu.Unlock()
	if rc.pending == nil || rc.pending.Token != token {
		return
	}
	op := rc.pending
	var st DesiredState
	if err := json.Unmarshal(op.Staged, &st); err != nil {
		rc.emit(Event{Type: "apply_fail", Message: "危险变更反序列化失败", Detail: err.Error()})
		return
	}
	rules := BuildRules(&st)
	if err := rc.backend.Apply(context.Background(), rules); err != nil {
		rc.emit(Event{Type: "apply_fail", Message: "危险变更应用失败", Detail: err.Error()})
		return
	}
	rc.lastGood = &st
	op.Applied = true
	rc.emit(Event{Type: "danger_applied", Message: "高危规则已生效", Detail: op.Reason})

	// 确认窗口：超时未确认 → 自动回滚
	confirmWindow := rc.confirmWindow
	t := time.AfterFunc(confirmWindow, func() {
		rc.mu.Lock()
		defer rc.mu.Unlock()
		if rc.pending != nil && rc.pending.Token == token && rc.pending.Applied {
			rc.rollbackLocked("确认窗口超时未确认")
		}
	})
	rc.watchdogs[token+":confirm"] = t
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
	defer rc.mu.Unlock()
	if rc.pending == nil || rc.pending.Token != token {
		return nil, errors.New("确认令牌无效或已过期")
	}
	op := rc.pending
	switch stage {
	case 1:
		if !op.Applied {
			rc.mu.Unlock()
			rc.applyPending(token) // 立即应用
			rc.mu.Lock()
			return &SyncResult{Applied: true, ConfirmSec: int(rc.confirmWindow.Seconds())}, nil
		}
		return &SyncResult{Applied: true}, nil
	case 2:
		if !op.Applied {
			return nil, errors.New("规则尚未生效，无法进行第二阶段确认")
		}
		rc.cancelWatchdogLocked(token + ":confirm")
		rc.pending = nil
		rc.emit(Event{Type: "apply_ok", Message: "高危规则已确认，取消自动回滚"})
		return &SyncResult{Applied: true}, nil
	}
	return nil, errors.New("未知确认阶段")
}

// CancelDanger 生效前撤销危险变更。
func (rc *Reconciler) CancelDanger(token string) bool {
	rc.mu.Lock()
	defer rc.mu.Unlock()
	if rc.pending != nil && rc.pending.Token == token && !rc.pending.Applied {
		rc.cancelWatchdogLocked(token + ":delay")
		rc.pending = nil
		rc.emit(Event{Type: "apply_ok", Message: "高危规则已撤销"})
		return true
	}
	return false
}

// rollbackLocked 回滚到上一份成功应用的期望状态（调用方需持锁）。
func (rc *Reconciler) rollbackLocked(reason string) {
	if rc.lastGood == nil {
		rc.emit(Event{Type: "rollback", Message: "无法回滚：没有可用的上一份快照", Detail: reason})
		return
	}
	rules := BuildRules(rc.lastGood)
	if err := rc.backend.Apply(context.Background(), rules); err != nil {
		rc.emit(Event{Type: "rollback", Message: "回滚失败", Detail: err.Error()})
		return
	}
	rc.pending = nil
	rc.emit(Event{Type: "rollback", Message: "已自动回滚到上一份规则", Detail: reason})
}

func (rc *Reconciler) cancelWatchdogLocked(key string) {
	if t, ok := rc.watchdogs[key]; ok {
		t.Stop()
		delete(rc.watchdogs, key)
	}
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

// diffRules 对比期望规则序列与内核快照（按后端语法规范化后比较）。
func diffRules(expected []Rule, snap *Snapshot) *DriftReport {
	rep := &DriftReport{}
	byChain := map[string][]KernelRule{}
	for _, kr := range snap.Rules {
		byChain[kr.Chain] = append(byChain[kr.Chain], kr)
	}
	expByChain := map[string][]Rule{}
	for _, r := range expected {
		expByChain[r.Chain] = append(expByChain[r.Chain], r)
	}
	for chain, exps := range expByChain {
		var kerns []KernelRule
		for _, k := range byChain[chain] {
			if k.Tag != "" {
				kerns = append(kerns, k)
			}
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

func normalizeDNATTo(to string) string {
	// iptables: 1.2.3.4:80-90；nft JSON: addr+port 分离。统一为 ip:port 形式（缺省端口为空）
	if i := strings.Index(to, "-"); i > 0 && strings.Index(to, ":") < 0 && strings.Contains(to, ".") {
		// 纯 "1.2.3.4" 不变；"30000-30010" 已带冒号前置
		_ = i
	}
	return strings.ToLower(to)
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

// toEquivalent 对比 DNAT 目标（容忍 ip:port / ip 两种形态与 nft JSON 拆分形式）。
func toEquivalent(a, b string) bool {
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
	}
	return ""
}

// portOfTo 从 DNAT 目标 "ip:port"/"ip:start-end"/"ip" 中提取端口部分。
func portOfTo(to string) string {
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
