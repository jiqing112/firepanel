package proxy

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"testing"
)

// memRunner 用内存文件表模拟 root 权限下的 cat/test/cp，验证「读真实文件 → 合并 → 备份 → 写入」全链路。
type memRunner struct {
	files map[string]string
	calls []string
}

func newMemRunner(initial map[string]string) *memRunner {
	return &memRunner{files: initial}
}

func (r *memRunner) Run(_ context.Context, name string, args ...string) (string, error) {
	r.calls = append(r.calls, name+" "+strings.Join(args, " "))
	switch name {
	case "systemctl":
		return "systemd 252", nil
	case "mkdir", "sh":
		return "", nil
	case "cat":
		if c, ok := r.files[args[0]]; ok {
			return c, nil
		}
		return "", fmt.Errorf("cat: %s: No such file or directory", args[0])
	case "test":
		if _, ok := r.files[args[len(args)-1]]; ok {
			return "", nil
		}
		return "", fmt.Errorf("not found")
	case "cp":
		src, dst := args[len(args)-2], args[len(args)-1]
		if c, ok := r.files[src]; ok {
			r.files[dst] = c
			return "", nil
		}
		b, err := os.ReadFile(src)
		if err != nil {
			return "", err
		}
		r.files[dst] = string(b)
		return "", nil
	case "caddy":
		return "", nil
	default:
		return "", nil
	}
}

func newTestManager(r Runner) *Manager {
	m := NewManager(slog.Default(), Options{Mode: ModeCaddy, CaddyAdmin: "http://127.0.0.1:2019"})
	m.SetRunner(r)
	return m
}

// 已有第三方配置：下发后原内容逐字节保留，只追加托管段，且先备份再写。
func TestDeployGeneratedKeepsForeignFile(t *testing.T) {
	foreign := "{\n\temail root@example.com\n}\n\nmine.example.org {\n\trespond \"hello\"\n}\n"
	r := newMemRunner(map[string]string{CaddyfilePath: foreign})
	m := newTestManager(r)

	res, err := m.DeployGenerated(context.Background(), mergeTestCfgs(), CaddyGlobalSettings{}, "")
	if err != nil {
		t.Fatalf("下发失败: %v", err)
	}
	if res.Mode != MergeAppend {
		t.Fatalf("期望 append，实际 %s", res.Mode)
	}
	if res.Backup == "" {
		t.Fatal("覆盖前必须生成备份")
	}
	if got := r.files[res.Backup]; got != foreign {
		t.Errorf("备份内容应为原始文件，实际:\n%s", got)
	}
	if got := r.files[CaddyfilePath+".fp-orig"]; got != foreign {
		t.Errorf("首次接管应保留 .fp-orig 原始副本，实际:\n%s", got)
	}
	written := r.files[CaddyfilePath]
	if !strings.HasPrefix(written, foreign) {
		t.Errorf("原文件内容必须完整保留，实际:\n%s", written)
	}
	if !strings.Contains(written, "app.example.com {") || !HasManagedBlock(written) {
		t.Errorf("托管段未写入，实际:\n%s", written)
	}
	// 备份必须在写入之前发生
	bakIdx, cpIdx := -1, -1
	for i, c := range r.calls {
		if strings.HasPrefix(c, "cp -p "+CaddyfilePath+" "+CaddyfilePath+".bak-") {
			bakIdx = i
		}
		if strings.HasPrefix(c, "cp ") && strings.HasSuffix(c, " "+CaddyfilePath) {
			cpIdx = i
		}
	}
	if bakIdx < 0 || cpIdx < 0 || bakIdx > cpIdx {
		t.Errorf("备份应先于写入，调用序列: %v", r.calls)
	}
}

