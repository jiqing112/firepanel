package sysinfo

import (
	"strconv"
	"strings"
)

// DevCounter /proc/net/dev 中一个网卡的累计计数。
type DevCounter struct {
	RxBytes uint64 `json:"rx_bytes"`
	TxBytes uint64 `json:"tx_bytes"`
}

// ParseProcNetDev 解析 /proc/net/dev 内容：`eth0: rx_bytes packets ... tx_bytes packets ...`。
func ParseProcNetDev(content string) map[string]DevCounter {
	out := map[string]DevCounter{}
	for _, line := range strings.Split(content, "\n") {
		i := strings.Index(line, ":")
		if i < 0 {
			continue
		}
		name := strings.TrimSpace(line[:i])
		if name == "" {
			continue
		}
		fields := strings.Fields(line[i+1:])
		// 列序：rx_bytes rx_packets rx_errs rx_drop rx_fifo rx_frame rx_compressed rx_multicast
		//       tx_bytes tx_packets ...（共 16 列）
		if len(fields) < 16 {
			continue
		}
		rx, err1 := strconv.ParseUint(fields[0], 10, 64)
		tx, err2 := strconv.ParseUint(fields[8], 10, 64)
		if err1 != nil || err2 != nil {
			continue
		}
		out[name] = DevCounter{RxBytes: rx, TxBytes: tx}
	}
	return out
}

// SocketSample 一次 ss -tinp 采样中一条 TCP 连接的累计计数。
type SocketSample struct {
	State   string `json:"state"`
	Local   string `json:"local"`  // ip:port
	Remote  string `json:"remote"` // ip:port
	Process string `json:"process"`
	PID     string `json:"pid"`
	RxTotal uint64 `json:"rx_total"` // bytes_received（对端→本机）
	TxTotal uint64 `json:"tx_total"` // bytes_sent（本机→对端）
}

// ParseSSDetail 解析 `ss -H -tinp` 输出：
//
//	ESTAB 0 0 10.0.185.3:22 192.168.1.5:52345 users:(("sshd",pid=699,fd=3))
//	 cubic wscale:7,7 ... bytes_sent:10234 bytes_acked:10235 bytes_received:5678 ...
//
// 首行（无前导空白）为连接五元组，缩进行为 tcp_info 明细。
func ParseSSDetail(out string) []SocketSample {
	var samples []SocketSample
	var cur *SocketSample
	for _, line := range strings.Split(out, "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		if line[0] == ' ' || line[0] == '\t' {
			if cur == nil {
				continue
			}
			for _, f := range strings.Fields(line) {
				switch {
				case strings.HasPrefix(f, "bytes_received:"):
					cur.RxTotal, _ = strconv.ParseUint(strings.TrimPrefix(f, "bytes_received:"), 10, 64)
				case strings.HasPrefix(f, "bytes_sent:"):
					cur.TxTotal, _ = strconv.ParseUint(strings.TrimPrefix(f, "bytes_sent:"), 10, 64)
				}
			}
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 5 {
			cur = nil
			continue
		}
		s := SocketSample{State: fields[0], Local: fields[3], Remote: fields[4]}
		if s.State != "ESTAB" {
			cur = nil
			continue
		}
		rest := strings.Join(fields[5:], " ")
		if m := ssProcRe.FindStringSubmatch(rest); m != nil {
			s.Process = m[1]
			s.PID = m[2]
		}
		samples = append(samples, s)
		cur = &samples[len(samples)-1]
	}
	return samples
}

// ParseSSUDPDetail 解析 `ss -H -uanp` 中已连接（ESTAB）的 UDP socket。
// 内核不提供 per-socket UDP 字节计数，故仅含五元组与进程归属（字节恒 0）。
func ParseSSUDPDetail(out string) []SocketSample {
	var samples []SocketSample
	for _, line := range strings.Split(out, "\n") {
		if strings.TrimSpace(line) == "" || line[0] == ' ' || line[0] == '\t' {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 5 || fields[0] != "ESTAB" {
			continue
		}
		s := SocketSample{State: fields[0], Local: fields[3], Remote: fields[4]}
		rest := strings.Join(fields[5:], " ")
		if m := ssProcRe.FindStringSubmatch(rest); m != nil {
			s.Process = m[1]
			s.PID = m[2]
		}
		samples = append(samples, s)
	}
	return samples
}
