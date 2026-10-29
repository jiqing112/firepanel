package fw

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

// healthState 一份典型期望状态：1 条 TCP 转发 + 1 条黑名单（含回程放行）。
var healthState = &DesiredState{
	Forwards: []ForwardSpec{{ID: 1, Protocol: "tcp", ListenPort: "80", DstIP: "192.168.1.100", DstPort: "8080", Enabled: true}},
	IPLists:  []IPListSpec{{ID: 1, ListType: "black", IP: "203.0.113.9"}},
}

const healthV4Save = `*filter
:INPUT ACCEPT [0:0]
:FORWARD DROP [0:0]
:FPANEL_INPUT - [0:0]
:FPANEL_FORWARD - [0:0]
-A INPUT -j FPANEL_INPUT
-A FORWARD -j FPANEL_FORWARD
-A FPANEL_INPUT -s 203.0.113.9/32 -m comment --comment "fp:bl:1:1" -j DROP
-A FPANEL_FORWARD -d 192.168.1.100/32 -p tcp -m tcp --dport 8080 -m comment --comment "fp:fw:1:2" -j ACCEPT
-A FPANEL_FORWARD -m conntrack --ctstate RELATED,ESTABLISHED -m comment --comment "fp:est:0:1" -j ACCEPT
COMMIT
*nat
:PREROUTING ACCEPT [0:0]
:POSTROUTING ACCEPT [0:0]
:FPANEL_DNAT - [0:0]
:FPANEL_SNAT - [0:0]
-A PREROUTING -j FPANEL_DNAT
-A POSTROUTING -j FPANEL_SNAT
-A FPANEL_DNAT -p tcp -m tcp --dport 80 -m comment --comment "fp:fw:1:1" -j DNAT --to-destination 192.168.1.100:8080
COMMIT
`

const healthV6Save = `*filter
:INPUT ACCEPT [0:0]
:FORWARD ACCEPT [0:0]
:FPANEL_INPUT - [0:0]
:FPANEL_FORWARD - [0:0]
-A INPUT -j FPANEL_INPUT
-A FORWARD -j FPANEL_FORWARD
COMMIT
*nat
:PREROUTING ACCEPT [0:0]
:POSTROUTING ACCEPT [0:0]
:FPANEL_DNAT - [0:0]
:FPANEL_SNAT - [0:0]
-A PREROUTING -j FPANEL_DNAT
-A POSTROUTING -j FPANEL_SNAT
COMMIT
`

// TestHealthCheckHealthyIPT 内核状态与期望一致：不应触发修复。
func TestHealthCheckHealthyIPT(t *testing.T) {
	fe := NewFakeExecutor()
	fe.On("iptables-save", func([]string) (string, error) { return healthV4Save, nil })
	fe.On("ip6tables-save", func([]string) (string, error) { return healthV6Save, nil })
	rc := NewReconciler(NewIPTablesBackend(fe), []int{22}, time.Second, time.Second)

	rep, err := rc.HealthCheck(context.Background(), healthState)
	if err != nil {
		t.Fatal(err)
	}
	if !rep.Healthy || rep.Repaired || rep.Drifted || len(rep.MissingChains) > 0 {
		t.Fatalf("健康状态误报: %+v", rep)
	}
	for _, c := range fe.Calls {
		if strings.HasPrefix(c, "iptables-restore") {
			t.Fatalf("健康状态不应触发重放: %v", fe.Calls)
		}
	}
}

