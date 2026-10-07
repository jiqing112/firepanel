// caddy.go：将反代期望配置转换为 Caddy v2 配置 JSON，并经 Admin API 下发。
// 适用于「面板不占用 80/443、由独立 Caddy 承担入口」的部署形态；
// 亦可用于 Caddy 已监听 80/443 的场景（面板只做配置托管，不监听端口）。
package proxy

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"
)

/* ------------------------- 配置构建 ------------------------- */

type caddyRoute struct {
	Match  []map[string]any `json:"match,omitempty"`
	Handle []map[string]any `json:"handle"`
}

type caddyServer struct {
	Listen         []string     `json:"listen"`
	Routes         []caddyRoute `json:"routes"`
	AutomaticHTTPS *struct {
		Disable bool `json:"disable"`
	} `json:"automatic_https,omitempty"`
}

type caddyConfig struct {
	Admin *struct {
		Disabled bool `json:"disabled"`
	} `json:"admin,omitempty"`
	Apps struct {
		HTTP struct {
			Servers map[string]caddyServer `json:"servers"`
		} `json:"http"`
	} `json:"apps"`
}

// caddyUpstreamDial 从上游 URL 提取 Caddy reverse_proxy 的 dial 地址（host:port）。
// IPv6 主机自动加方括号。
func caddyUpstreamDial(upstream string) (string, bool, error) {
	u, err := url.Parse(upstream)
	if err != nil {
		return "", false, err
	}
	host := u.Hostname()
	port := u.Port()
	if port == "" {
		if u.Scheme == "https" {
			port = "443"
		} else {
			port = "80"
		}
	}
	if strings.Contains(host, ":") { // IPv6
		host = "[" + host + "]"
	}
	return host + ":" + port, u.Scheme == "https", nil
}

// BuildCaddyConfig 构建完整 Caddy 配置（POST /load 的请求体）。
//   - tls_mode=none 的域名路由到 HTTP server（automatic_https 关闭）
//   - auto/manual 域名路由到 HTTPS server（Caddy automatic HTTPS 自动签发证书，
//     IP 证书需 Caddy ≥ 2.10 且其 certmagic 支持 shortlived）
//   - manual 模式证书由用户在 Caddy 侧自行加载（面板不下发，事件提示）
func BuildCaddyConfig(cfgs []DomainConfig, httpAddr, httpsAddr string) map[string]any {
	type domRoutes struct {
		domain  string
		routes  []caddyRoute
		tlsMode string
	}
	var all []domRoutes
	for _, c := range cfgs {
		if !c.Enabled || len(c.Routes) == 0 {
			continue
		}
		// 路径最长前缀优先
		routes := make([]RouteConfig, len(c.Routes))
		copy(routes, c.Routes)
		sort.Slice(routes, func(i, k int) bool {
			return len(normalizePathMatch(routes[i].PathMatch)) > len(normalizePathMatch(routes[k].PathMatch))
		})
		dr := domRoutes{domain: strings.ToLower(c.Domain), tlsMode: c.TLSMode}
		for _, rc := range routes {
			pm := normalizePathMatch(rc.PathMatch)
			pathMatcher := []string{"/*"}
			if pm != "/" {
				pathMatcher = []string{pm, pm + "/*"}
			}
			dial, isTLS, err := caddyUpstreamDial(rc.UpstreamURL)
			if err != nil || dial == "" {
				continue
			}
			rpHandler := map[string]any{
				"handler":   "reverse_proxy",
				"upstreams": []map[string]any{{"dial": dial}},
			}
			if isTLS {
				rpHandler["transport"] = map[string]any{
					"protocol": "http",
					"tls":      map[string]any{},
				}
			}
			dr.routes = append(dr.routes, caddyRoute{
				Match:  []map[string]any{{"host": []string{dr.domain}, "path": pathMatcher}},
				Handle: []map[string]any{rpHandler},
			})
		}
		if len(dr.routes) > 0 {
			all = append(all, dr)
		}
	}
	sort.Slice(all, func(i, k int) bool { return all[i].domain < all[k].domain })

	var httpRoutes, httpsRoutes []caddyRoute
	for _, d := range all {
		if d.tlsMode == "none" {
			httpRoutes = append(httpRoutes, d.routes...)
		} else {
			httpsRoutes = append(httpsRoutes, d.routes...)
		}
	}

	var cfg caddyConfig
	cfg.Apps.HTTP.Servers = map[string]caddyServer{}
	if len(httpRoutes) > 0 && httpAddr != "" {
		cfg.Apps.HTTP.Servers["firepanel_http"] = caddyServer{
			Listen:         []string{httpAddr},
			Routes:         httpRoutes,
			AutomaticHTTPS: &struct {
				Disable bool `json:"disable"`
			}{Disable: true},
		}
	}
	if len(httpsRoutes) > 0 && httpsAddr != "" {
		cfg.Apps.HTTP.Servers["firepanel_https"] = caddyServer{
			Listen: []string{httpsAddr},
			Routes: httpsRoutes,
		}
	}

	// 序列化再反序列化为 map，避免直接暴露内部类型
	b, _ := json.Marshal(cfg)
	var out map[string]any
	_ = json.Unmarshal(b, &out)
	return out
}

/* ------------------------- Admin API 客户端 ------------------------- */

// CaddyLoad 将配置下发到 Caddy Admin API（POST /load，整配置替换）。
// 适用前提：该 Caddy 实例由 FirePanel 托管（不与其他管理方共享）。
func CaddyLoad(ctx context.Context, adminURL string, body map[string]any, timeout time.Duration) error {
	b, err := json.Marshal(body)
	if err != nil {
		return err
	}
	cctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	req, err := http.NewRequestWithContext(cctx, http.MethodPost,
		strings.TrimRight(adminURL, "/")+"/load", bytes.NewReader(b))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("连接 Caddy Admin API %s: %w", adminURL, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		msg, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
		return fmt.Errorf("Caddy /load 返回 %d: %s", resp.StatusCode, strings.TrimSpace(string(msg)))
	}
	return nil
}

// CaddyRunning 探测 Admin API 是否可达。
func CaddyRunning(ctx context.Context, adminURL string, timeout time.Duration) bool {
	cctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	req, err := http.NewRequestWithContext(cctx, http.MethodGet,
		strings.TrimRight(adminURL, "/")+"/config/", nil)
	if err != nil {
		return false
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	return resp.StatusCode < 500
}
