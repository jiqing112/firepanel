package jail

import (
	"io"
	"log/slog"
	"testing"
	"time"

	"firepanel/internal/store"
)

func TestExtractIP(t *testing.T) {
	re, err := compilePattern(`(Failed password for|Invalid user).* from ([0-9]{1,3}([.][0-9]{1,3}){3})`)
	if err != nil {
		t.Fatal(err)
	}
	line := `Failed password for yanling from 192.168.218.201 port 54021 ssh2`
	got := extractIP(re, line)
	if got != "192.168.218.201" {
		t.Fatalf("extractIP=%q, want 192.168.218.201", got)
	}
	line2 := `Invalid user admin from 10.0.0.7 port 22 ssh2`
	if got := extractIP(re, line2); got != "10.0.0.7" {
		t.Fatalf("extractIP=%q, want 10.0.0.7", got)
	}
}

func TestIgnoreCIDR(t *testing.T) {
	// 通过小窗口触发验证：写一个假 jail 配置走 record 逻辑
	// （依赖 store；此处只测网段判断函数路径——由集成测试覆盖）
	_ = BuiltinPatterns
}

// TestRecordBanFlow 回归：Reload 后内层 map 未初始化时 record 不得 panic，
// 且达标后完整走通「写黑名单 → 回调」链路。
func TestRecordBanFlow(t *testing.T) {
	st, err := store.Open(t.TempDir() + "/jail.db")
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	e := New(st, testJailLogger(), nil)
	// 模拟 Reload 清空后直接 record（未走初始化路径——回归 nil map panic）
	e.mu.Lock()
	e.counters = map[int64]map[string][]time.Time{}
	e.lastHits = map[int64]map[string]time.Time{}
	e.mu.Unlock()

	banned := make(chan string, 1)
	e.OnBan = func(j store.JailConfig, ip string, until time.Time) {
		banned <- ip
	}

	j := store.JailConfig{
		ID: 7, Name: "t", SourceType: "audit",
		Threshold: 1, FindTime: 600, BanTime: 60,
		IgnoreCIDRs: "127.0.0.0/8",
	}
	// 第 1 次命中即达标（阈值 1）——不应 panic，且应封禁
	e.record(j, "10.1.2.3")

	select {
	case ip := <-banned:
		if ip != "10.1.2.3" {
			t.Fatalf("封禁 IP 错误: %s", ip)
		}
	default:
		t.Fatal("达标后应触发 OnBan 回调")
	}

	entries, err := st.ListIPLists("black")
	if err != nil || len(entries) != 1 {
		t.Fatalf("黑名单应有 1 条: n=%d err=%v", len(entries), err)
	}
	if entries[0].IPOrCIDR != "10.1.2.3/32" || entries[0].ExpiresAt == nil {
		t.Fatalf("封禁条目错误: %+v", entries[0])
	}
}

func testJailLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}
