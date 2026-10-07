// Package sysinfo：系统信息与指标采集（发行版、内核、运行时长、连接数、网卡速率）。
package sysinfo

import (
	"fmt"
	"net"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/shirou/gopsutil/v4/host"
	gnet "github.com/shirou/gopsutil/v4/net"
)

// Info 系统静态信息。
type Info struct {
	Hostname string `json:"hostname"`
	Distro   string `json:"distro"`
	Kernel   string `json:"kernel"`
	Arch     string `json:"arch"`
	Uptime   string `json:"uptime"`
}

// GetInfo 采集静态信息。
func GetInfo() Info {
	info := Info{}
	if hn, err := os.Hostname(); err == nil {
		info.Hostname = hn
	}
	info.Distro = prettyName()
	info.Kernel, _ = host.KernelVersion()
	info.Arch, _ = host.KernelArch()
	up, err := host.Uptime()
	if err == nil {
		d := up / 86400
		h := (up % 86400) / 3600
		m := (up % 3600) / 60
		info.Uptime = fmt.Sprintf("%d 天 %d 小时 %d 分", d, h, m)
	}
	return info
}

func prettyName() string {
	b, err := os.ReadFile("/etc/os-release")
	if err != nil {
		return "未知"
	}
	for _, line := range strings.Split(string(b), "\n") {
		if strings.HasPrefix(line, "PRETTY_NAME=") {
			return strings.Trim(strings.TrimPrefix(line, "PRETTY_NAME="), `"`)
		}
	}
	return "未知"
}

// NetInterfaces 枚举当前系统网卡（排除 lo），附带 IPv4 地址。
type NetInterface struct {
	Name  string   `json:"name"`
	Addrs []string `json:"addrs"` // IPv4/CIDR
}

// NetInterfaces 遍历系统网卡。
func NetInterfaces() []NetInterface {
	out := []NetInterface{}
	ifaces, err := net.Interfaces()
	if err != nil {
		return out
	}
	for _, ifc := range ifaces {
		if ifc.Name == "lo" || ifc.Flags&net.FlagLoopback != 0 {
			continue
		}
		ni := NetInterface{Name: ifc.Name, Addrs: []string{}}
		if addrs, err := ifc.Addrs(); err == nil {
			for _, a := range addrs {
				if ipnet, ok := a.(*net.IPNet); ok {
					if v4 := ipnet.IP.To4(); v4 != nil {
						ni.Addrs = append(ni.Addrs, v4.String())
					}
				}
			}
		}
		out = append(out, ni)
	}
	return out
}

// ConnCount 读取连接跟踪条目数。
func ConnCount() int64 {
	if b, err := os.ReadFile("/proc/sys/net/netfilter/nf_conntrack_count"); err == nil {
		if n, err := strconv.ParseInt(strings.TrimSpace(string(b)), 10, 64); err == nil {
			return n
		}
	}
	// 兜底：逐行统计 /proc/net/nf_conntrack
	if b, err := os.ReadFile("/proc/net/nf_conntrack"); err == nil {
		lines := 0
		for _, l := range strings.Split(string(b), "\n") {
			if strings.TrimSpace(l) != "" {
				lines++
			}
		}
		return int64(lines)
	}
	return 0
}

// NetSampler 网卡速率采样器（全接口聚合，排除 lo）。
type NetSampler struct {
	mu       sync.Mutex
	last     map[string]gnet.IOCountersStat
	lastTime time.Time
	rxBps    int64
	txBps    int64
}

func NewNetSampler() *NetSampler { return &NetSampler{} }

// Tick 采样一次，计算自上次调用以来的字节速率。
func (s *NetSampler) Tick() (rxBps, txBps int64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now()
	stats, err := gnet.IOCounters(true)
	if err != nil {
		return s.rxBps, s.txBps
	}
	cur := map[string]gnet.IOCountersStat{}
	var rxTotal, txTotal uint64
	for _, st := range stats {
		if st.Name == "lo" || strings.HasPrefix(st.Name, "veth") && false {
			continue
		}
		cur[st.Name] = st
		rxTotal += st.BytesRecv
		txTotal += st.BytesSent
	}
	if !s.lastTime.IsZero() {
		dt := now.Sub(s.lastTime).Seconds()
		if dt > 0.2 {
			var lastRx, lastTx uint64
			for _, st := range s.last {
				lastRx += st.BytesRecv
				lastTx += st.BytesSent
			}
			// 计数器回绕保护（重启/容器重建）
			if rxTotal >= lastRx && txTotal >= lastTx {
				s.rxBps = int64(float64(rxTotal-lastRx) / dt)
				s.txBps = int64(float64(txTotal-lastTx) / dt)
			}
		}
	}
	s.last = cur
	s.lastTime = now
	return s.rxBps, s.txBps
}
