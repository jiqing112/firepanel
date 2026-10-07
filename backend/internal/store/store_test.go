package store

import (
	"path/filepath"
	"testing"
	"time"
)

func openTest(t *testing.T) *Store {
	t.Helper()
	s, err := Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("打开测试库: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func TestMigrationsAndUsers(t *testing.T) {
	s := openTest(t)
	n, err := s.CountUsers()
	if err != nil || n != 0 {
		t.Fatalf("新库用户数应为 0: n=%d err=%v", n, err)
	}
	u := &User{Username: "admin", PassHash: "x", Role: "admin"}
	if err := s.CreateUser(u); err != nil {
		t.Fatal(err)
	}
	got, err := s.GetUserByName("admin")
	if err != nil || got.ID != u.ID {
		t.Fatalf("查询用户失败: %v", err)
	}
	if _, err := s.GetUserByName("nope"); err != ErrNotFound {
		t.Fatalf("不存在的用户应返回 ErrNotFound，得到 %v", err)
	}
}

func TestForwardCRUD(t *testing.T) {
	s := openTest(t)
	r := &ForwardRule{
		Name: "web", Protocol: "both", ListenPort: "80",
		DstIP: "192.168.1.100", DstPort: "8080", Enabled: true, Remark: "官网",
	}
	if err := s.CreateForward(r); err != nil {
		t.Fatal(err)
	}
	if r.ID == 0 {
		t.Fatal("创建后应回填 ID")
	}
	got, err := s.GetForward(r.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Protocol != "both" || !got.Enabled {
		t.Errorf("回读不一致: %+v", got)
	}
	got.ListenPort = "443"
	if err := s.UpdateForward(got); err != nil {
		t.Fatal(err)
	}
	if again, _ := s.GetForward(r.ID); again.ListenPort != "443" {
		t.Errorf("更新未生效: %+v", again)
	}
	if err := s.SetForwardEnabled(r.ID, false); err != nil {
		t.Fatal(err)
	}
	if again, _ := s.GetForward(r.ID); again.Enabled {
		t.Error("停用未生效")
	}
	if err := s.DeleteForward(r.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.GetForward(r.ID); err != ErrNotFound {
		t.Fatalf("删除后应 ErrNotFound，得到 %v", err)
	}
}

func TestIPLists(t *testing.T) {
	s := openTest(t)
	e := &IPListEntry{ListType: "black", IPOrCIDR: "10.0.0.5", Source: "manual"}
	if err := s.CreateIPList(e); err != nil {
		t.Fatal(err)
	}
	// 幂等 upsert：同类型同 IP 不报错且不重复
	e2 := &IPListEntry{ListType: "black", IPOrCIDR: "10.0.0.5", GroupTag: "cc"}
	if err := s.CreateIPList(e2); err != nil {
		t.Fatal(err)
	}
	items, err := s.ListIPLists("black")
	if err != nil || len(items) != 1 {
		t.Fatalf("黑名单应有 1 条: n=%d err=%v", len(items), err)
	}
	if items[0].GroupTag != "cc" {
		t.Errorf("upsert 应更新 group_tag: %+v", items[0])
	}
	// 过期清理
	past := time.Now().Add(-time.Hour)
	e3 := &IPListEntry{ListType: "black", IPOrCIDR: "10.0.0.6", ExpiresAt: &past}
	_ = s.CreateIPList(e3)
	deleted, err := s.DeleteIPListsExpired(time.Now())
	if err != nil || deleted != 1 {
		t.Fatalf("应清理 1 条过期: n=%d err=%v", deleted, err)
	}
}

func TestSyncStateAndSettings(t *testing.T) {
	s := openTest(t)
	if err := s.SetSetting("jwt_secret", "abc"); err != nil {
		t.Fatal(err)
	}
	if v, _ := s.GetSetting("jwt_secret"); v != "abc" {
		t.Errorf("setting 回读错误: %q", v)
	}
	now := time.Now()
	err := s.UpsertSyncState(&SyncState{Backend: "nftables", LastOKAt: &now, DriftJSON: "", LastGoodJSON: "{}"})
	if err != nil {
		t.Fatal(err)
	}
	st, err := s.GetSyncState()
	if err != nil || st.Backend != "nftables" {
		t.Fatalf("sync_state 回读错误: %+v err=%v", st, err)
	}
}
