// Package notify：告警通知（Telegram / Webhook / SMTP 邮件）。
package notify

import (
	"bytes"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"net"
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
	mu      sync.RWMutex
	tg      *TelegramConfig
	webhook *WebhookConfig
	smtp    *SMTPConfig
	client  *http.Client
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

// sendSMTP 发送邮件：UseTLS=true 走隐式 TLS（通常 465），否则先尝试 STARTTLS（587）。
// 旧实现用 smtp.SendMail，UseTLS 字段被静默忽略 —— 465 端口永远发不出去。
func (n *Notifier) sendSMTP(cfg *SMTPConfig, ev Event) error {
	if cfg.Host == "" || cfg.To == "" {
		return nil
	}
	port := cfg.Port
	if port == 0 {
		if cfg.UseTLS {
			port = 465
		} else {
			port = 587
		}
	}
	toList := []string{}
	for _, to := range strings.Split(cfg.To, ",") {
		if to = strings.TrimSpace(to); to != "" {
			toList = append(toList, to)
		}
	}
	if len(toList) == 0 {
		return nil
	}
	from := cfg.From
	if from == "" {
		from = cfg.Username
	}
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

	addr := net.JoinHostPort(cfg.Host, strconv.Itoa(port))
	tlsCfg := &tls.Config{ServerName: cfg.Host}
	dialer := &net.Dialer{Timeout: 10 * time.Second}

	var conn net.Conn
	var err error
	if cfg.UseTLS {
		conn, err = tls.DialWithDialer(dialer, "tcp", addr, tlsCfg)
	} else {
		conn, err = dialer.Dial("tcp", addr)
	}
	if err != nil {
		return err
	}
	defer conn.Close()

	c, err := smtp.NewClient(conn, cfg.Host)
	if err != nil {
		return err
	}
	defer c.Close()
	if !cfg.UseTLS {
		if ok, _ := c.Extension("STARTTLS"); ok {
			if err := c.StartTLS(tlsCfg); err != nil {
				return fmt.Errorf("STARTTLS 失败: %w", err)
			}
		}
	}
	if cfg.Username != "" {
		if err := c.Auth(smtp.PlainAuth("", cfg.Username, cfg.Password, cfg.Host)); err != nil {
			return fmt.Errorf("SMTP 认证失败: %w", err)
		}
	}
	if err := c.Mail(from); err != nil {
		return err
	}
	for _, to := range toList {
		if err := c.Rcpt(to); err != nil {
			return err
		}
	}
	w, err := c.Data()
	if err != nil {
		return err
	}
	if _, err := w.Write([]byte(msg)); err != nil {
		return err
	}
	if err := w.Close(); err != nil {
		return err
	}
	return c.Quit()
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
