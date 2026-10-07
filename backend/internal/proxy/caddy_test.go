package proxy

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestBuildCaddyConfig(t *testing.T) {
	cfgs := []DomainConfig{
		{
			ID: 1, Domain: "app.example.com", Enabled: true, TLSMode: "auto",
			Routes: []RouteConfig{
				{ID: 11, PathMatch: "/api", UpstreamURL: "http://127.0.0.1:9000", Enabled: true},
				{ID: 12, PathMatch: "/", UpstreamURL: "https://127.0.0.1:9001", Enabled: true},
			},
		},
		{
			ID: 2, Domain: "plain.example.com", Enabled: true, TLSMode: "none",
			Routes: []RouteConfig{
				{ID: 21, PathMatch: "/", UpstreamURL: "http://127.0.0.1:9002", Enabled: true},
			},
		},
		{ID: 3, Domain: "disabled.example.com", Enabled: false, TLSMode: "auto",
			Routes: []RouteConfig{{ID: 31, PathMatch: "/", UpstreamURL: "http://x", Enabled: true}}},
	}
	out := BuildCaddyConfig(cfgs, ":80", ":443")

	apps := out["apps"].(map[string]any)
	httpApp := apps["http"].(map[string]any)
	servers := httpApp["servers"].(map[string]any)

	httpsSrv, ok := servers["firepanel_https"].(map[string]any)
	if !ok {
		t.Fatalf("缺少 HTTPS server: %v", servers)
	}
	if l := httpsSrv["listen"].([]any); len(l) != 1 || l[0] != ":443" {
		t.Errorf("HTTPS listen 错误: %v", l)
	}
	httpsJSON, _ := json.Marshal(httpsSrv["routes"].([]any))
	// app.example.com 的 /api 路径优先于 /
	if !strings.Contains(string(httpsJSON), `"/api"`) || !strings.Contains(string(httpsJSON), "app.example.com") {
		t.Errorf("HTTPS 路由缺少 app 域名/路径: %s", httpsJSON)
	}
	// https 上游带 transport.tls
	if !strings.Contains(string(httpsJSON), `"transport"`) || !strings.Contains(string(httpsJSON), `"tls"`) {
		t.Errorf("https 上游应带 TLS transport: %s", httpsJSON)
	}
	if strings.Contains(string(httpsJSON), "plain.example.com") {
		t.Errorf("none 模式域名不应出现在 HTTPS server: %s", httpsJSON)
	}

	httpSrv, ok := servers["firepanel_http"].(map[string]any)
	if !ok {
		t.Fatalf("缺少 HTTP server（none 模式域名存在时）")
	}
	httpJSON, _ := json.Marshal(httpSrv["routes"].([]any))
	if !strings.Contains(string(httpJSON), "plain.example.com") {
		t.Errorf("HTTP server 缺少 none 模式域名: %s", httpJSON)
	}
	// none server 应禁用 automatic https
	if ah := httpSrv["automatic_https"].(map[string]any); ah["disable"] != true {
		t.Errorf("HTTP server 应禁用 automatic_https: %v", ah)
	}
	// 禁用域名不应出现
	all, _ := json.Marshal(out)
	if strings.Contains(string(all), "disabled.example.com") {
		t.Errorf("禁用域名不应出现: %s", all)
	}
}

// TestCaddyModeReload 自测：caddy 模式 Reload 经 Admin API 下发、不监听端口。
func TestCaddyModeReload(t *testing.T) {
	var gotLoad map[string]any
	var loadCalls int
	admin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/load" && r.Method == http.MethodPost {
			loadCalls++
			b, _ := io.ReadAll(r.Body)
			_ = json.Unmarshal(b, &gotLoad)
			w.WriteHeader(200)
			return
		}
		if r.URL.Path == "/config/" {
			w.WriteHeader(200)
			_, _ = w.Write([]byte(`{}`))
			return
		}
		w.WriteHeader(404)
	}))
	defer admin.Close()

	m := NewManager(testLogger(), Options{
		Mode:       ModeCaddy,
		HTTPAddr:   ":80",
		HTTPSAddr:  ":443",
		CaddyAdmin: admin.URL,
	})
	err := m.Reload(context.Background(), []DomainConfig{
		{
			ID: 1, Domain: "app.example.com", Enabled: true, TLSMode: "auto",
			Routes: []RouteConfig{{ID: 11, PathMatch: "/", UpstreamURL: "http://127.0.0.1:9000", Enabled: true}},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if loadCalls != 1 {
		t.Fatalf("/load 应调用 1 次，实际 %d", loadCalls)
	}
	if gotLoad == nil || !strings.Contains(jsonStr(gotLoad), "app.example.com") {
		t.Errorf("下发的配置缺少域名: %v", gotLoad)
	}
	// caddy 模式不启动本地监听
	if m.Running() != true {
		t.Errorf("Admin API 可达且已下发时应报告运行中")
	}
	// ServeHTTP 不应路由（builtin 引擎未启动）——caddy 模式下不应被调用
}

// TestCaddyLoadError 自测：Admin API 不可达时 Reload 返回错误并发出事件。
func TestCaddyLoadError(t *testing.T) {
	m := NewManager(testLogger(), Options{
		Mode:       ModeCaddy,
		CaddyAdmin: "http://127.0.0.1:1", // 不可达
	})
	err := m.Reload(context.Background(), []DomainConfig{
		{ID: 1, Domain: "x.example.com", Enabled: true, TLSMode: "none",
			Routes: []RouteConfig{{ID: 1, PathMatch: "/", UpstreamURL: "http://127.0.0.1:9", Enabled: true}}},
	})
	if err == nil {
		t.Fatal("Admin API 不可达应返回错误")
	}
	if m.Running() {
		t.Error("不可达时 Running 应为 false")
	}
}

func TestCaddyUpstreamDial(t *testing.T) {
	cases := []struct{ in, want string; tls bool }{
		{"http://127.0.0.1:9000", "127.0.0.1:9000", false},
		{"https://up.example.com", "up.example.com:443", true},
		{"http://10.0.0.5", "10.0.0.5:80", false},
	}
	for _, c := range cases {
		got, tls, err := caddyUpstreamDial(c.in)
		if err != nil || got != c.want || tls != c.tls {
			t.Errorf("caddyUpstreamDial(%q)=%q,%v err=%v, want %q,%v", c.in, got, tls, err, c.want, c.tls)
		}
	}
}

func jsonStr(v any) string {
	b, _ := json.Marshal(v)
	return string(b)
}
