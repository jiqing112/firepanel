// nftables 后端：nft -f 原子文件加载维护自有表 inet firepanel。
package fw

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
)

// NftablesBackend 通过 nft -f 原子文件加载维护自有表 inet firepanel。
type NftablesBackend struct {
	exec Executor
}

func NewNftablesBackend(e Executor) *NftablesBackend { return &NftablesBackend{exec: e} }

func (b *NftablesBackend) Name() string { return BackendNftables }

const nftTable = "firepanel"

// 面板自有基础链：priority 取负值保证先于发行版默认链评估。
var nftChains = map[string]string{
	ChainInput:  "type filter hook input priority -10; policy accept;",
	ChainFwd:    "type filter hook forward priority -10; policy accept;",
	ChainDstNat: "type nat hook prerouting priority -110; policy accept;",
	ChainSrcNat: "type nat hook postrouting priority -110; policy accept;",
}

func (b *NftablesBackend) Available(ctx context.Context) error {
	if _, ok := b.exec.LookPath("nft"); !ok {
		return fmt.Errorf("nft 不存在")
	}
	_, err := b.exec.Run(ctx, "nft", "list", "tables")
	return err
}

// Init 确保表与基础链存在（幂等）。
// nft -f 单文件是整体事务：表已存在会使同批 add chain 全部回滚，
// 因此逐条执行并忽略“已存在”类错误，最后以 list table 验证。
func (b *NftablesBackend) Init(ctx context.Context) error {
	_, _ = b.exec.RunStdin(ctx, "nft", "add table inet "+nftTable+"\n", "-f", "-")
	for name, spec := range nftChains {
		stmt := fmt.Sprintf("add chain inet %s %s { %s }\n", nftTable, name, spec)
		_, _ = b.exec.RunStdin(ctx, "nft", stmt, "-f", "-")
	}
	if _, err := b.exec.Run(ctx, "nft", "list", "table", "inet", nftTable); err != nil {
		return fmt.Errorf("初始化面板表失败: %w", err)
	}
	return nil
}

// Apply 生成完整的表内容并在单次 nft -f 事务中替换。
// flush table 只清面板表内规则，不触碰系统其他表（含 incus/docker/firewalld）。
func (b *NftablesBackend) Apply(ctx context.Context, rules []Rule) error {
	var sb strings.Builder
	sb.WriteString("flush table inet " + nftTable + "\n")
	for _, r := range rules {
		sb.WriteString(renderNftRule(r))
	}
	if _, err := b.exec.RunStdin(ctx, "nft", sb.String(), "-f", "-"); err != nil {
		return fmt.Errorf("nft 应用失败: %w", err)
	}
	return nil
}

// renderNftRule 渲染单条 add rule 语句。
func renderNftRule(r Rule) string {
	var parts []string
	parts = append(parts, "add rule inet "+nftTable+" "+r.Chain)
	famExpr := "ip"
	if r.Family == "ipv6" {
		famExpr = "ip6"
	}
	if r.Src != "" {
		parts = append(parts, famExpr+" saddr "+nftAddr(r.Src))
	}
	if r.Dst != "" {
		parts = append(parts, famExpr+" daddr "+nftAddr(r.Dst))
	}
	if r.OutIface != "" {
		parts = append(parts, `oifname "`+r.OutIface+`"`)
	}
	if r.Proto != "" {
		parts = append(parts, r.Proto+" dport "+nftPort(r.DstPort))
	}
	// 面板规则统一挂 counter 以支持命中统计
	parts = append(parts, "counter")
	if r.Limit != nil {
		parts = append(parts, renderNftLimit(r.Limit))
	}
	switch r.Action {
	case "accept":
		parts = append(parts, "accept")
	case "drop":
		parts = append(parts, "drop")
	case "dnat":
		if r.Family == "ipv6" {
			parts = append(parts, "dnat ip6 to "+r.To)
		} else {
			parts = append(parts, "dnat ip to "+r.To)
		}
	case "masquerade":
		parts = append(parts, "masquerade")
	case "snat":
		if r.Family == "ipv6" {
			parts = append(parts, "snat ip6 to "+r.To)
		} else {
			parts = append(parts, "snat ip to "+r.To)
		}
	}
	// 惯例：comment 位于语句末尾
	parts = append(parts, "comment \""+r.Tag+"\"")
	line := strings.Join(parts, " ") + "\n"
	if r.Proto == "" && r.DstPort != "" {
		// 无协议却带端口是非法 nft 语法，跳过端口（理论不可达，防御性处理）
		line = strings.Replace(line, " dport "+nftPort(r.DstPort), "", 1)
	}
	return line
}

