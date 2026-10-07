// Package jail：原生 fail2ban 防爆破引擎。
// 三种日志源：journald 流（journalctl -f）、日志文件 tail、面板审计表；
// 滑动窗口计数达标后自动写入黑名单（带过期时间，由 janitor 到期解封）。
package jail

import (
	"bufio"
	"context"
	"fmt"
	"log/slog"
	"net"
	"os"
	"regexp"
	"strings"
	"sync"
	"time"

	"firepanel/internal/store"
	"firepanel/internal/fw"
)

// BuiltinPatterns 内置匹配模板（前端预设选择，存库的是完整正则）。
var BuiltinPatterns = map[string]string{
	"sshd":        `(?:Failed password for|Invalid user\b).* from ([0-9]{1,3}(?:\.[0-9]{1,3}){3})`,
	"nginx_auth":  `client ([0-9]{1,3}(?:\.[0-9]{1,3}){3}).*(?:401|403)`,
	"panel_login": `^(?:panel )?([0-9]{1,3}(?:\.[0-9]{1,3}){3})$`,
}

// BanCallback 封禁回调（app 层接：写黑名单 + 同步内核 + 广播/告警）。
type BanCallback func(jail store.JailConfig, ip string, until time.Time)

// Engine 管理全部 jail 的运行态。
type Engine struct {
	st    *store.Store
	log   *slog.Logger
	exec  fw.Executor
	OnBan BanCallback

	mu       sync.Mutex
	cancels  map[int64]context.CancelFunc
	counters map[int64]map[string][]time.Time // jailID → ip → 命中时间
	lastHits map[int64]map[string]time.Time   // jailID → ip → 最近命中（UI 展示）
}

func New(st *store.Store, log *slog.Logger, exec fw.Executor) *Engine {
	return &Engine{
		st:       st,
		log:      log,
		exec:     exec,
		cancels:  map[int64]context.CancelFunc{},
		counters: map[int64]map[string][]time.Time{},
		lastHits: map[int64]map[string]time.Time{},
	}
}

// Reload 全量重载：停止全部，重启启用的 jail。
func (e *Engine) Reload() {
	e.mu.Lock()
	for _, cancel := range e.cancels {
		cancel()
	}
	e.cancels = map[int64]context.CancelFunc{}
	e.counters = map[int64]map[string][]time.Time{}
	e.lastHits = map[int64]map[string]time.Time{}
	jails, err := e.st.ListJails()
	if err != nil {
		e.mu.Unlock()
		e.log.Warn("jail 配置读取失败", "err", err)
		return
	}
	// 内层 map 必须逐 jail 初始化，否则 record() 首次赋值 panic
	for _, j := range jails {
		e.counters[j.ID] = map[string][]time.Time{}
		e.lastHits[j.ID] = map[string]time.Time{}
	}
	var starts []store.JailConfig
	for _, j := range jails {
		if j.Enabled {
			starts = append(starts, j)
		}
	}
	for _, sj := range starts {
		ctx, cancel := context.WithCancel(context.Background())
		jail := sj
		e.cancels[jail.ID] = cancel
		go e.run(ctx, jail)
	}
	e.mu.Unlock()
	e.log.Info("防爆破 jail 已加载", "count", len(starts))
}

// Stop 全部停止。
func (e *Engine) Stop() {
	e.mu.Lock()
	defer e.mu.Unlock()
	for _, cancel := range e.cancels {
		cancel()
	}
	e.cancels = map[int64]context.CancelFunc{}
}

// HitCount 当前窗口内的失败计数（UI 用）。
func (e *Engine) HitCount(jailID int64) map[string]int {
	e.mu.Lock()
	defer e.mu.Unlock()
	out := map[string]int{}
	for ip, ts := range e.counters[jailID] {
		out[ip] = len(ts)
	}
	return out
}

func (e *Engine) run(ctx context.Context, j store.JailConfig) {
	switch j.SourceType {
	case "journald":
		e.tailJournalctl(ctx, j)
	case "audit":
		e.pollAudit(ctx, j)
	default:
		e.tailFile(ctx, j)
	}
}

