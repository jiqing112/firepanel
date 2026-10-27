package api

import (
	"strings"
	"testing"

	"firepanel/internal/store"
)

func TestValidPathMatch(t *testing.T) {
	cases := []struct {
		path string
		want bool
	}{
		{"/", true},
		{"/api", true},
		{"/a/b-c_d", true},
		{"api", false},        // 缺少前导 /
		{"/a\nb", false},      // 换行可闭合 Caddyfile 指令
		{"/a{b}", false},      // 花括号可闭合站点块
		{"/a#comment", false}, // 注释符
		{`/a"b`, false},       // 引号
		{`/a\b`, false},       // 反斜杠
		{"", false},
	}
	for _, c := range cases {
		if got := validPathMatch(c.path); got != c.want {
			t.Errorf("validPathMatch(%q)=%v，期望 %v", c.path, got, c.want)
		}
	}
}

// 非法转发目标必须在写库前被拦下（恢复是全量替换，先校验后写）。
func TestValidateBundleRejectsBadForward(t *testing.T) {
	b := &store.Bundle{Forwards: []store.ForwardRule{
		{Name: "bad", Protocol: "tcp", ListenPort: "80", DstIP: "999.1.1.1", DstPort: "80"},
	}}
	if err := validateBundle(b); err == nil {
		t.Fatal("非法目标 IP 应被拒绝")
	}
}

func TestValidateBundleRejectsInjectedRoutePath(t *testing.T) {
	b := &store.Bundle{
		RPDomains: []store.RPDomainBackup{{RPDomain: store.RPDomain{ID: 1, Domain: "example.com", TLSMode: "none"}}},
		RPRoutes: []store.RPRoute{{
			DomainID: 1, PathMatch: "/a } evil", UpstreamURL: "http://127.0.0.1:8080",
		}},
	}
	err := validateBundle(b)
	if err == nil || !strings.Contains(err.Error(), "路径") {
		t.Fatalf("非法路径应被拒绝，得到 %v", err)
	}
}

func TestValidateBundleAcceptsValidBundle(t *testing.T) {
	b := &store.Bundle{
		Forwards: []store.ForwardRule{
			{Name: "web", Protocol: "tcp", ListenPort: "80", DstIP: "192.168.1.10", DstPort: "8080"},
			{Name: "ddns", Protocol: "both", ListenPort: "443", DstDomain: "nas.example.net", DstPort: "443"},
		},
		IPLists: []store.IPListEntry{
			{ListType: "black", IPOrCIDR: "203.0.113.9"},
			{ListType: "white", IPOrCIDR: "10.0.0.0/8"},
		},
		RPDomains: []store.RPDomainBackup{{RPDomain: store.RPDomain{ID: 1, Domain: "example.com", TLSMode: "auto"}}},
		RPRoutes:  []store.RPRoute{{DomainID: 1, PathMatch: "/api", UpstreamURL: "http://127.0.0.1:9000"}},
	}
	if err := validateBundle(b); err != nil {
		t.Fatalf("合法备份不应被拒绝: %v", err)
	}
}
