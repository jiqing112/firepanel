package fw

import (
	"context"
	"strings"
	"testing"
	"time"
)

func TestBuildRulesForwards(t *testing.T) {
	st := &DesiredState{
		Forwards: []ForwardSpec{
			{ID: 1, Name: "web", Protocol: "tcp", ListenPort: "80", DstIP: "192.168.1.100", DstPort: "8080", Enabled: true},
			{ID: 2, Name: "game", Protocol: "both", ListenPort: "30000-30010", DstIP: "192.168.1.102", DstPort: "30000-30010", Enabled: true},
			{ID: 3, Name: "disabled", Protocol: "tcp", ListenPort: "81", DstIP: "10.0.0.1", DstPort: "81", Enabled: false},
		},
		IPLists: []IPListSpec{
			{ID: 5, ListType: "black", IP: "203.0.113.9"},
			{ID: 6, ListType: "white", IP: "192.168.1.0/24"},
		},
	}
	rules := BuildRules(st)

	// 期望：白名单1 + 黑名单1 + 转发1(tcp DNAT+FWD=2) + 转发2(tcp+udp → 4) = 8
	if len(rules) != 8 {
		t.Fatalf("规则数量错误，期望 8，得到 %d: %+v", len(rules), rules)
	}
	if rules[0].Chain != ChainInput || rules[0].Action != "accept" {
		t.Errorf("首条应为白名单 accept，得到 %+v", rules[0])
	}
	if rules[0].Src != "192.168.1.0/24" {
		t.Errorf("白名单 CIDR 规范化失败: %q", rules[0].Src)
	}
	if rules[1].Src != "203.0.113.9/32" {
		t.Errorf("黑名单单 IP 应补 /32: %q", rules[1].Src)
	}
	// 单端口 DNAT
	dnat := rules[2]
	if dnat.Chain != ChainDstNat || dnat.Action != "dnat" || dnat.To != "192.168.1.100:8080" {
		t.Errorf("DNAT 渲染错误: %+v", dnat)
	}
	if dnat.Tag != "fp:fw:1:1" {
		t.Errorf("标签错误: %q", dnat.Tag)
	}
	// 端口段 both：DNAT 保持原端口
	var rangeDnat *Rule
	for i := range rules {
		r := rules[i]
		if r.Tag == "fp:fw:2:1" {
			rangeDnat = &r
		}
	}
	if rangeDnat == nil || rangeDnat.To != "192.168.1.102:30000-30010" {
		t.Errorf("端口段 DNAT 错误: %+v", rangeDnat)
	}
}

func TestBuildRulesDomainUnresolved(t *testing.T) {
	st := &DesiredState{
		Forwards: []ForwardSpec{{ID: 9, Protocol: "tcp", ListenPort: "443", DstDomain: "nas.example.net", DstPort: "443", Enabled: true}},
	}
	rules := BuildRules(st)
	if len(rules) != 0 {
		t.Errorf("未解析域名的规则应被跳过，得到 %d 条", len(rules))
	}
}

func TestIPTRender(t *testing.T) {
	r := Rule{
		Tag: "fp:fw:1:1", Chain: ChainDstNat, Family: "ipv4", Proto: "tcp",
		DstPort: "80", Action: "dnat", To: "192.168.1.100:8080", Comment: "web",
	}
	line := renderIPTRule(r)
	want := `-A FPANEL_DNAT -p tcp --dport 80 -m comment --comment "fp:fw:1:1" -j DNAT --to-destination 192.168.1.100:8080` + "\n"
	if line != want {
		t.Errorf("iptables 渲染错误:\n got: %q\nwant: %q", line, want)
	}
}