// renderNftLimit 渲染 nft meter（限速或连接数，二者不组合）：
//   限速:  meter fp1_tcp { [ip saddr] limit rate over 30/minute burst 10 packets }
//   连接:  meter fp1_tcp_c { ip saddr ct count over 20 }
func renderNftLimit(l *Limit) string {
	var sb strings.Builder
	sb.WriteString("meter " + meterSafe(l.MeterName) + " { ")
	if l.PerSrc {
		sb.WriteString("ip saddr ")
	}
	if l.Rate > 0 {
		sb.WriteString(fmt.Sprintf("limit rate over %d/%s", l.Rate, l.Unit))
		if l.Burst > 0 {
			sb.WriteString(fmt.Sprintf(" burst %d packets", l.Burst))
		}
	} else if l.ConnLimit > 0 {
		sb.WriteString(fmt.Sprintf("ct count over %d", l.ConnLimit))
	}
	sb.WriteString(" }")
	return sb.String()
}

// meterSafe meter 名仅允许字母数字下划线。
func meterSafe(s string) string {
	var out strings.Builder
	for _, r := range s {
		if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '_' {
			out.WriteRune(r)
		} else {
			out.WriteRune('_')
		}
	}
	return out.String()
}

func nftAddr(cidr string) string {
	// nft 接受 1.2.3.4 与 1.2.3.0/24 两种写法
	if strings.HasSuffix(cidr, "/32") {
		return strings.TrimSuffix(cidr, "/32")
	}
	if strings.HasSuffix(cidr, "/128") {
		return strings.TrimSuffix(cidr, "/128")
	}
	return cidr
}

func nftPort(p string) string { return p }

// nftJSON nft -j 输出结构（节选所需字段）。
// `list table` 输出为扁平条目流：metainfo / table / chain / rule 各为独立元素；
// `list ruleset` 的 rule 则嵌套在 chain 内。两种形态都兼容。
type nftJSON struct {
	Nftables []struct {
		Meta struct {
			Version string `json:"version"`
		} `json:"metainfo"`
		Table *struct {
			Family string `json:"family"`
			Name   string `json:"name"`
		} `json:"table"`
		Chain *struct {
			Family string        `json:"family"`
			Table  string        `json:"table"`
			Name   string        `json:"name"`
			Rule   []nftRuleJSON `json:"rule"`
		} `json:"chain"`
		Rule *nftRuleJSON `json:"rule"`
	} `json:"nftables"`
}

// nftRuleJSON 单条规则的 JSON（list table 与 list ruleset 通用）。
type nftRuleJSON struct {
	Family  string            `json:"family"`
	Table   string            `json:"table"`
	Chain   string            `json:"chain"`
	Comment string            `json:"comment"` // 面板标签挂在 rule 级
	Expr    []json.RawMessage `json:"expr"`
}

type nftInlineExpr struct {
	Inline map[string]json.RawMessage `json:"inline"`
}

