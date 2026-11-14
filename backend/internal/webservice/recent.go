package webservice

import (
	"net"
	"net/netip"
	"sync"
	"time"
)

// recentWindowSec 为活动 IP 的时间窗口：最近 60 秒内出现过的访客地址算活动 IP，
// 与速率环的 60 秒均值口径一致。
const recentWindowSec = 60

// recentCap 为每个节点记录的近期地址上限（大量不同来源时超出即标记截断）。
const recentCap = 1024

// recentIPs 记录一个节点（站点 / 后端 / 监听级动作）近期到达过的访客地址。
type recentIPs struct {
	mu sync.Mutex
	m  map[netip.Addr]int64 // addr -> 最后出现的 Unix 秒
}

func newRecentIPs() *recentIPs {
	return &recentIPs{m: map[netip.Addr]int64{}}
}

// touch 登记地址在 sec 秒出现过。
func (r *recentIPs) touch(a netip.Addr, sec int64) {
	if !a.IsValid() || sec <= 0 {
		return
	}
	a = a.Unmap()
	r.mu.Lock()
	if len(r.m) >= recentCap {
		if _, ok := r.m[a]; !ok {
			// 满员时挤掉一条最旧的，保证新地址仍能进入
			var oldSec int64 = 1 << 62
			var oldKey netip.Addr
			for k, v := range r.m {
				if v < oldSec {
					oldSec, oldKey = v, k
				}
			}
			delete(r.m, oldKey)
		}
	}
	r.m[a] = sec
	r.mu.Unlock()
}

// each 遍历窗口内出现过的地址（顺带清理过期项）。full 表示记录表已满（结果不完整）。
func (r *recentIPs) each(now int64, fn func(a netip.Addr, last int64)) (full bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	full = len(r.m) >= recentCap
	for a, last := range r.m {
		if last < now-recentWindowSec {
			delete(r.m, a)
			continue
		}
		fn(a, last)
	}
	return full
}

// listenerAct 统计在监听层处理、不属于任何站点的请求：明文跳转 HTTPS、未匹配站点返回 404。
type listenerAct struct {
	total  int64mux
	rate   rateRing
	recent *recentIPs
}

// int64mux 为带锁的累计数（读多写少，避免为每个请求取整把锁）。
type int64mux struct {
	mu sync.Mutex
	v  int64
}

func (x *int64mux) add(n int64) {
	x.mu.Lock()
	x.v += n
	x.mu.Unlock()
}

func (x *int64mux) load() int64 {
	x.mu.Lock()
	defer x.mu.Unlock()
	return x.v
}

// hit 登记一次监听级处理。
func (act *listenerAct) hit(client netip.Addr, now int64) {
	act.total.add(1)
	act.rate.add(now, 1)
	act.recent.touch(client, now)
}

// activeReq 是进行中请求在站点上的登记项：拓扑视图据此列出进行中的请求与访客地址。
type activeReq struct {
	client   netip.AddrPort
	start    time.Time
	upstream atomicUpstream
}

// atomicUpstream 为进行中请求当前选中的后端（换后端重试时更新）。
type atomicUpstream struct {
	mu sync.Mutex
	u  *upstream
}

func (a *atomicUpstream) store(u *upstream) {
	a.mu.Lock()
	a.u = u
	a.mu.Unlock()
}

func (a *atomicUpstream) load() *upstream {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.u
}

// connTracker 记录站点上进行中的请求，供拓扑视图列出与统计。
type connTracker struct {
	mu sync.Mutex
	m  map[*activeReq]struct{}
}

func newConnTracker() *connTracker {
	return &connTracker{m: map[*activeReq]struct{}{}}
}

func (t *connTracker) begin(ar *activeReq) {
	t.mu.Lock()
	t.m[ar] = struct{}{}
	t.mu.Unlock()
}

func (t *connTracker) end(ar *activeReq) {
	t.mu.Lock()
	delete(t.m, ar)
	t.mu.Unlock()
}

func (t *connTracker) active() []*activeReq {
	t.mu.Lock()
	defer t.mu.Unlock()
	out := make([]*activeReq, 0, len(t.m))
	for k := range t.m {
		out = append(out, k)
	}
	return out
}

// netipAddr 返回零值地址（测试与拓扑查询的空 IP 参数用）。
func netipAddr() netip.Addr {
	var a netip.Addr
	return a
}

// nowUnix 返回当前 Unix 秒（测试用）。
func nowUnix() int64 { return time.Now().Unix() }

// netListen 供测试辅助：监听指定地址。
func netListen(addr string) (net.Listener, error) {
	return net.Listen("tcp", addr)
}
