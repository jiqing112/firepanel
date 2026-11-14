package webservice

import (
	"context"
	"crypto/tls"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

func testLogger() *logger {
	return &logger{log: slog.New(slog.NewTextHandler(io.Discard, nil))}
}

// testUpstream 测试服务：回显收到的 Host、路径与转发头。
type testUpstream struct {
	srv   *httptest.Server
	mu    sync.Mutex
	seens []string
}

func newUpstream(t *testing.T, body string) *testUpstream {
	u := &testUpstream{}
	u.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		u.mu.Lock()
		u.seens = append(u.seens, r.Host+" "+r.URL.Path+" xfwd="+r.Header.Get("X-Forwarded-For"))
		u.mu.Unlock()
		w.Header().Set("X-Upstream", "yes")
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(u.srv.Close)
	return u
}

func (u *testUpstream) seen() []string {
	u.mu.Lock()
	defer u.mu.Unlock()
	return append([]string{}, u.seens...)
}

type fakeCerts struct{}

func (f *fakeCerts) Manage(context.Context, string, string) error { return nil }
func (f *fakeCerts) GetCertificate(*tls.ClientHelloInfo) (*tls.Certificate, error) {
	return nil, errNoCert
}
func (f *fakeCerts) Expiry(string) (time.Time, bool)                     { return time.Time{}, false }
func (f *fakeCerts) HTTPChallengeHandler(next http.Handler) http.Handler { return next }

var errNoCert = fmt.Errorf("no cert")

func freePort(t *testing.T) int {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	p := ln.Addr().(*net.TCPAddr).Port
	_ = ln.Close()
	return p
}

// engine 构建真实监听的引擎（端口由测试分配），返回管理器与主端口。
func engine(t *testing.T, listeners ...ListenerConfig) (*Manager, []int) {
	t.Helper()
	ports := make([]int, len(listeners))
	for i := range listeners {
		ports[i] = freePort(t)
		listeners[i].Port = ports[i]
	}
	_ = ports
	m := New(slog.New(slog.NewTextHandler(io.Discard, nil)), Options{Certs: &fakeCerts{}})
	if err := m.Reload(context.Background(), listeners); err != nil {
		t.Fatalf("Reload: %v", err)
	}
	// 等监听就绪
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		ok := true
		for _, p := range ports {
			c, err := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", p), 100*time.Millisecond)
			if err != nil {
				ok = false
				break
			}
			_ = c.Close()
		}
		if ok {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Cleanup(m.Stop)
	return m, ports
}

// get 向引擎的监听端口发请求（Host 覆写为站点域名）。
var noRedirectClient = &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}

func get(t *testing.T, port int, host, path string) *http.Response {
	t.Helper()
	req, err := http.NewRequest("GET", fmt.Sprintf("http://127.0.0.1:%d%s", port, path), nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Host = host
	resp, err := noRedirectClient.Do(req)
	if err != nil {
		t.Fatalf("请求失败: %v", err)
	}
	return resp
}

func body(t *testing.T, resp *http.Response) string {
	t.Helper()
	defer resp.Body.Close()
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestProxyRouting(t *testing.T) {
	up := newUpstream(t, "UPSTREAM_OK")
	m, ports := engine(t, ListenerConfig{
		ID: 1, Enable: true, Port: 80, Sites: []Site{{
			ID: 1, Domains: []string{"app.example.com"}, Type: TypeProxy, Enabled: true, TLSMode: "none",
			ForwardedHeaders: true,
			Targets:          []string{up.srv.URL},
			Locations: []Location{
				{Prefix: "/api", Targets: []string{up.srv.URL}, StripPrefix: true},
			},
		}},
	})

	resp := get(t, ports[0], "app.example.com", "/users?id=1")
	if b := body(t, resp); b != "UPSTREAM_OK" || resp.Header.Get("X-Upstream") != "yes" {
		t.Fatalf("反代未命中上游: %d %s", resp.StatusCode, b)
	}
	seen := up.seen()
	if len(seen) == 0 || !strings.HasPrefix(seen[0], "app.example.com /users") {
		t.Fatalf("Host 与路径应保留（location 未命中）: %v", seen)
	}
	if !strings.Contains(seen[0], "xfwd=127.0.0.1") {
		t.Fatalf("开启转发头时应写入 X-Forwarded-For: %v", seen)
	}

	// 分路径 + 去前缀
	up2 := newUpstream(t, "API_OK")
	m.Reload(context.Background(), []ListenerConfig{{
		ID: 1, Enable: true, Port: ports[0], Sites: []Site{{
			ID: 1, Domains: []string{"app.example.com"}, Type: TypeProxy, Enabled: true, TLSMode: "none",
			Targets: []string{up.srv.URL},
			Locations: []Location{
				{Prefix: "/api", Targets: []string{up2.srv.URL}, StripPrefix: true},
			},
		}},
	}})
	time.Sleep(300 * time.Millisecond)
	resp = get(t, ports[0], "app.example.com", "/api/zyqd/abc")
	if b := body(t, resp); b != "API_OK" {
		t.Fatalf("分路径未命中: %d %s", resp.StatusCode, b)
	}
	seen = up2.seen()
	if len(seen) == 0 || !strings.HasPrefix(seen[0], "app.example.com /zyqd/abc") || strings.Contains(seen[0], "/api/zyqd") {
		t.Fatalf("去前缀错误: %v", seen)
	}
}

func TestRedirectAndTextAndClose(t *testing.T) {
	_, ports := engine(t, ListenerConfig{
		ID: 1, Enable: true, Port: 80, Sites: []Site{
			{ID: 1, Domains: []string{"old.example.com"}, Type: TypeRedirect, Enabled: true, TLSMode: "none",
				RedirectTo: "https://{host}.new.example.com", RedirectCode: 301, KeepPath: true},
			{ID: 2, Domains: []string{"txt.example.com"}, Type: TypeText, Enabled: true, TLSMode: "none",
				Text: "hello", StatusCode: 200, ContentType: "text/plain"},
			{ID: 3, Domains: []string{"scan.example.com"}, Type: TypeClose, Enabled: true, TLSMode: "none"},
		},
	})

	resp := get(t, ports[0], "old.example.com", "/a/b?x=1")
	bodyStr := body(t, resp)
	if resp.StatusCode != 301 {
		t.Fatalf("重定向状态码: %d %s", resp.StatusCode, bodyStr)
	}
	if loc := resp.Header.Get("Location"); loc != "https://old.example.com.new.example.com/a/b?x=1" {
		t.Fatalf("重定向目标错误: %q", loc)
	}

	resp = get(t, ports[0], "txt.example.com", "/")
	if b := body(t, resp); b != "hello" || resp.StatusCode != 200 {
		t.Fatalf("固定文本站点错误: %d %s", resp.StatusCode, b)
	}

	// 断开连接：连接被直接关闭（EOF/重置）
	req, _ := http.NewRequest("GET", fmt.Sprintf("http://127.0.0.1:%d/", ports[0]), nil)
	req.Host = "scan.example.com"
	_, err := http.DefaultClient.Do(req)
	if err == nil {
		t.Errorf("断开连接站点应导致连接被关闭")
	}
}

func TestDefaultSiteAndNotFoundAction(t *testing.T) {
	up := newUpstream(t, "APP_OK")
	_, ports := engine(t, ListenerConfig{
		ID: 1, Enable: true, Port: 80, Sites: []Site{
			{ID: 1, Domains: []string{"app.example.com"}, Type: TypeProxy, Enabled: true, TLSMode: "none", Targets: []string{up.srv.URL}},
			{ID: 2, Domains: []string{"*"}, Type: TypeText, Enabled: true, TLSMode: "none", Default: true,
				Text: "unknown", StatusCode: 404},
		},
	})
	resp0 := get(t, ports[0], "app.example.com", "/x")
	if b := body(t, resp0); b != "APP_OK" {
		t.Fatalf("精确域名应优先: %d %s", resp0.StatusCode, b)
	}
	resp1 := get(t, ports[0], "unknown.example.net", "/")
	if b := body(t, resp1); b != "unknown" {
		t.Fatalf("默认站点应兜底未知域名: %d %s", resp1.StatusCode, b)
	}

	// 无默认站点：未匹配域名 404 且计入监听级动作
	m2, ports2 := engine(t, ListenerConfig{
		ID: 2, Enable: true, Port: 80, Sites: []Site{
			{ID: 1, Domains: []string{"app.example.com"}, Type: TypeText, Enabled: true, TLSMode: "none", Text: "ok"},
		},
	})
	resp := get(t, ports2[0], "other.example.net", "/")
	if resp.StatusCode != 404 {
		t.Fatalf("未匹配域名应 404: %d", resp.StatusCode)
	}
	resp.Body.Close()
	topo := m2.Topology(time.Now().Unix(), "", netipAddr())
	found := false
	for _, l := range topo.Listeners {
		for _, a := range l.Actions {
			if a.Kind == "notFound" && a.Requests >= 1 {
				found = true
			}
		}
	}
	if !found {
		t.Fatalf("未匹配请求应计入监听级 notFound 动作")
	}
}

func TestWildcardDomain(t *testing.T) {
	up := newUpstream(t, "WILD")
	_, ports := engine(t, ListenerConfig{
		ID: 1, Enable: true, Port: 80, Sites: []Site{
			{ID: 1, Domains: []string{"*.a.example.com"}, Type: TypeProxy, Enabled: true, TLSMode: "none", Targets: []string{up.srv.URL}},
			{ID: 2, Domains: []string{"a.example.com"}, Type: TypeProxy, Enabled: true, TLSMode: "none", Targets: []string{up.srv.URL}},
		},
	})
	respA := get(t, ports[0], "x.a.example.com", "/")
	if b := body(t, respA); b != "WILD" {
		t.Fatalf("通配域名应命中: %d %s", respA.StatusCode, b)
	}
	respB := get(t, ports[0], "a.example.com", "/")
	if b := body(t, respB); b != "WILD" {
		t.Fatalf("精确域名应命中: %d %s", respB.StatusCode, b)
	}
	if resp := get(t, ports[0], "b.example.com", "/"); resp.StatusCode != 404 {
		t.Fatalf("未配置域名应 404: %d", resp.StatusCode)
	}
}

func TestRateLimit(t *testing.T) {
	up := newUpstream(t, "RL")
	m, ports := engine(t, ListenerConfig{
		ID: 1, Enable: true, Port: 80, Sites: []Site{{
			ID: 1, Domains: []string{"rl.example.com"}, Type: TypeProxy, Enabled: true, TLSMode: "none",
			Targets:   []string{up.srv.URL},
			RateLimit: RateLimit{Enable: true, Rate: 5, Burst: 1},
		}},
	})
	codes := map[int]int{}
	for i := 0; i < 20; i++ {
		resp := get(t, ports[0], "rl.example.com", "/")
		codes[resp.StatusCode]++
		resp.Body.Close()
	}
	if codes[200] == 0 || codes[429] == 0 {
		t.Fatalf("限流应同时出现 200 与 429: %v", codes)
	}
	// 限流拒绝计入 blocked 原因
	topo := m.Topology(time.Now().Unix(), "", netipAddr())
	var rl int64
	for _, l := range topo.Listeners {
		for _, s := range l.Sites {
			rl += s.Blocked["rateLimit"]
		}
	}
	if rl == 0 {
		t.Fatalf("限流拒绝应计入 blocked.rateLimit")
	}
}

func TestIPAccessControl(t *testing.T) {
	up := newUpstream(t, "ACL")
	m, ports := engine(t, ListenerConfig{
		ID: 1, Enable: true, Port: 80, Sites: []Site{{
			ID: 1, Domains: []string{"acl.example.com"}, Type: TypeProxy, Enabled: true, TLSMode: "none",
			Targets: []string{up.srv.URL},
			IPMode:  IPModeBlacklist, IPList: []string{"127.0.0.0/8"},
		}},
	})
	if resp := get(t, ports[0], "acl.example.com", "/"); resp.StatusCode != 403 {
		t.Fatalf("黑名单 IP 应 403: %d", resp.StatusCode)
	}
	m.Reload(context.Background(), []ListenerConfig{{
		ID: 1, Enable: true, Port: ports[0], Sites: []Site{{
			ID: 1, Domains: []string{"acl.example.com"}, Type: TypeProxy, Enabled: true, TLSMode: "none",
			Targets: []string{up.srv.URL},
			IPMode:  IPModeWhitelist, IPList: []string{"198.51.100.1"},
		}},
	}})
	time.Sleep(300 * time.Millisecond)
	if resp := get(t, ports[0], "acl.example.com", "/"); resp.StatusCode != 403 {
		t.Fatalf("白名单外 IP 应 403: %d", resp.StatusCode)
	}
}

func TestHotReloadInheritsStats(t *testing.T) {
	up := newUpstream(t, "S")
	m, ports := engine(t, ListenerConfig{
		ID: 1, Enable: true, Port: 80, Sites: []Site{
			{ID: 1, Domains: []string{"s.example.com"}, Type: TypeProxy, Enabled: true, TLSMode: "none", Targets: []string{up.srv.URL}},
		},
	})
	resp := get(t, ports[0], "s.example.com", "/")
	resp.Body.Close()
	if st := m.Status(); st.Sites[0].Requests != 1 {
		t.Fatalf("首次请求应计数: %v", st.Sites[0].Requests)
	}
	// 热重载：同 ID 站点沿用统计（端口必须与原监听一致才能走热换路径）
	m.Reload(context.Background(), []ListenerConfig{{
		ID: 1, Enable: true, Port: ports[0], Sites: []Site{{
			ID: 1, Domains: []string{"s.example.com"}, Type: TypeProxy, Enabled: true, TLSMode: "none", Targets: []string{up.srv.URL},
			Locations: []Location{{Prefix: "/x", Targets: []string{up.srv.URL}}},
		}},
	}})
	time.Sleep(300 * time.Millisecond)
	if st := m.Status(); st.Sites[0].Requests != 1 {
		t.Fatalf("热重载后统计应沿用: %v", st.Sites[0].Requests)
	}
}

func TestTopologyView(t *testing.T) {
	up := newUpstream(t, "T")
	m, ports := engine(t, ListenerConfig{
		ID: 7, Enable: true, Port: 80, TLS: false, Sites: []Site{{
			ID: 3, Domains: []string{"t.example.com"}, Type: TypeProxy, Enabled: true, TLSMode: "none",
			ForwardedHeaders: true, Targets: []string{up.srv.URL + " weight=2"},
		}},
	})
	resp := get(t, ports[0], "t.example.com", "/a")
	body(t, resp)

	topo := m.Topology(time.Now().Unix(), "", netipAddr())
	if len(topo.Listeners) != 1 {
		t.Fatalf("应有一个监听: %d", len(topo.Listeners))
	}
	l := topo.Listeners[0]
	if l.ID != "l:7" || len(l.Sites) != 1 {
		t.Fatalf("监听编号与站点数错误: %+v", l)
	}
	s := l.Sites[0]
	if s.ID != "s:7/3" || !s.Running || s.Requests < 1 {
		t.Fatalf("站点快照错误: %+v", s)
	}
	if s.Rate.QPS < 0 || len(s.Upstreams) != 1 {
		t.Fatalf("后端快照错误: %+v", s.Upstreams)
	}
	u := s.Upstreams[0]
	if u.ID != "u:7/3/0" || u.Weight != 2 || u.Healthy == nil || !*u.Healthy || u.Requests < 1 {
		t.Fatalf("后端详情错误: %+v", u)
	}
	if s.Status["2xx"] < 1 {
		t.Fatalf("状态分类应计数: %+v", s.Status)
	}
	// 转发头让后端看到访客地址，活动 IP 应收录该站点
	if s.Requests >= 1 && topo.IPTotal < 1 {
		t.Fatalf("活动 IP 应至少有一个: %d", topo.IPTotal)
	}
	// focus 曲线
	topo2 := m.Topology(time.Now().Unix(), s.ID, netipAddr())
	if len(topo2.Series) != 60 {
		t.Fatalf("focus 曲线应有 60 个点: %d", len(topo2.Series))
	}
}

func TestValidation(t *testing.T) {
	cases := []struct {
		name string
		site Site
	}{
		{"缺域名", Site{Type: TypeProxy, Targets: []string{"http://127.0.0.1:1"}}},
		{"坏域名", Site{Domains: []string{"bad domain"}, Type: TypeProxy, Targets: []string{"http://127.0.0.1:1"}}},
		{"代理缺后端", Site{Domains: []string{"a.com"}, Type: TypeProxy}},
		{"坏后端协议", Site{Domains: []string{"a.com"}, Type: TypeProxy, Targets: []string{"ftp://x"}}},
		{"坏参数", Site{Domains: []string{"a.com"}, Type: TypeProxy, Targets: []string{"http://x weight=0"}}},
		{"坏分路径", Site{Domains: []string{"a.com"}, Type: TypeProxy, Targets: []string{"http://x"}, Locations: []Location{{Prefix: "api"}}}},
		{"重定向缺目标", Site{Domains: []string{"a.com"}, Type: TypeRedirect}},
		{"坏头", Site{Domains: []string{"a.com"}, Type: TypeText, RequestHeaders: []string{"Bad Header"}}},
	}
	for _, c := range cases {
		if err := c.site.Validate(); err == nil {
			t.Errorf("%s: 应报错", c.name)
		}
	}
	ok := Site{Domains: []string{"a.com", "*.b.com", "192.168.1.1"}, Type: TypeProxy, Targets: []string{"http://x:80 weight=2 backup"}, TLSMode: "none"}
	if err := ok.Validate(); err != nil {
		t.Errorf("合法站点不应报错: %v", err)
	}
	if ok.Domains[1] != "*.b.com" || ok.Domains[2] != "192.168.1.1" {
		t.Errorf("域名规范化错误: %v", ok.Domains)
	}
	// 监听校验
	if err := (ListenerConfig{Port: 0}).Validate(); err == nil {
		t.Errorf("端口 0 应报错")
	}
	if err := (ListenerConfig{Port: 80, HTTPPort: 80}).Validate(); err == nil {
		t.Errorf("HTTP 端口与主端口相同应报错")
	}
}

func TestHeaderRewrite(t *testing.T) {
	var mu sync.Mutex
	var seen []http.Header
	upSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		seen = append(seen, r.Header.Clone())
		mu.Unlock()
		_, _ = w.Write([]byte("H"))
	}))
	defer upSrv.Close()
	_, ports := engine(t, ListenerConfig{
		ID: 1, Enable: true, Port: 80, Sites: []Site{{
			ID: 1, Domains: []string{"h.example.com"}, Type: TypeProxy, Enabled: true, TLSMode: "none",
			Targets:        []string{upSrv.URL},
			RequestHeaders: []string{"X-Req: added", "User-Agent:"},
		}},
	})
	resp := get(t, ports[0], "h.example.com", "/")
	resp.Body.Close()
	mu.Lock()
	defer mu.Unlock()
	if len(seen) != 1 {
		t.Fatalf("上游应收到 1 个请求: %d", len(seen))
	}
	if seen[0].Get("X-Req") != "added" {
		t.Fatalf("请求头应被写入: %v", seen[0])
	}
	if seen[0].Get("User-Agent") != "" {
		t.Fatalf("空值头应被删除: %v", seen[0])
	}
}
