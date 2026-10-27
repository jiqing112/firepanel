// Package notify：告警通知（Telegram / Webhook / SMTP 邮件）。
package notify

import (
	"bytes"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/smtp"
	"net/url"
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

// ValidateConfig 按渠道校验配置（保存与测试共用），避免「保存成功但永远发不出去」。
func ValidateConfig(channel string, raw []byte) error {
	switch channel {
	case "telegram":
		var c TelegramConfig
		if err := json.Unmarshal(raw, &c); err != nil {
			return fmt.Errorf("telegram 配置格式错误（bot_token/chat_id 需为字符串）")
		}
		if strings.TrimSpace(c.BotToken) == "" || strings.TrimSpace(c.ChatID) == "" {
			return fmt.Errorf("telegram 需要 bot_token 与 chat_id")
		}
	case "webhook":
		var c WebhookConfig
		if err := json.Unmarshal(raw, &c); err != nil {
			return fmt.Errorf("webhook 配置格式错误")
		}
		u, err := url.Parse(strings.TrimSpace(c.URL))
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
			return fmt.Errorf("webhook 需要合法的 http(s) URL")
		}
		if c.Method != "" && !strings.EqualFold(c.Method, "POST") && !strings.EqualFold(c.Method, "GET") {
			return fmt.Errorf("webhook method 仅支持 POST 或 GET")
		}
	case "smtp":
		var c SMTPConfig
		if err := json.Unmarshal(raw, &c); err != nil {
			return fmt.Errorf("smtp 配置格式错误")
		}
		if strings.TrimSpace(c.Host) == "" || strings.TrimSpace(c.To) == "" {
			return fmt.Errorf("smtp 需要 host 与收件人 to")
		}
		if c.Port < 0 || c.Port > 65535 {
			return fmt.Errorf("smtp 端口不合法")
		}
	default:
		return fmt.Errorf("不支持的告警渠道: %s", channel)
	}
	return nil
}

// redactURLError 去掉 *url.Error 里的完整 URL（Telegram bot token、webhook 密钥
// 常在 URL 路径/查询串里，直接写日志会泄露凭据）。
func redactURLError(err error) error {
	var ue *url.Error
	if errors.As(err, &ue) {
		return ue.Err
	}
	return err
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
		// 不返回 *url.Error：它的 Error() 含完整 URL（bot token 就在路径里）
		return fmt.Errorf("telegram 请求失败: %w", redactURLError(err))
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
		// webhook URL 常带 token/secret，错误信息里不能带完整 URL
		return fmt.Errorf("webhook 请求失败: %w", redactURLError(err))
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
