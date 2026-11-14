package webservice

import (
	"net/netip"
	"sort"
	"strconv"
	"strings"
)

// Web 服务拓扑视图：互联网 → 引擎 → 监听 → 站点 / 监听级动作 → 后端 / 去向。
// 接口只读取内存中的计数快照，前端每 2 秒轮询一次。节点编号（与 gately 一致）：
//   - 监听 l:<监听 ID>
//   - 站点 s:<监听 ID>/<站点 ID>，默认站点的站点 ID 为 default
//   - 后端 u:<站点编号去掉 s: 前缀>/<序号>，序号为站点全部后端组（站点、分路径）展开后的位置
//   - 监听级动作 a:<监听 ID>/redirect（明文跳转 HTTPS）、a:<监听 ID>/notfound（未匹配站点返回 404）
//
// 速率口径：qps、bps 为最近 10 个完整秒的均值，qps60 为最近 60 秒均值；累计数为监听（站点）启动以来的值。
// 活动 IP 为最近 60 秒内在某个站点或监听级动作上出现过的访客地址，加上仍有进行中请求的地址。

const (
	// topoMaxIPs 为一次返回的活动 IP 上限（按进行中请求数、最后出现时间排序），超出时 ipTruncated 为 true
	topoMaxIPs = 500
	// topoMaxDomains 为每个站点返回的域名上限
	topoMaxDomains = 50
	// topoMaxAggIPs 为汇总活动 IP 时最多处理的地址数。每个节点各自最多记 1024 个近期地址，
	// 大量不同来源时汇总、排序会让每次轮询耗时上升；超出后只再收录有进行中请求的地址，并标记 ipTruncated
	topoMaxAggIPs = 5000
)

// TopoRatePoint 是曲线的一个点。
type TopoRatePoint struct {
	T        int64 `json:"t"`
	Requests int64 `json:"requests"`
	Bytes    int64 `json:"bytes"`
}

// TopoUpstream 是一个后端。
type TopoUpstream struct {
	ID     string `json:"id"`
	URL    string `json:"url"`
	Scope  string `json:"scope,omitempty"`
	Match  string `json:"match,omitempty"`
	Weight int    `json:"weight"`
	Backup bool   `json:"backup,omitempty"`
	// Healthy 在站点未运行时省略（状态未知）
	Healthy  *bool `json:"healthy,omitempty"`
	Conns    int64 `json:"conns"`
	Requests int64 `json:"requests"`
	Bytes    int64 `json:"bytes"`
	Rate     Rate  `json:"rate"`
}

// TopoSite 是一个站点（含默认站点）。
type TopoSite struct {
	ID      string   `json:"id"`
	Key     string   `json:"key"`
	Name    string   `json:"name"`
	Type    string   `json:"type"`
	Domains []string `json:"domains"`
	Default bool     `json:"default,omitempty"`
	Enable  bool     `json:"enable"`
	// Running 表示站点正在所属监听上生效（监听运行中且站点已启用）
	Running bool `json:"running"`
	// Target 为非代理站点的去向：跳转地址、静态目录或固定内容的状态码
	Target string   `json:"target,omitempty"`
	Guards []string `json:"guards"`
	// AccessLog 表示站点开启了访问日志（FirePanel 当前固定开启请求日志，此字段恒为 true）
	AccessLog bool             `json:"accessLog"`
	Requests  int64            `json:"requests"`
	Active    int64            `json:"active"`
	Errors    int64            `json:"errors"`
	Closed    int64            `json:"closed"`
	Rate      Rate             `json:"rate"`
	Status    map[string]int64 `json:"status"`
	Blocked   map[string]int64 `json:"blocked"`
	// Upstreams 为反向代理站点的后端（站点后端、分路径后端依次展开）
	Upstreams []TopoUpstream `json:"upstreams"`
}

// TopoAction 是在监听层处理、不经过站点的一类请求。
type TopoAction struct {
	ID string `json:"id"`
	// Kind 为 redirect（明文请求跳转到 HTTPS）或 notFound（未匹配任何站点且未启用默认站点，返回 404）
	Kind     string `json:"kind"`
	Requests int64  `json:"requests"`
	Rate     Rate   `json:"rate"`
}

