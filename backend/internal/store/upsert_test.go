package store

import (
	"testing"
	"time"
)

// UPSERT 冲突时必须回查真实 ID，并让 source 跟随最新来源（jail 复封已有名单）。
func TestCreateIPListUpsertReturnsRealID(t *testing.T) {
	s := openTest(t)
	first := &IPListEntry{ListType: "black", IPOrCIDR: "198.51.100.9", Source: "manual", Remark: "first"}
	if err := s.CreateIPList(first); err != nil {
		t.Fatal(err)
	}
	if first.ID == 0 {
		t.Fatal("首次插入应返回 ID")
	}
	exp := time.Now().Add(time.Minute)
	second := &IPListEntry{
		ListType: "black", IPOrCIDR: "198.51.100.9", Source: "jail:sshd",
		Remark: "banned", ExpiresAt: &exp,
	}
	if err := s.CreateIPList(second); err != nil {
		t.Fatal(err)
	}
	if second.ID != first.ID {
		t.Fatalf("冲突更新应返回同一 ID：%d vs %d", second.ID, first.ID)
	}
	list, err := s.ListIPLists("black")
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 1 {
		t.Fatalf("应只有一条记录: %+v", list)
	}
	if list[0].Source != "jail:sshd" || list[0].Remark != "banned" || list[0].ExpiresAt == nil {
		t.Fatalf("冲突更新字段未生效: %+v", list[0])
	}
}

func TestUpsertAlertReturnsRealID(t *testing.T) {
	s := openTest(t)
	a := &AlertConfig{Channel: "webhook", ConfigJSON: `{"url":"http://x"}`, EventsJSON: `["drift"]`, Enabled: true}
	if err := s.UpsertAlert(a); err != nil {
		t.Fatal(err)
	}
	if a.ID == 0 {
		t.Fatal("首次 upsert 应返回 ID")
	}
	b := &AlertConfig{Channel: "webhook", ConfigJSON: `{"url":"http://y"}`, EventsJSON: `[]`, Enabled: false}
	if err := s.UpsertAlert(b); err != nil {
		t.Fatal(err)
	}
	if b.ID != a.ID {
		t.Fatalf("重复 upsert 应返回同一 ID：%d vs %d", b.ID, a.ID)
	}
}

// 从未运行过的任务 last_run 必须是 NULL，不能是 0001-01-01。
func TestSetTaskRunZeroStoredAsNull(t *testing.T) {
	s := openTest(t)
	task := &ScheduledTask{Name: "sync-job", CronExpr: "*/5 * * * *", Action: "sync", Enabled: true}
	if err := s.CreateTask(task); err != nil {
		t.Fatal(err)
	}
	if err := s.SetTaskRun(task.ID, time.Time{}, time.Time{}); err != nil {
		t.Fatal(err)
	}
	got, err := s.GetTask(task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.LastRun != nil || got.NextRun != nil {
		t.Fatalf("零值时间应存为 NULL，实际 last=%v next=%v", got.LastRun, got.NextRun)
	}
	next := time.Now().Add(time.Minute)
	if err := s.SetTaskRun(task.ID, time.Now(), next); err != nil {
		t.Fatal(err)
	}
	got, _ = s.GetTask(task.ID)
	if got.LastRun == nil || got.NextRun == nil {
		t.Fatalf("非零时间应正常保存: %+v", got)
	}
}
