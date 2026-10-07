// Package scheduler：cron 定时任务调度（规则定时启停、同步、过期清理）。
package scheduler

import (
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/robfig/cron/v3"

	"firepanel/internal/store"
)

// Actions 任务可执行的动作。
type Actions struct {
	SetForward   func(id int64, enabled bool) error
	ExpireIPList func() error
	Sync         func() error
}

// Scheduler 定时任务管理器（支持热重载）。
type Scheduler struct {
	store *store.Store
	log   *slog.Logger
	acts  Actions

	mu    sync.Mutex
	cron  *cron.Cron
	ids   map[int64]cron.EntryID
}

func New(st *store.Store, log *slog.Logger, acts Actions) *Scheduler {
	return &Scheduler{
		store: st,
		log:   log,
		acts:  acts,
		ids:   map[int64]cron.EntryID{},
		cron: cron.New(cron.WithParser(cron.NewParser(
			cron.Minute | cron.Hour | cron.Dom | cron.Month | cron.Dow | cron.Descriptor,
		))),
	}
}

// Start 装载全部启用的任务并开始调度。
func (s *Scheduler) Start() error {
	s.cron.Start()
	return s.Reload()
}

// Reload 全量重载任务（增删改后调用）。
func (s *Scheduler) Reload() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	// 清空现有条目
	for _, id := range s.ids {
		s.cron.Remove(id)
	}
	s.ids = map[int64]cron.EntryID{}

	tasks, err := s.store.ListTasks()
	if err != nil {
		return err
	}
	for _, t := range tasks {
		if !t.Enabled {
			continue
		}
		task := t
		entry, err := s.cron.AddFunc(task.CronExpr, func() { s.run(task.ID) })
		if err != nil {
			s.log.Warn("定时任务 cron 表达式无效", "task", task.Name, "expr", task.CronExpr, "err", err)
			continue
		}
		s.ids[task.ID] = entry
		if next := s.cron.Entry(entry).Next; !next.IsZero() {
			_ = s.store.SetTaskRun(task.ID, nowOrZero(task.LastRun), next)
		}
	}
	s.log.Info("定时任务已加载", "count", len(s.ids))
	return nil
}

func nowOrZero(t *time.Time) time.Time {
	if t == nil {
		return time.Time{}
	}
	return *t
}

// run 执行任务动作。
func (s *Scheduler) run(taskID int64) {
	s.mu.Lock()
	entryID, ok := s.ids[taskID]
	next := time.Now()
	if ok {
		if e := s.cron.Entry(entryID); !e.Next.IsZero() {
			next = e.Next
		}
	}
	s.mu.Unlock()

	t, err := s.store.GetTask(taskID)
	if err != nil {
		return
	}
	var runErr error
	switch t.Action {
	case "forward_enable":
		runErr = s.acts.SetForward(t.TargetID, true)
	case "forward_disable":
		runErr = s.acts.SetForward(t.TargetID, false)
	case "iplist_expire":
		runErr = s.acts.ExpireIPList()
	case "sync":
		runErr = s.acts.Sync()
	default:
		runErr = fmt.Errorf("未知动作 %s", t.Action)
	}
	if runErr != nil {
		s.log.Error("定时任务执行失败", "task", t.Name, "err", runErr)
	} else {
		s.log.Info("定时任务已执行", "task", t.Name, "action", t.Action)
	}
	_ = s.store.SetTaskRun(taskID, time.Now(), next)
}

// Stop 停止调度。
func (s *Scheduler) Stop() {
	<-s.cron.Stop().Done()
}

// NextRun 计算表达式下一次执行时间（校验用）。
func NextRun(expr string) (time.Time, error) {
	spec := cron.NewParser(cron.Minute | cron.Hour | cron.Dom | cron.Month | cron.Dow | cron.Descriptor)
	sched, err := spec.Parse(expr)
	if err != nil {
		return time.Time{}, err
	}
	return sched.Next(time.Now()), nil
}
