package api

import (
	"errors"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"golang.org/x/crypto/bcrypt"

	"firepanel/internal/fw"
	"firepanel/internal/store"
)

// 本文件是认证入口的加固版本：auth.go 保留原有实现（历史兼容），
// 路由指向这里的实现。加固点：
//   - 密码最短 8 位（原 6 位），bcrypt 错误不再被忽略；
//   - 登录限速同时按来源 IP 与用户名计数（原仅 IP，且可被 X-Forwarded-For 伪造）；
//   - 用户名不存在时也执行一次 bcrypt 比对，消除账号枚举时间差。

const minPasswordLen = 8

// dummyHash 用于「用户不存在」分支的等时比对（固定内容，非真实凭据）。
var dummyHash = func() []byte {
	h, err := bcrypt.GenerateFromPassword([]byte("firepanel-dummy-password"), bcrypt.DefaultCost)
	if err != nil {
		return []byte("$2a$10$invalidinvalidinvalidinvalidinvalidinvalidinvalidinvalidinv")
	}
	return h
}()

// sessionResponse 登录/初始化响应；字段名走 JSON tag，保持 {token, user} 不变。
type sessionResponse struct {
	Session string      `json:"token"`
	User    *store.User `json:"user"`
}

// loginGuard 登录尝试计数（IP 与用户名双维度）。
var loginGuard = struct {
	sync.Mutex
	buckets map[string]*rateBucket
}{buckets: map[string]*rateBucket{}}

// allowAttempt 在 1 分钟窗口内限制某个 key 的尝试次数。
func allowAttempt(key string, max int) bool {
	if max <= 0 {
		max = 10
	}
	now := time.Now()
	loginGuard.Lock()
	defer loginGuard.Unlock()
	for k, b := range loginGuard.buckets {
		if now.Sub(b.windowAt) > 10*time.Minute {
			delete(loginGuard.buckets, k)
		}
	}
	b := loginGuard.buckets[key]
	if b == nil || now.Sub(b.windowAt) > time.Minute {
		b = &rateBucket{count: 0, windowAt: now}
		loginGuard.buckets[key] = b
	}
	b.count++
	return b.count <= max
}

// LoginSecure 登录：IP + 用户名双维度限速，未知用户走等时 bcrypt。
func (s *Server) LoginSecure(c *gin.Context) {
	var req struct {
		Username string `json:"username" binding:"required"`
		Password string `json:"password" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "请输入账号与密码"})
		return
	}
	max := s.App.Cfg.Auth.LoginRateMax
	userKey := "user:" + strings.ToLower(strings.TrimSpace(req.Username))
	if !allowAttempt("ip:"+c.ClientIP(), max) || !allowAttempt(userKey, max) {
		c.JSON(http.StatusTooManyRequests, gin.H{"error": "尝试过于频繁，请稍后再试"})
		return
	}

	u, err := s.App.Store.GetUserByName(req.Username)
	if errors.Is(err, store.ErrNotFound) {
		// 与真实校验耗时对齐，避免通过响应时间枚举账号
		_ = bcrypt.CompareHashAndPassword(dummyHash, []byte(req.Password))
		s.audit(c, "login_fail", "user:"+req.Username, nil, nil)
		c.JSON(http.StatusUnauthorized, gin.H{"error": "用户名或密码错误"})
		return
	}
	if err != nil {
		respErr(c, http.StatusInternalServerError, err)
		return
	}
	if bcrypt.CompareHashAndPassword([]byte(u.PassHash), []byte(req.Password)) != nil {
		s.audit(c, "login_fail", "user:"+req.Username, nil, nil)
		c.JSON(http.StatusUnauthorized, gin.H{"error": "用户名或密码错误"})
		return
	}
	sess, err := s.issueToken(u)
	if err != nil {
		respErr(c, http.StatusInternalServerError, err)
		return
	}
	s.audit(c, "login", "user:"+u.Username, nil, nil)
	c.JSON(http.StatusOK, sessionResponse{Session: sess, User: u})
}

// SetupSecure 首次初始化：串行化、密码 8 位起、bcrypt 失败即报错。
func (s *Server) SetupSecure(c *gin.Context) {
	s.setupMu.Lock()
	defer s.setupMu.Unlock()
	n, err := s.App.Store.CountUsers()
	if err != nil {
		respErr(c, http.StatusInternalServerError, err)
		return
	}
	if n > 0 {
		c.JSON(http.StatusForbidden, gin.H{"error": "已初始化，请直接登录"})
		return
	}
	var req struct {
		Username string `json:"username" binding:"required,min=2,max=32"`
		Password string `json:"password" binding:"required,min=8,max=72"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "用户名 2-32 字符，密码至少 8 位"})
		return
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(req.Password), bcrypt.DefaultCost)
	if err != nil {
		respErr(c, http.StatusInternalServerError, err)
		return
	}
	u := &store.User{Username: req.Username, PassHash: string(hash), Role: "admin"}
	if err := s.App.Store.CreateUser(u); err != nil {
		respErr(c, http.StatusInternalServerError, err)
		return
	}
	sess, err := s.issueToken(u)
	if err != nil {
		respErr(c, http.StatusInternalServerError, err)
		return
	}
	s.audit(c, "setup", "user:"+req.Username, nil, map[string]any{"username": req.Username})
	c.JSON(http.StatusOK, sessionResponse{Session: sess, User: u})
}

