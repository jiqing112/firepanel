package proxy

import (
	"strings"
	"testing"
)

func mergeTestCfgs() []DomainConfig {
	return []DomainConfig{{
		Domain: "app.example.com", Enabled: true, TLSMode: "auto",
		Routes: []RouteConfig{{PathMatch: "/", UpstreamURL: "http://127.0.0.1:8080", Enabled: true}},
	}}
}

// 空内容 → 新建，包含托管段边界。
func TestMergeCreate(t *testing.T) {
	block := BuildManagedBlock(mergeTestCfgs(), CaddyGlobalSettings{}, "", true)
	out, mode := MergeCaddyfile("", block)
	if mode != MergeCreate {
		t.Fatalf("期望 create，实际 %s", mode)
	}
	if !HasManagedBlock(out) || !strings.Contains(out, "app.example.com {") {
		t.Fatalf("新建内容异常:\n%s", out)
	}
}

// 已有托管段 → 只替换段内，段外内容逐字节保留。
func TestMergeUpdateKeepsOutside(t *testing.T) {
	old := BuildManagedBlock(mergeTestCfgs(), CaddyGlobalSettings{}, "", true)
	existing := "# 我的头部注释\n\n(common) {\n\theader X-Test 1\n}\n\n" + old +
		"\n\n# 用户自己的站点\nblog.local {\n\trespond \"hi\"\n}\n"
	block := BuildManagedBlock(nil, CaddyGlobalSettings{}, "", true) // 清空域名
	out, mode := MergeCaddyfile(existing, block)
	if mode != MergeUpdate {
		t.Fatalf("期望 update，实际 %s", mode)
	}
	for _, want := range []string{"# 我的头部注释", "(common) {", "blog.local {", `respond "hi"`} {
		if !strings.Contains(out, want) {
			t.Errorf("段外内容被破坏，缺少 %q\n输出:\n%s", want, out)
		}
	}
	if strings.Contains(out, "app.example.com {") {
		t.Errorf("托管段内的旧域名应被替换掉\n输出:\n%s", out)
	}
	if strings.Count(out, CaddyManagedBegin) != 1 || strings.Count(out, CaddyManagedEnd) != 1 {
		t.Errorf("托管段标记应唯一\n输出:\n%s", out)
	}
}

// 非面板文件 → 追加托管段，原内容一字不动，且不写全局块。
func TestMergeAppendKeepsForeignFile(t *testing.T) {
	existing := "{\n\temail me@example.com\n}\n\nexample.org {\n\trespond \"mine\"\n}\n"
	block := BuildManagedBlock(mergeTestCfgs(), CaddyGlobalSettings{Email: "ops@example.com"}, "", false)
	out, mode := MergeCaddyfile(existing, block)
	if mode != MergeAppend {
		t.Fatalf("期望 append，实际 %s", mode)
	}
	if !strings.HasPrefix(out, existing) {
		t.Errorf("原文件内容必须原样保留在开头\n输出:\n%s", out)
	}
	if !strings.Contains(out, "app.example.com {") {
		t.Errorf("缺少面板站点块\n输出:\n%s", out)
	}
	// 追加场景不能出现第二个全局块（Caddy 只允许文件首个块为全局块）
	if strings.Contains(out, "\n{\n\temail") {
		t.Errorf("追加内容不应包含全局块\n输出:\n%s", out)
	}
	if !strings.Contains(out, "# email ops@example.com") {
		t.Errorf("全局选项应以注释形式给出提示\n输出:\n%s", out)
	}
}

// 旧版整文件生成物 → 迁移（允许替换），并被正确识别。
func TestMergeMigrateLegacy(t *testing.T) {
	legacy := "# ============================================================\n" +
		"# 此文件由 FirePanel 生成与管理（完全接管模式）\n" +
		"# ============================================================\n\nold.example.com {\n\treverse_proxy 127.0.0.1:1\n}\n"
	if !IsLegacyGenerated(legacy) {
		t.Fatal("应识别为旧版生成物")
	}
	block := BuildManagedBlock(mergeTestCfgs(), CaddyGlobalSettings{}, "", true)
	out, mode := MergeCaddyfile(legacy, block)
	if mode != MergeMigrate {
		t.Fatalf("期望 migrate，实际 %s", mode)
	}
	if strings.Contains(out, "old.example.com") {
		t.Errorf("旧版生成内容应被迁移替换\n输出:\n%s", out)
	}
	if !HasManagedBlock(out) {
		t.Errorf("迁移后应带托管段标记\n输出:\n%s", out)
	}
}

// 自定义片段解析止于托管段结束标记，不会把标记行当配置。
func TestSplitCustomStopsAtManagedEnd(t *testing.T) {
	content := BuildManagedBlock(nil, CaddyGlobalSettings{}, "metrics.local {\n\trespond \"ok\"\n}", true) +
		"\n# 用户自己的站点\nblog.local {\n\trespond \"hi\"\n}\n"
	got := SplitCustom(content)
	if !strings.Contains(got, "metrics.local {") {
		t.Errorf("应包含自定义片段，实际:\n%s", got)
	}
	if strings.Contains(got, CaddyManagedEnd) || strings.Contains(got, "blog.local") {
		t.Errorf("自定义片段不应包含标记行或段外内容，实际:\n%s", got)
	}
}

// 重复合并应幂等：内容不变，不会叠加多份托管段。
func TestMergeIdempotent(t *testing.T) {
	block := BuildManagedBlock(mergeTestCfgs(), CaddyGlobalSettings{}, "", true)
	first, _ := MergeCaddyfile("", block)
	second, mode := MergeCaddyfile(first, block)
	if mode != MergeUpdate {
		t.Fatalf("期望 update，实际 %s", mode)
	}
	if strings.TrimSpace(first) != strings.TrimSpace(second) {
		t.Errorf("幂等性被破坏:\n第一次:\n%s\n第二次:\n%s", first, second)
	}
}

// 内容指纹稳定，用于并发修改检测。
func TestCaddyfileHashStable(t *testing.T) {
	if CaddyfileHash("a") != CaddyfileHash("a") || CaddyfileHash("a") == CaddyfileHash("b") {
		t.Error("hash 计算异常")
	}
	if len(CaddyfileHash("")) != 64 {
		t.Error("hash 长度异常")
	}
}
