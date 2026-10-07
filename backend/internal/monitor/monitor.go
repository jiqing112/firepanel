// Package monitor：实时流量监控。周期采样 /proc/net/dev（网卡速率）与 ss -tinp
// （TCP 连接级字节计数），差分出速率并维护滑动窗口，供「流量监控」页展示。
package monitor

import (
	"context"
	"os"
	"sort"
	"strings"
	"sync"
	"time"

	"firepanel/internal/fw"
	"firepanel/internal/sysinfo"
)

// WindowSize 每网卡保留的采样点数（interval=2s → 约 5 分钟窗口）。
const WindowSize = 150

type IfaceRate struct {
	Name    string  `json:"name"`
	RxBps   float64 `json:"rx_bps"`
	TxBps   float64 `json:"tx_bps"`
	RxTotal uint64  `json:"rx_total"`
	TxTotal uint64  `json:"tx_total"`
}

type IfacePoint struct {
	Ts    int64   `json:"ts"`
	RxBps float64 `json:"rx_bps"`
	TxBps float64 `json:"tx_bps"`
}

type ConnRate struct {
	Proto   string  `json:"proto"` // tcp|udp
	Local   string  `json:"local"`
	Remote  string  `json:"remote"`
	Process string  `json:"process"`
	PID     string  `json:"pid"`
	Count   int     `json:"count"`  // 聚合条数（TCP 恒 1；UDP 按进程聚合的会话数）
	Remotes int     `json:"remotes"` // UDP 聚合行的对端地址数
	RxBps   float64 `json:"rx_bps"`
	TxBps   float64 `json:"tx_bps"`
	RxTotal uint64  `json:"rx_total"`
	TxTotal uint64  `json:"tx_total"`
}

type snapshot struct {
	Interfaces []IfaceRate
	Window     map[string][]IfacePoint
	Conns      []ConnRate
	ConnsErr   string
	Ts         int64
}

type sockKey struct{ local, remote string }

type Monitor struct {
	exec     fw.Executor
	interval time.Duration

	mu       sync.Mutex
	prevDev  map[string]sysinfo.DevCounter
	prevDevT time.Time
	ifaces   map[string]IfaceRate
	window   map[string][]IfacePoint

	prevSock  map[sockKey]sysinfo.SocketSample
	prevSockT time.Time
	conns     []ConnRate
	connsErr  string
	ts        int64
}

func New(exec fw.Executor, interval time.Duration) *Monitor {
	if interval <= 0 {
		interval = 2 * time.Second
	}
	return &Monitor{
		exec:      exec,
		interval:  interval,
		ifaces:    map[string]IfaceRate{},
		window:    map[string][]IfacePoint{},
		prevSock:  map[sockKey]sysinfo.SocketSample{},
		prevDev:   map[string]sysinfo.DevCounter{},
	}
}

// Run 周期采样直到 ctx 取消。首个周期只建立基线。
func (m *Monitor) Run(ctx context.Context) {
	m.sample(ctx) // 建立基线
	t := time.NewTicker(m.interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			m.sample(ctx)
		}
	}
}

func (m *Monitor) sample(ctx context.Context) {
	now := time.Now()

	// 网卡速率（/proc/net/dev 差分）
	if data, err := os.ReadFile("/proc/net/dev"); err == nil {
		devs := sysinfo.ParseProcNetDev(string(data))
		m.mu.Lock()
		if !m.prevDevT.IsZero() {
			if dt := now.Sub(m.prevDevT).Seconds(); dt > 0.2 {
				for name, c := range devs {
					p := m.prevDev[name]
					r := IfaceRate{
						Name:    name,
						RxBps:   deltaRate(c.RxBytes, p.RxBytes, dt),
						TxBps:   deltaRate(c.TxBytes, p.TxBytes, dt),
						RxTotal: c.RxBytes,
						TxTotal: c.TxBytes,
					}
					m.ifaces[name] = r
					win := append(m.window[name], IfacePoint{Ts: now.Unix(), RxBps: r.RxBps, TxBps: r.TxBps})
					if len(win) > WindowSize {
						win = win[len(win)-WindowSize:]
					}
					m.window[name] = win
				}
				// 消失的网卡清理窗口
				for name := range m.window {
					if _, ok := devs[name]; !ok {
						delete(m.window, name)
						delete(m.ifaces, name)
					}
				}
			}
		}
		m.prevDev = devs
		m.prevDevT = now
		m.mu.Unlock()
	}

	// TCP 连接级速率（ss 字节计数差分）
	tcpOut, tcpErr := m.exec.Run(ctx, "ss", "-H", "-tinp")
	udpOut, udpErr := m.exec.Run(ctx, "ss", "-H", "-uanp")
	if tcpErr != nil && udpErr != nil {
		m.mu.Lock()
		m.conns = nil
		m.connsErr = "需要 ss 命令（iproute2）：安装后连接级流量自动恢复"
		m.mu.Unlock()
	} else {
		sNow := time.Now()
		var conns []ConnRate
		if tcpErr == nil {
			conns = append(conns, m.sampleTCP(tcpOut, sNow)...)
		}
		if udpErr == nil {
			conns = append(conns, aggregateUDP(sysinfo.ParseSSUDPDetail(udpOut))...)
		}
		m.mu.Lock()
		m.conns = conns
		m.connsErr = ""
		m.ts = sNow.Unix()
		m.mu.Unlock()
	}
}