// CreateUserSecure 新增用户：密码 8 位起，bcrypt 失败不再吞掉。
func (s *Server) CreateUserSecure(c *gin.Context) {
	var req struct {
		Username string `json:"username" binding:"required,min=2,max=32"`
		Password string `json:"password" binding:"required,min=8,max=72"`
		Role     string `json:"role" binding:"required,oneof=admin viewer"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "参数不合法（用户名 2-32 字符，密码至少 8 位）"})
		return
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(req.Password), bcrypt.DefaultCost)
	if err != nil {
		respErr(c, http.StatusInternalServerError, err)
		return
	}
	u := &store.User{Username: req.Username, PassHash: string(hash), Role: req.Role}
	if err := s.App.Store.CreateUser(u); err != nil {
		respErr(c, http.StatusInternalServerError, err)
		return
	}
	s.audit(c, "create", "user:"+u.Username, nil, map[string]any{"role": u.Role})
	c.JSON(http.StatusOK, gin.H{"item": u})
}

// BackupBundle 导出 JSON 备份（全量结构化配置）。
// 其他格式（iptables-save / nft）仍需在对应后端下导出。
func (s *Server) BackupBundle(c *gin.Context) {
	format := c.Query("format")
	switch format {
	case "iptables-save":
		if s.App.Backend.Name() != fw.BackendIPTables {
			c.JSON(http.StatusBadRequest, gin.H{"error": "当前后端不支持 iptables-save 导出"})
			return
		}
		out, err := s.App.Exec.Run(c.Request.Context(), "iptables-save")
		if err != nil {
			respErr(c, http.StatusInternalServerError, err)
			return
		}
		c.Header("Content-Disposition", "attachment; filename=firepanel-iptables.v4")
		c.Data(http.StatusOK, "text/plain; charset=utf-8", []byte(out))
	case "nft":
		if s.App.Backend.Name() != fw.BackendNftables {
			c.JSON(http.StatusBadRequest, gin.H{"error": "当前后端不支持 nft 导出"})
			return
		}
		out, err := s.App.Exec.Run(c.Request.Context(), "nft", "list", "ruleset")
		if err != nil {
			respErr(c, http.StatusInternalServerError, err)
			return
		}
		c.Header("Content-Disposition", "attachment; filename=firepanel-nft-ruleset.nft")
		c.Data(http.StatusOK, "text/plain; charset=utf-8", []byte(out))
	default:
		snap, err := s.backupBundle()
		if err != nil {
			respErr(c, http.StatusInternalServerError, err)
			return
		}
		c.Header("Content-Disposition", "attachment; filename=firepanel-backup.json")
		c.JSON(http.StatusOK, snap)
	}
}
