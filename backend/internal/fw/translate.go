package fw

import (
	"fmt"
	"net"
	"strconv"
	"strings"
)

func fmtTag(kind string, id int64, seq int) string {
	return "fp:" + kind + ":" + strconv.FormatInt(id, 10) + ":" + strconv.Itoa(seq)
}

// ParseTag 解析标签；非面板标签返回 ok=false。
func ParseTag(tag string) (kind string, id int64, seq int, ok bool) {
	parts := strings.Split(tag, ":")
	if len(parts) != 4 || parts[0] != "fp" {
		return "", 0, 0, false
	}
	id64, err := strconv.ParseInt(parts[2], 10, 64)
	if err != nil {
		return "", 0, 0, false
	}
	seqN, err := strconv.Atoi(parts[3])
	if err != nil {
		return "", 0, 0, false
	}
	return parts[1], id64, seqN, true
}

// BuildRules 将期望状态翻译为与后端无关的规则序列。
// 规则顺序即应用顺序：白名单 → 黑名单 → 转发（DNAT + FORWARD 放行）。
// 标签 seq 按规则局部编号，保证同一规则在多次同步间标签稳定（命中映射不漂移）。
func BuildRules(st *DesiredState) []Rule {
	var rules []Rule
	seqOf := map[string]int{}
	next := func(kind string, id int64) string {
		key := kind + ":" + strconv.FormatInt(id, 10)
		seqOf[key]++
		return "fp:" + kind + ":" + strconv.FormatInt(id, 10) + ":" + strconv.Itoa(seqOf[key])
	}

	// 1. 白名单（input accept）
	for _, e := range st.IPLists {
		if e.ListType != "white" {
			continue
		}
		family, ok := familyOf(e.IP)
		if !ok {
			continue
		}
		rules = append(rules, Rule{
			Tag: next("wl", e.ID), Chain: ChainInput, Family: family,
			Src: normalizeCIDR(e.IP), Action: "accept", Comment: "whitelist",
		})
	}

	// 2. 黑名单（input drop）
	for _, e := range st.IPLists {
		if e.ListType != "black" {
			continue
		}
		family, ok := familyOf(e.IP)
		if !ok {
			continue
		}
		rules = append(rules, Rule{
			Tag: next("bl", e.ID), Chain: ChainInput, Family: family,
			Src: normalizeCIDR(e.IP), Action: "drop", Comment: "blacklist",
		})
	}

	// 3. 限速（INPUT 链，白/黑名单之后）
	// 连接数限制与速率限制拆为两条独立规则：nft meter 内二者不能组合；
	// iptables 串联模块是 AND 语义，而防护需要 OR（任一超限即丢弃）。
	for _, rl := range st.RateLimits {
		if !rl.Enabled || rl.Rate <= 0 {
			continue
		}
		protos := []string{rl.Proto}
		if rl.Proto == "both" {
			protos = []string{"tcp", "udp"}
		}
		for _, proto := range protos {
			base := fmt.Sprintf("fp%d_%s", rl.ID, proto)
			rules = append(rules, Rule{
				Tag: next("rl", rl.ID), Chain: ChainInput, Family: "ipv4",
				Proto: proto, DstPort: rl.Port, Action: "drop", Comment: rl.Name,
				Limit: &Limit{
					Rate: rl.Rate, Unit: rl.RateUnit, Burst: rl.Burst,
					PerSrc: rl.PerSrc, MeterName: base,
				},
			})
			if rl.ConnLimit > 0 {
				rules = append(rules, Rule{
					Tag: next("rl", rl.ID), Chain: ChainInput, Family: "ipv4",
					Proto: proto, DstPort: rl.Port, Action: "drop", Comment: rl.Name,
					Limit: &Limit{ConnLimit: rl.ConnLimit, PerSrc: true, MeterName: base + "_c"},
				})
			}
		}
	}

	// 4. 端口转发：nat DNAT + forward 放行
	for _, f := range st.Forwards {
		if !f.Enabled || f.DstIP == "" {
			continue
		}
		family, ok := familyOf(f.DstIP)
		if !ok {
			continue
		}
		protos := []string{f.Protocol}
		if f.Protocol == "both" {
			protos = []string{"tcp", "udp"}
		}
		for _, proto := range protos {
			// DNAT
			rules = append(rules, Rule{
				Tag: next("fw", f.ID), Chain: ChainDstNat, Family: family,
				Proto: proto, Src: normalizeCIDR(f.SrcIP), DstPort: f.ListenPort,
				Action: "dnat", To: dnatTarget(f.DstIP, f.ListenPort, f.DstPort), Comment: f.Name,
			})
			// FORWARD 放行（策略为 DROP 时必需）
			rules = append(rules, Rule{
				Tag: next("fw", f.ID), Chain: ChainFwd, Family: family,
				Proto: proto, Dst: normalizeCIDR(f.DstIP), DstPort: f.DstPort,
				Action: "accept", Comment: f.Name,
			})
		}
	}

	// 5. NAT：SNAT / MASQUERADE（srcnat 链）
	for _, n := range st.NAT {
		if !n.Enabled {
			continue
		}
		rules = append(rules, Rule{
			Tag: next("nat", n.ID), Chain: ChainSrcNat, Family: "ipv4",
			Src: normalizeCIDR(n.SrcCIDR), OutIface: n.OutIface,
			Action: map[string]string{"SNAT": "snat", "MASQ": "masquerade"}[n.Type],
			To:     n.ToAddr, Comment: n.Name,
		})
	}
	return rules
}