// TestHealthCheckRepairsUnhookedJump 模拟外部工具（iptables-restore / firewalld
// reload）拔掉 -j FPANEL_* 挂接（v4/v6 一起被冲掉）：应识别缺失并重建挂接 + 重放规则。
func TestHealthCheckRepairsUnhookedJump(t *testing.T) {
	brokenV4 := strings.Replace(healthV4Save, "-A FORWARD -j FPANEL_FORWARD\n", "", 1)
	fe := NewFakeExecutor()
	saves4 := 0
	fe.On("iptables-save", func([]string) (string, error) {
		saves4++
		if saves4 == 1 {
			return brokenV4, nil // 修复前：挂接被拔
		}
		return healthV4Save, nil // 修复后：正常
	})
	// v6 恒为健康态：回归用例——iptables 按地址族各报一条同名视图，
	// v4 挂接被拔而 v6 完好时，合并视图不得用 v6 遮蔽 v4 的缺失
	fe.On("ip6tables-save", func([]string) (string, error) { return healthV6Save, nil })
	// -C 全部返回失败 → Init 会重新 -I 插入挂接
	fe.On("iptables -t", func(args []string) (string, error) {
		if strings.Contains(strings.Join(args, " "), " -C ") {
			return "", errors.New("no such rule")
		}
		return "", nil
	})
	rc := NewReconciler(NewIPTablesBackend(fe), []int{22}, time.Second, time.Second)

	rep, err := rc.HealthCheck(context.Background(), healthState)
	if err != nil {
		t.Fatal(err)
	}
	if !rep.Repaired || !rep.Healthy || rep.Error != "" {
		t.Fatalf("应修复成功: %+v\nCALLS:\n%s", rep, strings.Join(fe.Calls, "\n"))
	}
	if len(rep.MissingChains) == 0 || !strings.Contains(rep.MissingChains[0], "FORWARD") {
		t.Fatalf("缺失链识别错误: %+v", rep.MissingChains)
	}
	// 挂接重建
	var reinsert bool
	for _, c := range fe.Calls {
		if strings.HasPrefix(c, "iptables -t") && strings.Contains(c, "-I FORWARD 1 -j FPANEL_FORWARD") {
			reinsert = true
		}
	}
	if !reinsert {
		t.Fatalf("未重建 FORWARD 挂接: %v", fe.Calls)
	}
	// 规则重放（含回程放行）——stdin 在 Calls 中是独立元素，合并后检索
	all := strings.Join(fe.Calls, "\n")
	if !strings.Contains(all, "-m conntrack --ctstate RELATED,ESTABLISHED") {
		t.Fatalf("重放脚本缺少回程放行: %s", all)
	}
}

// TestHealthCheckRepairsDrift 模拟内核规则被删（回程放行丢失）：应重放修复。
func TestHealthCheckRepairsDrift(t *testing.T) {
	drifted := strings.Replace(healthV4Save,
		`-A FPANEL_FORWARD -m conntrack --ctstate RELATED,ESTABLISHED -m comment --comment "fp:est:0:1" -j ACCEPT`+"\n", "", 1)
	fe := NewFakeExecutor()
	saves4 := 0
	fe.On("iptables-save", func([]string) (string, error) {
		saves4++
		if saves4 == 1 {
			return drifted, nil
		}
		return healthV4Save, nil
	})
	fe.On("ip6tables-save", func([]string) (string, error) { return healthV6Save, nil })
	rc := NewReconciler(NewIPTablesBackend(fe), []int{22}, time.Second, time.Second)

	rep, err := rc.HealthCheck(context.Background(), healthState)
	if err != nil {
		t.Fatal(err)
	}
	if !rep.Drifted || rep.Drift == nil {
		t.Fatalf("漂移未识别: %+v", rep)
	}
	if !rep.Repaired || !rep.Healthy || rep.Remaining != nil {
		t.Fatalf("应修复成功: %+v", rep)
	}
}

// TestHealthCheckDualStackNoFalsePositive 双栈时同 tag 规则渲染两次，不应误报漂移。
func TestHealthCheckDualStackNoFalsePositive(t *testing.T) {
	dual := strings.Replace(healthV4Save,
		`-A FPANEL_INPUT -s 203.0.113.9/32 -m comment --comment "fp:bl:1:1" -j DROP`,
		`-A FPANEL_INPUT -s 203.0.113.9/32 -m comment --comment "fp:bl:1:1" -j DROP
-A FPANEL_INPUT -s 203.0.113.9/32 -m comment --comment "fp:bl:1:1" -j DROP`, 1)
	fe := NewFakeExecutor()
	fe.On("iptables-save", func([]string) (string, error) { return dual, nil })
	fe.On("ip6tables-save", func([]string) (string, error) { return healthV6Save, nil })
	rc := NewReconciler(NewIPTablesBackend(fe), []int{22}, time.Second, time.Second)

	rep, err := rc.HealthCheck(context.Background(), healthState)
	if err != nil {
		t.Fatal(err)
	}
	if !rep.Healthy {
		t.Fatalf("双栈同 tag 去重后应判定健康: %+v", rep)
	}
}

