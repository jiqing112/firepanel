package sysinfo

import (
	"regexp"
	"strconv"
	"strings"
)

// ListenPort 一条监听端口扫描结果（ss -tlnp / -ulnp 解析）。
type ListenPort struct {
	Proto   string `json:"proto"`   // tcp|udp
	Port    int    `json:"port"`
	Address string `json:"address"` // 监听地址：0.0.0.0 / :: = 对外；127.0.0.1 / ::1 = 仅本机
	Process string `json:"process"` // 进程名（无权限或已退出时为空）
	PID     string `json:"pid"`
	Docker  bool   `json:"docker"` // docker-proxy 端口映射
}

var ssProcRe = regexp.MustCompile(`\(\("([^"]*)",pid=(\d+)`)

// ParseSSOutput 解析 `ss -H -tlnp`（proto=tcp）或 `ss -H -ulnp`（proto=udp）输出。
// 行形态：LISTEN 0 128 0.0.0.0:22 0.0.0.0:* users:(("sshd",pid=800,fd=3))
func ParseSSOutput(out, proto string) []ListenPort {
	seen := map[string]bool{}
	var outPorts []ListenPort
	for _, line := range strings.Split(out, "\n") {
		fields := strings.Fields(strings.TrimSpace(line))
		if len(fields) < 5 {
			continue
		}
		local := fields[3]
		i := strings.LastIndex(local, ":")
		if i < 0 {
			continue
		}
		port, err := strconv.Atoi(local[i+1:])
		if err != nil || port <= 0 || port > 65535 {
			continue
		}
		p := ListenPort{Proto: proto, Port: port, Address: local[:i]}
		rest := strings.Join(fields[5:], " ")
		if m := ssProcRe.FindStringSubmatch(rest); m != nil {
			p.Process = m[1]
			p.PID = m[2]
			p.Docker = p.Process == "docker-proxy"
		}
		// 同端口可能绑定多个地址（如 0.0.0.0 与 [::]），合并为一行
		key := p.Proto + "|" + strconv.Itoa(p.Port)
		if seen[key] {
			for j := range outPorts {
				if outPorts[j].Proto == p.Proto && outPorts[j].Port == p.Port {
					if outPorts[j].Process == "" {
						outPorts[j].Process = p.Process
						outPorts[j].PID = p.PID
					}
					if externallyExposed(p.Address) && !externallyExposed(outPorts[j].Address) {
						outPorts[j].Address = p.Address
					}
				}
			}
			continue
		}
		seen[key] = true
		outPorts = append(outPorts, p)
	}
	return outPorts
}

// ExternallyExposed 监听地址是否对外可达（非仅本机回环）。
func externallyExposed(addr string) bool {
	switch addr {
	case "127.0.0.1", "::1", "[::1]":
		return false
	}
	return true
}

// ParseNetstatListening 解析 `netstat -tlnp` / `-ulnp` 输出（busybox / net-tools 风格），
// 作为无 iproute2（如 Alpine 最小安装）环境下 ss 的兜底：
//
//	tcp        0      0 0.0.0.0:22    0.0.0.0:*   LISTEN  699/sshd
//	udp        0      0 0.0.0.0:53    0.0.0.0:*           699/dnsmasq
func ParseNetstatListening(out, proto string) []ListenPort {
	seen := map[string]bool{}
	var ports []ListenPort
	for _, line := range strings.Split(out, "\n") {
		fields := strings.Fields(strings.TrimSpace(line))
		// 列：proto rq sq local peer [state] pid/name（state 仅 tcp 有）
		if len(fields) < 6 {
			continue
		}
		// 行协议须与查询一致（tcp/tcp6 → tcp；udp/udp6 → udp）
		base := strings.TrimSuffix(fields[0], "6")
		if base != proto {
			continue
		}
		local := fields[3]
		i := strings.LastIndex(local, ":")
		if i < 0 {
			continue
		}
		port, err := strconv.Atoi(local[i+1:])
		if err != nil || port <= 0 || port > 65535 {
			continue
		}
		p := ListenPort{Proto: proto, Port: port, Address: local[:i]}
		// 末列 699/sshd；无 -p 权限时缺省
		last := fields[len(fields)-1]
		if j := strings.Index(last, "/"); j > 0 {
			if _, err := strconv.Atoi(last[:j]); err == nil {
				p.PID = last[:j]
				p.Process = last[j+1:]
				p.Docker = p.Process == "docker-proxy"
			}
		}
		key := p.Proto + "|" + strconv.Itoa(p.Port)
		if seen[key] {
			for j := range ports {
				if ports[j].Proto == p.Proto && ports[j].Port == p.Port && ports[j].Process == "" {
					ports[j].Process = p.Process
					ports[j].PID = p.PID
				}
			}
			continue
		}
		seen[key] = true
		ports = append(ports, p)
	}
	return ports
}
