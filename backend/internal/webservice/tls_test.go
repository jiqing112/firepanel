package webservice

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"io"
	"log/slog"
	"math/big"
	"net"
	"net/http"
	"strconv"
	"testing"
	"time"
)

// selfSignedPEMs 生成自签证书（SAN 覆盖 host 与 127.0.0.1），返回 PEM。
func selfSignedPEMs(t *testing.T, host string) (certPEM, keyPEM string) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tpl := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: host},
		DNSNames:     []string{host},
		IPAddresses:  []net.IP{net.ParseIP("127.0.0.1")},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	der, err := x509.CreateCertificate(rand.Reader, tpl, tpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	cert := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	keyDER, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	k := pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER})
	return string(cert), string(k)
}

// TestTLSListenerSniff：TLS 端口嗅探——HTTPS+SNI 命中站点；明文请求按
// plain_http=serve 直接提供；拓扑显示监听运行中。
func TestTLSListenerSniff(t *testing.T) {
	certPEM, keyPEM := selfSignedPEMs(t, "tls.example.com")
	up := newUpstream(t, "TLS_OK")
	m, ports := engine(t, ListenerConfig{
		ID: 1, Enable: true, Port: 80, TLS: true, MinTLS: "1.2", PlainHTTP: "serve",
		Sites: []Site{{
			ID: 1, Domains: []string{"tls.example.com"}, Type: TypeProxy, Enabled: true,
			TLSMode: "manual", CertPEM: certPEM, KeyPEM: keyPEM,
			Targets: []string{up.srv.URL}, ForwardedHeaders: true,
		}},
	})

	// URL 主机名是 127.0.0.1，SNI 需显式指定为站点域名；拨号地址覆盖回本机
	dial := func(ctx context.Context, network, addr string) (net.Conn, error) {
		var d net.Dialer
		return d.DialContext(ctx, network, "127.0.0.1:"+strconv.Itoa(ports[0]))
	}
	client := &http.Client{Transport: &http.Transport{
		TLSClientConfig: &tls.Config{InsecureSkipVerify: true, ServerName: "tls.example.com"},
		DialContext:     dial,
	}}

	// 1) HTTPS + SNI → 站点
	req, _ := http.NewRequest("GET", "https://tls.example.com:"+strconv.Itoa(ports[0])+"/x", nil)
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("HTTPS 请求失败: %v", err)
	}
	if b := body(t, resp); b != "TLS_OK" {
		t.Fatalf("HTTPS 应命中站点: %d %s", resp.StatusCode, b)
	}

	// 2) 明文 HTTP 打到 TLS 端口（plain_http=serve）→ 同样提供站点
	req2, _ := http.NewRequest("GET", "http://127.0.0.1:"+strconv.Itoa(ports[0])+"/y", nil)
	req2.Host = "tls.example.com"
	resp2, err := http.DefaultClient.Do(req2)
	if err != nil {
		t.Fatalf("明文请求失败: %v", err)
	}
	if b := body(t, resp2); b != "TLS_OK" {
		t.Fatalf("plain_http=serve 应提供站点: %d %s", resp2.StatusCode, b)
	}

	// 3) 拓扑里监听 running、站点 running
	topo := m.Topology(nowUnix(), "", netipAddr())
	if len(topo.Listeners) != 1 || !topo.Listeners[0].Running {
		t.Fatalf("监听应 running: %+v", topo.Listeners)
	}
	if len(topo.Listeners[0].Sites) != 1 || !topo.Listeners[0].Sites[0].Running {
		t.Fatalf("站点应 running: %+v", topo.Listeners[0].Sites)
	}
	// plain_http=serve 时无 redirect 动作
	for _, a := range topo.Listeners[0].Actions {
		if a.Kind == "redirect" {
			t.Fatalf("plain_http=serve 不应有 redirect 动作: %+v", topo.Listeners[0].Actions)
		}
	}
}

