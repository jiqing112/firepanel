package proxy

import (
	"context"
	"fmt"
	"strings"
)

// InstallCaddy 安装 Caddy：按发行版选包管理器；Debian/Ubuntu 发行版包失败时
// 回退官方 Cloudsmith 仓库。全程 argv 直调（无 shell 拼接），已安装则直接返回版本。
func InstallCaddy(ctx context.Context, runner Runner) (string, error) {
	if v, err := runner.Run(ctx, "caddy", "version"); err == nil {
		return strings.TrimSpace(v), nil
	}
	var lastErr error
	try := func(name string, args ...string) bool {
		_, err := runner.Run(ctx, name, args...)
		lastErr = err
		return err == nil
	}

	// Debian / Ubuntu
	if _, err := runner.Run(ctx, "apt-get", "--version"); err == nil {
		if try("apt-get", "install", "-y", "caddy") {
			return caddyVersion(ctx, runner)
		}
		// 官方仓库（键与源文件先落盘再安装，避免管道）
		try("apt-get", "install", "-y", "debian-keyring", "debian-archive-keyring", "apt-transport-https", "curl")
		try("curl", "-1sLf", "-o", "/tmp/firepanel-caddy.key", "https://dl.cloudsmith.io/public/caddy/stable/gpg.key")
		try("gpg", "--dearmor", "-o", "/usr/share/keyrings/caddy-stable-archive-keyring.gpg", "/tmp/firepanel-caddy.key")
		try("curl", "-1sLf", "-o", "/etc/apt/sources.list.d/caddy-stable.list", "https://dl.cloudsmith.io/public/caddy/stable/debian.deb.txt")
		try("apt-get", "update", "-qq")
		if try("apt-get", "install", "-y", "caddy") {
			return caddyVersion(ctx, runner)
		}
		return "", fmt.Errorf("apt 安装失败: %v（可参考 caddyserver.com/docs/install 手动安装）", lastErr)
	}

	// RHEL / CentOS / Rocky / Fedora
	for _, pm := range []string{"dnf", "yum"} {
		if _, err := runner.Run(ctx, pm, "--version"); err == nil {
			if try(pm, "install", "-y", "caddy") {
				return caddyVersion(ctx, runner)
			}
			try(pm, "install", "-y", "epel-release")
			if try(pm, "install", "-y", "caddy") {
				return caddyVersion(ctx, runner)
			}
			return "", fmt.Errorf("%s 安装失败: %v", pm, lastErr)
		}
	}

	// Alpine
	if _, err := runner.Run(ctx, "apk", "--version"); err == nil {
		if try("apk", "add", "--no-cache", "caddy") {
			return caddyVersion(ctx, runner)
		}
		return "", fmt.Errorf("apk 安装失败: %v", lastErr)
	}

	// Arch / Manjaro
	if _, err := runner.Run(ctx, "pacman", "--version"); err == nil {
		if try("pacman", "-S", "--noconfirm", "caddy") {
			return caddyVersion(ctx, runner)
		}
		return "", fmt.Errorf("pacman 安装失败: %v", lastErr)
	}

	return "", fmt.Errorf("未识别的包管理器，请手动安装 Caddy")
}

func caddyVersion(ctx context.Context, runner Runner) (string, error) {
	v, err := runner.Run(ctx, "caddy", "version")
	if err != nil {
		return "", fmt.Errorf("安装后仍找不到 caddy 命令: %v", err)
	}
	return strings.TrimSpace(v), nil
}

// EnsureCaddyService 确保托管模式的 Caddy 服务在运行（systemd 系）。
// 面板切到 caddy 引擎后通过 Admin API 下发配置，前提是 Caddy 进程已启动。
// restart：装包时的自启可能因端口被内置引擎占用而失败，此时需在端口释放后重启。
func EnsureCaddyService(ctx context.Context, runner Runner) error {
	if _, err := runner.Run(ctx, "systemctl", "--version"); err != nil {
		return fmt.Errorf("未检测到 systemd，请手动启动 Caddy 后重试")
	}
	_, _ = runner.Run(ctx, "systemctl", "enable", "--now", "caddy")
	_, _ = runner.Run(ctx, "systemctl", "restart", "caddy")
	return nil
}

