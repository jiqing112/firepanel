package app

import (
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"testing"

	"firepanel/internal/config"
)

func testCfg(dir string) *config.Config {
	c := &config.Config{}
	c.Server.DataDir = dir
	return c
}

// 凭据应写入数据目录（Docker 下随卷持久化），并能被重新装载。
func TestBasicAuthPersistsInDataDir(t *testing.T) {
	dir := t.TempDir()
	a := &App{Cfg: testCfg(dir), Log: slog.New(slog.NewTextHandler(io.Discard, nil))}
	if err := a.SaveBasicAuth(true, "admin2", "s3cret-pass"); err != nil {
		t.Fatalf("保存失败: %v", err)
	}
	path := filepath.Join(dir, basicAuthFileName)
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("凭据应写入数据目录 %s: %v", path, err)
	}
	if src := a.BasicAuthSource(); src != "file" {
		t.Fatalf("来源应为 file，得到 %q", src)
	}
	st := LoadBasicAuth(nil, testCfg(dir))
	if st == nil || !st.Enabled() || !st.Match("admin2", "s3cret-pass") {
		t.Fatalf("重新装载失败: %+v", st)
	}
	u, p, ok := a.LoadBasicAuthFile()
	if !ok || u != "admin2" || p != "s3cret-pass" {
		t.Fatalf("回显凭据错误: u=%q p=%q ok=%v", u, p, ok)
	}
	// 关闭：文件保留但 enabled=false，装载结果为空
	if err := a.SaveBasicAuth(false, "admin2", "s3cret-pass"); err != nil {
		t.Fatal(err)
	}
	if st := LoadBasicAuth(nil, testCfg(dir)); st != nil && st.Enabled() {
		t.Fatal("enabled=false 时不应启用 Basic Auth")
	}
}

// 没有凭据文件时回落到主配置（-basic-auth / server.basic_auth_*）。
func TestBasicAuthFallsBackToConfig(t *testing.T) {
	dir := t.TempDir()
	cfg := testCfg(dir)
	cfg.Server.BasicAuthUser = "cfguser"
	cfg.Server.BasicAuthPass = "cfgpass"
	st := LoadBasicAuth(nil, cfg)
	if st == nil || !st.Enabled() || !st.Match("cfguser", "cfgpass") {
		t.Fatalf("应从主配置装载: %+v", st)
	}
	a := &App{Cfg: cfg}
	if src := a.BasicAuthSource(); src != "config" {
		t.Fatalf("来源应为 config，得到 %q", src)
	}
}
