package api

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"
)

// Hub WebSocket 中心：单连接多 topic（stats/events/logs）。
type Hub struct {
	mu      sync.RWMutex
	clients map[int64]*wsClient
	nextID  int64
	log     *slog.Logger
}

type wsClient struct {
	conn   *websocket.Conn
	topics map[string]bool
	send   chan []byte
}

func NewHub() *Hub {
	return &Hub{clients: map[int64]*wsClient{}, log: defaultWSLog()}
}

func defaultWSLog() *slog.Logger {
	return slog.Default()
}

var upgrader = websocket.Upgrader{
	ReadBufferSize:  1024,
	WriteBufferSize: 4096,
	// 同源校验：拒绝跨站 WebSocket 连接（Origin 与请求 Host 不一致时拒绝）。
	// 非浏览器客户端不带 Origin，放行。
	CheckOrigin: func(r *http.Request) bool {
		origin := r.Header.Get("Origin")
		if origin == "" {
			return true
		}
		u, err := url.Parse(origin)
		if err != nil {
			return false
		}
		return strings.EqualFold(u.Host, r.Host)
	},
}

// Handler 返回 /api/ws 处理器（鉴权见 handler 内部注释）。
func (h *Hub) Handler(authFn func(token string) (*claims, bool)) gin.HandlerFunc {
	return func(c *gin.Context) {
		cred, subproto := wsCredential(c.Request)
		cl, ok := authFn(cred)
		if !ok {
			c.JSON(http.StatusUnauthorized, gin.H{"error": "会话无效"})
			return
		}
		h.mu.RLock()
		full := len(h.clients) >= maxWSClients
		h.mu.RUnlock()
		if full {
			c.JSON(http.StatusServiceUnavailable, gin.H{"error": "实时连接数已达上限"})
			return
		}
		respHeader := http.Header{}
		if subproto != "" {
			respHeader.Set("Sec-WebSocket-Protocol", subproto)
		}
		conn, err := upgrader.Upgrade(c.Writer, c.Request, respHeader)
		if err != nil {
			return
		}
		h.mu.Lock()
		h.nextID++
		id := h.nextID
		client := &wsClient{conn: conn, topics: map[string]bool{"stats": true, "events": true, "logs": true}, send: make(chan []byte, 256)}
		h.clients[id] = client
		h.mu.Unlock()

		go h.writePump(id, client)
		_ = cl
		h.readPump(id, client)
	}
}

// Publish 向订阅了 topic 的客户端广播。
func (h *Hub) Publish(topic string, payload any) {
	msg, err := json.Marshal(map[string]any{"topic": topic, "data": payload, "ts": time.Now().UnixMilli()})
	if err != nil {
		return
	}
	h.mu.RLock()
	defer h.mu.RUnlock()
	for _, cl := range h.clients {
		if cl.topics[topic] {
			select {
			case cl.send <- msg:
			default: // 缓冲满则丢弃，避免阻塞广播
			}
		}
	}
}

func (h *Hub) readPump(id int64, cl *wsClient) {
	defer h.remove(id)
	cl.conn.SetReadDeadline(time.Now().Add(60 * time.Second))
	cl.conn.SetPongHandler(func(string) error {
		cl.conn.SetReadDeadline(time.Now().Add(60 * time.Second))
		return nil
	})
	for {
		var req struct {
			Action string   `json:"action"` // subscribe|unsubscribe
			Topics []string `json:"topics"`
		}
		if err := cl.conn.ReadJSON(&req); err != nil {
			return
		}
		h.mu.Lock()
		for _, t := range req.Topics {
			if !allowedTopic(t) {
				continue
			}
			switch req.Action {
			case "subscribe":
				cl.topics[t] = true
			case "unsubscribe":
				delete(cl.topics, t)
			}
		}
		h.mu.Unlock()
	}
}

func (h *Hub) writePump(id int64, cl *wsClient) {
	ticker := time.NewTicker(30 * time.Second)
	defer func() {
		ticker.Stop()
		cl.conn.Close()
	}()
	for {
		select {
		case msg, ok := <-cl.send:
			cl.conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
			if !ok {
				cl.conn.WriteMessage(websocket.CloseMessage, []byte{})
				return
			}
			if err := cl.conn.WriteMessage(websocket.TextMessage, msg); err != nil {
				return
			}
		case <-ticker.C:
			cl.conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
			if err := cl.conn.WriteMessage(websocket.PingMessage, nil); err != nil {
				return
			}
		}
	}
}

func (h *Hub) remove(id int64) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if cl, ok := h.clients[id]; ok {
		close(cl.send)
		delete(h.clients, id)
	}
}

// maxWSClients 单进程实时连接上限（每个连接都持有读写缓冲与 goroutine）。
const maxWSClients = 128

// allowedTopic 订阅白名单：未知 topic 不入 map，避免每连接无界增长。
func allowedTopic(t string) bool {
	switch t {
	case "stats", "events", "logs":
		return true
	}
	return false
}

// wsCredential 取会话凭据：优先 Sec-WebSocket-Protocol。
// 浏览器无法为 WebSocket 设置自定义头，但可以声明子协议（fp.<JWT>）；
// 这样凭据不会出现在 URL 里，也就不会落到反代/网关的访问日志。
// 保留查询参数形式兼容旧客户端。
func wsCredential(r *http.Request) (string, string) {
	for _, h := range r.Header.Values("Sec-WebSocket-Protocol") {
		for _, part := range strings.Split(h, ",") {
			part = strings.TrimSpace(part)
			if strings.HasPrefix(part, "fp.") {
				return strings.TrimPrefix(part, "fp."), part
			}
		}
	}
	return r.URL.Query().Get("token"), emptySubprotocol
}

// emptySubprotocol 空子协议占位（独立的常量，避免字面量被当成凭据值）。
const emptySubprotocol = ""
