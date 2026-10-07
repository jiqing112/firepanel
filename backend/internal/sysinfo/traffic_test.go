package sysinfo

import "testing"

const procNetDev = `Inter-|   Receive                                                |  Transmit
 face |bytes    packets errs drop fifo frame compressed multicast|bytes    packets errs drop fifo colls carrier compressed
    lo: 5576952   12876    0    0    0     0          0         0  5576952   12876    0    0    0     0       0          0
  eth0: 88213401   91234    0    0    0     0          0       122 43771908   76543    0    0    0     0       0          0
  incusbr0: 12345678   23456    0    0    0     0          0         0  9876543   12345    0    0    0     0       0          0`

func TestParseProcNetDev(t *testing.T) {
	devs := ParseProcNetDev(procNetDev)
	if len(devs) != 3 {
		t.Fatalf("期望 3 个网卡，实际 %d", len(devs))
	}
	eth := devs["eth0"]
	if eth.RxBytes != 88213401 || eth.TxBytes != 43771908 {
		t.Errorf("eth0 计数错误: %+v", eth)
	}
	if devs["lo"].RxBytes != 5576952 {
		t.Errorf("lo 计数错误: %+v", devs["lo"])
	}
}

const ssDetail = `ESTAB 0      0    10.0.185.3:22    192.168.218.1:52345 users:(("sshd",pid=699,fd=3))
	 cubic wscale:7,7 rto:364 rtt:182/90.75 ato:40 mss:1460 cwnd:10 bytes_sent:10234 bytes_retrans:0 bytes_acked:10235 bytes_received:5678 seg_out:120 segs_in:98 send 1.2Mbps lastsnd:2134 lastrcv:1980 lastack:1900 pacing_rate 2.4Mbps delivery_rate 2.2Mbps rcv_space:29200
ESTAB 0      0    10.0.185.3:8080  203.0.113.9:51234 users:(("firepanel",pid=194782,fd=8))
	 cubic wscale:8,7 rto:204 rtt:4.5/2.25 bytes_sent:99110 bytes_acked:99111 bytes_received:12011 seg_out:500 segs_in:400
LISTEN 0     128        0.0.0.0:22        0.0.0.0:* users:(("sshd",pid=699,fd=3))
	 skmem:(r0,rb212992,t0,tb212992,f0,w0,o0,bl0,d0)
ESTAB 0      0    10.0.185.3:443   198.51.100.7:3389
	 cubic bytes_sent:555 bytes_acked:556 bytes_received:44`

func TestParseSSDetail(t *testing.T) {
	conns := ParseSSDetail(ssDetail)
	if len(conns) != 3 {
		t.Fatalf("期望 3 条 ESTAB（LISTEN 应跳过），实际 %d", len(conns))
	}
	ssh := conns[0]
	if ssh.Local != "10.0.185.3:22" || ssh.Remote != "192.168.218.1:52345" {
		t.Errorf("五元组解析错误: %+v", ssh)
	}
	if ssh.Process != "sshd" || ssh.PID != "699" {
		t.Errorf("进程解析错误: %+v", ssh)
	}
	if ssh.TxTotal != 10234 || ssh.RxTotal != 5678 {
		t.Errorf("字节计数解析错误: %+v", ssh)
	}
	if conns[1].TxTotal != 99110 || conns[1].Process != "firepanel" {
		t.Errorf("第二条解析错误: %+v", conns[1])
	}
	if conns[2].Process != "" || conns[2].RxTotal != 44 {
		t.Errorf("无进程连接解析错误: %+v", conns[2])
	}
}

func TestParseSSDetailEmpty(t *testing.T) {
	if got := ParseSSDetail(""); len(got) != 0 {
		t.Errorf("空输出应返回空")
	}
	if got := ParseSSDetail("garbage\nshort line only"); len(got) != 0 {
		t.Errorf("坏行应被跳过: %+v", got)
	}
}

const ssUDPDetail = `ESTAB  0      0    10.0.185.3:51713  142.132.176.93:30000 users:(("socat",pid=62154,fd=6))
ESTAB  0      0    10.0.185.3:55832  142.132.176.93:30000 users:(("socat",pid=115821,fd=6))
UNCONN 0      0    0.0.0.0:30000     0.0.0.0:* users:(("socat",pid=825,fd=5))
ESTAB  0      0    10.0.185.3:47649  142.132.176.93:30000`

func TestParseSSUDPDetail(t *testing.T) {
	conns := ParseSSUDPDetail(ssUDPDetail)
	if len(conns) != 3 {
		t.Fatalf("期望 3 条已连接 UDP（UNCONN 监听应跳过），实际 %d", len(conns))
	}
	if conns[0].Process != "socat" || conns[0].PID != "62154" {
		t.Errorf("进程解析错误: %+v", conns[0])
	}
	if conns[0].Remote != "142.132.176.93:30000" {
		t.Errorf("对端解析错误: %+v", conns[0])
	}
	if conns[2].Process != "" {
		t.Errorf("无进程连接应为空: %+v", conns[2])
	}
}