// Snapshot 解析 nft -j list ruleset 中面板表规则。
func (b *NftablesBackend) Snapshot(ctx context.Context) (*Snapshot, error) {
	out, err := b.exec.Run(ctx, "nft", "-j", "list", "table", "inet", nftTable)
	if err != nil {
		return nil, err
	}
	snap := &Snapshot{Backend: BackendNftables, Raw: out}
	var doc nftJSON
	if err := json.Unmarshal([]byte(out), &doc); err != nil {
		return nil, fmt.Errorf("解析 nft JSON: %w", err)
	}
	chainSeen := map[string]bool{}
	for _, entry := range doc.Nftables {
		switch {
		case entry.Chain != nil && entry.Chain.Table == nftTable:
			name := entry.Chain.Name
			if !chainSeen[name] {
				chainSeen[name] = true
				snap.Chains = append(snap.Chains, ChainView{Name: name, Exists: true, Hooked: true, Reference: "inet/" + nftTable})
			}
			// list ruleset 形态：规则嵌套在 chain 内
			for _, rl := range entry.Chain.Rule {
				if kr := parseNftRuleJSON(name, rl); kr != nil {
					snap.Rules = append(snap.Rules, *kr)
				}
			}
		case entry.Rule != nil && entry.Rule.Table == nftTable:
			// list table 形态：规则为独立条目
			if kr := parseNftRuleJSON(entry.Rule.Chain, *entry.Rule); kr != nil {
				snap.Rules = append(snap.Rules, *kr)
			}
		}
	}
	return snap, nil
}

// parseNftRuleJSON 从 rule JSON 提取 tag/计数/摘要。
// 表达式按键分发（map 形式），null 值键（verdict/masquerade 等）靠键存在性判断。
func parseNftRuleJSON(chain string, rl nftRuleJSON) *KernelRule {
	kr := &KernelRule{Chain: chain, Tag: rl.Comment, Comment: rl.Comment}
	var specParts []string
	appendSpec := func(s string) {
		if s = strings.TrimSpace(s); s != "" {
			specParts = append(specParts, s)
		}
	}

	for _, raw := range rl.Expr {
		var expr map[string]json.RawMessage
		if json.Unmarshal(raw, &expr) != nil || len(expr) == 0 {
			continue
		}
		if v, ok := expr["counter"]; ok {
			var c struct {
				Packets uint64 `json:"packets"`
				Bytes   uint64 `json:"bytes"`
			}
			_ = json.Unmarshal(v, &c)
			kr.Counter = Counter{Packets: c.Packets, Bytes: c.Bytes}
			continue
		}
		if v, ok := expr["comment"]; ok {
			var s string
			if json.Unmarshal(v, &s) == nil && kr.Tag == "" {
				kr.Tag = s
			}
			continue
		}
		if v, ok := expr["match"]; ok {
			var m struct {
				Op   string `json:"op"`
				Left struct {
					Payload struct {
						Protocol string `json:"protocol"`
						Field    string `json:"field"`
					} `json:"payload"`
				} `json:"left"`
				Right json.RawMessage `json:"right"`
			}
			if json.Unmarshal(v, &m) == nil && (m.Op != "" || m.Right != nil) {
				p := m.Left.Payload
				right := jsonRawToValue(m.Right)
				switch {
				case p.Protocol == "oifname" || p.Protocol == "iifname":
					appendSpec(p.Protocol + " " + right)
				case p.Field != "" && p.Protocol != "":
					op := m.Op
					if op == "==" {
						op = ""
					}
					seg := p.Protocol + " " + p.Field
					if op != "" {
						seg += " " + op
					}
					seg += " " + right
					appendSpec(seg)
				case p.Field != "":
					appendSpec(p.Field + " " + right)
				default:
					appendSpec(m.Op + " " + right)
				}
			}
			continue
		}
		if v, ok := expr["limit"]; ok {
			var l struct {
				Rate uint64 `json:"rate"`
				Unit string `json:"unit"`
			}
			if json.Unmarshal(v, &l) == nil && l.Rate > 0 {
				appendSpec(fmt.Sprintf("limit rate over %d/%s", l.Rate, l.Unit))
			}
			continue
		}
		if _, ok := expr["meter"]; ok {
			// meter 包装（内含 limit/ct count），仅标记存在
			appendSpec("meter")
			continue
		}
		if v, ok := expr["dnat"]; ok {
			if addr, port := nftAddrPort(v); addr != "" {
				if port != "" {
					appendSpec("dnat to " + addr + ":" + port)
				} else {
					appendSpec("dnat to " + addr)
				}
			}
			continue
		}
		if v, ok := expr["snat"]; ok {
			if addr, port := nftAddrPort(v); addr != "" {
				if port != "" {
					appendSpec("snat to " + addr + ":" + port)
				} else {
					appendSpec("snat to " + addr)
				}
			}
			continue
		}
		if _, ok := expr["masquerade"]; ok {
			appendSpec("masquerade")
			continue
		}
		if _, ok := expr["reject"]; ok {
			appendSpec("reject")
			continue
		}
		if _, ok := expr["accept"]; ok {
			appendSpec("accept")
			continue
		}
		if _, ok := expr["drop"]; ok {
			appendSpec("drop")
			continue
		}
		// 兜底 inline 形态
		var inl nftInlineExpr
		if json.Unmarshal(raw, &inl) == nil && inl.Inline != nil {
			for k, v := range inl.Inline {
				appendSpec(k + " " + string(v))
			}
		}
	}
	kr.Spec = strings.Join(specParts, " ")
	return kr
}