// TestTLSPlainRedirectAndExtraHTTPPort：明文跳转 + 额外 HTTP 端口跳转 + 动作计数。
func TestTLSPlainRedirectAndExtraHTTPPort(t *testing.T) {
	certPEM, keyPEM := selfSignedPEMs(t, "red.example.com")
	up := newUpstream(t, "R")
	m, ports := engine(t, ListenerConfig{
		ID: 1, Enable: true, Port: 80, TLS: true, PlainHTTP: "redirect",
		HTTPPort: 80, HTTPPortMode: "redirect",
		Sites: []Site{{
			ID: 1, Domains: []string{"red.example.com"}, Type: TypeProxy, Enabled: true,
			TLSMode: "manual", CertPEM: certPEM, KeyPEM: keyPEM,
			Targets: []string{up.srv.URL},
		}},
	})

	noRedirect := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}

	// 1) 明文打到 TLS 端口 → 301 https://host/...
	req, _ := http.NewRequest("GET", "http://127.0.0.1:"+strconv.Itoa(ports[0])+"/a", nil)
	req.Host = "red.example.com"
	resp, err := noRedirect.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	body(t, resp)
	if resp.StatusCode != 301 {
		t.Fatalf("明文应 301: %d", resp.StatusCode)
	}
	if loc := resp.Header.Get("Location"); loc != "https://red.example.com/a" {
		t.Fatalf("跳转目标错误: %q", loc)
	}

	// 2) 额外 HTTP 端口 → 301 到主端口 https
	req2, _ := http.NewRequest("GET", "http://127.0.0.1:"+strconv.Itoa(ports[1])+"/b?x=1", nil)
	req2.Host = "red.example.com"
	resp2, err := noRedirect.Do(req2)
	if err != nil {
		t.Fatal(err)
	}
	body(t, resp2)
	if resp2.StatusCode != 301 {
		t.Fatalf("额外 HTTP 端口应 301: %d", resp2.StatusCode)
	}
	want := "https://red.example.com:" + strconv.Itoa(ports[0]) + "/b?x=1"
	if loc := resp2.Header.Get("Location"); loc != want {
		t.Fatalf("额外端口跳转目标错误: got %q want %q", loc, want)
	}

	// 3) 跳转计入监听级 redirect 动作
	topo := m.Topology(nowUnix(), "", netipAddr())
	n := int64(0)
	for _, l := range topo.Listeners {
		for _, a := range l.Actions {
			if a.Kind == "redirect" {
				n += a.Requests
			}
		}
	}
	if n < 2 {
		t.Fatalf("redirect 动作应计数 >=2: %d", n)
	}
}

// TestListenerRetryAfterFailedStart：启动失败（端口占用）后，再次 Reload
// 应重试启动并在端口可用时成功。
func TestListenerRetryAfterFailedStart(t *testing.T) {
	// 先占住一个端口（通配绑定，与引擎的绑定形态一致，冲突跨平台成立）
	blocker, err := net.Listen("tcp", ":0")
	if err != nil {
		t.Fatal(err)
	}
	port := blocker.Addr().(*net.TCPAddr).Port

	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	m := New(log, Options{Certs: &fakeCerts{}})
	t.Cleanup(m.Stop)

	cfg := ListenerConfig{
		ID: 42, Enable: true, Port: port, TLS: false,
		Sites: []Site{{
			ID: 1, Domains: []string{"busy.example.com"}, Type: TypeText, Enabled: true,
			TLSMode: "none", Text: "ok", StatusCode: 200,
		}},
	}
	// 端口被占用 → 启动失败
	if err := m.Reload(context.Background(), []ListenerConfig{cfg}); err == nil {
		t.Fatal("端口被占用应启动失败")
	}
	// 释放端口后再次 Reload → 应成功启动
	if err := blocker.Close(); err != nil {
		t.Fatal(err)
	}
	if err := m.Reload(context.Background(), []ListenerConfig{cfg}); err != nil {
		t.Fatalf("端口释放后应启动成功: %v", err)
	}
	if !m.Running() {
		t.Fatal("监听应 running")
	}
}
