package fw

import (
	"bufio"
	"context"
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// IPTablesBackend 基于 iptables-restore / iptables-save 的后端实现。
// 同时兼容 iptables-legacy 与 iptables-nft 兼容层；ip6tables 同步维护。
type IPTablesBackend struct {
	exec Executor
}

func NewIPTablesBackend(e Executor) *IPTablesBackend { return &IPTablesBackend{exec: e} }

func (b *IPTablesBackend) Name() string { return BackendIPTables }

// 面板自有链
var iptChains = map[string]struct{ table, chain, hook string }{
	ChainInput:  {"filter", "FPANEL_INPUT", "INPUT"},
	ChainFwd:    {"filter", "FPANEL_FORWARD", "FORWARD"},
	ChainDstNat: {"nat", "FPANEL_DNAT", "PREROUTING"},
	ChainSrcNat: {"nat", "FPANEL_SNAT", "POSTROUTING"},
}

func (b *IPTablesBackend) binFor(family string) string {
	if family == "ipv6" {
		return "ip6tables"
	}
	return "iptables"
}

func (b *IPTablesBackend) restoreBinFor(family string) string {
	if family == "ipv6" {
		return "ip6tables-restore"
	}
	return "iptables-restore"
}

func (b *IPTablesBackend) saveBinFor(family string) string {
	if family == "ipv6" {
		return "ip6tables-save"
	}
	return "iptables-save"
}

func (b *IPTablesBackend) Available(ctx context.Context) error {
	if _, ok := b.exec.LookPath("iptables"); !ok {
		return fmt.Errorf("iptables 不存在")
	}
	_, err := b.exec.Run(ctx, "iptables", "-L", "-n")
	return err
}

// Init 创建面板链并挂接到内置链（幂等，仅启动时执行）。
func (b *IPTablesBackend) Init(ctx context.Context) error {
	for _, family := range []string{"ipv4", "ipv6"} {
		for _, c := range iptChains {
			// 建链（已存在会报错，忽略）
			_, _ = b.exec.Run(ctx, b.binFor(family), "-t", c.table, "-N", c.chain)
			// 挂接：-C 检查存在，不存在则 -I 到内置链最前
			exists, err := b.checkJump(ctx, family, c)
			if err != nil {
				return fmt.Errorf("检查挂接 %s: %w", c.chain, err)
			}
			if !exists {
				if _, err := b.exec.Run(ctx, b.binFor(family), "-t", c.table,
					"-I", c.hook, "1", "-j", c.chain); err != nil {
					return fmt.Errorf("挂接 %s → %s: %w", c.hook, c.chain, err)
				}
			}
		}
	}
	return nil
}

func (b *IPTablesBackend) checkJump(ctx context.Context, family string, c struct{ table, chain, hook string }) (bool, error) {
	_, err := b.exec.Run(ctx, b.binFor(family), "-t", c.table, "-C", c.hook, "-j", c.chain)
	return err == nil, nil
}

// Apply 生成 iptables-restore 脚本，flush 面板链后写入全部规则。
// --noflush 保证只动 FPANEL_* 链；restore 每表一次 COMMIT，内核侧原子生效。
// 每个地址族（v4/v6）一次调用，规则经 stdin 传入。
func (b *IPTablesBackend) Apply(ctx context.Context, rules []Rule) error {
	for _, family := range []string{"ipv4", "ipv6"} {
		var filter, nat strings.Builder
		filter.WriteString("-F FPANEL_INPUT\n-F FPANEL_FORWARD\n")
		nat.WriteString("-F FPANEL_DNAT\n-F FPANEL_SNAT\n")
		for _, r := range rules {
			if r.Family != family {
				continue
			}
			if r.Chain == ChainDstNat || r.Chain == ChainSrcNat {
				nat.WriteString(renderIPTRule(r))
			} else {
				filter.WriteString(renderIPTRule(r))
			}
		}
		script := "*filter\n" + filter.String() + "COMMIT\n*nat\n" + nat.String() + "COMMIT\n"
		if _, err := b.exec.RunStdin(ctx, b.restoreBinFor(family), script, "--noflush"); err != nil {
			return fmt.Errorf("%s 应用失败: %w", b.restoreBinFor(family), err)
		}
	}
	return nil
}

// renderIPTRule 渲染一条 -A 规则（含尾部换行）。
func renderIPTRule(r Rule) string {
	var sb strings.Builder
	chain := iptChains[r.Chain].chain
	sb.WriteString("-A " + chain)
	if r.Src != "" {
		sb.WriteString(" -s " + r.Src)
	}
	if r.Dst != "" {
		sb.WriteString(" -d " + r.Dst)
	}
	if r.OutIface != "" {
		sb.WriteString(" -o " + r.OutIface)
	}
	if r.Proto != "" {
		sb.WriteString(" -p " + r.Proto)
	}
	if r.DstPort != "" {
		sb.WriteString(" --dport " + iptPort(r.DstPort))
	}
	if r.Limit != nil {
		sb.WriteString(renderIPTLimit(r.Limit, r.Proto))
	}
	sb.WriteString(` -m comment --comment "` + r.Tag + `"`)
	switch r.Action {
	case "dnat":
		sb.WriteString(" -j DNAT --to-destination " + r.To)
	case "snat":
		sb.WriteString(" -j SNAT --to-source " + r.To)
	case "masquerade":
		sb.WriteString(" -j MASQUERADE")
	default:
		target := map[string]string{"accept": "ACCEPT", "drop": "DROP"}[r.Action]
		sb.WriteString(" -j " + target)
	}
	sb.WriteString("\n")
	return sb.String()
}

// renderIPTLimit 渲染 hashlimit（速率）或 connlimit（连接数）二者之一。
func renderIPTLimit(l *Limit, proto string) string {
	var sb strings.Builder
	if l.Rate > 0 {
		sb.WriteString(" -m hashlimit")
		sb.WriteString(" --hashlimit-above " + strconv.FormatInt(l.Rate, 10) + "/" + l.Unit)
		if l.Burst > 0 {
			sb.WriteString(" --hashlimit-burst " + strconv.FormatInt(l.Burst, 10))
		}
		if l.PerSrc {
			sb.WriteString(" --hashlimit-mode srcip --hashlimit-name " + meterSafe(l.MeterName))
		} else {
			sb.WriteString(" --hashlimit-name " + meterSafe(l.MeterName) + "_g")
		}
	} else if l.ConnLimit > 0 {
		sb.WriteString(" -m connlimit --connlimit-above " + strconv.FormatInt(l.ConnLimit, 10) + " --connlimit-mask 32")
	}
	return sb.String()
}

// iptPort iptables 端口段分隔符为冒号。
func iptPort(p string) string { return strings.ReplaceAll(p, "-", ":") }

// Snapshot 解析 iptables-save 输出提取面板链规则。
func (b *IPTablesBackend) Snapshot(ctx context.Context) (*Snapshot, error) {
	snap := &Snapshot{Backend: BackendIPTables}
	var rules []KernelRule
	for _, fam := range []string{"ipv4", "ipv6"} {
		out, err := b.exec.Run(ctx, b.saveBinFor(fam))
		if err != nil {
			return nil, err
		}
		if fam == "ipv4" {
			snap.Raw = out
		}
		rs := parseIPTablesSave(out, fam)
		rules = append(rules, rs...)
		// 链存在与挂接状态
		for _, c := range iptChains {
			view := ChainView{Name: c.chain, Exists: strings.Contains(out, ":FPANEL"), Reference: c.table + "/" + c.hook}
			view.Exists = strings.Contains(out, ":"+c.chain+" - [")
			view.Hooked = existsJump(out, c.hook, c.chain)
			snap.Chains = append(snap.Chains, view)
		}
	}
	snap.Rules = rules
	return snap, nil
}

func existsJump(saveOut, hook, chain string) bool {
	re := regexp.MustCompile(`(?m)^-A ` + hook + ` .*-j ` + chain + `$`)
	return re.MatchString(saveOut)
}

// parseIPTablesSave 从 iptables-save 输出提取面板链规则。
// 行格式：-A FPANEL_INPUT -s 1.2.3.4/32 -p tcp --dport 22 [12:345] -m comment --comment "fp:bl:1:1" -j DROP
func parseIPTablesSave(out, family string) []KernelRule {
	var rules []KernelRule
	sc := bufio.NewScanner(strings.NewReader(out))
	sc.Buffer(make([]byte, 1024*1024), 1024*1024)
	countRe := regexp.MustCompile(`\[(\d+):(\d+)\]\s*`)
	commentRe := regexp.MustCompile(`-m comment --comment "([^"]+)"`)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if !strings.HasPrefix(line, "-A FPANEL_") {
			continue
		}
		rest := strings.TrimPrefix(line, "-A ")
		parts := strings.SplitN(rest, " ", 2)
		chain := parts[0]
		spec := ""
		if len(parts) > 1 {
			spec = parts[1]
		}
		var counter Counter
		spec2 := countRe.ReplaceAllStringFunc(spec, func(m string) string {
			sub := countRe.FindStringSubmatch(m)
			pk, _ := strconv.ParseUint(sub[1], 10, 64)
			by, _ := strconv.ParseUint(sub[2], 10, 64)
			counter = Counter{Packets: pk, Bytes: by}
			return ""
		})
		tag := ""
		comment := ""
		if sub := commentRe.FindStringSubmatch(spec2); sub != nil {
			tag = sub[1]
			comment = tag
			spec2 = strings.Replace(spec2, `-m comment --comment "`+tag+`"`, "", 1)
		}
		spec2 = strings.Join(strings.Fields(spec2), " ")
		rules = append(rules, KernelRule{
			Chain: chain, Tag: tag, Spec: spec2, Counter: counter, Comment: comment,
			// Family 通过链内地址语法推断意义不大，直接标注
		})
		_ = family
	}
	return rules
}

// Counters 从快照提取按 tag 的计数。
func (b *IPTablesBackend) Counters(ctx context.Context) (map[string]Counter, error) {
	snap, err := b.Snapshot(ctx)
	if err != nil {
		return nil, err
	}
	out := map[string]Counter{}
	for _, r := range snap.Rules {
		if r.Tag == "" {
			continue
		}
		c := out[r.Tag]
		c.Packets += r.Counter.Packets
		c.Bytes += r.Counter.Bytes
		out[r.Tag] = c
	}
	return out, nil
}

func (b *IPTablesBackend) Cleanup(ctx context.Context) error {
	for _, family := range []string{"ipv4", "ipv6"} {
		for _, c := range iptChains {
			_, _ = b.exec.Run(ctx, b.binFor(family), "-t", c.table, "-D", c.hook, "-j", c.chain)
			_, _ = b.exec.Run(ctx, b.binFor(family), "-t", c.table, "-F", c.chain)
			_, _ = b.exec.Run(ctx, b.binFor(family), "-t", c.table, "-X", c.chain)
		}
	}
	return nil
}
