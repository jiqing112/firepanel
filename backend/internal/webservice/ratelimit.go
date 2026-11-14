package webservice

import (
	"net/netip"
	"sync"
	"time"
)

// limiter 按客户端 IP 限流：令牌桶控制速率与突发，并发计数限制单 IP 同时处理的请求数。
type limiter struct {
	rate     float64
	burst    float64
	maxConns int

	mu     sync.Mutex
	tokens float64
	last   time.Time
	conns  map[netip.Addr]int64
}

func newLimiter(cfg RateLimit) *limiter {
	return &limiter{rate: cfg.Rate, burst: float64(cfg.Burst), maxConns: cfg.MaxConns, tokens: float64(cfg.Burst), last: time.Now(), conns: map[netip.Addr]int64{}}
}

// acquire 取得一次请求的配额。ok 为 false 时返回建议的等待时间。
func (l *limiter) acquire(ip netip.Addr) (release func(), retry time.Duration, ok bool) {
	now := time.Now()
	l.mu.Lock()
	defer l.mu.Unlock()

	// 补充令牌
	elapsed := now.Sub(l.last).Seconds()
	l.last = now
	l.tokens += elapsed * l.rate
	if l.tokens > l.burst {
		l.tokens = l.burst
	}
	if l.tokens < 1 {
		l.tokens += 0 // 不足一个令牌
		wait := time.Duration((1 - l.tokens) / l.rate * float64(time.Second))
		return nil, wait, false
	}
	l.tokens--
	// 并发限制
	if l.maxConns > 0 {
		if l.conns[ip] >= int64(l.maxConns) {
			return nil, time.Second, false
		}
		l.conns[ip]++
	}
	var once sync.Once
	return func() {
		once.Do(func() {
			l.mu.Lock()
			if l.maxConns > 0 {
				l.conns[ip]--
				if l.conns[ip] <= 0 {
					delete(l.conns, ip)
				}
			}
			l.mu.Unlock()
		})
	}, 0, true
}
