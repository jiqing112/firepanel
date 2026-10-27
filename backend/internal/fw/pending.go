package fw

// ClearPending 放弃当前挂起的高危变更并停掉相关定时器。
//
// 调用方不得持有 rc.mu（本方法自行加锁）；由于事件是在持锁期间发出的，
// app 层必须以 goroutine 方式调用，避免自死锁。
// reconciler 在应用/回滚失败时已自行清理挂起状态，本方法保留给 app 层兜底
// 与测试使用。
func (rc *Reconciler) ClearPending() {
	rc.mu.Lock()
	defer rc.mu.Unlock()
	if rc.pending == nil {
		return
	}
	rc.clearPendingLocked()
}
