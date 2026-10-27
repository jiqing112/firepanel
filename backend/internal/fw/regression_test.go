package fw

import (
	"context"
	"strings"
	"testing"
)

// TestNftRateLimitWithoutPort 全端口限速（有协议、无端口）不能生成非法 nft 语法。
func TestNftRateLimitWithoutPort(t *testing.T) {
	rules := BuildRules(&DesiredState{
		RateLimits: []RateLimitSpec{
			{ID: 1, Name: "all-tcp", Proto: "tcp", Port: "", PerSrc: true, Rate: 10, RateUnit: "second", Enabled: true},
		},
	})
	if len(rules) != 1 {
		t.Fatalf("期望 1 条规则，得到 %d", len(rules))
	}
	line := renderNftRule(rules[0])
	if strings.Contains(line, "tcp dport  ") || strings.HasSuffix(strings.TrimSpace(line), "dport") {
		t.Fatalf("无端口时不应渲染 dport: %q", line)
	}
	if !strings.Contains(line, "meta l4proto tcp") {
		t.Fatalf("无端口时应按协议匹配（meta l4proto）: %q", line)
	}

	// 有端口时仍使用 tcp dport
	rules2 := BuildRules(&DesiredState{
		RateLimits: []RateLimitSpec{
			{ID: 1, Name: "http", Proto: "tcp", Port: "80", PerSrc: true, Rate: 10, RateUnit: "second", Enabled: true},
		},
	})
	if line := renderNftRule(rules2[0]); !strings.Contains(line, "tcp dport 80") {
		t.Fatalf("有端口时应渲染 tcp dport: %q", line)
	}
}

// TestParseIPTablesSaveCounters 校验 iptables-save -c 的 "[pkts:bytes] " 前缀解析。
func TestParseIPTablesSaveCounters(t *testing.T) {
	out := `*filter
:INPUT ACCEPT [0:0]
:FPANEL_INPUT - [0:0]
-A INPUT -j FPANEL_INPUT
[123:4567] -A FPANEL_INPUT -s 203.0.113.9/32 -m comment --comment "fp:bl:3:1" -j DROP
[1:64] -A FPANEL_INPUT -s 192.168.1.0/24 -m comment --comment "fp:wl:2:1" -j ACCEPT
COMMIT`
	rules := parseIPTablesSave(out, "ipv4")
	if len(rules) != 2 {
		t.Fatalf("应解析出 2 条面板规则，得到 %d: %+v", len(rules), rules)
	}
	if rules[0].Tag != "fp:bl:3:1" || rules[0].Counter.Packets != 123 || rules[0].Counter.Bytes != 4567 {
		t.Fatalf("前缀计数解析失败: %+v", rules[0])
	}
	if rules[0].Spec != "-s 203.0.113.9/32 -j DROP" {
		t.Fatalf("Spec 规范化失败: %q", rules[0].Spec)
	}
	if rules[1].Counter.Packets != 1 || rules[1].Counter.Bytes != 64 {
		t.Fatalf("第二条计数解析失败: %+v", rules[1])
	}
	// 带 -c 前缀时仍能识别挂接
	if !existsJump(out, "INPUT", "FPANEL_INPUT") {
		t.Fatal("带计数前缀时应识别 INPUT → FPANEL_INPUT 挂接")
	}
}

// IPv6 DNAT 目标必须写成 [addr]:port，否则 ip6tables/nft 会把 ::1:9000 当成地址。
func TestIPv6DNATTargetFormatting(t *testing.T) {
	cases := []struct{ dstIP, listen, dstPort, want string }{
		{"::1", "18094", "9000", "[::1]:9000"},
		{"::1", "18094", "", "[::1]:18094"},
		{"::1", "8000-8010", "9000-9010", "[::1]:9000-9010"},
		{"::1", "8000-8010", "", "::1"},
		{"192.0.2.1", "18094", "9000", "192.0.2.1:9000"},
	}
	for _, c := range cases {
		if got := dnatTarget(c.dstIP, c.listen, c.dstPort); got != c.want {
			t.Errorf("dnatTarget(%q,%q,%q)=%q，期望 %q", c.dstIP, c.listen, c.dstPort, got, c.want)
		}
	}

	rules := BuildRules(&DesiredState{Forwards: []ForwardSpec{
		{ID: 7, Protocol: "tcp", ListenPort: "18094", DstIP: "::1", DstPort: "9000", Enabled: true},
	}})
	if len(rules) < 2 {
		t.Fatalf("应生成 DNAT+FORWARD 两条规则，实际 %d", len(rules))
	}
	nft := renderNftRule(rules[0])
	if !strings.Contains(nft, "dnat ip6 to [::1]:9000") {
		t.Errorf("nft IPv6 DNAT 渲染错误: %q", nft)
	}
	ipt := renderIPTRule(rules[0])
	if !strings.Contains(ipt, "--to-destination [::1]:9000") {
		t.Errorf("iptables IPv6 DNAT 渲染错误: %q", ipt)
	}
}

