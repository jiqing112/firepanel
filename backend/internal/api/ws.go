package api

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"
)

// Hub WebSocket 中心：单连接多 topic（stats/events/logs）。
type Hub struct {
	mu    sync.RWMutex
	clients map[int64]*wsClient
	nextID int64
	log   *slog.Logger
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
	CheckOrigin: func(r *http.Request) bool {
		return true // 同源部署 + JWT 鉴权；开发跨域由 token 保护
	},
}

// Handler 返回 /api/ws 处理器；authFn 校验查询参数 token。
func (h *Hub) Handler(authFn func(token string) (*claims, bool)) gin.HandlerFunc {
	return func(c *gin.Context) {
		token := c.Query("token")
		cl, ok := authFn(token)
		if !ok {
			c.JSON(http.StatusUnauthorized, gin.H{"error": "会话无效"})
			return
		}
		conn, err := upgrader.Upgrade(c.Writer, c.Request, nil)
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
			Action  string   `json:"action"` // subscribe|unsubscribe
			Topics  []string `json:"topics"`
		}
		if err := cl.conn.ReadJSON(&req); err != nil {
			return
		}
		h.mu.Lock()
		for _, t := range req.Topics {
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