// record 记录一次命中并判断是否达到封禁阈值。
func (e *Engine) record(j store.JailConfig, ip string) {
	parsed := net.ParseIP(ip)
	if parsed == nil {
		return
	}
	// 忽略网段（默认含私网，防误封管理网段）
	for _, cidr := range strings.Split(j.IgnoreCIDRs, ",") {
		cidr = strings.TrimSpace(cidr)
		if cidr == "" {
			continue
		}
		if _, network, err := net.ParseCIDR(cidr); err == nil && network.Contains(parsed) {
			return
		}
	}

	e.mu.Lock()
	now := time.Now()
	window := time.Duration(j.FindTime) * time.Second
	// 防御：内层 map 未初始化时（如 Reload 之外的新增路径）就地补齐
	if e.counters[j.ID] == nil {
		e.counters[j.ID] = map[string][]time.Time{}
	}
	if e.lastHits[j.ID] == nil {
		e.lastHits[j.ID] = map[string]time.Time{}
	}
	hits := append(e.counters[j.ID][ip], now)
	var keep []time.Time
	for _, t := range hits {
		if now.Sub(t) <= window {
			keep = append(keep, t)
		}
	}
	e.counters[j.ID][ip] = keep
	e.lastHits[j.ID][ip] = now
	count := len(keep)
	e.mu.Unlock()

	if count < j.Threshold {
		return
	}
	// 达标：清零计数并封禁
	e.mu.Lock()
	delete(e.counters[j.ID], ip)
	e.mu.Unlock()

	until := now.Add(time.Duration(j.BanTime) * time.Second)
	entry := &store.IPListEntry{
		ListType: "black",
		IPOrCIDR: ip + "/32",
		GroupTag: "jail:" + j.Name,
		Source:   "jail:" + j.Name,
		ExpiresAt: &until,
		Remark:   fmt.Sprintf("防爆破：%d 次/%d 秒，封禁 %d 秒", j.Threshold, j.FindTime, j.BanTime),
	}
	if err := e.st.CreateIPList(entry); err != nil {
		e.log.Error("jail 封禁写入失败", "jail", j.Name, "ip", ip, "err", err)
		return
	}
	e.log.Warn("jail 自动封禁", "jail", j.Name, "ip", ip, "until", until.Format("15:04:05"))
	if e.OnBan != nil {
		e.OnBan(j, ip, until)
	}
}

/* ------------------------- 来源1：journald ------------------------- */

// tailJournalctl 使用 --after-cursor 轮询（journalctl -f 对管道的逐行投递
// 受 stdio 缓冲影响不可靠；cursor 轮询确定性且零丢失）。
func (e *Engine) tailJournalctl(ctx context.Context, j store.JailConfig) {
	re, err := compilePattern(j.MatchRegex)
	if err != nil {
		e.log.Warn("jail 正则无效", "jail", j.Name, "err", err)
		return
	}
	baseArgs := []string{"--quiet", "-o", "cat", "--show-cursor"}
	if j.Unit != "" {
		baseArgs = append(baseArgs, "-u", j.Unit)
	}

	runJournal := func(args []string) string {
		out, err := e.exec.Run(ctx, "journalctl", append(args, baseArgs...)...)
		if err != nil {
			return ""
		}
		return out
	}

	// 首轮：仅取当前 cursor（不回放历史，避免启动时误判存量失败）
	cursor := ""
	if out := runJournal([]string{"-n", "0"}); out != "" {
		cursor = extractCursor(out)
	}
	e.log.Info("jail 已监听 journald", "jail", j.Name, "unit", j.Unit)

	ticker := time.NewTicker(1500 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			args := []string{}
			if cursor != "" {
				args = append(args, "--after-cursor", cursor)
			} else {
				args = append(args, "-n", "0")
			}
			out := runJournal(args)
			if out == "" {
				continue
			}
			for _, line := range strings.Split(out, "\n") {
				line = strings.TrimSpace(line)
				if strings.HasPrefix(line, "-- cursor:") {
					if c := extractCursor(line); c != "" {
						cursor = c
					}
					continue
				}
				if line == "" {
					continue
				}
				if ip := extractIP(re, line); ip != "" {
					e.log.Info("jail 命中", "jail", j.Name, "ip", ip)
					e.record(j, ip)
				}
			}
		}
	}
}