func TestPortOfToIPv6(t *testing.T) {
	cases := []struct{ to, want string }{
		{"[::1]:9000", "9000"},
		{"[::1]:8000-8010", "8000-8010"},
		{"[::1]", ""},
		{"::1", ""},
		{"192.0.2.1:80", "80"},
		{"192.0.2.1", ""},
	}
	for _, c := range cases {
		if got := portOfTo(c.to); got != c.want {
			t.Errorf("portOfTo(%q)=%q，期望 %q", c.to, got, c.want)
		}
	}
}

// IPv6 的两种内核摘要形态都要与期望规则判定一致（不得误报漂移）。
func TestDriftEquivalentIPv6DNAT(t *testing.T) {
	rules := BuildRules(&DesiredState{Forwards: []ForwardSpec{
		{ID: 8, Protocol: "tcp", ListenPort: "18094", DstIP: "::1", DstPort: "9000", Enabled: true},
	}})
	exp := rules[0]
	specs := []string{
		"tcp dport == 18094 dnat to ::1:9000",                      // nft JSON 摘要
		"-p tcp --dport 18094 -j DNAT --to-destination [::1]:9000", // iptables-save
	}
	for _, spec := range specs {
		kr := KernelRule{Chain: ChainDstNat, Tag: exp.Tag, Spec: spec}
		if !ruleEquivalent(kr, exp) {
			t.Errorf("IPv6 DNAT 摘要应判定一致: spec=%q expected=%q", spec, exp.To)
		}
	}
}

// IPv6 DNAT 的 [addr]:port 必须通过注入防御校验；其他字段仍不得出现方括号/换行。
func TestCheckRulesSafeIPv6Brackets(t *testing.T) {
	rules := BuildRules(&DesiredState{Forwards: []ForwardSpec{
		{ID: 9, Protocol: "tcp", ListenPort: "18094", DstIP: "::1", DstPort: "9000", Enabled: true},
	}})
	if err := checkRulesSafe(rules); err != nil {
		t.Fatalf("合法 IPv6 DNAT 不应被注入防御拦截: %v", err)
	}
	if err := ruleTokenSafe(Rule{Tag: "fp:fw:9:1", Chain: ChainDstNat, Action: "dnat", To: "1.2.3.4:80\nflush table"}); err == nil {
		t.Fatal("换行注入必须被拒绝")
	}
	if err := ruleTokenSafe(Rule{Tag: "fp:nat:9:1", Chain: ChainSrcNat, Action: "snat", OutIface: "[eth0]"}); err == nil {
		t.Fatal("非 to 字段的方括号必须被拒绝")
	}
}

// TestNftApplyRebuildsTable 校验 Apply 采用「删表+重建」而非 flush：
// flush 会留下命名 set（meter），旧限速元素会被同名新规则复用。
func TestNftApplyRebuildsTable(t *testing.T) {
	exec := NewFakeExecutor()
	b := NewNftablesBackend(exec)
	rules := BuildRules(&DesiredState{
		RateLimits: []RateLimitSpec{
			{ID: 1, Name: "cc", Proto: "tcp", Port: "80", PerSrc: true, Rate: 30, RateUnit: "minute", Burst: 10, Enabled: true},
		},
	})
	if err := b.Apply(context.Background(), rules); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	var script string
	for _, c := range exec.Calls {
		if strings.HasPrefix(c, "  [stdin] ") {
			script = c
		}
	}
	if script == "" {
		t.Fatal("没有通过 stdin 下发 nft 脚本")
	}
	if !strings.Contains(script, "delete table inet firepanel") || !strings.Contains(script, "add table inet firepanel") {
		t.Fatalf("脚本应删除并重建面板表: %s", script)
	}
	if strings.Contains(script, "flush table") {
		t.Fatalf("不应只是 flush（会残留 meter 集合）: %s", script)
	}
	for _, chain := range nftChainOrder {
		if !strings.Contains(script, "add chain inet firepanel "+chain) {
			t.Errorf("脚本缺少建链 %s: %s", chain, script)
		}
	}
	if !strings.Contains(script, "meter fp1_tcp") {
		t.Errorf("脚本缺少限速规则: %s", script)
	}
}

// TestIPTablesApplySkipsMissingIPv6 校验 ip6tables 不可用时只维护 IPv4（不整体失败）。
func TestIPTablesApplySkipsMissingIPv6(t *testing.T) {
	exec := NewFakeExecutor()
	b := NewIPTablesBackend(exec)
	// 模拟 ip6tables 缺失：FakeExecutor 默认都"存在"，这里直接构造未 Init 的实例
	if err := b.Apply(context.Background(), BuildRules(&DesiredState{
		Forwards: []ForwardSpec{{ID: 1, Protocol: "tcp", ListenPort: "80", DstIP: "192.168.1.5", DstPort: "8080", Enabled: true}},
	})); err != nil {
		t.Fatalf("Apply 失败: %v", err)
	}
	for _, call := range exec.Calls {
		if strings.HasPrefix(call, "ip6tables-restore") {
			t.Fatalf("未启用 IPv6 时不应调用 ip6tables-restore: %s", call)
		}
	}
}
