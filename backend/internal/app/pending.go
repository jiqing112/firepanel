package app

import (
	"encoding/json"
	"time"

	"firepanel/internal/fw"
	"firepanel/internal/store"
)

// pendingDangerKey settings 表中保存「未完成的高危变更」的键。
// 存在即表示上次进程退出时仍有延时生效/确认窗口未走完。
const pendingDangerKey = "pending_danger"

// pendingDangerRecord 高危变更的持久化快照。
// 设计原则：进程可能在任何时刻被杀掉，而数据库里已经是「新期望态」；
// 启动时若发现该记录，一律回滚到 Prior（变更前快照）再重放内核，
// 即「未确认的危险变更视为未发生」——这是唯一不会把管理员锁在门外的策略。
type pendingDangerRecord struct {
	Token     string       `json:"token"`
	Reason    string       `json:"reason"`
	CreatedAt time.Time    `json:"created_at"`
	Applied   bool         `json:"applied"`
	Prior     store.Bundle `json:"prior"`
}

// persistPendingDanger 写入挂起标记（失败仅记日志：不阻断变更本身）。
func (a *App) persistPendingDanger(rec *pendingDangerRecord) {
	b, err := json.Marshal(rec)
	if err != nil {
		a.Log.Error("序列化高危变更快照失败，重启后将无法自动回滚", "err", err)
		return
	}
	if err := a.Store.SetSetting(pendingDangerKey, string(b)); err != nil {
		a.Log.Error("持久化高危变更快照失败，重启后将无法自动回滚", "err", err)
	}
}

// markPendingDangerApplied 记录「危险规则已生效」，便于重启后日志说明。
func (a *App) markPendingDangerApplied(token string) {
	raw, err := a.Store.GetSetting(pendingDangerKey)
	if err != nil || raw == "" {
		return
	}
	var rec pendingDangerRecord
	if json.Unmarshal([]byte(raw), &rec) != nil {
		return
	}
	if rec.Token != "" && rec.Token != token {
		return
	}
	rec.Applied = true
	b, err := json.Marshal(&rec)
	if err != nil {
		return
	}
	_ = a.Store.SetSetting(pendingDangerKey, string(b))
}

// clearPersistedPendingDanger 清除挂起标记（确认/撤销/失败回滚后调用）。
func (a *App) clearPersistedPendingDanger() {
	if err := a.Store.DeleteSetting(pendingDangerKey); err != nil {
		a.Log.Warn("清除高危变更标记失败", "err", err)
	}
}

// RecoverPendingDanger 启动时恢复未完成的高危变更：把数据库回滚到变更前快照。
// 必须在启动重放（StartBackground 中的 Sync）之前调用。
// 返回是否执行了恢复。
func (a *App) RecoverPendingDanger() bool {
	raw, err := a.Store.GetSetting(pendingDangerKey)
	if err != nil || raw == "" {
		return false
	}
	var rec pendingDangerRecord
	if err := json.Unmarshal([]byte(raw), &rec); err != nil {
		a.Log.Warn("解析高危变更快照失败，已清除该标记", "err", err)
		a.clearPersistedPendingDanger()
		return false
	}
	a.Log.Warn("发现上次运行未完成的高危变更，正在回滚到变更前状态",
		"reason", rec.Reason, "applied", rec.Applied, "created_at", rec.CreatedAt.Format(time.RFC3339))
	if err := a.Store.ReplaceAll(rec.Prior); err != nil {
		a.Log.Error("回滚高危变更前的数据失败，请人工核对规则", "err", err)
		// 保留标记：下次启动仍有补救机会
		return false
	}
	a.clearPersistedPendingDanger()
	a.Log.Info("高危变更已回滚（未确认的变更视为未发生）")
	return true
}

// ReloadAux 重新装载与数据库配置相关的运行时组件（反代、定时任务、告警、防爆破）。
// 恢复备份后必须调用：这些组件的内存态不会随 Store.ReplaceAll 自动更新，
// 否则恢复的配置要等重启才生效（甚至一直不生效）。
func (a *App) ReloadAux() {
	if a.Proxy != nil {
		if err := a.ReloadProxy(); err != nil {
			a.Log.Error("重载反向代理失败", "err", err)
		}
	}
	if a.Sched != nil {
		if err := a.Sched.Reload(); err != nil {
			a.Log.Error("重载定时任务失败", "err", err)
		}
	}
	if a.Notify != nil {
		a.LoadNotify()
	}
	if a.Jail != nil {
		a.Jail.Reload()
	}
}

// ReloadAuxAsync 异步重载（证书签发等网络操作可能耗时数秒，不阻塞请求与事件回调）。
func (a *App) ReloadAuxAsync() {
	go a.ReloadAux()
}

// eventAppliesPendingDanger 判断事件是否意味着「危险变更已生效」。
func eventAppliesPendingDanger(e fw.Event) bool { return e.Type == "danger_applied" }
