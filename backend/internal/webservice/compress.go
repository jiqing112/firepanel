package webservice

import (
	"bufio"
	"compress/gzip"
	"io"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
)

// rateSlots 为秒级环的槽数：保留 60 个完整秒，另加正在累计的当前秒与余量。
const rateSlots = 64

type rateBucket struct {
	sec, req, bytes int64
}

// rateRing 记录最近 rateSlots 秒每秒的请求数与字节数。零值可用。
type rateRing struct {
	mu sync.Mutex
	b  [rateSlots]rateBucket
}

func (r *rateRing) add(sec, req int64) {
	if sec <= 0 {
		return
	}
	r.mu.Lock()
	b := &r.b[sec%rateSlots]
	if b.sec != sec {
		*b = rateBucket{sec: sec}
	}
	b.req += req
	r.mu.Unlock()
}

func (r *rateRing) addBytes(sec, bytes int64) {
	if sec <= 0 || bytes <= 0 {
		return
	}
	r.mu.Lock()
	b := &r.b[sec%rateSlots]
	if b.sec != sec {
		*b = rateBucket{sec: sec}
	}
	b.bytes += bytes
	r.mu.Unlock()
}

// rate60 返回 now 之前 n 个完整秒的每秒请求数曲线（UI 小曲线用），按时间正序，缺失的秒为 0。
func (r *rateRing) rate60(now int64, n int) []int64 {
	if n > rateSlots-1 {
		n = rateSlots - 1
	}
	out := make([]int64, 0, n)
	r.mu.Lock()
	defer r.mu.Unlock()
	for i := n; i >= 1; i-- {
		sec := now - int64(i)
		var req int64
		if b := r.b[sec%rateSlots]; b.sec == sec {
			req = b.req
		}
		out = append(out, req)
	}
	return out
}

// avg 返回 now 之前 n 个完整秒的平均 QPS。
func (r *rateRing) avg(now int64, n int) float64 {
	if n > rateSlots-1 {
		n = rateSlots - 1
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	var total int64
	for i := 1; i <= n; i++ {
		sec := now - int64(i)
		if b := r.b[sec%rateSlots]; b.sec == sec {
			total += b.req
		}
	}
	return float64(total) / float64(n)
}

const minCompressSize = 256

var compressibleTypes = []string{
	"text/", "application/json", "application/javascript", "application/xml", "application/xhtml",
	"application/rss", "application/atom", "application/wasm", "image/svg+xml", "application/x-javascript",
}

// compressible 判断 Content-Type 是否值得压缩。
func compressible(contentType string) bool {
	ct := strings.ToLower(contentType)
	if strings.HasPrefix(ct, "text/event-stream") {
		return false
	}
	for _, p := range compressibleTypes {
		if strings.HasPrefix(ct, p) {
			return true
		}
	}
	return false
}

// negotiate 根据 Accept-Encoding 选择 gzip，不支持时返回空串。
func negotiate(accept string) string {
	for _, part := range strings.Split(accept, ",") {
		name, params, _ := strings.Cut(strings.TrimSpace(part), ";")
		name = strings.ToLower(strings.TrimSpace(name))
		if q := strings.TrimSpace(params); strings.HasPrefix(q, "q=") {
			if v, err := strconv.ParseFloat(strings.TrimPrefix(q, "q="), 64); err == nil && v == 0 {
				continue
			}
		}
		if name == "gzip" || name == "*" {
			return "gzip"
		}
	}
	return ""
}

// rateAvgWindow 为 QPS 与吞吐取均值的完整秒数。
const rateAvgWindow = 10

// Rate 是一个节点的速率快照：QPS、BPS 为最近 rateAvgWindow 个完整秒的均值，QPS60 为最近 60 秒均值。
type Rate struct {
	QPS   float64 `json:"qps"`
	BPS   float64 `json:"bps"`
	QPS60 float64 `json:"qps60"`
}

// sum 返回 now 之前 n 个完整秒的请求数与字节数之和。
func (r *rateRing) sum(now int64, n int) (req, bytes int64) {
	if n > rateSlots-1 {
		n = rateSlots - 1
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	for i := 1; i <= n; i++ {
		sec := now - int64(i)
		if b := r.b[sec%rateSlots]; b.sec == sec {
			req += b.req
			bytes += b.bytes
		}
	}
	return req, bytes
}

// rate 返回速率快照。
func (r *rateRing) rate(now int64) Rate {
	q10, b10 := r.sum(now, rateAvgWindow)
	q60, _ := r.sum(now, 60)
	return Rate{
		QPS:   float64(q10) / rateAvgWindow,
		BPS:   float64(b10) / rateAvgWindow,
		QPS60: float64(q60) / 60,
	}
}

// addRate 把另一个节点的速率累加进来（汇总监听与实例总数用）。
func (r *Rate) addRate(o Rate) {
	r.QPS += o.QPS
	r.BPS += o.BPS
	r.QPS60 += o.QPS60
}

const rateSeriesSeconds = 60

// seriesPoints 是曲线的一个点。
type seriesPoint struct {
	Time     int64 `json:"t"`
	Requests int64 `json:"requests"`
	Bytes    int64 `json:"bytes"`
}

// series 返回 now 之前 n 个完整秒（不含正在累计的当前秒）的请求数与字节数曲线，按时间正序，缺失的秒为 0。
func (r *rateRing) series(now int64, n int) []seriesPoint {
	if n > rateSlots-1 {
		n = rateSlots - 1
	}
	out := make([]seriesPoint, 0, n)
	r.mu.Lock()
	defer r.mu.Unlock()
	for i := n; i >= 1; i-- {
		sec := now - int64(i)
		p := seriesPoint{Time: sec}
		if b := r.b[sec%rateSlots]; b.sec == sec {
			p.Requests, p.Bytes = b.req, b.bytes
		}
		out = append(out, p)
	}
	return out
}

// addSeries 把一条曲线累加进 out（同秒数值相加），用于把多个节点的曲线合并成汇总曲线。
func addSeries(out []seriesPoint, add []seriesPoint) []seriesPoint {
	if len(out) == 0 {
		return append(out, add...)
	}
	for i := range add {
		if i < len(out) {
			out[i].Requests += add[i].Requests
			out[i].Bytes += add[i].Bytes
		} else {
			out = append(out, add[i])
		}
	}
	return out
}

// compressHandler gzip 压缩中间件：首次写出响应头时决定是否压缩。
// 上游已自带 Content-Encoding（预压缩文件、反代上游的 gzip）时不再压。
func compressHandler(next http.Handler) http.Handler {
	pool := sync.Pool{New: func() any { return gzip.NewWriter(io.Discard) }}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		enc := negotiate(r.Header.Get("Accept-Encoding"))
		if enc == "" || r.Method == http.MethodHead {
			next.ServeHTTP(w, r)
			return
		}
		cw := &compressWriter{ResponseWriter: w, req: r, pool: &pool}
		defer cw.close()
		next.ServeHTTP(cw, r)
	})
}

