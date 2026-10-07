package sysinfo

import "testing"

const ssTCPOut = `LISTEN 0      128          0.0.0.0:22        0.0.0.0:*    users:(("sshd",pid=800,fd=3))
LISTEN 0      128             [::]:22           [::]:*    users:(("sshd",pid=800,fd=5))
LISTEN 0      511          127.0.0.1:6060          0.0.0.0:*    users:(("my-app",pid=1200,fd=7))
LISTEN 0      4096         0.0.0.0:8443        0.0.0.0:*    users:(("docker-proxy",pid=2100,fd=4))
LISTEN 0      128          192.168.1.10:8088   0.0.0.0:*    users:(("firepanel",pid=900,fd=8))`

const ssUDPOut = `UNCONN 0      0           0.0.0.0:53        0.0.0.0:*    users:(("dnsmasq",pid=700,fd=5))
UNCONN 0      0              [::]:5353          [::]:*`

func TestParseSSOutputTCP(t *testing.T) {
	ports := ParseSSOutput(ssTCPOut, "tcp")
	if len(ports) != 4 {
		t.Fatalf("期望 4 条（22 合并双栈），实际 %d: %+v", len(ports), ports)
	}
	byPort := map[int]ListenPort{}
	for _, p := range ports {
		byPort[p.Port] = p
	}
	ssh := byPort[22]
	if ssh.Process != "sshd" || ssh.PID != "800" || ssh.Address != "0.0.0.0" {
		t.Errorf("ssh 解析错误: %+v", ssh)
	}
	if byPort[6060].Address != "127.0.0.1" {
		t.Errorf("仅本机地址解析错误: %+v", byPort[6060])
	}
	if !byPort[8443].Docker {
		t.Errorf("docker-proxy 未识别: %+v", byPort[8443])
	}
	if byPort[8088].Process != "firepanel" {
		t.Errorf("普通进程解析错误: %+v", byPort[8088])
	}
}

func TestParseSSOutputUDP(t *testing.T) {
	ports := ParseSSOutput(ssUDPOut, "udp")
	if len(ports) != 2 {
		t.Fatalf("期望 2 条，实际 %d", len(ports))
	}
	if ports[0].Proto != "udp" || ports[0].Port != 53 || ports[0].Process != "dnsmasq" {
		t.Errorf("UDP 解析错误: %+v", ports[0])
	}
	if ports[1].Process != "" || ports[1].Port != 5353 {
		t.Errorf("无进程 UDP 行解析错误: %+v", ports[1])
	}
}

func TestParseSSOutputEmptyAndGarbage(t *testing.T) {
	if got := ParseSSOutput("", "tcp"); len(got) != 0 {
		t.Errorf("空输出应返回空")
	}
	if got := ParseSSOutput("garbage line\nshort", "tcp"); len(got) != 0 {
		t.Errorf("坏行应被跳过: %+v", got)
	}
}

const netstatOut = `Active Internet connections (only servers)
Proto Recv-Q Send-Q Local Address           Foreign Address         State       PID/Program name
tcp        0      0 0.0.0.0:22              0.0.0.0:*               LISTEN      699/sshd
tcp        0      0 192.168.1.10:8088    0.0.0.0:*               LISTEN      900/firepanel
tcp        0      0 :::22                   :::*                    LISTEN      699/sshd
udp        0      0 0.0.0.0:53              0.0.0.0:*                           699/dnsmasq
udp        0      0 0.0.0.0:67              0.0.0.0:*                           699/dnsmasq`

func TestParseNetstatListening(t *testing.T) {
	ports := ParseNetstatListening(netstatOut, "tcp")
	if len(ports) != 2 {
		t.Fatalf("期望 2 条 tcp（:::22 去重），实际 %d: %+v", len(ports), ports)
	}
	if ports[0].Port != 22 || ports[0].Process != "sshd" || ports[0].PID != "699" {
		t.Errorf("tcp 解析错误: %+v", ports[0])
	}
	if ports[1].Port != 8088 || ports[1].Process != "firepanel" {
		t.Errorf("tcp 8088 解析错误: %+v", ports[1])
	}
	udps := ParseNetstatListening(netstatOut, "udp")
	if len(udps) != 2 {
		t.Fatalf("期望 2 条 udp（:67 去重），实际 %d", len(udps))
	}
	if udps[0].Port != 53 || udps[0].Process != "dnsmasq" {
		t.Errorf("udp 解析错误: %+v", udps[0])
	}
}

func TestParseNetstatNoPermission(t *testing.T) {
	// 无 -p 权限时末列缺失（只有 6 列）
	out := "tcp        0      0 0.0.0.0:22              0.0.0.0:*               LISTEN"
	ports := ParseNetstatListening(out, "tcp")
	if len(ports) != 1 || ports[0].Process != "" || ports[0].Port != 22 {
		t.Errorf("无进程列解析错误: %+v", ports)
	}
}
