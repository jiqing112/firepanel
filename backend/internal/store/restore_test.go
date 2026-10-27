package store

import "testing"

// 恢复写入必须整体事务化：任一条失败时一条都不能改。
func TestReplaceAllRollsBackOnError(t *testing.T) {
	s := openTest(t)
	orig := &ForwardRule{
		Name: "orig", Protocol: "tcp", ListenPort: "80",
		DstIP: "10.0.0.1", DstPort: "80", Enabled: true,
	}
	if err := s.CreateForward(orig); err != nil {
		t.Fatal(err)
	}

	bad := Bundle{
		Forwards: []ForwardRule{{Name: "new", Protocol: "tcp", ListenPort: "81", DstIP: "10.0.0.2", DstPort: "81", Enabled: true}},
		// 同类型同 IP 违反 UNIQUE(list_type, ip_or_cidr)
		IPLists: []IPListEntry{
			{ListType: "black", IPOrCIDR: "1.2.3.4"},
			{ListType: "black", IPOrCIDR: "1.2.3.4"},
		},
	}
	if err := s.ReplaceAll(bad); err == nil {
		t.Fatal("重复名单应触发唯一约束错误")
	}

	fwds, err := s.ListForwards()
	if err != nil {
		t.Fatal(err)
	}
	if len(fwds) != 1 || fwds[0].Name != "orig" {
		t.Fatalf("失败后应保留原有转发规则，得到 %+v", fwds)
	}
	lists, _ := s.ListIPLists("")
	if len(lists) != 0 {
		t.Fatalf("失败后不应留下半恢复的名单，得到 %+v", lists)
	}
}

// 恢复保留原 ID 与时间字段，保证定时任务/审计引用不错位。
func TestReplaceAllKeepsIDs(t *testing.T) {
	s := openTest(t)
	b := Bundle{
		Forwards: []ForwardRule{{
			ID: 42, Name: "fixed", Protocol: "tcp", ListenPort: "80",
			DstIP: "10.0.0.1", DstPort: "80", Enabled: true,
		}},
	}
	if err := s.ReplaceAll(b); err != nil {
		t.Fatal(err)
	}
	got, err := s.GetForward(42)
	if err != nil {
		t.Fatalf("恢复后应保留 ID 42: %v", err)
	}
	if got.Name != "fixed" || !got.Enabled {
		t.Fatalf("字段恢复错误: %+v", got)
	}
}