type compressWriter struct {
	http.ResponseWriter
	req     *http.Request
	pool    *sync.Pool
	enc     string
	w       *gzip.Writer
	decided bool
	// active 表示由本层压缩
	active bool
}

func (c *compressWriter) decide(status int, sniff []byte) {
	if c.decided {
		return
	}
	c.decided = true
	h := c.Header()
	if h.Get("Content-Encoding") != "" || h.Get("Content-Range") != "" ||
		status < 200 || status == http.StatusNoContent || status == http.StatusNotModified || status == http.StatusSwitchingProtocols {
		return
	}
	ct := h.Get("Content-Type")
	if ct == "" && len(sniff) > 0 {
		ct = http.DetectContentType(sniff)
	}
	if !compressible(ct) {
		return
	}
	if n, err := strconv.Atoi(h.Get("Content-Length")); err == nil && n < minCompressSize {
		return
	}
	c.active = true
	c.enc = "gzip"
	h.Del("Content-Length")
	h.Set("Content-Encoding", c.enc)
	h.Add("Vary", "Accept-Encoding")
	// 压缩后内容变化，强校验 ETag 不再适用
	if et := h.Get("Etag"); et != "" && !strings.HasPrefix(et, "W/") {
		h.Set("Etag", "W/"+et)
	}
}

func (c *compressWriter) WriteHeader(code int) {
	c.decide(code, nil)
	c.ResponseWriter.WriteHeader(code)
}

func (c *compressWriter) Write(p []byte) (int, error) {
	if !c.decided {
		c.decide(http.StatusOK, p)
		c.ResponseWriter.WriteHeader(http.StatusOK)
	}
	if !c.active {
		return c.ResponseWriter.Write(p)
	}
	if c.w == nil {
		c.w = c.pool.Get().(*gzip.Writer)
		c.w.Reset(c.ResponseWriter)
	}
	return c.w.Write(p)
}

// Flush 支持流式输出（SSE 等）：刷新 gzip 缓冲与底层连接。
func (c *compressWriter) Flush() {
	if c.w != nil {
		_ = c.w.Flush()
	}
	_ = http.NewResponseController(c.ResponseWriter).Flush()
}

// Hijack 与 Unwrap 保持协议升级（WebSocket）可用。
func (c *compressWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	return http.NewResponseController(c.ResponseWriter).Hijack()
}

func (c *compressWriter) Unwrap() http.ResponseWriter { return c.ResponseWriter }

// close 结束压缩流（请求结束时调用）。
func (c *compressWriter) close() {
	if c.w != nil {
		_ = c.w.Close()
	}
}