func TestNftRender(t *testing.T) {
	r := Rule{
		Tag: "fp:bl:3:1", Chain: ChainInput, Family: "ipv4",
		Src: "203.0.113.9/32", Action: "drop",
	}
	line := renderNftRule(r)
	if !strings.Contains(line, "add rule inet firepanel input") ||
		!strings.Contains(line, "ip saddr 203.0.113.9") ||
		!strings.Contains(line, `comment "fp:bl:3:1"`) ||
		!strings.Contains(line, "counter drop") {
		t.Errorf("nft 渲染错误: %q", line)
	}
	// /32 应被剥掉
	if strings.Contains(line, "/32") {
		t.Errorf("nft 单 IP 不应带 /32: %q", line)
	}
}

func TestParseIPTablesSave(t *testing.T) {
	out := `*filter
:INPUT ACCEPT [0:0]
:FPANEL_INPUT - [0:0]
-A INPUT -j FPANEL_INPUT
-A FPANEL_INPUT -s 203.0.113.9/32 [123:4567] -m comment --comment "fp:bl:3:1" -j DROP
-A FPANEL_INPUT -s 192.168.1.0/24 [1:64] -m comment --comment "fp:wl:2:1" -j ACCEPT
COMMIT`
	rules := parseIPTablesSave(out, "ipv4")
	if len(rules) != 2 {
		t.Fatalf("应解析出 2 条面板规则，得到 %d", len(rules))
	}
	if rules[0].Tag != "fp:bl:3:1" || rules[0].Counter.Packets != 123 || rules[0].Counter.Bytes != 4567 {
		t.Errorf("计数解析失败: %+v", rules[0])
	}
	if rules[0].Spec != "-s 203.0.113.9/32 -j DROP" {
		t.Errorf("Spec 规范化失败: %q", rules[0].Spec)
	}
}

func TestDriftDetection(t *testing.T) {
	expected := BuildRules(&DesiredState{
		Forwards: []ForwardSpec{{ID: 1, Protocol: "tcp", ListenPort: "80", DstIP: "192.168.1.100", DstPort: "8080", Enabled: true}},
		IPLists:  []IPListSpec{{ID: 3, ListType: "black", IP: "8.8.8.8"}},
	})
	// 内核快照：与期望一致
	snap := &Snapshot{Backend: BackendIPTables}
	for _, e := range expected {
		snap.Rules = append(snap.Rules, KernelRule{Chain: e.Chain, Tag: e.Tag, Spec: iptSpecOf(e)})
	}
	rep := diffRules(expected, snap)
	if rep.Drifted {
		t.Errorf("无漂移场景误报: %+v", rep)
	}

	// 场景2：内核同位置规则被改（tag 与内容都不同 → modified）
	snap2 := &Snapshot{Backend: BackendIPTables}
	for _, e := range expected {
		if e.Tag == "fp:bl:3:1" {
			continue
		}
		snap2.Rules = append(snap2.Rules, KernelRule{Chain: e.Chain, Tag: e.Tag, Spec: iptSpecOf(e)})
	}
	snap2.Rules = append(snap2.Rules, KernelRule{Chain: ChainInput, Tag: "fp:bl:99:1", Spec: "-s 1.2.3.4/32 -j DROP"})
	rep2 := diffRules(expected, snap2)
	if !rep2.Drifted {
		t.Fatal("漂移未检出")
	}
	if len(rep2.Modified) != 1 {
		t.Errorf("漂移明细错误(场景2): missing=%d extra=%d modified=%d", len(rep2.Missing), len(rep2.Extra), len(rep2.Modified))
	}

	// 场景3：内核多出一条面板标签规则 → extra
	snap3 := &Snapshot{Backend: BackendIPTables}
	for _, e := range expected {
		snap3.Rules = append(snap3.Rules, KernelRule{Chain: e.Chain, Tag: e.Tag, Spec: iptSpecOf(e)})
	}
	snap3.Rules = append(snap3.Rules, KernelRule{Chain: ChainInput, Tag: "fp:bl:99:1", Spec: "-s 1.2.3.4/32 -j DROP"})
	rep3 := diffRules(expected, snap3)
	if !rep3.Drifted || len(rep3.Extra) != 1 {
		t.Errorf("漂移明细错误(场景3): missing=%d extra=%d modified=%d", len(rep3.Missing), len(rep3.Extra), len(rep3.Modified))
	}
}

