package api

import (
	"strings"
	"testing"

	"firepanel/internal/store"
)

// TestValidateJail 覆盖内置方案的关键校验路径。
func TestValidateJail(t *testing.T) {
	valid := func(mod func(*store.JailConfig)) *store.JailConfig {
		j := &store.JailConfig{
			Name: "sshd", SourceType: "journald", Unit: "ssh",
			MatchRegex: "(Failed password for|Invalid user).* from ([0-9]{1,3}([.][0-9]{1,3}){3})",
			Threshold:  5, FindTime: 600, BanTime: 1800,
			IgnoreCIDRs: "192.168.0.0/16",
		}
		if mod != nil {
			mod(j)
		}
		return j
	}

	if err := validateJail(valid(nil)); err != nil {
		t.Fatalf("标准 sshd jail 应合法: %v", err)
	}

	// 内置「面板登录爆破防护」：audit 源 + 空正则 → 必须合法（回归）
	audit := valid(func(j *store.JailConfig) {
		j.Name = "panel"; j.SourceType = "audit"; j.Unit = ""; j.MatchRegex = ""
	})
	if err := validateJail(audit); err != nil {
		t.Fatalf("audit 源空正则应合法: %v", err)
	}

	// file 源缺路径
	file := valid(func(j *store.JailConfig) { j.Name = "web"; j.SourceType = "file"; j.Unit = "" })
	if err := validateJail(file); err == nil {
		t.Fatal("file 源缺路径应报错")
	}

	// file 源非法正则
	fileBad := valid(func(j *store.JailConfig) {
		j.Name = "web"; j.SourceType = "file"; j.Unit = ""
		j.LogPath = "/var/log/x.log"; j.MatchRegex = "([unclosed"
	})
	if err := validateJail(fileBad); err == nil {
		t.Fatal("非法正则应报错")
	}

	// journald 空正则（引擎依赖正则提取 IP）
	jEmpty := valid(func(j *store.JailConfig) { j.MatchRegex = "" })
	if err := validateJail(jEmpty); err == nil {
		t.Fatal("journald 空正则应报错")
	}

	// 非法忽略网段
	badCidr := valid(func(j *store.JailConfig) { j.IgnoreCIDRs = "192.168.0.0/99" })
	if err := validateJail(badCidr); err == nil || !strings.Contains(err.Error(), "CIDR") {
		t.Fatalf("非法忽略网段应报 CIDR 错误: %v", err)
	}

	// 阈值越界
	badThreshold := valid(func(j *store.JailConfig) { j.Threshold = 0 })
	if err := validateJail(badThreshold); err == nil {
		t.Fatal("阈值 0 应报错（调用方应先应用默认值）")
	}
}
