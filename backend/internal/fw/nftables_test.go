package fw

import (
	"encoding/json"
	"strings"
	"testing"
)

// rlFromExprs 用表达式 JSON 数组构造 nftRuleJSON（模拟 nft -j 真实输出片段）。
func rlFromExprs(t *testing.T, exprsJSON string) nftRuleJSON {
	t.Helper()
	var exprs []json.RawMessage
	if err := json.Unmarshal([]byte(exprsJSON), &exprs); err != nil {
		t.Fatalf("表达式解析失败: %v", err)
	}
	return nftRuleJSON{Chain: "input", Expr: exprs}
}

// TestParseNftVMShapes 用 VM（nft 1.1.3 / Debian 13）上捕获的真实 JSON 形态逐条验证。
func TestParseNftVMShapes(t *testing.T) {
	cases := []struct {
		name     string
		exprs    string
		wantSpec []string // spec 必须包含的片段
	}{
		{
			name:  "saddr 黑名单",
			exprs: `[{"match":{"op":"==","left":{"payload":{"protocol":"ip","field":"saddr"}},"right":"192.0.2.55"}},{"counter":{"packets":0,"bytes":0}},{"drop":null}]`,
			wantSpec: []string{"ip saddr 192.0.2.55", "drop"},
		},
		{
			name:  "MASQ 前缀对象 + oifname + masquerade",
			exprs: `[{"match":{"op":"==","left":{"payload":{"protocol":"ip","field":"saddr"}},"right":{"prefix":{"addr":"192.168.218.0","len":24}}}},{"match":{"op":"==","left":{"payload":{"protocol":"oifname","field":""}},"right":"ens18"}},{"counter":{"packets":0,"bytes":0}},{"masquerade":null}]`,
			wantSpec: []string{"ip saddr 192.168.218.0/24", "oifname ens18", "masquerade"},
		},
		{
			name:  "DNAT 端口",
			exprs: `[{"match":{"op":"==","left":{"payload":{"protocol":"tcp","field":"dport"}},"right":18080}},{"counter":{"packets":0,"bytes":0}},{"dnat":{"addr":"127.0.0.1","port":18081,"family":"ip"}}]`,
			wantSpec: []string{"tcp dport 18080", "dnat to 127.0.0.1:18081"},
		},
		{
			name:  "FORWARD accept",
			exprs: `[{"match":{"op":"==","left":{"payload":{"protocol":"ip","field":"daddr"}},"right":"127.0.0.1"}},{"match":{"op":"==","left":{"payload":{"protocol":"tcp","field":"dport"}},"right":18081}},{"counter":{"packets":0,"bytes":0}},{"accept":null}]`,
			wantSpec: []string{"ip daddr 127.0.0.1", "tcp dport 18081", "accept"},
		},
		{
			name:  "限速 meter+limit",
			exprs: `[{"match":{"op":"==","left":{"payload":{"protocol":"tcp","field":"dport"}},"right":80}},{"meter":{"key":1,"name":"fp1_tcp"}},{"limit":{"rate":30,"unit":"minute","burst":10,"per":"ip"}},{"counter":{"packets":0,"bytes":0}},{"drop":null}]`,
			wantSpec: []string{"tcp dport 80", "limit rate over 30/minute", "drop"},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rl := rlFromExprs(t, tc.exprs)
			rl.Comment = "fp:test:1:1"
			kr := parseNftRuleJSON("input", rl)
			for _, want := range tc.wantSpec {
				if !strings.Contains(kr.Spec, want) {
					t.Errorf("spec 缺少 %q: %s", want, kr.Spec)
				}
			}
			if kr.Tag != "fp:test:1:1" {
				t.Errorf("tag=%q", kr.Tag)
			}
		})
	}
}

// TestSemMASQPrefix 回归：MASQ 前缀对象形态不得再误报漂移。
func TestSemMASQPrefix(t *testing.T) {
	expected := Rule{
		Tag: "fp:nat:1:1", Chain: ChainSrcNat, Family: "ipv4",
		Src: "192.168.218.0/24", OutIface: "ens18", Action: "masquerade",
	}
	kr := KernelRule{
		Chain: ChainSrcNat, Tag: "fp:nat:1:1",
		Spec: `ip saddr 192.168.218.0/24 oifname ens18 masquerade`,
	}
	if !ruleEquivalent(kr, expected) {
		t.Fatalf("MASQ 规则应判定一致: kernel=%q", kr.Spec)
	}
}

// TestSemMASQPrefixObjectForm 用解析器真实输出（含前缀对象归一化前的原始形态）验证。
func TestSemMASQPrefixObjectForm(t *testing.T) {
	exprs := `[{"match":{"op":"==","left":{"payload":{"protocol":"ip","field":"saddr"}},"right":{"prefix":{"addr":"192.168.218.0","len":24}}}},{"match":{"op":"==","left":{"payload":{"protocol":"oifname","field":""}},"right":"ens18"}},{"counter":{"packets":0,"bytes":0}},{"masquerade":null}]`
	rl := rlFromExprs(t, exprs)
	rl.Comment = "fp:nat:1:1"
	kr := parseNftRuleJSON(ChainSrcNat, rl)

	expected := Rule{
		Tag: "fp:nat:1:1", Chain: ChainSrcNat, Family: "ipv4",
		Src: "192.168.218.0/24", OutIface: "ens18", Action: "masquerade",
	}
	if !ruleEquivalent(*kr, expected) {
		t.Fatalf("解析后仍误报漂移: spec=%q", kr.Spec)
	}
}
