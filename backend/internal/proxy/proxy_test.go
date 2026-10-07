package proxy

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
)

func testLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func TestRoutingAndReload(t *testing.T) {
	// 测试上游：回显收到的 Host 与路径
	var mu sync.Mutex
	seen := map[string]string{}
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		seen[r.Host] = r.URL.Path
		mu.Unlock()
		w.Header().Set("X-Upstream", "yes")
		w.WriteHeader(200)
		_, _ = w.Write([]byte("UPSTREAM_OK"))
	}))
	defer up.Close()

	m := NewManager(testLogger(), Options{
		Mode:      ModeBuiltin,
		HTTPAddr:  ":0",
		HTTPSAddr: ":0",
		CertDir:   t.TempDir(),
	})
	// 不真正监听（avoid 占用端口）：直接构造内部状态测 ServeHTTP
	err := m.Reload(context.Background(), []DomainConfig{
		{
			ID: 1, Domain: "app.example.com", Enabled: true, TLSMode: "none",
			Routes: []RouteConfig{
				{ID: 11, PathMatch: "/", UpstreamURL: up.URL, Enabled: true},
				{ID: 12, PathMatch: "/api", UpstreamURL: up.URL, Enabled: true},
			},
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	// 精确前缀路由命中
	req := httptest.NewRequest("GET", "http://app.example.com/api/users", nil)
	rec := httptest.NewRecorder()
	m.ServeHTTP(rec, req)
	if rec.Body.String() != "UPSTREAM_OK" || rec.Header().Get("X-Upstream") != "yes" {
		t.Fatalf("反代未命中上游: %d %s", rec.Code, rec.Body.String())
	}
	mu.Lock()
	if seen["app.example.com"] != "/api/users" {
		t.Errorf("路径透传错误: %v", seen)
	}
	mu.Unlock()

	// 未配置域名 → 404
	req2 := httptest.NewRequest("GET", "http://other.example.com/x", nil)
	rec2 := httptest.NewRecorder()
	m.ServeHTTP(rec2, req2)
	if rec2.Code != 404 {
		t.Errorf("未配置域名应 404，得到 %d", rec2.Code)
	}

	// 热重载：禁用域名 → 404
	err = m.Reload(context.Background(), []DomainConfig{
		{ID: 1, Domain: "app.example.com", Enabled: false, TLSMode: "none"},
	})
	if err != nil {
		t.Fatal(err)
	}
	rec3 := httptest.NewRecorder()
	m.ServeHTTP(rec3, httptest.NewRequest("GET", "http://app.example.com/", nil))
	if rec3.Code != 404 {
		t.Errorf("禁用域名应 404，得到 %d", rec3.Code)
	}
}

func TestNormalizePathMatch(t *testing.T) {
	cases := map[string]string{
		"":         "/",
		"/":        "/",
		"api":      "/api",
		"/api/":    "/api",
		"/api/v2/": "/api/v2",
	}
	for in, want := range cases {
		if got := normalizePathMatch(in); got != want {
			t.Errorf("normalizePathMatch(%q)=%q, want %q", in, got, want)
		}
	}
}

func TestRouteBuildValidation(t *testing.T) {
	if _, err := buildRouteForTest("ftp://x"); err == nil {
		t.Error("非 http(s) 上游应报错")
	}
	if _, err := buildRouteForTest("http://"); err == nil {
		t.Error("缺主机应报错")
	}
}

func buildRouteForTest(upstream string) (*routeEntry, error) {
	m := &Manager{}
	return m.buildRoute(RouteConfig{ID: 1, PathMatch: "/", UpstreamURL: upstream, Enabled: true})
}
