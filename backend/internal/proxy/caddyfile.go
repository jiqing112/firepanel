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

// 面板托管段边界：只有这两行标记之间的内容归面板所有，重新下发时整体替换。
// 段外的一切内容（已有站点、snippets、import、全局块…）面板永不改动。
const (
	CaddyManagedBegin = "# >>> firepanel managed begin · 本段由 FirePanel 生成，增删域名/改路由时整体替换 >>>"
	CaddyManagedEnd   = "# <<< firepanel managed end · 以下内容归你所有，面板不会改动 <<<"
)

// legacyHeader 旧版本（整文件覆盖模式）生成物首行特征，用于识别并迁移。
const legacyHeader = "此文件由 FirePanel 生成与管理"

// SplitCustom 从完整 Caddyfile 内容中拆出用户自定义片段（标记之后的部分）。
func SplitCustom(content string) string {
	if i := strings.Index(content, CaddyCustomMarker); i >= 0 {
		tail := content[i+len(CaddyCustomMarker):]
		// 托管段存在时，自定义片段止于 managed end，不要把标记行当成配置
		if j := strings.Index(tail, CaddyManagedEnd); j >= 0 {
			tail = tail[:j]
		}
		return strings.Trim(strings.TrimSpace(tail), "\n")
	}
	return ""
}

// IsLegacyGenerated 是否为旧版整文件生成物（无托管段标记，整篇由面板生成）。
func IsLegacyGenerated(content string) bool {
	head := content
	if len(head) > 600 {
		head = head[:600]
	}
	return strings.Contains(head, legacyHeader)
}

// HasManagedBlock 是否包含面板托管段。
func HasManagedBlock(content string) bool {
	_, _, ok := managedSpan(content)
	return ok
}

