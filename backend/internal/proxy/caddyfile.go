package proxy

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

// CaddyCustomMarker 自定义片段分隔标记：此注释之后的内容由用户维护，
// 面板重新生成时原样保留；整文件手改保存时也按此标记拆分回填。
const CaddyCustomMarker = "# ==== 自定义配置（面板保留此段，重新生成不会覆盖） ===="

// SplitCustom 从完整 Caddyfile 内容中拆出用户自定义片段（标记之后的部分）。
func SplitCustom(content string) string {
	if i := strings.Index(content, CaddyCustomMarker); i >= 0 {
		return strings.Trim(strings.TrimSpace(content[i+len(CaddyCustomMarker):]), "\n")
	}
	return ""
}

// CaddyGlobalSettings Caddyfile 全局选项（settings 表 caddy_global JSON）。
type CaddyGlobalSettings struct {
	Email string `json:"email"` // ACME 账户邮箱（空=不指定）
	Debug bool   `json:"debug"` // 全局 debug 日志
}

// Validate 校验全局设置。
func (g CaddyGlobalSettings) Validate() error {
	if g.Email != "" && !strings.Contains(g.Email, "@") {
		return fmt.Errorf("ACME 邮箱格式不正确")
	}
	return nil
}

// Mask 脱敏（当前无敏感字段，占位以对齐其他配置结构）。
func (g CaddyGlobalSettings) Mask() CaddyGlobalSettings { return g }

// CaddySiteOpts 站点级选项（rp_domains.caddy_opts JSON，空=全默认开）。
type CaddySiteOpts struct {
	Encode          bool `json:"encode"`           // zstd/gzip 压缩
	SecurityHeaders bool `json:"security_headers"` // 常用安全响应头
	LogAccess       bool `json:"log_access"`       // 站点访问日志
}

func parseCaddySiteOpts(raw string) CaddySiteOpts {
	o := CaddySiteOpts{Encode: true, SecurityHeaders: true, LogAccess: true}
	if strings.TrimSpace(raw) == "" {
		return o
	}
	_ = json.Unmarshal([]byte(raw), &o) // 字段缺失保持默认 true
	return o
}

// domainSiteOpts 域名 → opts（GlobalCaddySettings 暂未参与站点开关，保留参数位）。
func domainSiteOpts(d DomainConfig) CaddySiteOpts {
	return parseCaddySiteOpts(d.CaddyOpts)
}

// caddySiteAddr 站点地址：tls_mode=none 用 http:// 前缀禁 TLS，其余直接写 host。
func caddySiteAddr(domain string, tlsMode string) string {
	if tlsMode == "none" {
		return "http://" + domain
	}
	return domain
}

// BuildCaddyfile 生成完整 Caddyfile：全局块 + 每域名站点块 + 自定义片段。
// 路由按路径最长前缀优先；路径 / 渲染为无 matcher 的 reverse_proxy。
// custom 为用户自定义片段（原样追加，面板重新生成时不丢失）。
func BuildCaddyfile(cfgs []DomainConfig, g CaddyGlobalSettings, custom string) string {
	var b strings.Builder
	b.WriteString("# ============================================================\n")
	b.WriteString("# 此文件由 FirePanel 生成与管理（完全接管模式）\n")
	b.WriteString("# 在面板「反向代理 → Caddyfile」中查看/导出；手动修改会被下次下发覆盖\n")
	b.WriteString("# ============================================================\n\n")
	// 全局块：仅有内容时输出（空 {} 虽合法但无意义）
	var gb strings.Builder
	if g.Email != "" {
		gb.WriteString("\temail " + g.Email + "\n")
	}
	if g.Debug {
		gb.WriteString("\tdebug\n")
	}
	if gb.Len() > 0 {
		b.WriteString("{\n" + gb.String() + "}\n")
	}

	// 稳定输出：按域名排序
	sorted := make([]DomainConfig, 0, len(cfgs))
	for _, c := range cfgs {
		if c.Enabled && len(c.Routes) > 0 {
			sorted = append(sorted, c)
		}
	}
	sort.Slice(sorted, func(i, k int) bool { return strings.ToLower(sorted[i].Domain) < strings.ToLower(sorted[k].Domain) })

	for _, c := range sorted {
		opts := domainSiteOpts(c)
		b.WriteString("\n# ---- " + c.Domain + " ----\n")
		b.WriteString(caddySiteAddr(c.Domain, c.TLSMode) + " {\n")
		if c.TLSMode == "manual" {
			b.WriteString("\t# manual 模式：请在此块内手动添加 tls 指令加载证书\n")
		}
		if opts.Encode {
			b.WriteString("\tencode zstd gzip\n")
		}
		if opts.SecurityHeaders {
			b.WriteString("\theader {\n")
			b.WriteString("\t\tStrict-Transport-Security \"max-age=31536000; includeSubDomains\"\n")
			b.WriteString("\t\tX-Content-Type-Options nosniff\n")
			b.WriteString("\t\tX-Frame-Options DENY\n")
			b.WriteString("\t\tReferrer-Policy strict-origin-when-cross-origin\n")
			b.WriteString("\t\t-Server\n")
			b.WriteString("\t}\n")
		}
		if opts.LogAccess {
			b.WriteString("\tlog {\n")
			b.WriteString("\t\toutput file /var/log/caddy/fp-" + strings.ToLower(c.Domain) + ".log {\n\t\t\troll_size 50MiB\n\t\t\troll_keep 5\n\t\t}\n")
			b.WriteString("\t}\n")
		}
		// 路由：路径最长前缀优先；/ 为根
		routes := make([]RouteConfig, len(c.Routes))
		copy(routes, c.Routes)
		sort.Slice(routes, func(i, k int) bool {
			return len(normalizePathMatch(routes[i].PathMatch)) > len(normalizePathMatch(routes[k].PathMatch))
		})
		for _, rc := range routes {
			if !rc.Enabled {
				continue
			}
			dial, isTLS, err := caddyUpstreamDial(rc.UpstreamURL)
			if err != nil || dial == "" {
				continue
			}
			pm := normalizePathMatch(rc.PathMatch)
			matcher := ""
			if pm != "/" {
				matcher = pm + "/* "
			}
			if isTLS {
				b.WriteString("\treverse_proxy " + matcher + dial + " {\n")
				b.WriteString("\t\ttransport http {\n")
				b.WriteString("\t\t\ttls\n")
				b.WriteString("\t\t}\n")
				b.WriteString("\t}\n")
			} else {
				b.WriteString("\treverse_proxy " + matcher + dial + "\n")
			}
		}
		b.WriteString("}\n")
	}

	// 自定义片段：原样追加（面板保存于 settings，重新生成不丢失）
	if strings.TrimSpace(custom) != "" {
		b.WriteString("\n" + CaddyCustomMarker + "\n")
		b.WriteString(strings.TrimRight(custom, "\n") + "\n")
	}
	return b.String()
}
