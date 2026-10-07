package api

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/golang-jwt/jwt/v5"
	"golang.org/x/crypto/bcrypt"

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

/* ------------------------- 登录限速 ------------------------- */

type rateBucket struct {
	count    int
	windowAt time.Time
}

var loginLimiter struct {
	sync.Mutex
	buckets map[string]*rateBucket
}

func init() { loginLimiter.buckets = map[string]*rateBucket{} }

func (s *Server) rateLimitLogin() gin.HandlerFunc {
	return func(c *gin.Context) {
		ip := c.ClientIP()
		max := s.App.Cfg.Auth.LoginRateMax
		if max <= 0 {
			max = 10
		}
		now := time.Now()
		loginLimiter.Lock()
		// 顺手清理过期桶（上次活动超 10 分钟），防止长期运行内存缓慢增长
		for k, b := range loginLimiter.buckets {
			if now.Sub(b.windowAt) > 10*time.Minute {
				delete(loginLimiter.buckets, k)
			}
		}
		b := loginLimiter.buckets[ip]
		if b == nil || now.Sub(b.windowAt) > time.Minute {
			b = &rateBucket{count: 0, windowAt: now}
			loginLimiter.buckets[ip] = b
		}
		b.count++
		over := b.count > max
		loginLimiter.Unlock()
		if over {
			c.AbortWithStatusJSON(http.StatusTooManyRequests, gin.H{"error": "尝试过于频繁，请稍后再试"})
			return
		}
		c.Next()
	}
}

/* ------------------------- setup / login / users ------------------------- */

func (s *Server) Setup(c *gin.Context) {
	n, err := s.App.Store.CountUsers()
	if err != nil {
		respErr(c, 500, err)
		return
	}
	if n > 0 {
		c.JSON(http.StatusForbidden, gin.H{"error": "已初始化，请直接登录"})
		return
	}
	var req struct {
		Username string `json:"username" binding:"required,min=2,max=32"`
		Password string `json:"password" binding:"required,min=6,max=72"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(400, gin.H{"error": "用户名 2-32 字符，密码至少 6 位"})
		return
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(req.Password), bcrypt.DefaultCost)
	if err != nil {
		respErr(c, 500, err)
		return
	}
	u := &store.User{Username: req.Username, PassHash: string(hash), Role: "admin"}
	if err := s.App.Store.CreateUser(u); err != nil {
		respErr(c, 500, err)
		return
	}
	token, _ := s.issueToken(u)
	s.audit(c, "setup", "user:"+req.Username, nil, map[string]any{"username": req.Username})
	c.JSON(200, gin.H{"token": token, "user": u})
}

func (s *Server) Login(c *gin.Context) {
	var req struct {
		Username string `json:"username" binding:"required"`
		Password string `json:"password" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(400, gin.H{"error": "请输入账号与密码"})
		return
	}
	u, err := s.App.Store.GetUserByName(req.Username)
	if errors.Is(err, store.ErrNotFound) {
		s.audit(c, "login_fail", "user:"+req.Username, nil, nil)
		c.JSON(401, gin.H{"error": "用户名或密码错误"})
		return
	}
	if err != nil {
		respErr(c, 500, err)
		return
	}
	if bcrypt.CompareHashAndPassword([]byte(u.PassHash), []byte(req.Password)) != nil {
		s.audit(c, "login_fail", "user:"+req.Username, nil, nil)
		c.JSON(401, gin.H{"error": "用户名或密码错误"})
		return
	}
	token, err := s.issueToken(u)
	if err != nil {
		respErr(c, 500, err)
		return
	}
	s.audit(c, "login", "user:"+u.Username, nil, nil)
	c.JSON(200, gin.H{"token": token, "user": u})
}

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

func (s *Server) CreateUser(c *gin.Context) {
	if s.roleOf(c) != "admin" {
		c.AbortWithStatusJSON(403, gin.H{"error": "需要管理员权限"})
		return
	}
	var req struct {
		Username string `json:"username" binding:"required,min=2,max=32"`
		Password string `json:"password" binding:"required,min=6,max=72"`
		Role     string `json:"role" binding:"required,oneof=admin viewer"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(400, gin.H{"error": "参数不合法"})
		return
	}
	hash, _ := bcrypt.GenerateFromPassword([]byte(req.Password), bcrypt.DefaultCost)
	u := &store.User{Username: req.Username, PassHash: string(hash), Role: req.Role}
	if err := s.App.Store.CreateUser(u); err != nil {
		respErr(c, 500, err)
		return
	}
	s.audit(c, "create", "user:"+u.Username, nil, map[string]any{"role": u.Role})
	c.JSON(200, gin.H{"item": u})
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