// nftAddrPort 从 dnat/snat 表达式值提取地址与端口。
// addr 兼容字符串、prefix 对象与数组（端口段）形态。
func nftAddrPort(v json.RawMessage) (addr, port string) {
	var single struct {
		Addr string `json:"addr"`
		Port int    `json:"port"`
	}
	if err := json.Unmarshal(v, &single); err == nil && single.Addr != "" {
		if single.Port != 0 {
			return single.Addr, strconv.Itoa(single.Port)
		}
		return single.Addr, ""
	}
	var arr []map[string]any
	if err := json.Unmarshal(v, &arr); err == nil && len(arr) > 0 {
		first := arr[0]
		if a, ok := first["addr"].(string); ok {
			p := ""
			if pv, ok := first["port"].(float64); ok {
				p = strconv.Itoa(int(pv))
			}
			return a, p
		}
	}
	return "", ""
}

// jsonRawToValue 把 JSON 值归一化为人类可读片段：
// 字符串去引号；prefix 对象转 addr/len；数组逐项拼接；数字原样。
func jsonRawToValue(raw json.RawMessage) string {
	trimmed := strings.TrimSpace(string(raw))
	if trimmed == "null" || trimmed == "" {
		return ""
	}
	if strings.HasPrefix(trimmed, "\"") {
		var s string
		if err := json.Unmarshal(raw, &s); err == nil {
			return s
		}
		return trimmed
	}
	if strings.HasPrefix(trimmed, "{") {
		var p struct {
			Prefix struct {
				Addr string `json:"addr"`
				Len  int    `json:"len"`
			} `json:"prefix"`
		}
		if err := json.Unmarshal(raw, &p); err == nil && p.Prefix.Addr != "" {
			return fmt.Sprintf("%s/%d", p.Prefix.Addr, p.Prefix.Len)
		}
		return trimmed
	}
	if strings.HasPrefix(trimmed, "[") {
		var items []json.RawMessage
		if err := json.Unmarshal(raw, &items); err == nil {
			parts := make([]string, 0, len(items))
			for _, it := range items {
				if s := jsonRawToValue(it); s != "" {
					parts = append(parts, s)
				}
			}
			return strings.Join(parts, "-")
		}
	}
	return trimmed
}

// Counters 按 tag 汇总。
func (b *NftablesBackend) Counters(ctx context.Context) (map[string]Counter, error) {
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

func (b *NftablesBackend) Cleanup(ctx context.Context) error {
	_, err := b.exec.Run(ctx, "nft", "delete", "table", "inet", nftTable)
	return err
}
