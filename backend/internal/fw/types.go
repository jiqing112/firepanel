package fw

// 规则与应用计划的与后端无关表示。

// 抽象链名（各后端映射到自有链）。
const (
	ChainInput  = "input"
	ChainFwd    = "forward"
	ChainDstNat = "dstnat"
	ChainSrcNat = "srcnat"
)

// Rule 一条与后端无关的防火墙规则。
type Rule struct {
	Tag     string `json:"tag"`      // fp:<rule_id>:<seq> / fp:sys:<seq>；用于命中映射与漂移对比
	Chain   string `json:"chain"`    // input|forward|dstnat|srcnat
	Family  string `json:"family"`   // ipv4|ipv6
	Proto   string `json:"proto"`    // tcp|udp|""（任意）
	Src     string `json:"src"`      // IP/CIDR，空=任意
	Dst     string `json:"dst"`      // IP/CIDR，空=任意
	DstPort string `json:"dst_port"` // "80" 或 "30000-30010"，空=任意
	OutIface string `json:"out_iface"` // NAT 出口网卡
	Action  string `json:"action"`   // accept|drop|dnat|snat|masquerade
	To      string `json:"to"`       // dnat 目标 "ip:port"/"ip:port-port"/"ip"；snat 目标 "ip"
	Comment string `json:"comment"`
	Limit   *Limit `json:"limit,omitempty"`
}

// Limit 限速/限连参数（INPUT 链防 CC/扫描）。
type Limit struct {
	Rate      int64  `json:"rate"`
	Unit      string `json:"unit"` // second|minute|hour
	Burst     int64  `json:"burst"`
	PerSrc    bool   `json:"per_src"`
	ConnLimit int64  `json:"conn_limit"` // >0 额外限制单 IP 并发连接
	MeterName string `json:"meter_name"` // nft meter 名，每规则唯一
}

// RateLimitSpec 期望状态中的限速规则。
type RateLimitSpec struct {
	ID        int64  `json:"id"`
	Name      string `json:"name"`
	Proto     string `json:"proto"`
	Port      string `json:"port"`
	PerSrc    bool   `json:"per_src"`
	Rate      int64  `json:"rate"`
	RateUnit  string `json:"rate_unit"`
	Burst     int64  `json:"burst"`
	ConnLimit int64  `json:"conn_limit"`
	Enabled   bool   `json:"enabled"`
}

// NATSpec 期望状态中的 NAT 规则（SNAT/MASQ）。
type NATSpec struct {
	ID       int64  `json:"id"`
	Name     string `json:"name"`
	Type     string `json:"type"` // SNAT|MASQ
	SrcCIDR  string `json:"src_cidr"`
	OutIface string `json:"out_iface"`
	ToAddr   string `json:"to_addr"`
	Enabled  bool   `json:"enabled"`
}

// DesiredState 期望状态：由 store 层装载，翻译为 []Rule。
type DesiredState struct {
	Forwards   []ForwardSpec   `json:"forwards"`
	IPLists    []IPListSpec    `json:"ip_lists"`
	RateLimits []RateLimitSpec `json:"rate_limits"`
	NAT        []NATSpec       `json:"nat"`
}

type ForwardSpec struct {
	ID         int64  `json:"id"`
	Name       string `json:"name"`
	Protocol   string `json:"protocol"` // tcp|udp|both
	ListenPort string `json:"listen_port"`
	SrcIP      string `json:"src_ip"`
	DstIP      string `json:"dst_ip"`      // 已解析（域名目标由 reconciler 先做 DNS）
	DstDomain  string `json:"dst_domain"`  // 原始域名（DstIP 为空时解析）
	DstPort    string `json:"dst_port"`
	Enabled    bool   `json:"enabled"`
}

type IPListSpec struct {
	ID       int64  `json:"id"`
	ListType string `json:"list_type"` // black|white
	IP       string `json:"ip"`        // IP 或 CIDR
}

// Counter 规则命中计数。
type Counter struct {
	Packets uint64 `json:"packets"`
	Bytes   uint64 `json:"bytes"`
}

// KernelRule 内核中面板链内的一条规则（快照/漂移用）。
type KernelRule struct {
	Chain   string  `json:"chain"`
	Tag     string  `json:"tag"`
	Spec    string  `json:"spec"` // 规范化参数串（不含计数与 tag）
	Counter Counter `json:"counter"`
	Comment string  `json:"comment"`
}

// Snapshot 面板链的内核快照。
type Snapshot struct {
	Backend string       `json:"backend"`
	Chains  []ChainView  `json:"chains"`
	Rules   []KernelRule `json:"rules"`
	Raw     string       `json:"raw,omitempty"` // iptables-save / nft list ruleset 输出
}

type ChainView struct {
	Name      string `json:"name"`
	Exists    bool   `json:"exists"`
	Hooked    bool   `json:"hooked"` // 是否已挂接到内置链
	Reference string `json:"reference"` // 挂接点描述，如 filter/INPUT
}

// DriftReport 漂移对比结果。
type DriftReport struct {
	Drifted  bool          `json:"drifted"`
	Missing  []KernelRule  `json:"missing"`  // 期望有、内核没有
	Extra    []KernelRule  `json:"extra"`    // 内核多出（面板标签内）
	Modified []DriftModify `json:"modified"` // 同 tag 但内容不同
}

type DriftModify struct {
	Expected string `json:"expected"`
	Actual   string `json:"actual"`
	Tag      string `json:"tag"`
}

// TagFor 规则标签：fp:<来源>:<id>:<seq>
func TagFor(kind string, id int64, seq int) string {
	if id <= 0 {
		return ""
	}
	return fmtTag(kind, id, seq)
}