// TestDriftNftSpec 验证 nft JSON 摘要形式同样可比对。
func TestDriftNftSpec(t *testing.T) {
	expected := BuildRules(&DesiredState{
		Forwards: []ForwardSpec{{ID: 1, Protocol: "tcp", ListenPort: "80", DstIP: "192.168.1.100", DstPort: "8080", Enabled: true}},
	})
	snap := &Snapshot{Backend: BackendNftables}
	// nft 摘要由 parseNftRuleJSON 生成，形如 "tcp dport == 80 ... dnat to 192.168.1.100:8080 accept"
	snap.Rules = append(snap.Rules,
		KernelRule{Chain: ChainDstNat, Tag: "fp:fw:1:1", Spec: `tcp dport == 80 dnat to 192.168.1.100:8080`},
		KernelRule{Chain: ChainFwd, Tag: "fp:fw:1:2", Spec: `ip daddr == 192.168.1.100 tcp dport == 8080 accept`},
	)
	rep := diffRules(expected, snap)
	if rep.Drifted {
		t.Errorf("nft 摘要对比误报漂移: %+v", rep)
	}
}

// iptSpecOf 渲染期望规则对应的 iptables-save spec 形式（供漂移对比）。
func iptSpecOf(e Rule) string {
	var parts []string
	if e.Src != "" {
		parts = append(parts, "-s "+e.Src)
	}
	if e.Dst != "" {
		parts = append(parts, "-d "+e.Dst)
	}
	if e.Proto != "" {
		parts = append(parts, "-p "+e.Proto)
	}
	if e.DstPort != "" {
		parts = append(parts, "--dport "+strings.ReplaceAll(e.DstPort, "-", ":"))
	}
	switch e.Action {
	case "accept":
		parts = append(parts, "-j ACCEPT")
	case "drop":
		parts = append(parts, "-j DROP")
	case "dnat":
		parts = append(parts, "-j DNAT --to-destination "+e.To)
	}
	return strings.Join(parts, " ")
}

func TestDangerClassification(t *testing.T) {
	clientIP := "192.168.218.1"
	rules := BuildRules(&DesiredState{
		IPLists: []IPListSpec{{ID: 1, ListType: "black", IP: "192.168.218.0/24"}},
	})
	if reason := classifyDanger(rules, clientIP, []int{22}); reason == "" {
		t.Fatal("封禁管理网段应判为高危")
	}

	rules2 := BuildRules(&DesiredState{
		Forwards: []ForwardSpec{{ID: 2, Protocol: "tcp", ListenPort: "22", DstIP: "10.0.0.5", DstPort: "22", Enabled: true}},
	})
	if reason := classifyDanger(rules2, clientIP, []int{22}); reason == "" {
		t.Fatal("转发 SSH 端口应判为高危")
	}

	rules3 := BuildRules(&DesiredState{
		Forwards: []ForwardSpec{{ID: 3, Protocol: "tcp", ListenPort: "8080", DstIP: "10.0.0.5", DstPort: "80", Enabled: true}},
	})
	if reason := classifyDanger(rules3, clientIP, []int{22}); reason != "" {
		t.Fatalf("普通规则不应判为高危: %s", reason)
	}
}