// TopoListener 是一个监听。
type TopoListener struct {
	ID      string `json:"id"`
	Key     string `json:"key"`
	Name    string `json:"name"`
	Enable  bool   `json:"enable"`
	Running bool   `json:"running"`
	Error   string `json:"error,omitempty"`
	// FirePanel 的监听为一个端口：TLS 决定 https/http，HTTPPort 为额外的明文端口
	Network  string `json:"network"`
	ListenIP string `json:"listenIp"`
	Port     int    `json:"port"`
	TLS      bool   `json:"tls"`
	HTTP3    bool   `json:"http3"`
	PlainHTTP string `json:"plainHttp"`
	HTTPPort      int    `json:"httpPort,omitempty"`
	HTTPPortMode  string `json:"httpPortMode,omitempty"`
	ProxyProtocol bool   `json:"proxyProtocol"`
	// Requests、Active、Rate 为该监听全部站点与监听级动作的汇总
	Requests int64        `json:"requests"`
	Active   int64        `json:"active"`
	Rate     Rate         `json:"rate"`
	Sites    []TopoSite   `json:"sites"`
	Actions  []TopoAction `json:"actions"`
}

// TopoIP 是一个活动的访客地址。
type TopoIP struct {
	IP string `json:"ip"`
	// Nodes 为该地址近期到达过的站点、监听级动作与后端（含进行中请求所在的后端）的节点编号
	Nodes []string `json:"nodes"`
	// Active 为该地址当前进行中的请求总数，ActiveAt 为按站点与后端节点分的进行中请求数，Last 为最后出现的时间（Unix 秒）
	Active   int            `json:"active"`
	ActiveAt map[string]int `json:"activeAt,omitempty"`
	Last     int64          `json:"last"`
}

// TopologyView 是拓扑接口的输出。
type TopologyView struct {
	// Time 为快照时刻（Unix 秒），Window 为 qps / bps 取均值的秒数
	Time   int64 `json:"time"`
	Window int   `json:"window"`
	// Enabled 为 Web 服务是否运行（有任一监听在服务）
	Enabled bool `json:"enabled"`
	// Requests、Active、Rate 为整个实例（全部监听）的汇总
	Requests  int64          `json:"requests"`
	Active    int64          `json:"active"`
	Rate      Rate           `json:"rate"`
	Listeners []TopoListener `json:"listeners"`
	// IPs 为活动 IP，IPTotal 为去重后的总数；IPTruncated 表示列表超出上限或某个节点的近期地址表已满而不完整
	IPs         []TopoIP `json:"ips"`
	IPTotal     int      `json:"ipTotal"`
	IPTruncated bool     `json:"ipTruncated"`
	// Focus 为请求的 focus 参数，Series 为该节点（或 internet / gately 表示整个实例）近 60 秒每秒的请求数与字节数
	Focus  string          `json:"focus,omitempty"`
	Series []TopoRatePoint `json:"series,omitempty"`
}

// topoSiteRef 记录一个运行中站点对应的处理器，在释放锁后再读取它的计数。
type topoSiteRef struct {
	li, si int
	h      *siteHandler
	// ups 为与 TopoSite.Upstreams 一一对应的后端
	ups []topoUp
}

type topoUp struct {
	u            *upstream
	scope, match string
}

// siteGuards 返回站点启用的防护：ip（黑白名单）、rateLimit。
func siteGuards(s *Site) []string {
	g := []string{}
	if s.Type == TypeClose {
		return g
	}
	if s.IPMode != IPModeOff {
		g = append(g, "ip")
	}
	if s.RateLimit.Enable {
		g = append(g, "rateLimit")
	}
	return g
}

// siteTarget 返回非代理站点的去向。
func siteTarget(s *Site) string {
	switch s.Type {
	case TypeRedirect:
		return clipStr(s.RedirectTo, 256)
	case TypeStatic:
		return clipStr(s.Root, 256)
	case TypeText:
		return strconv.Itoa(s.StatusCode)
	}
	return ""
}