// extractCursor 从 "-- cursor: s=..." 行提取游标串。
func extractCursor(line string) string {
	idx := strings.Index(line, "s=")
	if idx < 0 {
		return ""
	}
	return strings.TrimSpace(line[idx+2:])
}

/* ------------------------- 来源2：日志文件 tail ------------------------- */

func (e *Engine) tailFile(ctx context.Context, j store.JailConfig) {
	re, err := compilePattern(j.MatchRegex)
	if err != nil {
		e.log.Warn("jail 正则无效", "jail", j.Name, "err", err)
		return
	}
	var offset int64 = -1
	ticker := time.NewTicker(700 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			f, err := os.Open(j.LogPath)
			if err != nil {
				continue // 文件可能尚未创建/轮转
			}
			st, err := f.Stat()
			if err != nil {
				f.Close()
				continue
			}
			if offset < 0 {
				// 首次打开：只监听新增内容，不追溯历史（与 fail2ban 行为一致，
				// 避免把日志里的存量旧 IP 一并封禁）
				offset = st.Size()
			} else if st.Size() < offset {
				offset = 0 // 文件轮转：从头读新文件
			}
			if st.Size() > offset {
				f.Seek(offset, 0)
				sc := bufio.NewScanner(f)
				sc.Buffer(make([]byte, 64*1024), 1024*1024)
				for sc.Scan() {
					if ip := extractIP(re, sc.Text()); ip != "" {
						e.record(j, ip)
					}
				}
				offset = st.Size()
			}
			f.Close()
		}
	}
}

/* ------------------------- 来源3：面板审计（登录失败） ------------------------- */

func (e *Engine) pollAudit(ctx context.Context, j store.JailConfig) {
	lastID := int64(0)
	_ = e.st.DB().QueryRow(`SELECT COALESCE(MAX(id),0) FROM audit_logs`).Scan(&lastID)
	ticker := time.NewTicker(3 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			// 先取结果并关闭 rows，再逐条 record——record 内部会写库/触发同步，
			// 连接池 MaxOpenConns(1)，嵌套查询会自死锁
			type ev struct {
				id int64
				ip string
			}
			var batch []ev
			rows, err := e.st.DB().Query(`SELECT id, ip FROM audit_logs WHERE action='login_fail' AND id>? ORDER BY id`, lastID)
			if err != nil {
				continue
			}
			for rows.Next() {
				var id int64
				var ip string
				if err := rows.Scan(&id, &ip); err == nil {
					batch = append(batch, ev{id, ip})
				}
			}
			rows.Close()
			for _, ev := range batch {
				lastID = ev.id
				if ev.ip != "" {
					e.record(j, ev.ip)
				}
			}
		}
	}
}

/* ------------------------- 工具 ------------------------- */

func compilePattern(pattern string) (*regexp.Regexp, error) {
	if strings.TrimSpace(pattern) == "" {
		return nil, fmt.Errorf("匹配正则为空")
	}
	return regexp.Compile(pattern)
}

// CompilePatternCheck 供 API 校验正则（不含副作用）。
func CompilePatternCheck(pattern string) (*regexp.Regexp, error) {
	return compilePattern(pattern)
}

// v4Re 日志行内 IPv4 兜底提取（预编译，日志热路径避免重复编译）。
var v4Re = regexp.MustCompile(`\b\d{1,3}(?:\.\d{1,3}){3}\b`)

// extractIP 从行中提取捕获组里的 IP（取第一个能解析为 IP 的捕获组）；
// 无捕获组或捕获组都不是 IP 时，退化为在行内找第一个 IPv4。
func extractIP(re *regexp.Regexp, line string) string {
	if re == nil {
		return ""
	}
	if m := re.FindStringSubmatch(line); m != nil {
		for _, g := range m[1:] {
			g = strings.TrimSpace(g)
			if net.ParseIP(g) != nil {
				return g
			}
		}
	}
	return v4Re.FindString(line)
}
