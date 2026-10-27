package app

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"firepanel/internal/config"
	"firepanel/internal/fw"
	"firepanel/internal/store"
)

// fakeBackend 记录应用过的规则（app 层测试用）。
type fakeBackend struct {
	failOnce bool
	applied  int
	rules    []fw.Rule
}

func (f *fakeBackend) Name() string                    { return "fake" }
func (f *fakeBackend) Available(context.Context) error { return nil }
func (f *fakeBackend) Init(context.Context) error      { return nil }
func (f *fakeBackend) Snapshot(context.Context) (*fw.Snapshot, error) {
	return &fw.Snapshot{Backend: "fake"}, nil
}
func (f *fakeBackend) Counters(context.Context) (map[string]fw.Counter, error) {
	return map[string]fw.Counter{}, nil
}
func (f *fakeBackend) Cleanup(context.Context) error { return nil }
func (f *fakeBackend) Apply(_ context.Context, rules []fw.Rule) error {
	if f.failOnce {
		f.failOnce = false
		return errors.New("apply failed")
	}
	f.applied++
	f.rules = append([]fw.Rule(nil), rules...)
	return nil
}

func newTestApp(t *testing.T, fb fw.FirewallBackend) *App {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("打开测试库: %v", err)
	}
	t.Cleanup(func() { st.Close() })
	log := slog.New(slog.NewTextHandler(io.Discard, &slog.HandlerOptions{Level: slog.LevelError}))
	rec := fw.NewReconciler(fb, []int{22}, time.Hour, time.Hour)
	a := &App{Store: st, Rec: rec, Backend: fb, Log: log, Cfg: &config.Config{}}
	rec.Events = a.BroadcastEvent
	return a
}

func mustForward(t *testing.T, st *store.Store, name string) {
	t.Helper()
	if err := st.CreateForward(&store.ForwardRule{
		Name: name, Protocol: "tcp", ListenPort: "8080",
		DstIP: "192.168.1.10", DstPort: "80", Enabled: true,
	}); err != nil {
		t.Fatalf("创建转发规则 %s: %v", name, err)
	}
}

// 变更闭包失败时必须回滚数据库，不能留下半截状态。
func TestMutateRollsBackOnClosureError(t *testing.T) {
	fb := &fakeBackend{}
	a := newTestApp(t, fb)
	mustForward(t, a.Store, "keep")

	_, err := a.Mutate("10.1.2.3", func() error {
		mustForward(t, a.Store, "half-written")
		return errors.New("业务校验失败")
	})
	if err == nil {
		t.Fatal("闭包失败应返回错误")
	}
	fwds, _ := a.Store.ListForwards()
	if len(fwds) != 1 || fwds[0].Name != "keep" {
		t.Fatalf("数据库应回滚到变更前状态，得到 %+v", fwds)
	}
	if fb.applied == 0 {
		t.Fatal("回滚后应重新同步内核")
	}
}

// 内核应用失败时同样要回滚数据库。
func TestMutateRollsBackOnApplyError(t *testing.T) {
	fb := &fakeBackend{failOnce: true}
	a := newTestApp(t, fb)
	mustForward(t, a.Store, "keep")

	_, err := a.Mutate("10.1.2.3", func() error { return a.Store.DeleteForward(1) })
	if err == nil || !strings.Contains(err.Error(), "已回滚") {
		t.Fatalf("同步失败应返回已回滚错误，得到 %v", err)
	}
	fwds, _ := a.Store.ListForwards()
	if len(fwds) != 1 {
		t.Fatalf("数据库应保留原规则，得到 %+v", fwds)
	}
}

// 高危变更挂起态必须持久化；重启后自动回滚到变更前状态（不能绕过确认生效）。
func TestDangerPersistedAndRecoveredOnRestart(t *testing.T) {
	fb := &fakeBackend{}
	a := newTestApp(t, fb)
	mustForward(t, a.Store, "keep")

	res, err := a.Mutate("10.1.2.3", func() error {
		// 全网段黑名单 → 必判高危
		return a.Store.CreateIPList(&store.IPListEntry{
			ListType: "black", IPOrCIDR: "0.0.0.0/0", Source: "manual",
		})
	})
	if err != nil {
		t.Fatalf("Mutate: %v", err)
	}
	if res == nil || !res.Danger {
		t.Fatalf("全网段黑名单应进入高危队列，得到 %+v", res)
	}
	raw, err := a.Store.GetSetting(pendingDangerKey)
	if err != nil || raw == "" {
		t.Fatalf("高危挂起态应持久化到 settings，err=%v raw=%q", err, raw)
	}
	// 危险规则此时不应已经下发内核
	for _, r := range fb.rules {
		if r.Chain == fw.ChainInput && r.Action == "drop" {
			t.Fatalf("延时生效窗口内不应下发 drop 规则: %+v", r)
		}
	}

	// 模拟进程重启：新 reconciler（内存态为空）+ 同一数据库
	a2 := newTestAppWithStore(t, fb, a.Store)
	if !a2.RecoverPendingDanger() {
		t.Fatal("重启后应检测到未完成的高危变更并回滚")
	}
	lists, _ := a2.Store.ListIPLists("")
	if len(lists) != 0 {
		t.Fatalf("未确认的高危变更应被回滚，名单仍为 %+v", lists)
	}
	fwds, _ := a2.Store.ListForwards()
	if len(fwds) != 1 {
		t.Fatalf("变更前数据应恢复，得到 %+v", fwds)
	}
	if _, err := a2.Store.GetSetting(pendingDangerKey); err == nil {
		t.Fatal("回滚后应清除挂起标记")
	}
}

func newTestAppWithStore(t *testing.T, fb fw.FirewallBackend, st *store.Store) *App {
	t.Helper()
	log := slog.New(slog.NewTextHandler(io.Discard, &slog.HandlerOptions{Level: slog.LevelError}))
	rec := fw.NewReconciler(fb, []int{22}, time.Hour, time.Hour)
	a := &App{Store: st, Rec: rec, Backend: fb, Log: log, Cfg: &config.Config{}}
	rec.Events = a.BroadcastEvent
	return a
}
