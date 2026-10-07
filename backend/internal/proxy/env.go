package proxy

import (
	"context"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"firepanel/internal/sysinfo"
)

// Runner 探测占用进程时执行系统命令的接口（与 fw.Executor 结构兼容）。
type Runner interface {
	Run(ctx context.Context, name string, args ...string) (string, error)
}

// PortProbe 一个端口的可绑定状态。
type PortProbe struct {
	Port     int    `json:"port"`
	Bindable bool   `json:"bindable"`
	Occupier string `json:"occupier"` // 占用进程名（可探测到时）
	Detail   string `json:"detail"`   // 不可绑定时的原因
}

// ProbePort 试绑 TCP 端口判断可用性；不可绑定时尝试用 ss 找占用进程。
func ProbePort(port int, runner Runner) PortProbe {
	p := PortProbe{Port: port}
	ln, err := net.Listen("tcp4", fmt.Sprintf(":%d", port))
	if err == nil {
		_ = ln.Close()
		p.Bindable = true
		return p
	}
	p.Detail = err.Error()
	if ope, ok := err.(*net.OpError); ok && ope.Op == "listen" {
		p.Detail = "端口被占用"
	}
	if runner != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if out, rerr := runner.Run(ctx, "ss", "-H", "-tlnp"); rerr == nil {
			for _, lp := range sysinfo.ParseSSOutput(out, "tcp") {
				if lp.Port == port && lp.Process != "" {
					p.Occupier = lp.Process
					break
				}
			}
		}
	}
	return p
}

// EnvReport 反代运行环境报告（端口探测 + 推荐 + Caddy 探测）。
type EnvReport struct {
	HTTP         PortProbe `json:"http"`
	HTTPS        PortProbe `json:"https"`
	Engine       string    `json:"engine"`
	ListenHTTP   string    `json:"listen_http"`
	ListenHTTPS  string    `json:"listen_https"`
	Recommended  struct {
		HTTPPort  int    `json:"http_port"`
		HTTPSPort int    `json:"https_port"`
		Challenge string `json:"challenge"` // http-01 | tls-alpn-01 | dns-01
		Note      string `json:"note"`
	} `json:"recommended"`
	CaddyInstalled bool   `json:"caddy_installed"`
	CaddyVersion   string `json:"caddy_version"`
}

// Environment 探测 80/443 与 Caddy，给出推荐配置。
// 端口被面板自身占用时视为「挑战可达」：certmagic 绑定失败但端口有响应时
// 会把挑战委托给持有者，本面板的监听器均挂有挑战处理器。
func (m *Manager) Environment(runner Runner) EnvReport {
	rep := EnvReport{Engine: m.mode, ListenHTTP: m.httpAddr, ListenHTTPS: m.httpsAddr}
	rep.HTTP = ProbePort(80, runner)
	rep.HTTPS = ProbePort(443, runner)
	selfOK := isSelfProcess()
	httpChallengeOK := rep.HTTP.Bindable || rep.HTTP.Occupier == selfOK
	alpnChallengeOK := rep.HTTPS.Bindable || rep.HTTPS.Occupier == selfOK

	switch {
	case httpChallengeOK:
		rep.Recommended.Challenge = "http-01"
		if rep.HTTP.Occupier == selfOK {
			rep.Recommended.Note = "80/443 由本面板监听：HTTP-01 挑战经面板挑战处理器直接应答，正常签发（含 IP 证书）"
		} else {
			rep.Recommended.Note = "80 端口可用：HTTP-01 挑战直连即可签发（含 IP 证书）"
		}
	case alpnChallengeOK:
		rep.Recommended.Challenge = "tls-alpn-01"
		rep.Recommended.Note = "80 被占用：" + occupierText(rep.HTTP) + "持有；TLS-ALPN 挑战需本面板监听 443 或上游 DNAT 443→面板 HTTPS 端口"
	default:
		rep.Recommended.Challenge = "dns-01"
		rep.Recommended.Note = "80/443 均被占用：" + occupierText(rep.HTTP) + " / " + occupierText(rep.HTTPS) +
			"。域名证书用 DNS-01 或寄生前置（让占用者转发 /.well-known/acme-challenge/）；IP 证书无 DNS-01，只能寄生前置/DNAT 或手动证书"
	}
	rep.Recommended.HTTPPort = pickPort(rep.HTTP.Bindable || rep.HTTP.Occupier == selfOK, 80, 8080)
	rep.Recommended.HTTPSPort = pickPort(rep.HTTPS.Bindable || rep.HTTPS.Occupier == selfOK, 443, 8443)

	if runner != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if v, err := runner.Run(ctx, "caddy", "version"); err == nil {
			rep.CaddyInstalled = true
			rep.CaddyVersion = strings.TrimSpace(v)
		}
	}
	return rep
}

// isSelfProcess 当前进程名（与 ss 报告的占用进程名比对，判断端口是否为本面板持有）。
func isSelfProcess() string {
	if exe, err := os.Executable(); err == nil {
		return strings.ToLower(strings.TrimSuffix(filepath.Base(exe), ".exe"))
	}
	return "firepanel"
}

func occupierText(p PortProbe) string {
	if p.Occupier != "" {
		return p.Occupier
	}
	return "其他程序"
}

func pickPort(bindable bool, std, alt int) int {
	if bindable {
		return std
	}
	return alt
}

// listenPort 从监听地址提取端口号（":80" / "0.0.0.0:80" / "80"）。
func listenPort(addr string) int {
	a := strings.TrimSpace(addr)
	if a == "" {
		return 0
	}
	if i := strings.LastIndex(a, ":"); i >= 0 {
		a = a[i+1:]
	}
	n, err := strconv.Atoi(a)
	if err != nil {
		return 0
	}
	return n
}

// ssOccupier 供测试与外部复用：解析 ss 输出找指定端口的占用进程。
func ssOccupier(ssOut string, port int) string {
	for _, lp := range sysinfo.ParseSSOutput(ssOut, "tcp") {
		if lp.Port == port && lp.Process != "" {
			return lp.Process
		}
	}
	return ""
}