// nftHealthJSON 表 inet firepanel 完整健康形态（含四链与期望规则）。
const nftHealthJSON = `{"nftables":[
 {"chain":{"family":"inet","table":"firepanel","name":"input","rule":[
   {"expr":[{"match":{"op":"==","left":{"payload":{"protocol":"ip","field":"saddr"}},"right":"203.0.113.9"}},{"counter":{"packets":0,"bytes":0}},{"drop":null}],"comment":"fp:bl:1:1"}]}},
 {"chain":{"family":"inet","table":"firepanel","name":"dstnat","rule":[
   {"expr":[{"match":{"op":"==","left":{"payload":{"protocol":"tcp","field":"dport"}},"right":80}},{"counter":{"packets":0,"bytes":0}},{"dnat":{"addr":"192.168.1.100","port":8080,"family":"ip"}}],"comment":"fp:fw:1:1"}]}},
 {"chain":{"family":"inet","table":"firepanel","name":"forward","rule":[
   {"expr":[{"match":{"op":"==","left":{"payload":{"protocol":"ip","field":"daddr"}},"right":"192.168.1.100"}},{"match":{"op":"==","left":{"payload":{"protocol":"tcp","field":"dport"}},"right":8080}},{"counter":{"packets":0,"bytes":0}},{"accept":null}],"comment":"fp:fw:1:2"},
   {"expr":[{"match":{"op":"==","left":{"payload":{"protocol":"ct","field":"state"}},"right":["established","related"]}},{"counter":{"packets":0,"bytes":0}},{"accept":null}],"comment":"fp:est:0:1"}]}},
 {"chain":{"family":"inet","table":"firepanel","name":"srcnat"}}
]}`

// TestHealthCheckRepairsNftTableMissing 模拟 nft 表被外部工具整体删除：
// Snapshot 报错 → 重建表链 + 重放规则 → 复查健康。
func TestHealthCheckRepairsNftTableMissing(t *testing.T) {
	fe := NewFakeExecutor()
	lists := 0
	fe.On("nft -j", func([]string) (string, error) {
		lists++
		if lists == 1 {
			return "", errors.New("No such file or directory")
		}
		return nftHealthJSON, nil
	})
	rc := NewReconciler(NewNftablesBackend(fe), []int{22}, time.Second, time.Second)

	rep, err := rc.HealthCheck(context.Background(), healthState)
	if err != nil {
		t.Fatal(err)
	}
	if !rep.Repaired || !rep.Healthy || rep.Error != "" {
		t.Fatalf("应修复成功: %+v", rep)
	}
	// Init 建表 + Apply 重放（nft Apply 为整表重建：delete + add；stdin 在 Calls 中是独立元素，合并后检索）
	all := strings.Join(fe.Calls, "\n")
	if !strings.Contains(all, "delete table inet firepanel") ||
		!strings.Contains(all, `ct state related,established counter accept`) {
		t.Fatalf("nft 重放脚本缺少回程放行: %s", all)
	}
}

// TestHealthCheckSkipsWhenDangerPending 存在待确认高危变更时只报告不修复。
func TestHealthCheckSkipsWhenDangerPending(t *testing.T) {
	brokenV4 := strings.Replace(healthV4Save, "-A FORWARD -j FPANEL_FORWARD\n", "", 1)
	fe := NewFakeExecutor()
	fe.On("iptables-save", func([]string) (string, error) { return brokenV4, nil })
	fe.On("ip6tables-save", func([]string) (string, error) { return healthV6Save, nil })
	rc := NewReconciler(NewIPTablesBackend(fe), []int{22}, time.Second, time.Second)
	// 伪造待确认危险变更
	rc.pending = &DangerOp{Token: "t1", Staged: []byte("{}")}

	rep, err := rc.HealthCheck(context.Background(), healthState)
	if err != nil {
		t.Fatal(err)
	}
	if rep.Repaired || rep.Error == "" {
		t.Fatalf("高危挂起时应跳过修复: %+v", rep)
	}
}
