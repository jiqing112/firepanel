// Package fw：防火墙后端抽象层。
// 面板只操作自有链/自有表，通过 reconcile 模型把 SQLite 期望状态原子地应用到内核。
package fw

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
	"time"
)

// Executor 抽象命令执行：单测注入 fake，不依赖 Linux。
type Executor interface {
	// LookPath 在 PATH 与常见 sbin 目录中查找可执行文件。
	LookPath(name string) (string, bool)
	// Run 执行命令，argv 数组传参（防注入的根本手段）。
	// 返回 stdout；失败时 error 包含 stderr。
	Run(ctx context.Context, name string, args ...string) (string, error)
	// RunStdin 同 Run，但通过 stdin 提供输入（iptables-restore / nft -f -）。
	RunStdin(ctx context.Context, name string, stdin string, args ...string) (string, error)
}

// RealExecutor 真实执行器。
type RealExecutor struct {
	extraPaths []string
	timeout    time.Duration
}

func NewRealExecutor() *RealExecutor {
	return &RealExecutor{
		extraPaths: []string{"/usr/sbin", "/sbin", "/usr/local/sbin", "/usr/bin", "/bin"},
		timeout:    30 * time.Second,
	}
}

func (e *RealExecutor) LookPath(name string) (string, bool) {
	if strings.ContainsRune(name, '/') {
		return name, true
	}
	if p, err := exec.LookPath(name); err == nil {
		return p, true
	}
	for _, dir := range e.extraPaths {
		full := dir + "/" + name
		if p, err := exec.LookPath(full); err == nil {
			return p, true
		}
	}
	return "", false
}

func (e *RealExecutor) Run(ctx context.Context, name string, args ...string) (string, error) {
	return e.run(ctx, name, "", args)
}

func (e *RealExecutor) RunStdin(ctx context.Context, name, stdin string, args ...string) (string, error) {
	return e.run(ctx, name, stdin, args)
}

func (e *RealExecutor) run(ctx context.Context, name, stdin string, args []string) (string, error) {
	cctx, cancel := context.WithTimeout(ctx, e.timeout)
	defer cancel()
	bin, ok := e.LookPath(name)
	if !ok {
		return "", fmt.Errorf("命令 %s 不存在", name)
	}
	cmd := exec.CommandContext(cctx, bin, args...)
	var stdout, stderr strings.Builder
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if stdin != "" {
		cmd.Stdin = strings.NewReader(stdin)
	}
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = err.Error()
		}
		return stdout.String(), fmt.Errorf("%s: %s", name, msg)
	}
	return stdout.String(), nil
}

// FakeExecutor 测试桩：按 "name args..." 前缀注册响应脚本。
type FakeExecutor struct {
	// handlers：键为 "name arg1 arg2"（变长时用前缀匹配），值为返回 (stdout, err)
	handlers map[string]func(args []string) (string, error)
	Calls    []string
}

func NewFakeExecutor() *FakeExecutor {
	return &FakeExecutor{handlers: map[string]func([]string) (string, error){}}
}

func (f *FakeExecutor) On(prefix string, fn func(args []string) (string, error)) {
	f.handlers[prefix] = fn
}

func (f *FakeExecutor) LookPath(name string) (string, bool) { return "/usr/sbin/" + name, true }

func (f *FakeExecutor) Run(_ context.Context, name string, args ...string) (string, error) {
	return f.RunStdin(context.Background(), name, "", args...)
}

func (f *FakeExecutor) RunStdin(_ context.Context, name, stdin string, args ...string) (string, error) {
	full := name + " " + strings.Join(args, " ")
	f.Calls = append(f.Calls, full)
	if stdin != "" {
		f.Calls = append(f.Calls, "  [stdin] "+strings.ReplaceAll(stdin, "\n", "\\n"))
	}
	for prefix, fn := range f.handlers {
		if strings.HasPrefix(full, prefix) {
			return fn(args)
		}
	}
	// 默认：探测类命令成功返回空
	if len(args) == 1 && (args[0] == "--version" || args[0] == "-V") {
		return "", nil
	}
	return "", nil
}
