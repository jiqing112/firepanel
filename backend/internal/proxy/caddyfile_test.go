package proxy

import (
	"strings"
	"testing"
)

func TestBuildCaddyfile(t *testing.T) {
	cfgs := []DomainConfig{
		{
			Domain: "app.example.com", Enabled: true, TLSMode: "auto",
			Routes: []RouteConfig{
				{ID: 1, PathMatch: "/api", UpstreamURL: "http://127.0.0.1:9000", Enabled: true},
				{ID: 2, PathMatch: "/", UpstreamURL: "http://127.0.0.1:8080", Enabled: true},
			},
		},
		{
			Domain: "plain.local", Enabled: true, TLSMode: "none", CaddyOpts: `{"encode":false,"security_headers":true,"log_access":false}`,
			Routes: []RouteConfig{{ID: 3, PathMatch: "/", UpstreamURL: "https://127.0.0.1:8443", Enabled: true}},
		},
		{
			Domain: "disabled.example.com", Enabled: false,
			Routes: []RouteConfig{{PathMatch: "/", UpstreamURL: "http://127.0.0.1:1", Enabled: true}},
		},
	}
	out := BuildCaddyfile(cfgs, CaddyGlobalSettings{Email: "ops@example.com"}, "")

	checks := []struct{ name, want string }{
		{"全局 email", "\temail ops@example.com\n"},
		{"无 disabled 站点", "disabled.example.com"},
		{"none 站点 http:// 前缀", "http://plain.local {"},
		{"auto 站点地址", "app.example.com {"},
		{"压缩", "\tencode zstd gzip\n"},
		{"安全头", "Strict-Transport-Security"},
		{"访问日志", "/var/log/caddy/fp-app.example.com.log"},
		{"根路由无 matcher", "\treverse_proxy 127.0.0.1:8080\n"},
		{"路径路由 matcher", "\treverse_proxy /api/* 127.0.0.1:9000\n"},
		{"https 上游块", "\t\t\ttls\n"},
	}
	for _, c := range checks {
		shouldContain := !strings.Contains(c.name, "disabled")
		if shouldContain && !strings.Contains(out, c.want) {
			t.Errorf("期望包含 %q（%s）\n生成内容:\n%s", c.want, c.name, out)
		}
	}
	if strings.Contains(out, "disabled.example.com") {
		t.Errorf("停用域名不应出现在 Caddyfile 中")
	}
	// plain.local 关了压缩与日志
	plain := out[strings.Index(out, "plain.local"):]
	if strings.Contains(plain, "encode") {
		t.Errorf("plain.local 不应有 encode")
	}
	if strings.Contains(plain, "log {") {
		t.Errorf("plain.local 不应有访问日志")
	}
	// https 上游必须在 plain.local 块内
	if !strings.Contains(plain, "tls") {
		t.Errorf("plain.local 的 https 上游应有 tls transport")
	}
	// 路径优先级：/api 应出现在 / 之前
	api := strings.Index(out, "/api/*")
	root := strings.Index(out, "reverse_proxy 127.0.0.1:8080")
	if api < 0 || root < 0 || api > root {
		t.Errorf("路径最长前缀应排在根路由之前")
	}
}

func TestCaddySiteOptsDefaults(t *testing.T) {
	o := parseCaddySiteOpts("")
	if !o.Encode || !o.SecurityHeaders || !o.LogAccess {
		t.Errorf("空 opts 应全默认开: %+v", o)
	}
	o = parseCaddySiteOpts(`{"encode":false}`)
	if o.Encode || !o.SecurityHeaders {
		t.Errorf("部分覆盖错误: %+v", o)
	}
}

func TestBuildCaddyfileCustom(t *testing.T) {
	cfgs := []DomainConfig{{Domain: "app.example.com", Enabled: true, TLSMode: "auto",
		Routes: []RouteConfig{{PathMatch: "/", UpstreamURL: "http://127.0.0.1:8080", Enabled: true}}}}
	custom := "(blocked) {\n\trespond 403\n}\n\nmetrics.local {\n\trespond \"ok\"\n}"
	out := BuildCaddyfile(cfgs, CaddyGlobalSettings{}, custom)
	for _, want := range []string{"自定义配置（面板保留此段", "(blocked) {", "metrics.local {"} {
		if !strings.Contains(out, want) {
			t.Errorf("自定义片段未包含 %q\n输出:\n%s", want, out)
		}
	}
	// 自定义片段必须在生成站点块之后
	if strings.LastIndex(out, "app.example.com {") > strings.Index(out, "自定义配置") {
		t.Errorf("自定义片段应追加在生成内容之后")
	}
	// 空片段不输出分隔注释
	if out2 := BuildCaddyfile(cfgs, CaddyGlobalSettings{}, "  \n"); strings.Contains(out2, "自定义配置") {
		t.Errorf("空自定义片段不应输出分隔注释")
	}
}
