// Package notify：告警通知（Telegram / Webhook / SMTP 邮件）。
package notify

import (
	"bytes"
	"io"
	"encoding/json"
	"fmt"
	"net/http"
	"net/smtp"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Config 各渠道配置（config_json 反序列化目标）。
type TelegramConfig struct {
	BotToken string `json:"bot_token"`
	ChatID   string `json:"chat_id"`
}

type WebhookConfig struct {
	URL     string            `json:"url"`
	Method  string            `json:"method"` // POST|GET
	Headers map[string]string `json:"headers"`
}

type SMTPConfig struct {
	Host     string `json:"host"`
	Port     int    `json:"port"`
	Username string `json:"username"`
	Password string `json:"password"`
	From     string `json:"from"`
	To       string `json:"to"` // 逗号分隔
	UseTLS   bool   `json:"use_tls"`
}

// Event 告警事件。
type Event struct {
	Type    string
	Message string
	Detail  string
	Time    time.Time
}

func (e Event) format() string {
	ts := e.Time.Format("2006-01-02 15:04:05")
	if e.Detail != "" {
		return fmt.Sprintf("[FirePanel %s] %s\n%s\n—— %s", e.Type, e.Message, e.Detail, ts)
	}
	return fmt.Sprintf("[FirePanel %s] %s\n—— %s", e.Type, e.Message, ts)
}

// Notifier 多渠道通知器。
type Notifier struct {
	mu       sync.RWMutex
	tg       *TelegramConfig
	webhook  *WebhookConfig
	smtp     *SMTPConfig
	client   *http.Client
}

func New() *Notifier {
	return &Notifier{client: &http.Client{Timeout: 10 * time.Second}}
}

// Load 装载启用的渠道配置。
func (n *Notifier) Load(tg *TelegramConfig, wh *WebhookConfig, sm *SMTPConfig) {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.tg, n.webhook, n.smtp = tg, wh, sm
}

// Send 向全部已装载渠道发送事件（单渠道失败不影响其他）。
func (n *Notifier) Send(ev Event) []error {
	n.mu.RLock()
	tg, wh, sm := n.tg, n.webhook, n.smtp
	n.mu.RUnlock()
	return n.SendWith(ev, tg, wh, sm)
}

// SendWith 按给定渠道发送（测试接口用）。
func (n *Notifier) SendWith(ev Event, tg *TelegramConfig, wh *WebhookConfig, sm *SMTPConfig) []error {
	var errs []error
	if tg != nil {
		if err := n.sendTelegram(tg, ev); err != nil {
			errs = append(errs, fmt.Errorf("telegram: %w", err))
		}
	}
	if wh != nil {
		if err := n.sendWebhook(wh, ev); err != nil {
			errs = append(errs, fmt.Errorf("webhook: %w", err))
		}
	}
	if sm != nil {
		if err := n.sendSMTP(sm, ev); err != nil {
			errs = append(errs, fmt.Errorf("smtp: %w", err))
		}
	}
	return errs
}

func (n *Notifier) sendTelegram(cfg *TelegramConfig, ev Event) error {
	if cfg.BotToken == "" || cfg.ChatID == "" {
		return nil
	}
	body, _ := json.Marshal(map[string]string{
		"chat_id": cfg.ChatID,
		"text":    ev.format(),
	})
	resp, err := n.client.Post(
		"https://api.telegram.org/bot"+cfg.BotToken+"/sendMessage",
		"application/json", bytes.NewReader(body),
	)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		return fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	return nil
}

func (n *Notifier) sendWebhook(cfg *WebhookConfig, ev Event) error {
	if cfg.URL == "" {
		return nil
	}
	method := cfg.Method
	if method == "" {
		method = "POST"
	}
	var body io.Reader
	if method == "POST" {
		payload, _ := json.Marshal(map[string]any{
			"type": ev.Type, "message": ev.Message, "detail": ev.Detail,
			"time": ev.Time.Format(time.RFC3339), "text": ev.format(),
		})
		body = bytes.NewReader(payload)
	}
	req, err := http.NewRequest(method, cfg.URL, body)
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	for k, v := range cfg.Headers {
		req.Header.Set(k, v)
	}
	resp, err := n.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		return fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	return nil
}

func (n *Notifier) sendSMTP(cfg *SMTPConfig, ev Event) error {
	if cfg.Host == "" || cfg.To == "" {
		return nil
	}
	port := cfg.Port
	if port == 0 {
		port = 587
	}
	addr := cfg.Host + ":" + strconv.Itoa(port)
	from := cfg.From
	if from == "" {
		from = cfg.Username
	}
	toList := strings.Split(cfg.To, ",")

	subject := "FirePanel 告警: " + ev.Type
	msg := strings.Join([]string{
		"From: " + from,
		"To: " + strings.Join(toList, ","),
		"Subject: =?utf-8?B?" + base64Of(subject) + "?=",
		"MIME-Version: 1.0",
		"Content-Type: text/plain; charset=utf-8",
		"",
		ev.format(),
	}, "\r\n")

	var auth smtp.Auth
	if cfg.Username != "" {
		auth = smtp.PlainAuth("", cfg.Username, cfg.Password, cfg.Host)
	}
	return smtp.SendMail(addr, auth, from, toList, []byte(msg))
}

func base64Of(s string) string {
	const b64 = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789+/"
	var out strings.Builder
	data := []byte(s)
	for i := 0; i < len(data); i += 3 {
		var chunk [3]byte
		n := copy(chunk[:], data[i:])
		var v uint32
		for j := 0; j < 3; j++ {
			v = v<<8 | uint32(chunk[j])
		}
		for j := 0; j < 4; j++ {
			if j <= n {
				out.WriteByte(b64[(v>>uint(18-6*j))&0x3f])
			} else {
				out.WriteByte('=')
			}
		}
	}
	return out.String()
}