// managedSpan 返回托管段的 [start, end) 字节区间（end 含结束标记与其后换行）。
func managedSpan(s string) (int, int, bool) {
	i := strings.Index(s, CaddyManagedBegin)
	if i < 0 {
		return 0, 0, false
	}
	j := strings.Index(s[i:], CaddyManagedEnd)
	if j < 0 {
		return 0, 0, false
	}
	start := i
	end := i + j + len(CaddyManagedEnd)
	for end < len(s) && (s[end] == '\n' || s[end] == '\r') {
		end++
	}
	return start, end, true
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

// safeCaddyPath 写入前的最后一道校验：路径会被拼进 Caddyfile，
// 控制字符/花括号/引号可闭合站点块并注入指令。存储层已拦一次，这里再做纵深防御。
func safeCaddyPath(p string) bool {
	if p == "" || len(p) > 200 || !strings.HasPrefix(p, "/") {
		return false
	}
	if strings.ContainsAny(p, "{}#\"'\\") {
		return false
	}
	for _, r := range p {
		if r < 0x20 || r == 0x7f {
			return false
		}
	}
	return true
}

// safeCaddySite 站点地址只允许主机名/IP（不含空白、花括号等结构字符）。
func safeCaddySite(domain string) bool {
	if domain == "" || len(domain) > 253 {
		return false
	}
	for _, r := range domain {
		if r <= 0x20 || r == 0x7f || strings.ContainsRune("{}#\"'\\/:", r) {
			return false
		}
	}
	return true
}

// BuildCaddyfile 生成完整 Caddyfile 文本（面板托管段，含全局块）。
// 仅用于「文件不存在」时首次创建，或 UI 预览；写入现有文件时请用 ApplyMerge 合并。
func BuildCaddyfile(cfgs []DomainConfig, g CaddyGlobalSettings, custom string) string {
	return BuildManagedBlock(cfgs, g, custom, true)
}

// BuildManagedBlock 生成面板托管段：边界标记 + 全局块 + 每域名站点块 + 自定义片段。
// withGlobal=false（追加到已有文件）时不写全局块——Caddy 要求全局块必须位于文件
// 首个块，追加到他人文件中部会导致整份配置校验失败，此时以注释给出待并入内容。
func BuildManagedBlock(cfgs []DomainConfig, g CaddyGlobalSettings, custom string, withGlobal bool) string {
	var b strings.Builder
	b.WriteString(CaddyManagedBegin + "\n")
	b.WriteString("# 说明：仅标记之间的内容由面板生成；标记之外的内容不会被修改或删除。\n")
	b.WriteString("# 手改本段会被下次下发替换；请把自有站点/snippets 写在 managed end 之后。\n\n")

	// 全局块：仅有内容时输出（空 {} 虽合法但无意义）
	var gb strings.Builder
	if g.Email != "" {
		gb.WriteString("\temail " + g.Email + "\n")
	}
	if g.Debug {
		gb.WriteString("\tdebug\n")
	}
	if gb.Len() > 0 {
		if withGlobal {
			b.WriteString("{\n" + gb.String() + "}\n")
		} else {
			b.WriteString("# 目标文件已有自己的配置，以下全局选项未自动写入；如需生效请手动并入文件顶部的全局块：\n")
			for _, line := range strings.Split(strings.TrimRight(gb.String(), "\n"), "\n") {
				b.WriteString("# " + strings.TrimLeft(line, "\t") + "\n")
			}
		}
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
		if !safeCaddySite(c.Domain) {
			continue
		}
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
			if !safeCaddyPath(normalizePathMatch(rc.PathMatch)) {
				continue
			}
			dial, isTLS, err := caddyUpstreamDial(rc.UpstreamURL)
			if err != nil || dial == "" {
				continue
			}
			pm := normalizePathMatch(rc.PathMatch)
			matcher := ""
			if pm != "/" {
				// 同时匹配精确路径与子路径：只写 "/api/*" 时请求 "/api" 本身
				// 会落到更宽的 "/" 路由，与内置引擎（path == pm）行为不一致。
				matcher = pm + " " + pm + "/* "
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
	b.WriteString(CaddyManagedEnd + "\n")
	return b.String()
}

/* ------------------------- 与现有文件的合并（不整盘覆盖） ------------------------- */

// MergeMode 下发时对现有文件采取的合并方式。
type MergeMode string

const (
	MergeCreate  MergeMode = "create"  // 目标不存在：直接创建
	MergeUpdate  MergeMode = "update"  // 已有托管段：只替换段内内容
	MergeMigrate MergeMode = "migrate" // 旧版整文件生成物：迁移为托管段（已备份）
	MergeAppend  MergeMode = "append"  // 非面板文件：原样保留，托管段追加到末尾
)

// Label 中文说明（UI/事件展示）。
func (m MergeMode) Label() string {
	switch m {
	case MergeCreate:
		return "新建文件"
	case MergeUpdate:
		return "仅替换面板托管段"
	case MergeMigrate:
		return "迁移旧版生成物为托管段"
	case MergeAppend:
		return "保留原文件并追加托管段"
	default:
		return string(m)
	}
}

// DetectMergeMode 判断应如何把托管段合并进现有内容。
func DetectMergeMode(existing string) MergeMode {
	if strings.TrimSpace(existing) == "" {
		return MergeCreate
	}
	if HasManagedBlock(existing) {
		return MergeUpdate
	}
	if IsLegacyGenerated(existing) {
		return MergeMigrate
	}
	return MergeAppend
}

// ApplyMerge 按合并方式把托管段写入现有内容，返回新内容。
// 除 update/migrate 外，原有内容一律逐字节保留。
func ApplyMerge(existing string, mode MergeMode, block string) string {
	block = strings.TrimRight(block, "\n") + "\n"
	switch mode {
	case MergeCreate, MergeMigrate:
		return block
	case MergeUpdate:
		if s, e, ok := managedSpan(existing); ok {
			return existing[:s] + block + existing[e:]
		}
		return strings.TrimRight(existing, "\n") + "\n\n" + block
	default: // MergeAppend
		return strings.TrimRight(existing, "\n") + "\n\n" + block
	}
}

// MergeCaddyfile 便捷封装：自动判定合并方式后写入（block 需已按需生成）。
func MergeCaddyfile(existing, block string) (string, MergeMode) {
	mode := DetectMergeMode(existing)
	return ApplyMerge(existing, mode, block), mode
}