func TestValidateForward(t *testing.T) {
	cases := []struct {
		name    string
		fields  []string // name, protocol, listen, src, dstIP, dstDomain, dstPort
		wantErr bool
	}{
		{"正常单端口", []string{"web", "tcp", "80", "", "192.168.1.1", "", "8080"}, false},
		{"正常端口段", []string{"game", "udp", "30000-30010", "", "192.168.1.2", "", "30000-30010"}, false},
		{"域名目标", []string{"ddns", "tcp", "554", "", "", "cam.example.net", "554"}, false},
		{"端口段宽度不一致", []string{"bad", "tcp", "30000-30010", "", "192.168.1.1", "", "30000"}, true},
		{"非法端口", []string{"bad", "tcp", "0", "", "192.168.1.1", "", "80"}, true},
		{"注入尝试", []string{"bad; iptables -F", "tcp", "80; rm", "", "192.168.1.1", "", "80"}, true},
		{"非法域名", []string{"bad", "tcp", "80", "", "", "exa mple.net", "80"}, true},
		{"非法IP", []string{"bad", "tcp", "80", "", "999.1.1.1", "", "80"}, true},
		{"来源CIDR", []string{"db", "tcp", "13306", "192.168.0.0/16", "192.168.1.10", "", "3306"}, false},
		{"非法来源", []string{"db", "tcp", "13306", "192.168.0.0/99", "192.168.1.10", "", "3306"}, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := ValidateForward(tc.fields[0], tc.fields[1], tc.fields[2], tc.fields[3], tc.fields[4], tc.fields[5], tc.fields[6])
			if (err != nil) != tc.wantErr {
				t.Errorf("期望错误=%v，实际=%v", tc.wantErr, err)
			}
		})
	}
}

func TestRateLimitAndNATTranslation(t *testing.T) {
	st := &DesiredState{
		RateLimits: []RateLimitSpec{
			{ID: 1, Name: "web-cc", Proto: "tcp", Port: "80", PerSrc: true, Rate: 30, RateUnit: "minute", Burst: 10, ConnLimit: 20, Enabled: true},
			{ID: 2, Name: "global-udp", Proto: "udp", Port: "", PerSrc: false, Rate: 100, RateUnit: "second", Burst: 100, Enabled: true},
		},
		NAT: []NATSpec{
			{ID: 5, Name: "lan-out", Type: "MASQ", SrcCIDR: "192.168.1.0/24", OutIface: "eth0", Enabled: true},
			{ID: 6, Name: "fixed-ip", Type: "SNAT", SrcCIDR: "10.0.0.0/8", OutIface: "eth1", ToAddr: "203.0.113.5", Enabled: true},
		},
	}
	rules := BuildRules(st)
	if len(rules) != 5 {
		t.Fatalf("期望 5 条规则（限速+连接数拆分），得到 %d", len(rules))
	}
	// nft 限速渲染（速率与连接数拆为两条独立规则）
	rl := rules[0]
	if rl.Tag != "fp:rl:1:1" || rl.Limit == nil {
		t.Fatalf("限速规则错误: %+v", rl)
	}
	nft := renderNftRule(rl)
	for _, want := range []string{
		`meter fp1_tcp { ip saddr limit rate over 30/minute burst 10 packets }`,
		"counter meter fp1_tcp", "drop", `comment "fp:rl:1:1"`, "tcp dport 80",
	} {
		if !strings.Contains(nft, want) {
			t.Errorf("nft 限速缺少 %q: %s", want, nft)
		}
	}
	// 连接数限制独立规则
	rlConn := rules[1]
	if rlConn.Limit == nil || rlConn.Limit.ConnLimit != 20 || rlConn.Limit.Rate != 0 {
		t.Fatalf("连接数规则错误: %+v", rlConn)
	}
	nftConn := renderNftRule(rlConn)
	if !strings.Contains(nftConn, `meter fp1_tcp_c { ip saddr ct count over 20 }`) {
		t.Errorf("nft 连接数限制错误: %s", nftConn)
	}
	iptConn := renderIPTRule(rlConn)
	if !strings.Contains(iptConn, "-m connlimit --connlimit-above 20 --connlimit-mask 32") {
		t.Errorf("iptables 连接数限制错误: %s", iptConn)
	}
	// nft 全局限速
	rl2 := rules[2]
	nft2 := renderNftRule(rl2)
	if !strings.Contains(nft2, "meter fp2_udp { limit rate over 100/second burst 100 packets }") {
		t.Errorf("nft 全局限速错误: %s", nft2)
	}
	// iptables 限速（hashlimit 单独一条，无 connlimit 串联）
	ipt := renderIPTRule(rl)
	for _, want := range []string{
		"-m hashlimit --hashlimit-above 30/minute --hashlimit-burst 10 --hashlimit-mode srcip --hashlimit-name fp1_tcp",
		"-j DROP",
	} {
		if !strings.Contains(ipt, want) {
			t.Errorf("iptables 限速缺少 %q: %s", want, ipt)
		}
	}
	if strings.Contains(ipt, "connlimit") {
		t.Errorf("iptables 限速规则不应串联 connlimit: %s", ipt)
	}
	// nft MASQ / SNAT
	masq := renderNftRule(rules[3])
	if !strings.Contains(masq, `oifname "eth0"`) || !strings.Contains(masq, "masquerade") {
		t.Errorf("MASQ 渲染错误: %s", masq)
	}
	snatIPT := renderIPTRule(rules[4])
	if !strings.Contains(snatIPT, `-j SNAT --to-source 203.0.113.5`) || !strings.Contains(snatIPT, "-o eth1") {
		t.Errorf("SNAT 渲染错误: %s", snatIPT)
	}
	// 标签稳定性：两次构建序列号一致
	rules2 := BuildRules(st)
	for i := range rules {
		if rules[i].Tag != rules2[i].Tag {
			t.Errorf("标签漂移: %s != %s", rules[i].Tag, rules2[i].Tag)
		}
	}
}