// 已有托管段：只替换段内内容，段外手写站点不动。
func TestDeployGeneratedOnlyTouchesManagedBlock(t *testing.T) {
	block := BuildManagedBlock(mergeTestCfgs(), CaddyGlobalSettings{}, "", true)
	existing := "# 头部\n\n" + block + "\nblog.local {\n\trespond \"hi\"\n}\n"
	r := newMemRunner(map[string]string{CaddyfilePath: existing})
	m := newTestManager(r)

	res, err := m.DeployGenerated(context.Background(), nil, CaddyGlobalSettings{}, "")
	if err != nil {
		t.Fatalf("下发失败: %v", err)
	}
	if res.Mode != MergeUpdate {
		t.Fatalf("期望 update，实际 %s", res.Mode)
	}
	written := r.files[CaddyfilePath]
	if !strings.HasPrefix(written, "# 头部") || !strings.Contains(written, "blog.local {") {
		t.Errorf("段外内容被破坏:\n%s", written)
	}
	if strings.Contains(written, "app.example.com {") {
		t.Errorf("托管段内旧域名应被移除:\n%s", written)
	}
}

// 内容未变化时不得重复备份/写入/重载（否则每次面板启动都多一份 .bak）。
func TestDeployGeneratedSkipsUnchanged(t *testing.T) {
	r := newMemRunner(map[string]string{})
	m := newTestManager(r)
	if _, err := m.DeployGenerated(context.Background(), mergeTestCfgs(), CaddyGlobalSettings{}, ""); err != nil {
		t.Fatalf("首次下发失败: %v", err)
	}
	callsBefore := len(r.calls)
	res, err := m.DeployGenerated(context.Background(), mergeTestCfgs(), CaddyGlobalSettings{}, "")
	if err != nil {
		t.Fatalf("二次下发失败: %v", err)
	}
	if !res.Skipped {
		t.Fatalf("内容未变化时应跳过，实际 res=%+v", res)
	}
	if res.Backup != "" {
		t.Fatalf("跳过时不应产生备份，实际 %q", res.Backup)
	}
	for _, c := range r.calls[callsBefore:] {
		if strings.Contains(c, ".bak-") || strings.HasPrefix(c, "cp ") {
			t.Fatalf("跳过时不应写盘/备份，实际调用: %v", r.calls[callsBefore:])
		}
	}
}

// 读取失败时拒绝下发，磁盘文件保持不变。
func TestDeployGeneratedAbortsOnReadError(t *testing.T) {
	r := newMemRunner(map[string]string{})
	m := newTestManager(r)
	m.runner = failingCatRunner{memRunner: r}

	_, err := m.DeployGenerated(context.Background(), mergeTestCfgs(), CaddyGlobalSettings{}, "")
	if err == nil || !strings.Contains(err.Error(), "已中止") {
		t.Fatalf("读取失败应中止下发，实际 err=%v", err)
	}
}

type failingCatRunner struct{ *memRunner }

func (f failingCatRunner) Run(ctx context.Context, name string, args ...string) (string, error) {
	if name == "cat" {
		return "", fmt.Errorf("permission denied")
	}
	return f.memRunner.Run(ctx, name, args...)
}

// 乐观并发：基线指纹不一致时拒绝写入。
func TestDeployRawRejectsStaleBase(t *testing.T) {
	r := newMemRunner(map[string]string{CaddyfilePath: "old-content\n"})
	m := newTestManager(r)

	_, err := m.DeployRawCaddyfileIfUnchanged(context.Background(), "new-content\n", CaddyfileHash("some-other-version"))
	if err != ErrCaddyfileChanged {
		t.Fatalf("期望冲突错误，实际 %v", err)
	}
	if got := r.files[CaddyfilePath]; got != "old-content\n" {
		t.Errorf("冲突时不应写入，实际文件:\n%s", got)
	}
	// 基线一致则正常保存
	res, err := m.DeployRawCaddyfileIfUnchanged(context.Background(), "new-content\n", CaddyfileHash("old-content\n"))
	if err != nil {
		t.Fatalf("保存失败: %v", err)
	}
	if got := r.files[CaddyfilePath]; got != "new-content\n" {
		t.Errorf("内容未写入，实际:\n%s", got)
	}
	if res.Backup == "" {
		t.Error("手改保存也应备份")
	}
}