// configUpstreams 列出未运行站点在配置中的后端（无运行状态）。
func configUpstreams(s *Site) []TopoUpstream {
	if s.Type == TypeClose {
		return []TopoUpstream{}
	}
	out := []TopoUpstream{}
	add := func(targets []string, scope, match string) {
		for _, t := range targets {
			spec, err := ParseUpstream(t)
			if err != nil {
				continue
			}
			u := &upstream{url: spec.URL}
			out = append(out, TopoUpstream{URL: u.displayURL(), Scope: scope, Match: match, Weight: spec.Weight, Backup: spec.Backup})
		}
	}
	if s.Type == TypeProxy {
		add(s.Targets, "", "")
	}
	for _, l := range s.Locations {
		if len(l.Targets) > 0 {
			add(l.Targets, "location", l.Prefix)
		}
	}
	return out
}

// newTopoSite 生成站点节点的配置部分。
func newTopoSite(lid int64, s *Site, deflt bool) TopoSite {
	key := strconv.FormatInt(s.ID, 10)
	if deflt {
		key = "default"
	}
	domains := s.Domains
	if len(domains) > topoMaxDomains {
		domains = domains[:topoMaxDomains]
	}
	return TopoSite{
		ID: "s:" + strconv.FormatInt(lid, 10) + "/" + key, Key: key, Name: s.Name, Type: s.Type,
		Domains: append([]string{}, domains...), Default: deflt, Enable: s.Enabled,
		Target: siteTarget(s), Guards: siteGuards(s), AccessLog: true,
		Status: map[string]int64{}, Blocked: map[string]int64{}, Upstreams: []TopoUpstream{},
	}
}

// clipStr 截断超长文本。
func clipStr(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}