func TestDangerDelta(t *testing.T) {
	// 基线：已含 SSH 转发（历史遗留）
	base := BuildRules(&DesiredState{
		Forwards: []ForwardSpec{{ID: 2, Protocol: "tcp", ListenPort: "12022", DstIP: "192.168.218.150", DstPort: "22", Enabled: true}},
	})
	rc := NewReconciler(NewNftablesBackend(NewFakeExecutor()), []int{22}, time.Second, time.Second)
	rc.lastGood = &DesiredState{
		Forwards: []ForwardSpec{{ID: 2, Protocol: "tcp", ListenPort: "12022", DstIP: "192.168.218.150", DstPort: "22", Enabled: true}},
	}
	// 新状态 = 基线 + 一个普通限速 → 增量不含 SSH → 不高危
	st := &DesiredState{
		Forwards: []ForwardSpec{{ID: 2, Protocol: "tcp", ListenPort: "12022", DstIP: "192.168.218.150", DstPort: "22", Enabled: true}},
		RateLimits: []RateLimitSpec{{ID: 1, Name: "web", Proto: "tcp", Port: "80", PerSrc: true, Rate: 30, RateUnit: "minute", Burst: 5, Enabled: true}},
	}
	res, err := rc.Sync(context.Background(), st, "192.168.218.1")
	if err != nil {
		t.Fatal(err)
	}
	if res.Danger {
		t.Fatalf("未触及 SSH 的增量不应判危: %+v", res)
	}

	// 新状态新增一条 SSH 转发 → 高危
	st2 := &DesiredState{
		Forwards: []ForwardSpec{
			{ID: 2, Protocol: "tcp", ListenPort: "12022", DstIP: "192.168.218.150", DstPort: "22", Enabled: true},
			{ID: 3, Protocol: "tcp", ListenPort: "2222", DstIP: "10.0.0.9", DstPort: "22", Enabled: true},
		},
	}
	res2, err := rc.Sync(context.Background(), st2, "192.168.218.1")
	if err != nil {
		t.Fatal(err)
	}
	if !res2.Danger {
		t.Fatal("新增 SSH 转发应判危")
	}
	_ = base
}