// sampleTCP 差分计算 TCP 连接速率并更新上一帧缓存（调用方持锁或不并发）。
func (m *Monitor) sampleTCP(out string, sNow time.Time) []ConnRate {
	socks := sysinfo.ParseSSDetail(out)
	conns := make([]ConnRate, 0, len(socks))
	m.mu.Lock()
	defer m.mu.Unlock()
	dt := sNow.Sub(m.prevSockT).Seconds()
	computable := !m.prevSockT.IsZero() && dt > 0.2 // 首帧只建基线
	for _, s := range socks {
		cr := ConnRate{
			Proto: "tcp", Local: s.Local, Remote: s.Remote, Process: s.Process, PID: s.PID, Count: 1,
			RxTotal: s.RxTotal, TxTotal: s.TxTotal,
		}
		if computable {
			key := sockKey{s.Local, s.Remote}
			if p, ok := m.prevSock[key]; ok {
				cr.RxBps = deltaRate(s.RxTotal, p.RxTotal, dt)
				cr.TxBps = deltaRate(s.TxTotal, p.TxTotal, dt)
			}
		}
		conns = append(conns, cr)
	}
	prev := make(map[sockKey]sysinfo.SocketSample, len(socks))
	for _, s := range socks {
		prev[sockKey{s.Local, s.Remote}] = s
	}
	m.prevSock = prev
	m.prevSockT = sNow
	return conns
}

// aggregateUDP 把 UDP 会话按进程聚合为一行（内核无 UDP 字节计数，逐条展示只有噪声；
// fork 型进程如 socat 会产生数百个单会话子进程，聚合 key 不含 PID 与对端）。
func aggregateUDP(socks []sysinfo.SocketSample) []ConnRate {
	type agg struct {
		row     *ConnRate
		remotes map[string]bool
	}
	order := []string{}
	byName := map[string]*agg{}
	for _, s := range socks {
		name := s.Process
		a, ok := byName[name]
		if !ok {
			a = &agg{
				row:     &ConnRate{Proto: "udp", Local: localIPOnly(s.Local), Remote: s.Remote, Process: name, PID: s.PID, Count: 0},
				remotes: map[string]bool{},
			}
			byName[name] = a
			order = append(order, name)
		}
		a.row.Count++
		if len(a.row.PID) == 0 {
			a.row.PID = s.PID
		}
		a.remotes[s.Remote] = true
	}
	out := make([]ConnRate, 0, len(order))
	for _, name := range order {
		row := byName[name].row
		row.Remotes = len(byName[name].remotes)
		if row.Count == 1 {
			row.Remotes = 1
		}
		out = append(out, *row)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Count > out[j].Count })
	return out
}

// localIPOnly 去掉端口，保留归属匹配用的地址部分。
func localIPOnly(addr string) string {
	if strings.HasPrefix(addr, "[") {
		if i := strings.Index(addr, "]"); i >= 0 {
			return addr[:i+1]
		}
		return addr
	}
	if i := strings.LastIndex(addr, ":"); i >= 0 {
		return addr[:i]
	}
	return addr
}

func deltaRate(cur, prev uint64, dt float64) float64 {
	if cur < prev {
		return 0 // 计数器回绕/重置
	}
	return float64(cur-prev) / dt
}

// Snapshot 返回当前速率、窗口与连接列表（连接按收+发速率降序）。
func (m *Monitor) Snapshot() snapshot {
	m.mu.Lock()
	defer m.mu.Unlock()
	ifaces := make([]IfaceRate, 0, len(m.ifaces))
	for _, r := range m.ifaces {
		ifaces = append(ifaces, r)
	}
	sort.Slice(ifaces, func(i, j int) bool { return ifaces[i].Name < ifaces[j].Name })
	win := make(map[string][]IfacePoint, len(m.window))
	for k, v := range m.window {
		cp := make([]IfacePoint, len(v))
		copy(cp, v)
		win[k] = cp
	}
	conns := make([]ConnRate, len(m.conns))
	copy(conns, m.conns)
	// 活跃优先：有速率的 TCP 在前；无速率的按 TCP → UDP（会话多在前）排列
	sort.Slice(conns, func(i, j int) bool {
		a, b := conns[i], conns[j]
		ra, rb := a.RxBps+a.TxBps, b.RxBps+b.TxBps
		if ra != rb {
			return ra > rb
		}
		if a.Proto != b.Proto {
			return a.Proto == "tcp"
		}
		if a.Count != b.Count {
			return a.Count > b.Count
		}
		return a.Process < b.Process
	})
	if len(conns) > 300 {
		conns = conns[:300]
	}
	return snapshot{Interfaces: ifaces, Window: win, Conns: conns, ConnsErr: m.connsErr, Ts: m.ts}
}

// SumBps 汇总一组网卡（排除 lo）的当前速率。
func SumBps(ifaces []IfaceRate) (rx, tx float64) {
	for _, i := range ifaces {
		if strings.EqualFold(i.Name, "lo") {
			continue
		}
		rx += i.RxBps
		tx += i.TxBps
	}
	return
}