// Topology 生成拓扑快照。focus 为需要返回曲线的节点编号；wantIP 有效时保证该地址出现在活动 IP 列表中。
func (m *Manager) Topology(now int64, focus string, wantIP netip.Addr) TopologyView {
	v := TopologyView{Time: now, Window: rateAvgWindow, Listeners: []TopoListener{}, IPs: []TopoIP{}, Focus: focus}
	var refs []topoSiteRef
	runners := map[int]*listenerRunner{}

	m.mu.RLock()
	v.Enabled = m.started
	for li := range m.cfgs {
		l := &m.cfgs[li]
		tl := TopoListener{
			ID: "l:" + strconv.FormatInt(l.ID, 10), Key: strconv.FormatInt(l.ID, 10), Name: l.Name,
			Enable: l.Enable, Network: "tcp", ListenIP: l.ListenIP, Port: l.Port, TLS: l.TLS,
			PlainHTTP: l.PlainHTTP, HTTPPort: l.HTTPPort, HTTPPortMode: l.HTTPPortMode,
			Sites: []TopoSite{}, Actions: []TopoAction{},
		}
		run := m.runners[l.ID]
		if run != nil {
			tl.Running = run.ln != nil && run.err == ""
			tl.Error = run.err
			runners[li] = run
		}
		addSite := func(s *Site, h *siteHandler, isDefault bool) {
			ts := newTopoSite(l.ID, s, isDefault)
			if h == nil {
				ts.Upstreams = configUpstreams(s)
				for i := range ts.Upstreams {
					ts.Upstreams[i].ID = "u:" + strings.TrimPrefix(ts.ID, "s:") + "/" + strconv.Itoa(i)
				}
				tl.Sites = append(tl.Sites, ts)
				return
			}
			ts.Running = tl.Running
			ref := topoSiteRef{li: li, si: len(tl.Sites), h: h}
			for _, p := range h.pools() {
				for _, u := range p.ups {
					ref.ups = append(ref.ups, topoUp{u: u, scope: p.scope, match: p.match})
				}
			}
			tl.Sites = append(tl.Sites, ts)
			refs = append(refs, ref)
		}
		for i := range l.Sites {
			s := &l.Sites[i]
			var h *siteHandler
			if run != nil {
				t := run.current()
				for _, sh := range t.all() {
					if sh.site.ID == s.ID {
						h = sh
						break
					}
				}
				if h == nil && run.deflt != nil && run.deflt.site.ID == s.ID {
					h = run.deflt
				}
			}
			addSite(s, h, s.Default)
		}
		if run != nil {
			if l.TLS && (l.PlainHTTP == HTTPRedirect || (l.HTTPPort > 0 && l.HTTPPortMode == HTTPRedirect)) {
				tl.Actions = append(tl.Actions, TopoAction{ID: "a:" + strconv.FormatInt(l.ID, 10) + "/redirect", Kind: "redirect"})
			}
			if run.deflt == nil {
				tl.Actions = append(tl.Actions, TopoAction{ID: "a:" + strconv.FormatInt(l.ID, 10) + "/notfound", Kind: "notFound"})
			}
		}
		v.Listeners = append(v.Listeners, tl)
	}
	m.mu.RUnlock()

	// 曲线：后端节点只取自身；站点与监听级动作节点取自身，并计入所属监听与整个实例（internet / gately）的汇总。
	// 请求从站点到后端是同一批请求，汇总时不能再加后端，否则会重复计数
	var focusListenerID string
	if strings.HasPrefix(focus, "l:") {
		focusListenerID = strings.TrimPrefix(focus, "l:")
	}
	addFocus := func(id, lk string, ring *rateRing) {
		if focus == "" {
			return
		}
		if focus == id || focus == "internet" || focus == "gately" || (focusListenerID != "" && focusListenerID == lk) {
			v.Series = addTopoSeries(v.Series, ring.series(now, rateSeriesSeconds))
		}
	}

	// 活动 IP：地址 → 节点集合
	type ipAgg struct {
		nodes    map[string]bool
		active   int
		activeAt map[string]int
		last     int64
	}
	ips := map[netip.Addr]*ipAgg{}
	// agg 返回地址的汇总项；达到上限后，除非 force（有进行中请求）或是正在筛选的地址，否则返回 nil 不再收录
	agg := func(a netip.Addr, force bool) *ipAgg {
		x := ips[a]
		if x == nil {
			if len(ips) >= topoMaxAggIPs && !force && a != wantIP {
				v.IPTruncated = true
				return nil
			}
			x = &ipAgg{nodes: map[string]bool{}}
			ips[a] = x
		}
		return x
	}

	for _, ref := range refs {
		ts := &v.Listeners[ref.li].Sites[ref.si]
		h := ref.h
		ts.Requests = h.stats.Requests.Load()
		ts.Active = h.stats.Active.Load()
		ts.Errors = h.stats.Errors.Load()
		ts.Closed = h.stats.Status[4].Load() // 断开连接记 444 → 4xx（仅展示口径）
		ts.Rate = h.rate.rate(now)
		ts.Blocked = h.stats.blocked()
		for c := 2; c <= 5; c++ {
			ts.Status[strconv.Itoa(c)+"xx"] = h.stats.Status[c].Load()
		}
		addFocus(ts.ID, v.Listeners[ref.li].Key, h.rate)
		upIDs := map[*upstream]string{}
		for i, x := range ref.ups {
			u := x.u
			id := "u:" + strings.TrimPrefix(ts.ID, "s:") + "/" + strconv.Itoa(i)
			upIDs[u] = id
			healthy := !u.down.Load()
			ts.Upstreams = append(ts.Upstreams, TopoUpstream{ID: id, URL: u.displayURL(), Scope: x.scope, Match: x.match,
				Weight: u.weight, Backup: u.backup, Healthy: &healthy, Conns: u.conns.Load(),
				Requests: u.stats.requests.Load(), Bytes: u.stats.bytes.Load(), Rate: u.stats.rate.rate(now)})
			if focus == id {
				v.Series = addTopoSeries(v.Series, u.stats.rate.series(now, rateSeriesSeconds))
			}
			if u.stats.recent.each(now, func(a netip.Addr, last int64) {
				if x := agg(a, false); x != nil {
					x.nodes[id] = true
				}
			}) {
				v.IPTruncated = true
			}
		}
		if h.recent.each(now, func(a netip.Addr, last int64) {
			x := agg(a, false)
			if x == nil {
				return
			}
			x.nodes[ts.ID] = true
			if last > x.last {
				x.last = last
			}
		}) {
			v.IPTruncated = true
		}
		for _, a := range h.conns.active() {
			// 经内部测试桩等进来的请求没有有效的客户端地址
			if !a.client.Addr().IsValid() {
				continue
			}
			x := agg(a.client.Addr().Unmap(), true)
			if x.activeAt == nil {
				x.activeAt = map[string]int{}
			}
			x.nodes[ts.ID] = true
			x.active++
			x.activeAt[ts.ID]++
			if up := a.upstream.load(); up != nil {
				if id, ok := upIDs[up]; ok {
					x.nodes[id] = true
					x.activeAt[id]++
				}
			}
			if s := a.start.Unix(); s > x.last {
				x.last = s
			}
		}
	}

	// 监听级动作与汇总
	for li := range v.Listeners {
		tl := &v.Listeners[li]
		run := runners[li]
		for ai := range tl.Actions {
			ta := &tl.Actions[ai]
			if run == nil {
				continue
			}
			var act *listenerAct
			if ta.Kind == "redirect" {
				act = &run.acts.redirect
			} else {
				act = &run.acts.notFound
			}
			ta.Requests = act.total.load()
			ta.Rate = act.rate.rate(now)
			addFocus(ta.ID, tl.Key, &act.rate)
			if act.recent.each(now, func(a netip.Addr, last int64) {
				x := agg(a, false)
				if x == nil {
					return
				}
				x.nodes[ta.ID] = true
				if last > x.last {
					x.last = last
				}
			}) {
				v.IPTruncated = true
			}
			tl.Requests += ta.Requests
			tl.Rate.addRate(ta.Rate)
		}
		for _, ts := range tl.Sites {
			tl.Requests += ts.Requests
			tl.Active += ts.Active
			tl.Rate.addRate(ts.Rate)
		}
		v.Requests += tl.Requests
		v.Active += tl.Active
		v.Rate.addRate(tl.Rate)
	}

	// 活动 IP 排序：进行中请求多的在前，其次最近出现的在前，最后按地址
	list := make([]TopoIP, 0, len(ips))
	for a, x := range ips {
		t := TopoIP{IP: a.String(), Active: x.active, ActiveAt: x.activeAt, Last: x.last, Nodes: make([]string, 0, len(x.nodes))}
		for n := range x.nodes {
			t.Nodes = append(t.Nodes, n)
		}
		sort.Strings(t.Nodes)
		list = append(list, t)
	}
	sort.Slice(list, func(i, j int) bool {
		if list[i].Active != list[j].Active {
			return list[i].Active > list[j].Active
		}
		if list[i].Last != list[j].Last {
			return list[i].Last > list[j].Last
		}
		return list[i].IP < list[j].IP
	})
	v.IPTotal = len(list)
	if len(list) > topoMaxIPs {
		v.IPTruncated = true
		keep := list[:topoMaxIPs]
		// 正在筛选的地址排在上限之外时也保留，筛选不会因为列表截断而失效
		if wantIP.IsValid() {
			ws := wantIP.String()
			found := false
			for _, t := range keep {
				if t.IP == ws {
					found = true
					break
				}
			}
			if !found {
				for _, t := range list[topoMaxIPs:] {
					if t.IP == ws {
						keep = append(keep, t)
						break
					}
				}
			}
		}
		list = keep
	}
	v.IPs = list
	return v
}

// addTopoSeries 把一条曲线累加进 out（同秒数值相加）。
func addTopoSeries(out []TopoRatePoint, add []seriesPoint) []TopoRatePoint {
	if len(out) == 0 {
		for _, p := range add {
			out = append(out, TopoRatePoint{T: p.Time, Requests: p.Requests, Bytes: p.Bytes})
		}
		return out
	}
	for i := range add {
		if i < len(out) {
			out[i].Requests += add[i].Requests
			out[i].Bytes += add[i].Bytes
		} else {
			out = append(out, TopoRatePoint{T: add[i].Time, Requests: add[i].Requests, Bytes: add[i].Bytes})
		}
	}
	return out
}

