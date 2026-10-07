package fw

import (
	"context"
	"fmt"
	"strings"
)

// BackendName 支持的后端标识。
const (
	BackendIPTables = "iptables"
	BackendNftables = "nftables"
)

// FirewallBackend 防火墙后端接口。
type FirewallBackend interface {
	Name() string
	// Available 探测当前环境是否可用。
	Available(ctx context.Context) error
	// Init 确保面板自有链/表存在（幂等）。
	Init(ctx context.Context) error
	// Apply 原子应用面板链规则（全量替换面板链内容）。
	Apply(ctx context.Context, rules []Rule) error
	// Snapshot 采集面板链内核快照（用于漂移检测与备份）。
	Snapshot(ctx context.Context) (*Snapshot, error)
	// Counters 按 tag 汇总命中计数。
	Counters(ctx context.Context) (map[string]Counter, error)
	// Cleanup 清除面板链（卸载用）。
	Cleanup(ctx context.Context) error
}

// DetectResult 环境探测结果。
type DetectResult struct {
	IPTablesVariant string // legacy | nft | ""（不存在）
	IPTablesPath    string
	IP6TablesPath   string
	IPTablesRestore string
	NftPath         string
	NftVersion      string
	FirewalldActive bool
	UfwActive       bool
	DockerPresent   bool
	KernelNft       bool
}

// Detect 探测系统防火墙环境。
func Detect(ctx context.Context, exec Executor) (*DetectResult, error) {
	d := &DetectResult{}
	if p, ok := exec.LookPath("iptables"); ok {
		out, err := exec.Run(ctx, "iptables", "--version")
		if err == nil {
			d.IPTablesPath = p
			if strings.Contains(out, "nf_tables") {
				d.IPTablesVariant = "nft"
			} else {
				d.IPTablesVariant = "legacy"
			}
		}
	}
	d.IP6TablesPath, _ = exec.LookPath("ip6tables")
	d.IPTablesRestore, _ = exec.LookPath("iptables-restore")
	if p, ok := exec.LookPath("nft"); ok {
		out, err := exec.Run(ctx, "nft", "--version")
		if err == nil {
			d.NftPath = p
			d.NftVersion = strings.TrimSpace(out)
			d.KernelNft = true
		}
	}
	// firewalld / ufw 活动状态
	if _, ok := exec.LookPath("firewall-cmd"); ok {
		if out, err := exec.Run(ctx, "firewall-cmd", "--state"); err == nil && strings.TrimSpace(out) == "running" {
			d.FirewalldActive = true
		}
	}
	if _, ok := exec.LookPath("ufw"); ok {
		if out, err := exec.Run(ctx, "ufw", "status"); err == nil && strings.HasPrefix(strings.TrimSpace(out), "Status: active") {
			d.UfwActive = true
		}
	}
	// Docker：内核模块或 docker 命令
	if _, ok := exec.LookPath("docker"); ok {
		d.DockerPresent = true
	} else if out, err := exec.Run(ctx, "sh", "-c", "ls /proc/net/ip_tables_names 2>/dev/null || true"); err == nil {
		_ = out // 无关键测，保持 false
	}
	return d, nil
}

// PickBackend 按配置选择后端。
func PickBackend(cfg string, d *DetectResult) (string, error) {
	switch cfg {
	case BackendIPTables:
		if d.IPTablesPath == "" {
			return "", fmt.Errorf("未找到 iptables，无法使用 iptables 后端")
		}
		return BackendIPTables, nil
	case BackendNftables:
		if d.NftPath == "" {
			return "", fmt.Errorf("未找到 nft，无法使用 nftables 后端")
		}
		return BackendNftables, nil
	default: // auto
		if d.NftPath != "" {
			return BackendNftables, nil
		}
		if d.IPTablesPath != "" {
			return BackendIPTables, nil
		}
		return "", fmt.Errorf("系统中未找到 iptables 或 nftables，请先安装")
	}
}