func familyOf(ipOrCIDR string) (string, bool) {
	if ipOrCIDR == "" {
		return "", false
	}
	// 域名不应进入此处（reconciler 已解析）
	ip := ipOrCIDR
	if strings.Contains(ipOrCIDR, "/") {
		if _, _, err := net.ParseCIDR(ipOrCIDR); err != nil {
			return "", false
		}
		return familyOfIP(strings.SplitN(ipOrCIDR, "/", 2)[0])
	}
	return familyOfIP(ip)
}

func familyOfIP(ip string) (string, bool) {
	parsed := net.ParseIP(ip)
	if parsed == nil {
		return "", false
	}
	if parsed.To4() != nil {
		return "ipv4", true
	}
	return "ipv6", true
}

// normalizeCIDR 单 IP 补全为 /32 或 /128；CIDR 原样。
func normalizeCIDR(v string) string {
	v = strings.TrimSpace(v)
	if v == "" {
		return ""
	}
	if strings.Contains(v, "/") {
		if _, cidr, err := net.ParseCIDR(v); err == nil {
			return cidr.String()
		}
		return ""
	}
	ip := net.ParseIP(v)
	if ip == nil {
		return ""
	}
	if ip.To4() != nil {
		return v + "/32"
	}
	return v + "/128"
}

// dnatTarget 计算 DNAT --to-destination：
//   监听端口段 + 目标同宽端口段 → ip:start-end
//   监听端口段 + 目标端口省略 → ip（保持原端口）
//   单端口 → ip:port
func dnatTarget(dstIP, listenPort, dstPort string) string {
	listenRange := parsePortRange(listenPort)
	dstRange := parsePortRange(dstPort)
	switch {
	case listenRange == nil:
		return dstIP // 非法，已在 API 层校验
	case dstPort == "" || dstRange == nil:
		if isRange(listenPort) {
			return dstIP // 端口段转发且未指定目标段：保持原端口
		}
		return dstIP + ":" + listenPort
	case isRange(listenPort):
		return dstIP + ":" + strconv.Itoa(dstRange.start) + "-" + strconv.Itoa(dstRange.end)
	default:
		return dstIP + ":" + strconv.Itoa(dstRange.start)
	}
}

type portRange struct{ start, end int }

func parsePortRange(v string) *portRange {
	if v == "" {
		return nil
	}
	if strings.Contains(v, "-") {
		parts := strings.SplitN(v, "-", 2)
		a, err1 := strconv.Atoi(parts[0])
		b, err2 := strconv.Atoi(parts[1])
		if err1 != nil || err2 != nil || a > b || a < 1 || b > 65535 {
			return nil
		}
		return &portRange{a, b}
	}
	n, err := strconv.Atoi(v)
	if err != nil || n < 1 || n > 65535 {
		return nil
	}
	return &portRange{n, n}
}

func isRange(v string) bool { return strings.Contains(v, "-") }

// ValidateForward 校验转发规则字段（防注入 + 语义）。
func ValidateForward(name, protocol, listenPort, srcIP, dstIP, dstDomain, dstPort string) error {
	if strings.TrimSpace(name) == "" || len(name) > 100 {
		return errStr("规则名称不能为空且不超过 100 字符")
	}
	switch protocol {
	case "tcp", "udp", "both":
	default:
		return errStr("协议必须为 tcp/udp/both")
	}
	if parsePortRange(listenPort) == nil {
		return errStr("监听端口格式错误（1-65535 单端口或 start-end 端口段）")
	}
	if dstPort != "" && parsePortRange(dstPort) == nil {
		return errStr("目标端口格式错误")
	}
	// 监听端口段与目标端口段宽度需一致
	if lr := parsePortRange(listenPort); lr != nil && lr.start != lr.end {
		if dstPort != "" {
			dr := parsePortRange(dstPort)
			if dr == nil || dr.end-dr.start != lr.end-lr.start {
				return errStr("端口段转发的目标端口段宽度必须与监听端口段一致")
			}
		}
	}
	if dstDomain != "" {
		if !validDomain(dstDomain) {
			return errStr("目标域名格式错误")
		}
	} else if parseIP(dstIP) == nil {
		return errStr("目标 IP 无效")
	}
	if srcIP != "" && normalizeCIDR(srcIP) == "" {
		return errStr("来源限制必须是合法 IP 或 CIDR")
	}
	return nil
}

func parseIP(v string) net.IP { return net.ParseIP(strings.TrimSpace(v)) }

func validDomain(d string) bool {
	d = strings.TrimSpace(d)
	if len(d) == 0 || len(d) > 253 {
		return false
	}
	for _, label := range strings.Split(d, ".") {
		if label == "" || len(label) > 63 {
			return false
		}
		for _, r := range label {
			ok := r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '-'
			if !ok {
				return false
			}
		}
	}
	return true
}

// ValidateIPOrCIDR 校验黑/白名单条目。
func ValidateIPOrCIDR(v string) error {
	if normalizeCIDR(v) == "" {
		return errStr("必须是合法的 IP 或 CIDR 网段")
	}
	return nil
}

type errStr string

func (e errStr) Error() string { return string(e) }

// NormalizeCIDRCheck 导出校验：合法 CIDR/IP 返回规范化形式，否则空串。
func NormalizeCIDRCheck(v string) string { return normalizeCIDR(v) }

// NormalizeIPCheck 导出校验：合法 IP 返回原样，否则空串。
func NormalizeIPCheck(v string) string {
	return strings.TrimSpace(v)
}
