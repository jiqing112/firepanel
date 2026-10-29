// 防火墙自愈：外部防火墙管理器（Docker / incus / firewalld / ufw /
// netfilter-persistent 等）在启动、重启或规则重载时会改动共享内置链——
// 重设 FORWARD 策略、flush 整表、删掉不认识的链挂接（-j FPANEL_*）。
// 面板不区分破坏者是谁：周期体检「链是否齐全挂接 + 内核规则是否与期望一致」，
// 异常即重建（Init + Apply 幂等重放），实现与具体工具无关的自愈。
package fw

import (
	"context"
	"strings"
)

// HealthReport 一次体检的结果。
type HealthReport struct {
	Healthy       bool         `json:"healthy"`                  // 当前内核状态与期望一致
	MissingChains []string     `json:"missing_chains,omitempty"` // 缺失或未挂接的面板链
	Drifted       bool         `json:"drifted"`                  // 修复前是否存在规则漂移
	Drift         *DriftReport `json:"drift,omitempty"`          // 修复前的漂移明细
	Repaired      bool         `json:"repaired"`                 // 是否执行了修复
	Remaining     *DriftReport `json:"remaining,omitempty"`      // 修复后仍存在的漂移（nil=已一致）
	Error         string       `json:"error,omitempty"`          // 修复失败原因（空=成功或未需修复）
}

// chainsHealthy 对照已知的四条面板链，提取缺失/未挂接的链。
// iptables 后端 Snapshot 恒返回 4 条链视图；nftables 后端只列实际存在的链，
// 表被删或链缺失时直接缺席，因此必须按已知集合反向核对。
func chainsHealthy(snap *Snapshot) (missing []string) {
	refOf := map[string]string{
		ChainInput:  "filter/INPUT",
		ChainFwd:    "filter/FORWARD",
		ChainDstNat: "nat/PREROUTING",
		ChainSrcNat: "nat/POSTROUTING",
	}
	seen := map[string]ChainView{}
	for _, c := range snap.Chains {
		// iptables 视图用内核链名（FPANEL_*），nft 用抽象名，统一归一化
		seen[normChain(c.Name)] = c
	}
	for name, ref := range refOf {
		c, ok := seen[name]
		if !ok || !c.Exists || !c.Hooked {
			missing = append(missing, name+"("+ref+")")
		}
	}
	return missing
}

// HealthCheck 体检面板链与内核规则，异常时自动修复。
// 存在待确认危险变更时只报告不修复，避免与延时生效/回滚流程竞争。
func (rc *Reconciler) HealthCheck(ctx context.Context, st *DesiredState) (*HealthReport, error) {
	resolved, err := rc.ResolveDomains(ctx, st)
	if err != nil {
		return nil, err
	}
	rules := BuildRules(resolved)
	rep := &HealthReport{}

	snap, snapErr := rc.backend.Snapshot(ctx)
	if snapErr != nil {
		// 快照失败通常意味着面板表/链整体缺失（如 nft 表被删）
		rep.MissingChains = []string{"快照失败: " + snapErr.Error()}
	} else {
		rep.MissingChains = chainsHealthy(snap)
		if d := diffRules(rules, snap); d.Drifted {
			rep.Drifted = true
			rep.Drift = d
		}
	}
	if len(rep.MissingChains) == 0 && rep.Drift == nil {
		rep.Healthy = true
		return rep, nil
	}

	if rc.PendingExists() {
		rep.Error = "存在待确认的高危变更，本次跳过自动修复"
		return rep, nil
	}

	if err := rc.backend.Init(ctx); err != nil {
		rep.Error = "重建面板链失败: " + err.Error()
		return rep, nil
	}
	if err := rc.backend.Apply(ctx, rules); err != nil {
		rep.Error = "重放规则失败: " + err.Error()
		return rep, nil
	}
	rep.Repaired = true

	snap2, err := rc.backend.Snapshot(ctx)
	if err != nil {
		rep.Error = "修复后复查失败: " + err.Error()
		return rep, nil
	}
	if miss := chainsHealthy(snap2); len(miss) > 0 {
		rep.Error = "修复后链仍缺失: " + strings.Join(miss, ", ")
	}
	if d := diffRules(rules, snap2); d.Drifted {
		rep.Remaining = d
		rep.Error = strings.TrimSpace(rep.Error + " 修复后仍有漂移")
	}
	rep.Healthy = rep.Error == ""
	return rep, nil
}
