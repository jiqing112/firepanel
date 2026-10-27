package config

import "testing"

func TestResolveSSHPortsNewKeyWins(t *testing.T) {
	c := &Config{}
	c.Server.SSHPortsLegacy = []int{22} // 旧版示例残留
	c.Firewall.SSHPorts = []int{2222}
	resolveSSHPorts(c)
	if len(c.Firewall.SSHPorts) != 1 || c.Firewall.SSHPorts[0] != 2222 {
		t.Fatalf("firewall.ssh_ports 应优先，得到 %v", c.Firewall.SSHPorts)
	}
	if len(c.Warnings) == 0 {
		t.Fatal("新旧键同时存在时应给出告警")
	}
	if c.Server.SSHPortsLegacy != nil {
		t.Fatal("旧键处理后应清空，避免被重复使用")
	}
}

func TestResolveSSHPortsLegacyFallback(t *testing.T) {
	c := &Config{}
	c.Server.SSHPortsLegacy = []int{2200}
	resolveSSHPorts(c)
	if len(c.Firewall.SSHPorts) != 1 || c.Firewall.SSHPorts[0] != 2200 {
		t.Fatalf("仅旧键时应作为兜底，得到 %v", c.Firewall.SSHPorts)
	}
	if len(c.Warnings) == 0 {
		t.Fatal("使用旧键时应提示迁移")
	}
}

func TestResolveSSHPortsDefault(t *testing.T) {
	c := &Config{}
	resolveSSHPorts(c)
	if len(c.Firewall.SSHPorts) != 1 || c.Firewall.SSHPorts[0] != 22 {
		t.Fatalf("缺省应为 22，得到 %v", c.Firewall.SSHPorts)
	}
	if len(c.Warnings) != 0 {
		t.Fatalf("缺省不应产生告警: %v", c.Warnings)
	}
}
