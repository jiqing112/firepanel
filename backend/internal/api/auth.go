package api

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/golang-jwt/jwt/v5"

	"firepanel/internal/store"
)

/* ------------------------- JWT ------------------------- */

type claims struct {
	UID      int64  `json:"uid"`
	Username string `json:"username"`
	Role     string `json:"role"`
	jwt.RegisteredClaims
}

func (s *Server) jwtSecret() []byte {
	if v, err := s.App.Store.GetSetting("jwt_secret"); err == nil && v != "" {
		return []byte(v)
	}
	// 自动生成并持久化
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	secret := hex.EncodeToString(b)
	_ = s.App.Store.SetSetting("jwt_secret", secret)
	return []byte(secret)
}

func (s *Server) issueToken(u *store.User) (string, error) {
	c := claims{
		UID: u.ID, Username: u.Username, Role: u.Role,
		RegisteredClaims: jwt.RegisteredClaims{
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(s.App.Cfg.Auth.TokenTTL)),
			IssuedAt:  jwt.NewNumericDate(time.Now()),
			Issuer:    "firepanel",
		},
	}
	return jwt.NewWithClaims(jwt.SigningMethodHS256, c).SignedString(s.jwtSecret())
}

func (s *Server) parseToken(tokenStr string) (*claims, error) {
	var c claims
	tok, err := jwt.ParseWithClaims(tokenStr, &c, func(t *jwt.Token) (any, error) {
		if _, ok := t.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, errors.New("非法签名算法")
		}
		return s.jwtSecret(), nil
	})
	if err != nil || !tok.Valid {
		return nil, errors.New("会话无效或已过期")
	}
	return &c, nil
}

func (s *Server) requireAuth() gin.HandlerFunc {
	return func(c *gin.Context) {
		// JWT 走独立 X-Panel-Token 头，与整站 Basic Auth（浏览器自动附带的
		// Authorization: Basic）互不冲突；Authorization: Bearer 保留兼容。
		token := c.GetHeader("X-Panel-Token")
		if token == "" {
			token = strings.TrimPrefix(c.GetHeader("Authorization"), "Bearer ")
		}
		if token == "" {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "未登录"})
			return
		}
		cl, err := s.parseToken(token)
		if err != nil {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": err.Error()})
			return
		}
		c.Set("uid", cl.UID)
		c.Set("username", cl.Username)
		c.Set("role", cl.Role)
		c.Next()
	}
}

func (s *Server) authFromQuery(token string) (*claims, bool) {
	cl, err := s.parseToken(token)
	if err != nil {
		return nil, false
	}
	// 与 HTTP 侧保持一致：账号被删除/改角色后，WebSocket 也必须立即按新状态鉴权。
	if u, uerr := s.App.Store.GetUserByID(cl.UID); uerr == nil {
		cl.Role, cl.Username = u.Role, u.Username
	} else if errors.Is(uerr, store.ErrNotFound) {
		return nil, false
	}
	return cl, true
}

func (s *Server) roleOf(c *gin.Context) string {
	role, _ := c.Get("role")
	if v, ok := role.(string); ok {
		return v
	}
	return ""
}

func (s *Server) requireAdmin() gin.HandlerFunc {
	return func(c *gin.Context) {
		if s.roleOf(c) != "admin" {
			c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"error": "需要管理员权限"})
			return
		}
		c.Next()
	}
}

/* ------------------------- 登录限速桶 ------------------------- */

// rateBucket 登录尝试计数窗口（限速逻辑见 secure.go 的 allowAttempt）。
type rateBucket struct {
	count    int
	windowAt time.Time
}

/* ------------------------- 当前会话 / 用户 ------------------------- */

func (s *Server) Me(c *gin.Context) {
	c.JSON(200, gin.H{
		"uid":      c.GetInt64("uid"),
		"username": c.GetString("username"),
		"role":     s.roleOf(c),
	})
}

func (s *Server) ListUsers(c *gin.Context) {
	users, err := s.App.Store.ListUsers()
	if err != nil {
		respErr(c, 500, err)
		return
	}
	c.JSON(200, gin.H{"items": users})
}

func (s *Server) DeleteUser(c *gin.Context) {
	if s.roleOf(c) != "admin" {
		c.AbortWithStatusJSON(403, gin.H{"error": "需要管理员权限"})
		return
	}
	id := pathID(c)
	if id == c.GetInt64("uid") {
		c.JSON(400, gin.H{"error": "不能删除自己"})
		return
	}
	if err := s.App.Store.DeleteUser(id); err != nil {
		respErr(c, 500, err)
		return
	}
	s.audit(c, "delete", fmtUser(id), nil, nil)
	c.JSON(200, gin.H{"ok": true})
}

/* ------------------------- 工具 ------------------------- */

func (s *Server) audit(c *gin.Context, action, target string, before, after any) {
	e := &store.AuditEntry{
		UserID:   c.GetInt64("uid"),
		Username: c.GetString("username"),
		Action:   action,
		Target:   target,
		IP:       c.ClientIP(),
	}
	if before != nil {
		e.BeforeJSON = string(mustJSON(before))
	}
	if after != nil {
		e.AfterJSON = string(mustJSON(after))
	}
	_ = s.App.Store.AddAudit(e)
}

func mustJSON(v any) []byte {
	b, _ := json.Marshal(v)
	return b
}

func respErr(c *gin.Context, code int, err error) {
	c.JSON(code, gin.H{"error": err.Error()})
}
