<script lang="ts">
  // Web 服务拓扑视图：互联网 → 引擎 → 监听 → 站点 / 动作 → 后端 / 去向，每 2 秒刷新。
  // 1:1 复刻自 gately web/src/components/WebTopology.vue（回放需要服务端录制，未移植）。
  import {
    Activity, AppWindow, ArrowRightLeft, Ban, ChartPie, Check, ChevronDown, ChevronRight, ChevronsDownUp, CornerUpRight,
    ChevronsUp, ChevronsUpDown, Ellipsis, EthernetPort, Eye, EyeOff, FileText, Filter, FolderOpen, Globe,
    Hand, LayoutDashboard, ListFilter, ListTree, LocateFixed, Lock, Maximize, Minimize, Network, Orbit,
    Pencil, CircleDot, Router as RouterIcon, SearchX, Server, Unplug, Waves, X, ZoomIn, ZoomOut,
  } from '@lucide/svelte';
  import { Button } from '$lib/components/ui/button';
  import * as DropdownMenu from '$lib/components/ui/dropdown-menu';
  import * as Tooltip from '$lib/components/ui/tooltip';
  import * as Popover from '$lib/components/ui/popover';
  import { toast } from 'svelte-sonner';
  import { backend } from '$lib/api';
  import {
    COLS, LAYOUT_MODES, RADIAL_LABEL, SANKEY_COLS, SANKEY_LABEL, SANKEY_ROW, activeAt, aliasDomain,
    aliasDomainText, aliasHost, aliasIP, aliasText, aliasURL, Aliases, buildLayout, countActive,
    edgeWidth, filterActive, filterView, flowLevel, formatCount, formatQps, ipPath, isColumnar,
    isIPLiteral, isValidIP, maskDomain, maskDomainText, maskHost, maskIP, maskText, maskURL,
    nodeCovers, pathOf, resolveId,
    type Layout, type LayoutEdge, type LayoutMode, type LayoutNode, type MaskStyle, type TopoFilter,
    type TopoIP, type TopologyView,
  } from '$lib/topology';
  import { fade } from 'svelte/transition';

  type MaskPart = 'visitor' | 'backend' | 'listener' | 'domain';
  const MASK_PARTS: MaskPart[] = ['visitor', 'backend', 'listener', 'domain'];
  const MASK_STYLES: MaskStyle[] = ['stars', 'alias'];

  let { filterText = '', onedit, oneditlistener }: { filterText?: string; onedit?: (listenerId: number, siteId: number) => void; oneditlistener?: (listenerId: number) => void } = $props();

  /* ---------------- 数据与轮询 ---------------- */
  let data = $state<TopologyView>();
  let loadError = $state('');
  let paused = $state(false);
  let selected = $state('gately');
  let hover = $state('');
  let ipFilter = $state('');
  let ipQuery = $state('');
  let ipOpen = $state(false);
  let seriesFor = $state('');

  let timer: ReturnType<typeof setTimeout> | undefined;
  let seq = 0;
  let alive = true;

  async function load() {
    const my = ++seq;
    try {
      const v = (await backend.wsTopology(detailFocus, ipFilter && isValidIP(ipFilter) ? ipFilter : '')).view;
      if (my !== seq || !alive) return;
      data = v;
      seriesFor = detailFocus;
      loadError = '';
    } catch (e) {
      if (my === seq && alive) loadError = e instanceof Error ? e.message : String(e);
    }
  }

  function schedule() {
    clearTimeout(timer);
    timer = setTimeout(async () => {
      if (!alive) return;
      if (document.hidden) {
        paused = true;
        schedule();
        return;
      }
      paused = false;
      await load();
      if (alive) schedule();
    }, 2000);
  }

  export function reload() {
    clearTimeout(timer);
    load().then(() => alive && schedule());
  }

  $effect(() => {
    alive = true;
    reload();
    const onVis = () => {
      if (!document.hidden && !timer) reload();
    };
    document.addEventListener('visibilitychange', onVis);
    return () => {
      alive = false;
      clearTimeout(timer);
      document.removeEventListener('visibilitychange', onVis);
    };
  });

  /* ---------------- 打码 ---------------- */
  const maskKey = 'firepanel.topology.mask';
  let masked = $state(readFlag(maskKey));
  let maskParts = $state(readMaskParts());
  let maskStyle = $state(readMaskStyle());
  const aliases = new Aliases();

  function readFlag(k: string): boolean {
    try { return localStorage.getItem(k) === '1'; } catch { return false; }
  }
  function writeFlag(k: string, v: boolean) {
    try { localStorage.setItem(k, v ? '1' : '0'); } catch { /* 忽略 */ }
  }
  function readMaskParts(): Set<MaskPart> {
    try {
      const raw = localStorage.getItem('firepanel.topology.maskParts');
      if (raw !== null) return new Set(raw.split(',').filter((p): p is MaskPart => MASK_PARTS.includes(p as MaskPart)));
    } catch { /* 忽略 */ }
    return new Set<MaskPart>(['visitor', 'backend', 'listener']);
  }
  function writeMaskParts() {
    try { localStorage.setItem('firepanel.topology.maskParts', [...maskParts].join(',')); } catch { /* 忽略 */ }
  }
  function readMaskStyle(): MaskStyle {
    try {
      const v = localStorage.getItem('firepanel.topology.maskStyle') as MaskStyle;
      return MASK_STYLES.includes(v) ? v : 'stars';
    } catch { return 'stars'; }
  }
  function toggleMaskPart(p: MaskPart) {
    const next = new Set(maskParts);
    if (next.has(p)) next.delete(p); else next.add(p);
    maskParts = next;
    if (next.has(p)) { masked = true; writeFlag(maskKey, true); }
    writeMaskParts();
  }
  function hide(p: MaskPart) { return masked && maskParts.has(p); }
  function aliasing() { return maskStyle === 'alias'; }

  function showIP(ip: string) {
    if (!hide('visitor')) return ip;
    return aliasing() ? aliasIP(aliases, ip, 'Visitor') : maskIP(ip);
  }
  function showURL(u: string) {
    if (!hide('backend')) return u;
    return aliasing() ? aliasURL(aliases, u, 'Backend') : maskURL(u, true);
  }
  function showListenIP(h: string) {
    if (!hide('listener')) return h;
    return aliasing() ? aliasHost(aliases, h, 'Listen') : maskHost(h);
  }
  function showHost(h: string) {
    if (isIPLiteral(h)) return showListenIP(h);
    if (!hide('domain')) return h;
    return aliasing() ? aliasDomain(aliases, h) : maskDomain(h);
  }
  function siteName(s: NonNullable<LayoutNode['site']>) {
    if (s.default) return '默认站点';
    if (s.name) {
      if (!hide('domain')) return s.name;
      return aliasing() ? aliasDomainText(aliases, s.name) : maskDomainText(s.name);
    }
    return (s.domains[0] ? showHost(s.domains[0]) : '') || s.key;
  }

  /* ---------------- 折叠 / 搜索 / 筛选 ---------------- */
  const collapseKey = 'firepanel.topology.collapsed';
  let collapsed = $state<Set<string>>(readCollapsed());
  function readCollapsed(): Set<string> {
    try { return new Set(JSON.parse(localStorage.getItem(collapseKey) ?? '[]')); } catch { return new Set(); }
  }
  function writeCollapsed() {
    try { localStorage.setItem(collapseKey, JSON.stringify([...collapsed])); } catch { /* 忽略 */ }
  }
  function setCollapsed(key: string, v: boolean) {
    const next = new Set(collapsed);
    if (v) next.add(key); else next.delete(key);
    collapsed = next;
    writeCollapsed();
  }
  const listenerKeys = $derived((data?.listeners ?? []).map((l) => l.key));
  const allCollapsed = $derived(listenerKeys.length > 0 && listenerKeys.every((k) => collapsed.has(k)));
  function toggleAll() {
    const all = !allCollapsed;
    collapsed = new Set(all ? listenerKeys : []);
    writeCollapsed();
  }
  function toggleListenerNode(n: LayoutNode) {
    if (n.listener) setCollapsed(n.listener.key, !n.collapsed);
  }

  let query = $derived(filterText.trim());
  let filter = $state<Required<Omit<TopoFilter, 'active'>>>({ listeners: [], issues: false, blocked: [] });
  const filtering = $derived(filterActive(filter));
  const filterCount = $derived(filter.listeners.length + filter.blocked.length + (filter.issues ? 1 : 0));
  function onFilterCommand(c: string) {
    const f = { ...filter };
    const toggle = (list: string[], v: string) => (list.includes(v) ? list.filter((x) => x !== v) : [...list, v]);
    if (c === 'clear') {
      f.listeners = []; f.blocked = []; f.issues = false;
    } else if (c === 'issues') {
      f.issues = !f.issues;
    } else if (c.startsWith('blocked:')) {
      f.blocked = toggle(f.blocked, c.slice(8));
    } else if (c.startsWith('listener:')) {
      f.listeners = toggle(f.listeners, c.slice(9));
    }
    filter = f;
  }

  const activeOnlyKey = 'firepanel.topology.activeOnly';
  let activeOnly = $state(readFlag(activeOnlyKey));
  function toggleActiveOnly() {
    activeOnly = !activeOnly;
    writeFlag(activeOnlyKey, activeOnly);
    tick().then(refit);
  }
  function tick(): Promise<void> { return new Promise((r) => requestAnimationFrame(() => r())); }

  const activeCount = $derived(data ? countActive(filterView(data, filter), query) : 0);

  /* ---------------- 布局 ---------------- */
  const layoutKey = 'firepanel.topology.layout';
  let layoutMode = $state<LayoutMode>(readLayoutMode());
  function readLayoutMode(): LayoutMode {
    try {
      const v = localStorage.getItem(layoutKey) as LayoutMode;
      return LAYOUT_MODES.includes(v) ? v : 'tree';
    } catch { return 'tree'; }
  }
  function setLayoutMode(m: LayoutMode) {
    layoutMode = m;
    try { localStorage.setItem(layoutKey, m); } catch { /* 忽略 */ }
  }
  const layoutIcons: Record<LayoutMode, typeof ListTree> = {
    tree: ListTree, vertical: Network, radial: Orbit, sankey: Waves, sunburst: ChartPie, treemap: LayoutDashboard, pack: CircleDot,
  };
  const layoutMeta: Record<LayoutMode, { label: string; desc: string }> = {
    tree: { label: '树形', desc: '从左到右，每个去向占一行' },
    vertical: { label: '竖向树', desc: '从上到下，横向滚动查看' },
    radial: { label: '径向', desc: '以 Gately 为圆心的三层圆环' },
    sankey: { label: '桑基图', desc: '连线粗细按流量分配' },
    sunburst: { label: '旭日图', desc: '同心环形分区' },
    treemap: { label: '矩形树图', desc: '嵌套矩形，颜色深浅即流量' },
    pack: { label: '气泡图', desc: '嵌套圆形，颜色深浅即流量' },
  };
  let expanded = $state<Set<string>>(new Set());
  function toggleMore(n: LayoutNode) {
    if (!n.more) return;
    const next = new Set(expanded);
    if (n.more.expanded) next.delete(n.more.owner); else next.add(n.more.owner);
    expanded = next;
  }

  const layout = $derived.by<Layout | undefined>(() => {
    if (!data) return undefined;
    const v = filterView(data, { ...filter, active: activeOnly });
    if (activeOnly) {
      return buildLayout(v, new Set(), query, { mode: layoutMode, limit: Infinity, upstreamLimit: Infinity });
    }
    return buildLayout(v, collapsed, query, { mode: layoutMode, expanded });
  });

  const noHover = typeof window !== 'undefined' && !!window.matchMedia?.('(hover: none)').matches;
  const reduceMotion = typeof window !== 'undefined' && window.matchMedia?.('(prefers-reduced-motion: reduce)').matches;
  const motion = $derived(!reduceMotion);
  const manyEdges = $derived((layout?.edges.length ?? 0) > 120);

  const HEAD = 26;
  const colTitles = $derived(['互联网', 'Gately', '监听', '站点 / 动作', '后端 / 去向']);

  const ipInfo = $derived(data?.ips.find((i) => i.ip === ipFilter));
  const ipOptions = $derived.by(() => {
    const list = data?.ips ?? [];
    const q = ipQuery.trim().toLowerCase();
    if (!q) return list;
    return list.filter((i) => i.ip.toLowerCase().includes(q) || showIP(i.ip).toLowerCase().includes(q));
  });

  const litSet = $derived.by<Set<string> | null>(() => {
    const l = layout;
    if (!l) return null;
    if (hover && l.byId.has(hover)) return pathOf(l, hover);
    if (ipFilter && ipInfo) return ipPath(l, ipInfo);
    return pathOf(l, selected);
  });

  const ipCounts = $derived.by(() => {
    const m = new Map<string, number>();
    const lay = layout;
    for (const ip of data?.ips ?? []) {
      const seen = new Set<string>();
      for (const n of ip.nodes) {
        const ids = [n];
        if (n.startsWith('s:') || n.startsWith('a:')) {
          ids.push('l:' + n.slice(2).split('/')[0]);
        }
        const shown = lay ? resolveId(lay, n) : '';
        if (shown && shown !== n) ids.push(shown);
        for (const id of ids) {
          if (!seen.has(id)) {
            seen.add(id);
            m.set(id, (m.get(id) ?? 0) + 1);
          }
        }
      }
    }
    return m;
  });
  function ipCount(id: string) { return ipCounts.get(id) ?? 0; }

  function nodeClass(n: LayoutNode) {
    const lit = litSet;
    return {
      dim: !!lit && !lit.has(n.id),
      hot: !!lit && lit.has(n.id),
      sel: selected === n.id,
    };
  }
  function edgeClass(e: LayoutEdge) {
    const lit = litSet;
    const on = !!lit && lit.has(e.from) && lit.has(e.to);
    return { dim: !!lit && !on, hot: on };
  }
  function animated(e: LayoutEdge) {
    if (!motion || flowLevel(e.rate) === 0) return false;
    if (manyEdges) {
      const lit = litSet;
      return !!lit && lit.has(e.from) && lit.has(e.to);
    }
    return true;
  }
  function showLabel(e: LayoutEdge) {
    const lit = litSet;
    if (!lit || !lit.has(e.from) || !lit.has(e.to)) return false;
    return e.rate.qps > 0 || e.rate.bps > 0;
  }

  // nodeState 返回节点状态：ok 正常、down 故障、off 未运行、expected 预期的拒绝或断开
  function nodeState(n: LayoutNode): string {
    switch (n.kind) {
      case 'internet':
      case 'gately':
        return data?.enabled ? 'ok' : 'off';
      case 'listener':
        if (n.listener?.error) return 'down';
        return n.listener?.running ? 'ok' : 'off';
      case 'site':
        if (!n.site?.running) return 'off';
        return n.site.type === 'close' ? 'expected' : 'ok';
      case 'action':
        return 'expected';
      case 'terminal':
        if (!n.site?.running) return 'off';
        return n.site.type === 'close' ? 'expected' : 'ok';
      case 'upstream':
        if (n.upstream?.healthy === undefined) return 'off';
        return n.upstream.healthy ? 'ok' : 'down';
      case 'more':
        return 'more';
    }
    return 'idle';
  }

  const icons = {
    globe: Globe, router: RouterIcon, lock: Lock, port: EthernetPort, ban: Ban, redirect: CornerUpRight,
    folder: FolderOpen, file: FileText, app: AppWindow, https: ArrowRightLeft, notfound: SearchX,
    unplug: Unplug, server: Server, right: ChevronRight, down: ChevronDown, more: Ellipsis, less: ChevronsUp,
  };
  function nodeIcon(n: LayoutNode): string {
    const byType = (type?: string, fallback = 'file') => (type === 'close' ? 'ban' : type === 'redirect' ? 'redirect' : type === 'static' ? 'folder' : type === 'text' ? 'file' : fallback);
    switch (n.kind) {
      case 'listener': return n.listener?.tls ? 'lock' : 'port';
      case 'site': return byType(n.site?.type, 'app');
      case 'action': return n.action?.kind === 'redirect' ? 'https' : 'notfound';
      case 'terminal': return n.site?.type === 'close' ? 'unplug' : byType(n.site?.type);
      case 'more': return n.more?.expanded ? 'less' : 'more';
      default: return 'server';
    }
  }

  function listenerAddr(l: NonNullable<LayoutNode['listener']>) {
    const host = l.listenIp ? showListenIP(l.listenIp) : '0.0.0.0';
    return (host.includes(':') ? `[${host}]` : host) + ':' + l.port;
  }

  const typeLabels: Record<string, string> = { proxy: '代理', redirect: '重定向', static: '静态', text: '文本', close: '断开' };
  function typeLabel(type: string) { return typeLabels[type] ?? type; }

  const blockLabels: Record<string, string> = { ip: 'IP 名单', rateLimit: '限流' };
  const guardLabels: Record<string, string> = { auth: '认证', waf: 'WAF', ip: 'IP 名单', country: '国家过滤', rateLimit: '限流', denyClose: '拒绝断开' };

  function matchLabel(s: NonNullable<LayoutNode['site']>) {
    if (s.default) return '未匹配域名';
    if (s.domains.includes('*')) return '任意域名';
    return s.domains.some((d) => d.includes('*')) ? '通配域名' : '精确域名';
  }
  function rateText(r: { qps: number; bps: number }) {
    return `${formatQps(r.qps)}/s · ${formatRate(r.bps)}`;
  }
  function formatRate(n: number): string {
    if (!Number.isFinite(n)) return '0 B/s';
    const units = ['B/s', 'KB/s', 'MB/s', 'GB/s', 'TB/s'];
    let v = Math.abs(n);
    let i = 0;
    while (v >= 1024 && i < units.length - 1) { v /= 1024; i++; }
    return `${v.toFixed(i === 0 ? 0 : 1)} ${units[i]}`;
  }
  function formatBytes(n: number): string {
    if (!Number.isFinite(n)) return '0 B';
    const units = ['B', 'KB', 'MB', 'GB', 'TB', 'PB'];
    let v = Math.abs(n);
    let i = 0;
    while (v >= 1024 && i < units.length - 1) { v /= 1024; i++; }
    return `${v.toFixed(i === 0 ? 0 : 1)} ${units[i]}`;
  }

  function nodeTitle(n: LayoutNode): string {
    switch (n.kind) {
      case 'listener': {
        const l = n.listener!;
        return (l.name ? l.name + ' · ' : '') + listenerAddr(l);
      }
      case 'site': return siteName(n.site!);
      case 'action': return n.action!.kind === 'redirect' ? '明文跳转 HTTPS' : '未匹配站点 404';
      case 'terminal': return terminalTitle(n.site!);
      case 'upstream': return showURL(n.upstream!.url);
      case 'more': {
        const m = n.more!;
        if (m.expanded) return `收起 ${m.count} 项`;
        return m.childKind === 'site' ? `还有 ${m.count} 个站点` : `还有 ${m.count} 个后端`;
      }
    }
    return '';
  }
  function terminalTitle(s: NonNullable<LayoutNode['site']>) {
    switch (s.type) {
      case 'close': return '断开连接';
      case 'redirect': return s.target ? showURL(s.target) : '重定向';
      case 'static': return s.target || '静态目录';
      default: return `固定文本 ${s.target || '200'}`;
    }
  }
  function nodeSub(n: LayoutNode): string {
    switch (n.kind) {
      case 'listener': {
        const l = n.listener!;
        if (!l.running) return l.error ? '启动失败' : '未运行';
        const extra = n.collapsed ? ' · 已折叠 ' + (n.hidden ?? 0) : '';
        return rateText(l.rate) + extra;
      }
      case 'site': {
        const s = n.site!;
        if (!s.running) return s.enable ? '未运行' : '已停用';
        return `${matchLabel(s)} · ${formatQps(s.rate.qps)}/s · 活跃 ${s.active}`;
      }
      case 'action':
        return `${n.action!.kind === 'redirect' ? '301 / 308' : '404'} · ${formatQps(n.action!.rate.qps)}/s`;
      case 'terminal': {
        const s = n.site!;
        if (s.type === 'close') return '预期行为 · 444';
        return typeLabel(s.type) + ' · ' + rateText(s.rate);
      }
      case 'upstream': {
        const u = n.upstream!;
        const scope = u.scope === 'location' ? `路径 ${u.match}` : '';
        const health = u.healthy === undefined ? '状态未知' : u.healthy ? '健康' : '不可用';
        return [scope, u.backup ? '备用' : '', health, formatQps(u.rate.qps) + '/s'].filter(Boolean).join(' · ');
      }
      case 'more': {
        const m = n.more!;
        if (m.expanded) return '点击收起';
        if (!m.active && !m.rate.qps && !m.rate.qps60) return '空闲';
        return `${formatQps(m.rate.qps)}/s · 活跃 ${m.active}`;
      }
    }
    return '';
  }
  function hubSub(n: LayoutNode) {
    const v = data;
    if (!v) return '';
    if (n.kind === 'internet') return `活动 IP ${v.ipTotal}`;
    const running = v.listeners.filter((l) => l.running).length;
    return `监听 ${running}/${v.listeners.length} 运行中`;
  }

  // fit 按估算宽度截断文本（SVG 文本不会自动省略）
  function fit(s: string, px: number): string {
    let w = 0;
    for (let i = 0; i < s.length; i++) {
      w += s.charCodeAt(i) < 128 ? 6.6 : 12;
      if (w > px) {
        return s.slice(0, Math.max(1, i - 1)) + '…';
      }
    }
    return s;
  }
  function ago(sec: number) {
    const now = data?.time ?? Math.floor(Date.now() / 1000);
    const d = Math.max(0, now - sec);
    return d <= 1 ? '刚刚' : `${d} 秒前`;
  }

  /* ---------------- 详情面板 ---------------- */
  interface Metric { label: string; value: string; help?: string; cls?: string }
  interface Detail { title: string; sub: string; error?: string; tags: { text: string; type?: string }[]; metrics: Metric[]; rows: Metric[]; actions: { key: string; label: string; icon: typeof Pencil; run: () => void }[] }

  const selectedNode = $derived(layout?.byId.get(selected) ?? layout?.byId.get('gately'));
  const detailFocus = $derived.by(() => {
    const id = selectedNode?.id ?? 'gately';
    if (id.startsWith('t:')) return 's:' + id.slice(2);
    return id === 'internet' ? 'gately' : id;
  });

  $effect(() => {
    // 曲线所属节点变化后立即刷新
    if (alive && data) reload();
  });

  function rateMetrics(r: { qps: number; bps: number; qps60: number }): Metric[] {
    return [
      { label: 'QPS', value: formatQps(r.qps), help: '最近 10 秒的每秒请求数' },
      { label: 'QPS 60s', value: formatQps(r.qps60), help: '最近 60 秒的每秒请求数' },
      { label: '吞吐', value: formatRate(r.bps), help: '最近 10 秒的每秒响应字节数' },
    ];
  }

  const detail = $derived.by<Detail | undefined>(() => {
    const n = selectedNode;
    const v = data;
    if (!n || !v) return undefined;
    const d: Detail = { title: '', sub: '', tags: [], metrics: [], rows: [], actions: [] };
    switch (n.kind) {
      case 'internet':
      case 'gately': {
        d.title = n.kind === 'internet' ? '互联网' : 'Gately';
        d.sub = n.kind === 'internet' ? '访客从这里到达' : hubSub(n);
        if (!v.enabled) d.tags.push({ text: 'Web 服务未运行', type: 'info' });
        d.metrics = [
          ...rateMetrics(v.rate),
          { label: '活跃请求', value: String(v.active), help: '进行中的请求数' },
          { label: '请求数', value: formatCount(v.requests), help: '启动以来的累计请求数' },
          { label: '活动 IP', value: String(v.ipTotal), help: '最近 60 秒出现过的访客地址数' },
        ];
        break;
      }
      case 'listener': {
        const l = n.listener!;
        d.title = l.name || listenerAddr(l);
        d.sub = [listenerAddr(l), l.tls ? 'HTTPS' : 'HTTP'].filter(Boolean).join(' · ');
        d.error = hide('listener') && l.error ? (aliasing() ? aliasText(aliases, l.error, 'Listen') : maskText(l.error)) : l.error;
        d.tags.push(l.running ? { text: '运行中', type: 'success' } : { text: l.enable ? '未运行' : '已停用', type: 'info' });
        if (l.tls && l.plainHttp === 'redirect') d.tags.push({ text: '明文跳转 HTTPS' });
        else if (l.tls && l.plainHttp === 'serve') d.tags.push({ text: '明文直接提供' });
        if (l.httpPort) d.tags.push({ text: l.tls && l.httpPortMode === 'redirect' ? `${l.httpPort} 跳转 HTTPS` : `${l.httpPort} 直接提供` });
        d.metrics = [
          ...rateMetrics(l.rate),
          { label: '活跃请求', value: String(l.active), help: '进行中的请求数' },
          { label: '请求数', value: formatCount(l.requests), help: '启动以来的累计请求数' },
          { label: '站点', value: String(l.sites.length) },
        ];
        if (!activeOnly) {
          d.actions.push({ key: 'fold', label: n.collapsed ? '展开' : '折叠', icon: ChevronsUpDown, run: () => toggleListenerNode(n) });
        }
        d.actions.push({ key: 'edit', label: '编辑监听', icon: Pencil, run: () => oneditlistener?.(Number(n.id.slice(2))) });
        break;
      }
      case 'site': {
        const s = n.site!;
        const lk = Number(n.parent!.slice(2));
        d.title = siteName(s);
        d.sub = `${typeLabel(s.type)} · ${matchLabel(s)}`;
        d.tags.push(s.running ? { text: '运行中', type: 'success' } : { text: s.enable ? '未运行' : '已停用', type: 'info' });
        if (s.type === 'close') d.tags.push({ text: '预期行为', type: 'info' });
        for (const g of s.guards) {
          d.tags.push({ text: guardLabels[g] ?? g, type: 'primary' });
        }
        d.metrics = [
          ...rateMetrics(s.rate),
          { label: '活跃请求', value: String(s.active), help: '进行中的请求数' },
          { label: '请求数', value: formatCount(s.requests), help: '启动以来的累计请求数' },
          { label: '后端错误', value: formatCount(s.errors), help: '转发失败的请求数', cls: s.errors ? 'bad' : '' },
        ];
        const blockedTotal = Object.values(s.blocked).reduce((a, b) => a + b, 0);
        d.rows.push({ label: '拦截', value: formatCount(blockedTotal), help: '访问控制与限流的拒绝总数', cls: blockedTotal ? 'warn' : '' });
        for (const [k, label] of Object.entries(blockLabels)) {
          if (s.blocked[k]) {
            d.rows.push({ label: '　' + label, value: formatCount(s.blocked[k]), cls: 'warn' });
          }
        }
        d.rows.push({ label: '状态码', value: ['2xx', '3xx', '4xx', '5xx'].map((c) => `${c} ${formatCount(s.status[c] ?? 0)}`).join(' · ') });
        if (s.closed) {
          d.rows.push({ label: '断开连接', value: formatCount(s.closed) });
        }
        if (s.domains.length && !s.default) {
          d.rows.push({ label: '域名', value: s.domains.map(showHost).join(', ') });
        }
        d.actions.push({ key: 'edit', label: '编辑站点', icon: Pencil, run: () => onedit?.(lk, s.default ? -1 : Number(s.key)) });
        break;
      }
      case 'action': {
        const a = n.action!;
        d.title = a.kind === 'redirect' ? '明文跳转 HTTPS' : '未匹配站点 404';
        d.sub = a.kind === 'redirect' ? 'HTTP 端口的请求跳转到同主机 HTTPS 地址' : '没有站点匹配 Host 且未启用默认站点';
        d.tags.push({ text: '预期行为', type: 'info' });
        d.metrics = [...rateMetrics(a.rate), { label: '请求数', value: formatCount(a.requests), help: '启动以来的累计请求数' }];
        break;
      }
      case 'terminal': {
        const s = n.site!;
        d.title = terminalTitle(s);
        d.sub = s.type === 'close' ? '不返回任何响应，直接断开' : `${typeLabel(s.type)} · ${siteName(s)}`;
        if (s.type === 'close') d.tags.push({ text: '预期行为', type: 'info' });
        d.metrics = [...rateMetrics(s.rate), { label: '请求数', value: formatCount(s.requests), help: '启动以来的累计请求数' }];
        break;
      }
      case 'upstream': {
        const u = n.upstream!;
        d.title = showURL(u.url);
        d.sub = siteName(n.site!);
        if (u.healthy === undefined) d.tags.push({ text: '状态未知', type: 'info' });
        else d.tags.push(u.healthy ? { text: '健康', type: 'success' } : { text: '不可用', type: 'danger' });
        if (u.scope === 'location') d.tags.push({ text: `路径 ${u.match}` });
        if (u.backup) d.tags.push({ text: '备用', type: 'warning' });
        d.metrics = [
          ...rateMetrics(u.rate),
          { label: '连接数', value: String(u.conns), help: '进行中的后端连接数' },
          { label: '请求数', value: formatCount(u.requests), help: '转发到该后端的累计请求数' },
          { label: '响应字节', value: formatBytes(u.bytes), help: '后端返回的累计响应体字节数' },
        ];
        d.rows.push({ label: '权重', value: String(u.weight) });
        d.actions.push({ key: 'edit', label: '编辑站点', icon: Pencil, run: () => onedit?.(Number(n.site!.id.slice(2).split('/')[0]), Number(n.site!.key)) });
        break;
      }
    }
    return d;
  });

  const panelIps = $derived.by<(TopoIP & { n: number })[]>(() => {
    const node = selectedNode;
    if (!node) return [];
    const list = (data?.ips ?? []).filter((ip) => nodeCovers(ip, node.id, node.more?.ids)).map((ip) => ({ ...ip, n: activeAt(ip, node.id, node.more?.ids) }));
    return list.sort((a, b) => (b.n > 0 ? 1 : 0) - (a.n > 0 ? 1 : 0));
  });

  const seriesMax = $derived(Math.max(0, ...(data?.series ?? []).map((p) => p.requests)));
  function curvePath(values: number[], w: number, h: number): string {
    if (values.length < 2) return '';
    const max = Math.max(...values, 1);
    return values.map((v, i) => `${i === 0 ? 'M' : 'L'} ${((i / (values.length - 1)) * w).toFixed(1)},${(h - 3 - (v / max) * (h - 6)).toFixed(1)}`).join(' ');
  }

  /* ---------------- 选中与高亮 ---------------- */
  function select(id: string) { selected = id; }
  function onNodeClick(n: LayoutNode) {
    if (n.kind === 'more') { toggleMore(n); return; }
    select(n.id);
  }

  function radialLabel(n: LayoutNode) {
    const r = n.w / 2;
    const a = n.angle ?? 0;
    const deg = (a * 180) / Math.PI;
    const flip = Math.cos(a) < -1e-6;
    const outward = !!n.leaf;
    const off = r + 5;
    const max = outward ? RADIAL_LABEL - 24 : n.kind === 'listener' ? 92 : 140;
    return {
      transform: `translate(${r},${r}) rotate(${flip ? deg + 180 : deg})`,
      x: (outward ? off : -off) * (flip ? -1 : 1),
      anchor: (outward !== flip ? 'start' : 'end') as 'start' | 'end',
      max,
    };
  }
  function sunLabel(n: LayoutNode) {
    const l = n.label!;
    const deg = (l.angle * 180) / Math.PI;
    const flip = Math.cos(l.angle) < -1e-6;
    return { transform: `translate(${l.x},${l.y}) rotate(${flip ? deg + 180 : deg})` };
  }
  function nestTitle(n: LayoutNode) {
    if (n.kind === 'gately') return `Gately · ${formatQps(data?.rate.qps ?? 0)}/s`;
    const r = n.listener?.rate ?? n.site?.rate ?? n.more?.rate;
    return nodeTitle(n) + (r ? ` · ${formatQps(r.qps)}/s` : '');
  }
  function shortTitle(n: LayoutNode) {
    const s = nodeTitle(n);
    return n.kind === 'upstream' || n.kind === 'terminal' ? s.replace(/^https?:\/\//, '') : s;
  }
  function packHeadMax(n: LayoutNode) {
    const r = n.w / 2;
    const d = r - 11;
    return d > 0 && d < r ? 2 * Math.sqrt(r * r - d * d) - 12 : 0;
  }
  function sunOpacity(n: LayoutNode) {
    const r = n.listener?.rate ?? n.action?.rate ?? n.upstream?.rate ?? n.more?.rate ?? n.site?.rate;
    const q = r ? Math.max(r.qps, r.qps60) : 0;
    return 0.22 + 0.6 * Math.min(1, Math.log10(1 + q) / 2);
  }

  /* ---------------- 平移与缩放 ---------------- */
  const box = $derived.by(() => {
    const l = layout;
    if (!l) return { x: 0, y: 0, w: 1, h: 1 };
    return isColumnar(l.mode) ? { x: 0, y: -HEAD, w: l.width, h: l.height + HEAD + 8 } : { x: 0, y: 0, w: l.width, h: l.height };
  });

  const ringRadii = $derived.by(() => {
    const l = layout;
    if (!l || l.mode !== 'radial') return [];
    const out: number[] = [];
    for (const col of [2, 3, 4]) {
      const n = l.nodes.find((x) => x.col === col);
      if (n) out.push(Math.round(Math.hypot(n.x + n.w / 2 - l.width / 2, n.y + n.h / 2 - l.height / 2)));
    }
    return out;
  });

  let canvasEl = $state<HTMLElement>();
  let rootEl = $state<HTMLElement>();
  let viewW = $state(800);
  let rootW = $state(1200);
  let winH = $state(typeof window !== 'undefined' ? window.innerHeight : 800);
  let scale = $state(1);
  let tx = $state(0);
  let ty = $state(0);
  let moving = $state(false);
  let animating = $state(false);
  let dragging = $state(false);
  const MIN_SCALE = 0.15;
  const MAX_SCALE = 2.5;
  const EDGE = 48;

  const treeScale = $derived(Math.min(1, Math.max(0.5, viewW / box.w)));

  const heightKey = 'firepanel.topology.height';
  const MIN_H = 220;
  let userH = $state(readHeight());
  function readHeight(): number {
    try {
      const n = Number(localStorage.getItem(heightKey));
      return Number.isFinite(n) && n >= MIN_H ? n : 0;
    } catch { return 0; }
  }
  const maxUserH = $derived(Math.max(MIN_H + 100, winH * 3));

  let full = $state(false);
  let fullH = $state(600);

  const viewH = $derived.by(() => {
    if (full) return Math.max(MIN_H, fullH);
    if (userH) return Math.round(Math.min(maxUserH, Math.max(MIN_H, userH)));
    const maxH = rootW <= 1100 ? winH * 0.7 : winH - 230;
    const mode = layout?.mode;
    const s = mode === 'vertical' ? 1 : mode && !isColumnar(mode) ? Math.min(1, viewW / box.w) : treeScale;
    return Math.round(Math.max(320, Math.min(box.h * s + 2, Math.max(360, maxH))));
  });

  function clampScale(s: number) { return Math.min(MAX_SCALE, Math.max(MIN_SCALE, s)); }
  function clampPan(x: number, y: number, s: number): [number, number] {
    const cw = box.w * s;
    const ch = box.h * s;
    const vw = viewW;
    const vh = viewH;
    const nx = cw <= vw ? (vw - cw) / 2 : Math.min(EDGE, Math.max(vw - cw - EDGE, x));
    const ny = ch <= vh ? (layout && isColumnar(layout.mode) ? 0 : (vh - ch) / 2) : Math.min(EDGE, Math.max(vh - ch - EDGE, y));
    return [nx, ny];
  }
  function setView(s: number, x: number, y: number) {
    scale = s;
    [tx, ty] = clampPan(x, y, s);
  }
  function zoomAt(s: number, px: number, py: number) {
    const ns = clampScale(s);
    const cx = (px - tx) / scale;
    const cy = (py - ty) / scale;
    setView(ns, px - cx * ns, py - cy * ns);
  }
  let animTimer: ReturnType<typeof setTimeout> | undefined;
  function animate() {
    userMoved = true;
    animating = true;
    clearTimeout(animTimer);
    animTimer = setTimeout(() => (animating = false), 260);
  }
  function zoomBy(f: number) { animate(); zoomAt(scale * f, viewW / 2, viewH / 2); }
  function zoomTo(s: number) { animate(); zoomAt(s, viewW / 2, viewH / 2); }

  function fitView(all = false, smooth = false) {
    viewW = canvasEl?.clientWidth || viewW;
    const sw = viewW / box.w;
    const sh = viewH / box.h;
    const mode = layout?.mode;
    if (smooth) animate();
    if (!all && activeOnly) {
      const floor = mode && (isColumnar(mode) || mode === 'vertical') ? 0.5 : MIN_SCALE;
      setView(Math.min(1, Math.max(floor, Math.min(sw, sh))), 0, 0);
      return;
    }
    if (!all && mode === 'vertical') {
      const s = Math.min(1, Math.max(0.5, sh));
      const g = layout!.byId.get('gately');
      const gx = g ? g.x + g.w / 2 : box.w / 2;
      setView(s, viewW / 2 - gx * s, 0);
      return;
    }
    const tree = !!mode && isColumnar(mode);
    const s = all || !tree ? Math.min(1, Math.max(MIN_SCALE, Math.min(sw, sh))) : treeScale;
    setView(s, 0, 0);
  }
  let userMoved = false;
  function refit() { userMoved = false; fitView(); }
  let moveTimer: ReturnType<typeof setTimeout> | undefined;
  function markMoving() {
    userMoved = true;
    moving = true;
    clearTimeout(moveTimer);
    moveTimer = setTimeout(() => (moving = false), 160);
  }
  function localPoint(e: { clientX: number; clientY: number }) {
    const r = canvasEl!.getBoundingClientRect();
    return { x: e.clientX - r.left, y: e.clientY - r.top };
  }
  function onWheel(e: WheelEvent) {
    if (!layout) return;
    const unit = e.deltaMode === 1 ? 16 : e.deltaMode === 2 ? viewH : 1;
    if (e.ctrlKey || e.metaKey) {
      e.preventDefault();
      const d = Math.max(-60, Math.min(60, e.deltaY * unit));
      const p = localPoint(e);
      zoomAt(scale * Math.exp(-d * 0.01), p.x, p.y);
      markMoving();
      return;
    }
    const [nx, ny] = clampPan(tx - e.deltaX * unit, ty - e.deltaY * unit, scale);
    if (Math.abs(nx - tx) < 0.5 && Math.abs(ny - ty) < 0.5) return;
    e.preventDefault();
    tx = nx;
    ty = ny;
    markMoving();
  }

  const pointers = new Map<number, { x: number; y: number }>();
  let dragStart: { x: number; y: number; tx: number; ty: number } | null = null;
  let pinch: { d: number; s: number; cx: number; cy: number } | null = null;
  let suppressClick = false;
  function startPinch() {
    const [a, b] = [...pointers.values()];
    const mx = (a.x + b.x) / 2;
    const my = (a.y + b.y) / 2;
    pinch = { d: Math.max(1, Math.hypot(a.x - b.x, a.y - b.y)), s: scale, cx: (mx - tx) / scale, cy: (my - ty) / scale };
  }
  function capture(el: HTMLElement | undefined, id: number) {
    try { el?.setPointerCapture?.(id); } catch { /* 忽略 */ }
  }
  function onPointerDown(e: PointerEvent) {
    if (!layout || (e.pointerType === 'mouse' && e.button !== 0)) return;
    pointers.set(e.pointerId, localPoint(e));
    if (pointers.size === 1) {
      suppressClick = false;
      const p = pointers.get(e.pointerId)!;
      dragStart = { x: p.x, y: p.y, tx, ty };
    } else if (pointers.size === 2) {
      dragStart = null;
      suppressClick = true;
      startPinch();
    }
  }
  function onPointerMove(e: PointerEvent) {
    if (!pointers.has(e.pointerId)) return;
    const p = localPoint(e);
    pointers.set(e.pointerId, p);
    if (pinch && pointers.size >= 2) {
      const [a, b] = [...pointers.values()];
      const s = clampScale((pinch.s * Math.hypot(a.x - b.x, a.y - b.y)) / pinch.d);
      setView(s, (a.x + b.x) / 2 - pinch.cx * s, (a.y + b.y) / 2 - pinch.cy * s);
      markMoving();
      return;
    }
    if (!dragStart) return;
    const dx = p.x - dragStart.x;
    const dy = p.y - dragStart.y;
    if (!dragging) {
      if (Math.hypot(dx, dy) < 4) return;
      dragging = true;
      suppressClick = true;
      capture(canvasEl, e.pointerId);
    }
    setView(scale, dragStart.tx + dx, dragStart.ty + dy);
    markMoving();
  }
  function onPointerUp(e: PointerEvent) {
    pointers.delete(e.pointerId);
    if (pointers.size < 2) pinch = null;
    if (pointers.size === 1) {
      const p = [...pointers.values()][0];
      dragStart = { x: p.x, y: p.y, tx, ty };
      dragging = true;
    } else if (!pointers.size) {
      dragStart = null;
      dragging = false;
    }
  }
  function onClickCapture(e: MouseEvent) {
    if (suppressClick) {
      suppressClick = false;
      e.stopImmediatePropagation();
      e.preventDefault();
    }
  }

  let fitted = false;
  $effect(() => {
    if (layout && !fitted) {
      fitted = true;
      tick().then(refit);
    }
  });
  $effect(() => {
    void layoutMode;
    tick().then(refit);
  });
  $effect(() => {
    void box.w; void box.h; void viewW; void viewH;
    if (!userMoved && layout) fitView();
    else setView(scale, tx, ty);
  });

  let resizeObs: ResizeObserver | undefined;
  $effect(() => {
    if (!canvasEl) return;
    resizeObs = new ResizeObserver(() => {
      viewW = canvasEl?.clientWidth || viewW;
      rootW = rootEl?.clientWidth || rootW;
    });
    resizeObs.observe(canvasEl);
    if (rootEl) resizeObs.observe(rootEl);
    return () => resizeObs?.disconnect();
  });
  function onWinResize() {
    winH = window.innerHeight;
    tick().then(measureFull);
  }
  $effect(() => {
    window.addEventListener('resize', onWinResize);
    return () => window.removeEventListener('resize', onWinResize);
  });

  /* ---------------- 调整高度 ---------------- */
  let gripStart: { y: number; h: number } | null = null;
  let gripping = $state(false);
  function onGripDown(e: PointerEvent) {
    if (e.pointerType === 'mouse' && e.button !== 0) return;
    gripStart = { y: e.clientY, h: viewH };
    gripping = true;
    capture(e.currentTarget as HTMLElement, e.pointerId);
    e.preventDefault();
  }
  function onGripMove(e: PointerEvent) {
    if (!gripStart) return;
    userH = Math.round(Math.min(maxUserH, Math.max(MIN_H, gripStart.h + e.clientY - gripStart.y)));
  }
  function onGripUp() {
    if (!gripStart) return;
    gripStart = null;
    gripping = false;
    try { localStorage.setItem(heightKey, String(userH)); } catch { /* 忽略 */ }
  }
  function resetHeight() {
    userH = 0;
    try { localStorage.removeItem(heightKey); } catch { /* 忽略 */ }
  }

  /* ---------------- 窗口内全屏 ---------------- */
  async function toggleFull() {
    full = !full;
    await tick();
    measureFull();
    refit();
  }
  function measureFull() {
    if (!full || !canvasEl || !rootEl) return;
    rootW = rootEl.clientWidth || rootW;
    const top = canvasEl.getBoundingClientRect().top - rootEl.getBoundingClientRect().top;
    const foot = rootEl.querySelector('.topo-foot')?.getBoundingClientRect().height ?? 0;
    fullH = Math.floor(winH - top - (rootW <= 1100 ? 0 : foot) - 36);
  }
  function onKeydown(e: KeyboardEvent) {
    if (e.key === 'Escape' && full) toggleFull();
  }
  $effect(() => {
    window.addEventListener('keydown', onKeydown);
    return () => window.removeEventListener('keydown', onKeydown);
  });

  /* ---------------- IP 筛选 ---------------- */
  function setIp(ip: string) {
    ipFilter = ip;
    reload();
  }
  function onIpSelect(v: string) {
    const s = (v ?? '').trim();
    if (s && !isValidIP(s)) {
      toast.warning('IP 地址不完整或无效');
      return;
    }
    ipFilter = s;
    reload();
  }
  function clearIp() {
    ipFilter = '';
    reload();
  }
</script>

<div bind:this={rootEl} class="topo" class:full>
  <div class="topo-bar">
    <div class="ip-filter" class:on={!!ipFilter}>
      <Filter size={15} class="bar-icon" />
      <Popover.Root bind:open={ipOpen}>
        <Popover.Trigger>
          {#snippet child({ props })}
            <button {...props} type="button" class="ip-trigger">
              {#if ipFilter}
                <span class="mono">{showIP(ipFilter)}</span>
              {:else}
                <span class="placeholder">按访客 IP 筛选…</span>
              {/if}
              <ChevronDown size={13} />
            </button>
          {/snippet}
        </Popover.Trigger>
        <Popover.Content class="w-72 p-0" align="start">
          <div class="ip-search">
            <input
              class="ip-input"
              placeholder="输入 IP 筛选，或选择活动 IP"
              bind:value={ipQuery}
              onkeydown={(e) => {
                if (e.key === 'Enter') {
                  onIpSelect(ipQuery.trim());
                  ipOpen = false;
                }
              }}
            />
          </div>
          <div class="ip-list">
            {#if !ipOptions.length}
              <div class="ip-none">暂无活动 IP</div>
            {/if}
            {#each ipOptions as ip (ip.ip)}
              <button type="button" class="ip-row" onclick={() => { onIpSelect(ip.ip); ipOpen = false; }}>
                <span class="mono">{showIP(ip.ip)}</span>
                <span class="opt-meta">{ip.active ? `活跃 ${ip.active}` : ago(ip.last)}</span>
              </button>
            {/each}
          </div>
        </Popover.Content>
      </Popover.Root>
      {#if ipFilter}
        <span class="filter-state">{ipInfo ? `只看 ${showIP(ipFilter)} 的链路` : `${showIP(ipFilter)} 已不活跃`}</span>
        <Button size="sm" variant="outline" onclick={clearIp}><X size={13} /> 清除</Button>
      {/if}
    </div>
    <div class="bar-right">
      {#if data}
        <div class="kpis">
          <span class="kpi" title="最近 10 秒的每秒请求数"><b>{formatQps(data.rate.qps)}</b> QPS</span>
          <span class="kpi" title="最近 10 秒的每秒响应字节数"><b>{formatRate(data.rate.bps)}</b></span>
          <span class="kpi" title="进行中的请求数"><b>{data.active}</b> 活跃</span>
          <span class="kpi" title="最近 60 秒出现过的访客地址数"><b>{data.ipTotal}</b> IP</span>
        </div>
      {/if}
      <Tooltip.Root>
        <Tooltip.Trigger>
          {#snippet child({ props })}
            <Button {...props} class={"active-btn h-8" + (activeOnly ? " on" : "")} variant={activeOnly ? 'default' : 'outline'} aria-pressed={activeOnly} onclick={toggleActiveOnly}>
              <Activity size={14} /> 聚焦活动<span class="active-count" class:zero={!activeCount}>{activeCount > 999 ? '999+' : activeCount}</span>
            </Button>
          {/snippet}
        </Tooltip.Trigger>
        <Tooltip.Content>{activeOnly ? '显示全部站点' : '只看最近 60 秒有请求的服务'}</Tooltip.Content>
      </Tooltip.Root>
      <DropdownMenu.Root>
        <DropdownMenu.Trigger>
          {#snippet child({ props })}
            <Button {...props} variant={filtering ? 'default' : 'outline'} class="h-8">
              <ListFilter size={14} /> {filterCount ? `筛选 ${filterCount}` : '筛选'} <ChevronDown size={13} class="opacity-60" />
            </Button>
          {/snippet}
        </DropdownMenu.Trigger>
        <DropdownMenu.Content align="end" class="w-64">
          <DropdownMenu.Item onclick={() => onFilterCommand('issues')}>
            <span class="mm-box" class:on={filter.issues}>{#if filter.issues}<Check size={12} />{/if}</span>
            <span>只看运行异常</span>
          </DropdownMenu.Item>
          <DropdownMenu.Separator />
          <div class="mm-sec">按拦截原因</div>
          {#each ['ip', 'rateLimit'] as r (r)}
            <DropdownMenu.Item onclick={() => onFilterCommand('blocked:' + r)}>
              <span class="mm-box" class:on={filter.blocked.includes(r)}>{#if filter.blocked.includes(r)}<Check size={12} />{/if}</span>
              <span>{blockLabels[r]}</span>
            </DropdownMenu.Item>
          {/each}
          {#if listenerKeys.length > 1}
            <DropdownMenu.Separator />
            <div class="mm-sec">按监听</div>
            {#each data?.listeners ?? [] as l (l.key)}
              <DropdownMenu.Item onclick={() => onFilterCommand('listener:' + l.key)}>
                <span class="mm-box" class:on={filter.listeners.includes(l.key)}>{#if filter.listeners.includes(l.key)}<Check size={12} />{/if}</span>
                <span class="min-w-0">
                  <span class="block truncate">{l.name || `:${l.port}`}</span>
                  <span class="block truncate font-mono text-[10.5px] text-muted-foreground">:{l.port}{l.tls ? ' (HTTPS)' : ''}</span>
                </span>
              </DropdownMenu.Item>
            {/each}
          {/if}
          {#if filtering}
            <DropdownMenu.Separator />
            <DropdownMenu.Item onclick={() => onFilterCommand('clear')}>
              <X size={14} /> 清除全部筛选
            </DropdownMenu.Item>
          {/if}
        </DropdownMenu.Content>
      </DropdownMenu.Root>
      <DropdownMenu.Root>
        <DropdownMenu.Trigger>
          {#snippet child({ props })}
            <Button {...props} variant="outline" class="h-8">
              {@const LIcon = layoutIcons[layoutMode]}
              <LIcon size={14} /> {layoutMeta[layoutMode].label} <ChevronDown size={13} class="opacity-60" />
            </Button>
          {/snippet}
        </DropdownMenu.Trigger>
        <DropdownMenu.Content align="end" class="w-64">
          {#each LAYOUT_MODES as m (m)}
            {@const LIcon = layoutIcons[m]}
            <DropdownMenu.Item class={"layout-item" + (m === layoutMode ? " on" : "")} onclick={() => setLayoutMode(m)}>
              <LIcon size={17} class="shrink-0 opacity-70" />
              <span class="min-w-0 flex-1">
                <span class="block text-[12.5px] font-medium">{layoutMeta[m].label}</span>
                <span class="block truncate text-[11px] text-muted-foreground">{layoutMeta[m].desc}</span>
              </span>
              {#if m === layoutMode}<Check size={14} class="text-primary" />{/if}
            </DropdownMenu.Item>
          {/each}
        </DropdownMenu.Content>
      </DropdownMenu.Root>
      <DropdownMenu.Root>
        <DropdownMenu.Trigger>
          {#snippet child({ props })}
            <Button {...props} variant={masked ? 'default' : 'outline'} class="h-8">
              {#if masked}<EyeOff size={14} />{:else}<Eye size={14} />{/if} 打码
            </Button>
          {/snippet}
        </DropdownMenu.Trigger>
        <DropdownMenu.Content align="end" class="w-60">
          <div class="mm-head">打码方式</div>
          {#each MASK_STYLES as st (st)}
            <DropdownMenu.Item onclick={() => { maskStyle = st; masked = true; writeFlag(maskKey, true); }}>
              <span class="mm-radio" class:on={maskStyle === st} />
              <span class="min-w-0">{st === 'stars' ? '星号遮蔽' : '代号替换'}</span>
            </DropdownMenu.Item>
          {/each}
          <div class="mm-sec">打码范围</div>
          {#each MASK_PARTS as p (p)}
            <DropdownMenu.Item onclick={() => toggleMaskPart(p)}>
              <span class="mm-box" class:on={maskParts.has(p)}>{#if maskParts.has(p)}<Check size={12} />{/if}</span>
              <span>{{ visitor: '访客 IP', backend: '后端与跳转地址', listener: '监听地址', domain: '域名' }[p]}</span>
            </DropdownMenu.Item>
          {/each}
        </DropdownMenu.Content>
      </DropdownMenu.Root>
      {#if listenerKeys.length && !activeOnly}
        <Button variant="outline" class="h-8" onclick={toggleAll}>
          {#if allCollapsed}<ChevronsUpDown size={14} />{:else}<ChevronsDownUp size={14} />{/if}
          {allCollapsed ? '全部展开' : '全部折叠'}
        </Button>
      {/if}
    </div>
  </div>

  <div class="topo-main">
    <div class="canvas-wrap">
      <div
        bind:this={canvasEl}
        class="canvas"
        class:moving
        class:grabbing={dragging}
        style="height: {viewH}px"
        onwheel={onWheel}
        onpointerdown={onPointerDown}
        onpointermove={onPointerMove}
        onpointerup={onPointerUp}
        onpointercancel={onPointerUp}
        onclickcapture={onClickCapture}
        onclick={() => select('gately')}
      >
        {#if !data && !loadError}
          <div class="canvas-empty"></div>
        {:else if loadError && !data}
          <div class="canvas-empty msg">{loadError}</div>
        {:else if layout}
          <div class="stage" class:animate={animating} style="width: {box.w}px; height: {box.h}px; transform: translate({tx}px, {ty}px) scale({scale})">
            {#key layout.mode}
              <svg
                class="graph m-{layout.mode}"
                class:has-focus={!!litSet}
                class:no-motion={!motion}
                width={box.w}
                height={box.h}
                viewBox={`${box.x} ${box.y} ${box.w} ${box.h}`}
                role="img"
                aria-label="Web 服务拓扑"
                onmouseleave={() => (hover = '')}
              >
                {#if layout.mode === 'tree'}
                  <g class="cols">
                    {#each colTitles as c, i (i)}
                      <text x={i < 2 ? COLS[i].x + COLS[i].w / 2 : COLS[i].x} y={-HEAD + 14} text-anchor={i < 2 ? 'middle' : 'start'}>{c}</text>
                    {/each}
                  </g>
                {:else if layout.mode === 'sankey'}
                  <g class="cols">
                    {#each colTitles as c, i (i)}
                      <text x={SANKEY_COLS[i]} y={-HEAD + 14}>{c}</text>
                    {/each}
                  </g>
                {:else if layout.mode === 'radial'}
                  <g class="rings">
                    {#each ringRadii as r (r)}
                      <circle cx={layout.width / 2} cy={layout.height / 2} r={r} />
                    {/each}
                  </g>
                {/if}
                <defs>
                  {#each Object.entries(icons) as [name, ic] (name)}
                    {@const Icon = ic}
                    <symbol id="gtopo-{name}" viewBox="0 0 24 24"><Icon size={24} /></symbol>
                  {/each}
                  <linearGradient id="topo-edge-hot" x1="0" x2="1" y1="0" y2="0">
                    <stop offset="0" stop-color="var(--topo-accent)" stop-opacity="0.35" />
                    <stop offset="1" stop-color="var(--topo-accent)" stop-opacity="1" />
                  </linearGradient>
                  <radialGradient id="topo-core" cx="0.5" cy="0.4" r="0.7">
                    <stop offset="0" stop-color="var(--topo-accent)" stop-opacity="var(--topo-core-a)" />
                    <stop offset="1" stop-color="var(--topo-accent)" stop-opacity="var(--topo-core-b)" />
                  </radialGradient>
                </defs>
                <g class="edges">
                  {#each layout.edges as e (e.id)}
                    <g class="edge" class:band={e.band} {...edgeClass(e)}>
                      <path class="edge-base" class:band={e.band} d={e.d} style={e.band ? undefined : `stroke-width: ${edgeWidth(e.rate)}`} />
                      {#if animated(e)}
                        <path class="edge-flow lv{flowLevel(e.rate)}" d={e.flow ?? e.d} style="stroke-width: {e.band ? 1.6 : edgeWidth(e.rate)}" />
                      {/if}
                    </g>
                  {/each}
                </g>
                <g class="edge-labels">
                  {#each layout.edges as e (e.id)}
                    {#if showLabel(e)}
                      <text class="edge-label" class:hot={edgeClass(e).hot} x={e.labelX} y={e.labelY} text-anchor="middle">
                        {formatQps(e.rate.qps)}/s · {formatRate(e.rate.bps)}
                      </text>
                    {/if}
                  {/each}
                </g>
                {#each layout.nodes as n (n.id)}
                  <g
                    class="node k-{n.kind} st-{nodeState(n)}"
                    {...nodeClass(n)}
                    transform="translate({n.x},{n.y})"
                    onmouseenter={() => (hover = n.id)}
                    onmouseleave={() => (hover = '')}
                    onclick={(e) => { e.stopPropagation(); onNodeClick(n); }}
                  >
                    {#if n.kind === 'more'}
                      <title>{nodeTitle(n)}&#10;{n.more!.expanded ? '点击收起' : '点击展开'}</title>
                    {:else if layout.mode === 'radial' && n.kind !== 'internet' && n.kind !== 'gately'}
                      <title>{nodeTitle(n)}&#10;{nodeSub(n)}</title>
                    {/if}
                    <!-- 桑基图 -->
                    {#if layout.mode === 'sankey'}
                      <rect class="hit" x="-6" y={Math.min(0, n.h / 2 - SANKEY_ROW / 2)} width={SANKEY_LABEL - 16} height={Math.max(n.h, SANKEY_ROW)} />
                      <rect class="sbar" width={n.w} height={n.h} rx="3" />
                      <text class="title halo" x={n.w + 8} y={n.h / 2 - 3}>{fit(n.kind === 'internet' ? '互联网' : n.kind === 'gately' ? 'Gately' : nodeTitle(n), SANKEY_LABEL - 40)}</text>
                      <text class="sub halo" x={n.w + 8} y={n.h / 2 + 12}>{fit(n.kind === 'internet' || n.kind === 'gately' ? hubSub(n) : nodeSub(n), SANKEY_LABEL - 40)}</text>
                      {#if ipCount(n.id) > 0 && n.kind !== 'upstream' && n.kind !== 'internet' && n.kind !== 'gately'}
                        <g class="ipbadge">
                          <rect x="-30" y={n.h / 2 - 7} width="22" height="13" rx="6.5" />
                          <text x="-19" y={n.h / 2 + 2.5} text-anchor="middle">{ipCount(n.id) > 99 ? '99+' : ipCount(n.id)}</text>
                        </g>
                      {/if}
                    <!-- 旭日图 -->
                    {:else if layout.mode === 'sunburst' && n.shape}
                      <path class="sarc" d={n.shape} style="fill-opacity: {sunOpacity(n)}" />
                      {#if n.label?.show}
                        <text class="slabel" {...sunLabel(n)} text-anchor="middle" dominant-baseline="central">{fit(n.kind === 'more' && !n.more!.expanded ? '+' + n.more!.count : nodeTitle(n), n.label.max)}</text>
                      {/if}
                    <!-- 矩形树图 -->
                    {:else if layout.mode === 'treemap'}
                      <rect class="tm" class:leafbox={n.leaf} class:root={n.kind === 'gately'} width={n.w} height={n.h} rx={n.kind === 'gately' ? 10 : 5} style={n.leaf ? `fill-opacity: ${sunOpacity(n)}` : undefined} />
                      {#if n.leaf}
                        {#if n.w > 36 && n.h > 16}
                          <text class="tm-title" x="7" y={Math.min(17, n.h / 2 + 4)}>{fit(shortTitle(n), n.w - 14)}</text>
                        {/if}
                        {#if n.w > 70 && n.h > 36}
                          <text class="tm-sub" x="7" y="32">{fit(nodeSub(n), n.w - 14)}</text>
                        {/if}
                      {:else if n.w > 36}
                        <text class="tm-head" x="8" y="16">{fit(nestTitle(n), n.w - 16)}</text>
                      {/if}
                    <!-- 气泡图 -->
                    {:else if layout.mode === 'pack'}
                      <circle class="pk" class:leafbox={n.leaf} class:root={n.kind === 'gately'} cx={n.w / 2} cy={n.h / 2} r={n.w / 2} style={n.leaf ? `fill-opacity: ${sunOpacity(n)}` : undefined} />
                      {#if n.leaf && n.w > 30}
                        <text class="pk-title" x={n.w / 2} y={n.h / 2} text-anchor="middle" dominant-baseline="central">{fit(shortTitle(n), n.w - 8)}</text>
                      {:else if !n.leaf && packHeadMax(n) > 30}
                        <text class="pk-head halo" x={n.w / 2} y="15" text-anchor="middle">{fit(nestTitle(n), packHeadMax(n))}</text>
                      {/if}
                    <!-- 互联网 / Gately -->
                    {:else if n.kind === 'internet' || n.kind === 'gately'}
                      <circle class="hub-halo" cx={n.w / 2} cy={n.h / 2} r={n.w / 2 + 6} />
                      <circle class="hub" cx={n.w / 2} cy={n.h / 2} r={n.w / 2} />
                      <use href={n.kind === 'internet' ? '#gtopo-globe' : '#gtopo-router'} x={n.w / 2 - 13} y={n.h / 2 - (layout.mode === 'sunburst' ? 22 : 13)} width="26" height="26" class="hub-icon" />
                      {#if layout.mode === 'sunburst'}
                        <text class="hub-title" x={n.w / 2} y={n.h / 2 + 18} text-anchor="middle">Gately</text>
                        <text class="hub-sub" x={n.w / 2} y={n.h / 2 + 31} text-anchor="middle">{formatQps(data!.rate.qps)}/s</text>
                      {:else if layout.mode === 'vertical'}
                        <text class="hub-title" x={n.w + 12} y={n.h / 2 - 2}>{n.kind === 'internet' ? '互联网' : 'Gately'}</text>
                        <text class="hub-sub" x={n.w + 12} y={n.h / 2 + 13}>{hubSub(n)}</text>
                      {:else}
                        <text class="hub-title" x={n.w / 2} y={n.h + 16} text-anchor="middle">{n.kind === 'internet' ? '互联网' : 'Gately'}</text>
                        <text class="hub-sub" x={n.w / 2} y={n.h + 30} text-anchor="middle">{hubSub(n)}</text>
                      {/if}
                    <!-- 径向 -->
                    {:else if layout.mode === 'radial'}
                      <circle class="rdot" cx={n.w / 2} cy={n.h / 2} r={n.w / 2} />
                      {#if n.kind === 'more' && !n.more!.expanded}
                        <text class="rmore" x={n.w / 2} y={n.h / 2} text-anchor="middle" dominant-baseline="central">
                          {n.more!.count > 99 ? '99+' : '+' + n.more!.count}
                        </text>
                      {:else}
                        <use href="#gtopo-{nodeIcon(n)}" x={n.w * 0.22} y={n.h * 0.22} width={n.w * 0.56} height={n.h * 0.56} class="node-icon" />
                      {/if}
                      {@const rl = radialLabel(n)}
                      <text class="rlabel" transform={rl.transform} x={rl.x} text-anchor={rl.anchor} dominant-baseline="central">{fit(nodeTitle(n), rl.max)}</text>
                      {#if n.kind === 'listener' && !activeOnly}
                        <g class="fold" onclick={(e) => { e.stopPropagation(); toggleListenerNode(n); }}>
                          <circle cx={n.w + 1} cy="-1" r="7" />
                          <use href={n.collapsed ? '#gtopo-right' : '#gtopo-down'} x={n.w - 5} y="-7" width="12" height="12" />
                        </g>
                      {/if}
                      {#if ipCount(n.id) > 0 && n.kind !== 'upstream'}
                        <g class="ipbadge">
                          <rect x="-14" y="-9" width="22" height="13" rx="6.5" />
                          <text x="-3" y="0.5" text-anchor="middle">{ipCount(n.id) > 99 ? '99+' : ipCount(n.id)}</text>
                        </g>
                      {/if}
                    <!-- 默认卡片 -->
                    {:else}
                      <rect class="card" width={n.w} height={n.h} rx="10" />
                      <rect class="card-accent" x="0" y="8" width="3" height={n.h - 16} rx="1.5" />
                      <circle class="icon-bg" cx="23" cy={n.h / 2} r="14" />
                      <use href="#gtopo-{nodeIcon(n)}" x="14" y={n.h / 2 - 9} width="18" height="18" class="node-icon" />
                      <text class="title" x="44" y="19">{fit(nodeTitle(n), n.w - 64)}</text>
                      <text class="sub" x="44" y="35">{fit(nodeSub(n), n.w - 54)}</text>
                      {#if n.kind !== 'more'}
                        <circle class="dot" cx={n.w - 10} cy="10" r="4" />
                      {/if}
                      {#if n.kind === 'listener' && !activeOnly}
                        <g class="fold" onclick={(e) => { e.stopPropagation(); toggleListenerNode(n); }}>
                          <rect x={n.w - 26} y={n.h - 22} width="20" height="18" rx="5" />
                          <use href={n.collapsed ? '#gtopo-right' : '#gtopo-down'} x={n.w - 23} y={n.h - 20} width="14" height="14" />
                        </g>
                      {/if}
                      {#if ipCount(n.id) > 0 && n.kind !== 'upstream'}
                        <g class="ipbadge">
                          <rect x={n.w - 30} y="-8" width="26" height="15" rx="7.5" />
                          <text x={n.w - 17} y="3" text-anchor="middle">{ipCount(n.id) > 99 ? '99+' : ipCount(n.id)}</text>
                        </g>
                      {/if}
                    {/if}
                  </g>
                {/each}
              </svg>
            {/key}
          </div>
          {#if !layout.nodes.some((n) => n.kind === 'listener')}
            <div class="canvas-hint">
              {#if activeOnly && data?.listeners.length}
                <div>{query || filtering ? '没有活动中且匹配的服务' : '最近 60 秒没有请求'}</div>
                <Button class="hint-btn mt-2.5" size="sm" variant="outline" onclick={(e) => { e.stopPropagation(); toggleActiveOnly(); }}>显示全部</Button>
              {:else}
                {query || filtering ? '没有匹配的站点' : '还没有监听与站点，去 Web 服务页添加'}
              {/if}
            </div>
          {/if}
          <div class="zoom" onclick={(e) => e.stopPropagation()} onpointerdown={(e) => e.stopPropagation()} onwheel={(e) => e.stopPropagation()}>
            <Tooltip.Root>
              <Tooltip.Trigger>
                {#snippet child({ props })}
                  <button {...props} type="button" class="zbtn" aria-label="缩小" onclick={() => zoomBy(1 / 1.25)}><ZoomOut size={15} /></button>
                {/snippet}
              </Tooltip.Trigger>
              <Tooltip.Content>缩小</Tooltip.Content>
            </Tooltip.Root>
            <button type="button" class="zbtn pct" onclick={() => zoomTo(1)}>{Math.round(scale * 100)}%</button>
            <Tooltip.Root>
              <Tooltip.Trigger>
                {#snippet child({ props })}
                  <button {...props} type="button" class="zbtn" aria-label="放大" onclick={() => zoomBy(1.25)}><ZoomIn size={15} /></button>
                {/snippet}
              </Tooltip.Trigger>
              <Tooltip.Content>放大</Tooltip.Content>
            </Tooltip.Root>
            <Tooltip.Root>
              <Tooltip.Trigger>
                {#snippet child({ props })}
                  <button {...props} type="button" class="zbtn" aria-label="适应画布" onclick={() => fitView(true, true)}><LocateFixed size={15} /></button>
                {/snippet}
              </Tooltip.Trigger>
              <Tooltip.Content>适应画布</Tooltip.Content>
            </Tooltip.Root>
            <span class="zbtn help" title="拖动平移，Ctrl+滚轮缩放"><Hand size={15} /></span>
            <span class="zsep" />
            <button type="button" class="zbtn" class:on={full} aria-label={full ? '退出全屏' : '窗口内全屏'} onclick={toggleFull}>
              {#if full}<Minimize size={15} />{:else}<Maximize size={15} />{/if}
            </button>
          </div>
        {/if}
      </div>
      {#if !full}
        <div class="grip" class:active={gripping} class:custom={!!userH} onpointerdown={onGripDown} onpointermove={onGripMove} onpointerup={onGripUp} onpointercancel={onGripUp} ondblclick={resetHeight}>
          <span class="grip-bar" />
          <span class="grip-text">{userH ? `高度 ${viewH}px` : '拖动调整画布高度，双击恢复'}</span>
          {#if userH}
            <button type="button" class="grip-reset" onpointerdown={(e) => e.stopPropagation()} onclick={(e) => { e.stopPropagation(); resetHeight(); }}>自动</button>
          {/if}
        </div>
      {/if}
    </div>

    <aside class="panel">
      {#if detail}
        <div class="panel-head">
          <div class="panel-title">{detail.title}</div>
          <div class="panel-sub">{detail.sub}</div>
          {#if detail.tags.length}
            <div class="tags">
              {#each detail.tags as tg (tg.text)}
                <span class="tag t-{tg.type ?? 'info'}">{tg.text}</span>
              {/each}
            </div>
          {/if}
          {#if detail.error}
            <div class="panel-error">{detail.error}</div>
          {/if}
        </div>
        <div class="metrics">
          {#each detail.metrics as m (m.label)}
            <div class="metric">
              <div class="m-label">{m.label}</div>
              <div class="m-value" class:bad={m.cls === 'bad'} class:warn={m.cls === 'warn'}>{m.value}</div>
            </div>
          {/each}
        </div>
        {#if seriesFor === detailFocus && data?.series?.length}
          <div class="chart-title">
            请求曲线
            <span class="chart-meta">峰值 {formatQps(seriesMax)}/s</span>
          </div>
          <svg class="curve" width="100%" height="56" viewBox="0 0 280 56" preserveAspectRatio="none" aria-hidden="true">
            <path d={curvePath(data.series.map((p) => p.requests), 280, 56)} fill="none" stroke="var(--topo-accent)" stroke-width="1.6" />
          </svg>
          <div class="chart-title">字节曲线</div>
          <svg class="curve" width="100%" height="40" viewBox="0 0 280 40" preserveAspectRatio="none" aria-hidden="true">
            <path d={curvePath(data.series.map((p) => p.bytes), 280, 40)} fill="none" stroke="var(--chart-2)" stroke-width="1.6" />
          </svg>
        {/if}
        {#if detail.rows.length}
          <div class="rows">
            {#each detail.rows as r (r.label)}
              <div class="row">
                <span class="r-label">{r.label}</span>
                <span class="r-value" class:bad={r.cls === 'bad'} class:warn={r.cls === 'warn'}>{r.value}</span>
              </div>
            {/each}
          </div>
        {/if}
        {#if detail.actions.length}
          <div class="panel-actions">
            {#each detail.actions as a (a.key)}
              {@const AIcon = a.icon}
              <Button size="sm" variant="outline" onclick={a.run}><AIcon size={13} /> {a.label}</Button>
            {/each}
          </div>
        {/if}
        <div class="chart-title ips-title">
          活动 IP
          <span class="chart-meta">{panelIps.length}</span>
        </div>
        {#if data?.ipTruncated}
          <div class="ip-warn">活动地址较多，列表已截断</div>
        {/if}
        <div class="ip-list">
          {#if !panelIps.length}
            <div class="ip-empty">暂无活动 IP</div>
          {/if}
          {#each panelIps as ip (ip.ip)}
            <button type="button" class="ip-row" onclick={() => setIp(ip.ip)}>
              <span class="mono">{showIP(ip.ip)}</span>
              <span class="opt-meta">{ip.n > 0 ? `活跃 ${ip.n}` : ago(ip.last)}</span>
            </button>
          {/each}
        </div>
      {:else}
        <div class="ip-empty">点击画布中的节点查看详情</div>
      {/if}
      <div class="topo-foot">
        {#if data}<span>更新于 {new Date(data.time * 1000).toLocaleTimeString('zh-CN', { hour12: false })}{paused ? ' · 页面在后台已暂停' : ''}</span>{/if}
        {#if loadError}<span class="text-destructive">{loadError}</span>{/if}
      </div>
    </aside>
  </div>
</div>

<style>
  .topo {
    --topo-accent: var(--primary);
    --topo-bg: color-mix(in oklab, var(--primary) 3%, var(--background));
    --topo-bg2: color-mix(in oklab, var(--primary) 6%, var(--card));
    --topo-card: var(--card);
    --topo-card-hi: color-mix(in oklab, var(--primary) 6%, var(--card));
    --topo-card-stroke: color-mix(in oklab, var(--foreground) 14%, transparent);
    --topo-card-stroke-hi: color-mix(in oklab, var(--foreground) 26%, transparent);
    --topo-line: color-mix(in oklab, var(--foreground) 20%, var(--background));
    --topo-grid: color-mix(in oklab, var(--foreground) 12%, transparent);
    --topo-glow: color-mix(in oklab, var(--primary) 8%, transparent);
    --topo-text: var(--foreground);
    --topo-sub: var(--muted-foreground);
    --topo-ok: var(--success);
    --topo-down: var(--destructive);
    --topo-off: oklch(0.6 0.01 250);
    --topo-expected: oklch(0.7 0.01 250);
    --topo-hub-icon: var(--primary);
    --topo-core-a: 0.32;
    --topo-core-b: 0.06;
    --topo-shadow: 0 1px 2px color-mix(in oklab, var(--foreground) 8%, transparent);
    display: flex;
    flex-direction: column;
    gap: 12px;
    container-type: inline-size;
  }
  :global(.dark) .topo {
    --topo-bg: color-mix(in oklab, var(--background) 70%, #0b1220);
    --topo-bg2: color-mix(in oklab, var(--card) 70%, #111b2e);
    --topo-card: color-mix(in oklab, var(--card) 72%, #131d31);
    --topo-card-hi: color-mix(in oklab, var(--card) 60%, #1c2a46);
    --topo-card-stroke: rgba(148, 163, 184, 0.18);
    --topo-card-stroke-hi: rgba(148, 163, 184, 0.35);
    --topo-line: color-mix(in oklab, var(--foreground) 18%, var(--topo-bg));
    --topo-grid: rgba(148, 163, 184, 0.12);
    --topo-glow: rgba(56, 189, 248, 0.08);
    --topo-ok: #34d399;
    --topo-down: #f87171;
    --topo-off: #64748b;
    --topo-expected: #94a3b8;
    --topo-hub-icon: #fff;
    --topo-core-a: 0.55;
    --topo-core-b: 0.08;
    --topo-shadow: none;
  }
  .topo.full {
    position: fixed;
    inset: 0;
    z-index: 100;
    padding: 14px 16px 10px;
    overflow: auto;
    background: var(--background);
  }
  .topo-bar {
    display: flex;
    align-items: center;
    justify-content: space-between;
    gap: 12px;
    flex-wrap: wrap;
  }
  .ip-filter {
    display: flex;
    align-items: center;
    gap: 8px;
    flex-wrap: wrap;
    padding: 6px 10px;
    border: 1px solid var(--border);
    border-radius: var(--radius);
    background: var(--card);
    transition: border-color 0.2s, box-shadow 0.2s;
  }
  .ip-filter.on {
    border-color: var(--primary);
    box-shadow: 0 0 0 3px color-mix(in oklab, var(--primary) 12%, transparent);
  }
  .bar-icon { color: var(--muted-foreground); }
  .ip-trigger {
    display: inline-flex;
    align-items: center;
    gap: 6px;
    min-width: 200px;
    padding: 4px 10px;
    border: 1px solid var(--input);
    border-radius: calc(var(--radius) - 2px);
    background: var(--background);
    color: var(--foreground);
    font-size: 12.5px;
    cursor: pointer;
    text-align: left;
  }
  .ip-trigger .placeholder { color: var(--muted-foreground); }
  .filter-state { font-size: 13px; color: var(--primary); }
  .bar-right {
    display: flex;
    align-items: center;
    gap: 8px;
    flex-wrap: wrap;
  }
  .kpis { display: flex; gap: 6px; margin-right: 4px; }
  .kpi {
    padding: 4px 10px;
    border-radius: 999px;
    background: var(--secondary);
    font-size: 12px;
    color: var(--muted-foreground);
    white-space: nowrap;
  }
  .kpi b { color: var(--foreground); font-variant-numeric: tabular-nums; }
  .active-btn .active-count {
    min-width: 18px;
    height: 18px;
    margin-left: 6px;
    padding: 0 5px;
    border-radius: 9px;
    background: color-mix(in oklab, var(--primary) 14%, transparent);
    color: var(--primary);
    font-size: 11px;
    font-weight: 600;
    line-height: 18px;
    font-variant-numeric: tabular-nums;
  }
  .active-btn .active-count.zero {
    background: var(--muted);
    color: var(--muted-foreground);
  }
  .topo-main {
    display: grid;
    grid-template-columns: minmax(0, 1fr) 320px;
    gap: 12px;
    align-items: start;
  }
  @container (max-width: 1100px) {
    .topo-main { grid-template-columns: minmax(0, 1fr); }
    .panel { position: static; max-height: none; }
  }
  .canvas {
    position: relative;
    overflow: hidden;
    min-height: 320px;
    border-radius: calc(var(--radius) + 2px);
    background:
      radial-gradient(circle at 1px 1px, var(--topo-grid) 1px, transparent 0) 0 0 / 22px 22px,
      radial-gradient(1200px 500px at 10% 0%, var(--topo-glow), transparent 60%),
      linear-gradient(160deg, var(--topo-bg2), var(--topo-bg));
    box-shadow: inset 0 0 0 1px var(--topo-card-stroke);
    touch-action: none;
    user-select: none;
    -webkit-user-select: none;
    cursor: grab;
  }
  .canvas.grabbing { cursor: grabbing; }
  .stage { position: absolute; top: 0; left: 0; transform-origin: 0 0; }
  .stage.animate { transition: transform 0.25s ease; }
  .canvas.moving .stage { will-change: transform; }
  .canvas-empty { height: 300px; }
  .canvas-empty.msg,
  .canvas-hint {
    display: flex;
    align-items: center;
    justify-content: center;
    color: var(--topo-sub);
    font-size: 13px;
  }
  .canvas-hint {
    position: absolute;
    inset: 40% 0 auto;
    flex-direction: column;
    padding: 0 16px;
    text-align: center;
    pointer-events: none;
  }
  .hint-btn { margin-top: 10px; pointer-events: auto; }
  .zoom {
    position: absolute;
    right: 10px;
    bottom: 10px;
    display: flex;
    align-items: center;
    gap: 2px;
    padding: 3px;
    border-radius: 10px;
    background: color-mix(in oklab, var(--topo-card) 88%, transparent);
    box-shadow: inset 0 0 0 1px var(--topo-card-stroke), 0 4px 14px rgba(0, 0, 0, 0.12);
    backdrop-filter: blur(6px);
    cursor: default;
  }
  .zbtn {
    display: inline-flex;
    align-items: center;
    justify-content: center;
    min-width: 28px;
    height: 28px;
    padding: 0 6px;
    border: 0;
    border-radius: 7px;
    background: none;
    color: var(--topo-sub);
    font: inherit;
    font-size: 12px;
    font-variant-numeric: tabular-nums;
    cursor: pointer;
  }
  .zbtn:hover { background: color-mix(in oklab, var(--primary) 14%, transparent); color: var(--topo-text); }
  .zbtn.pct { min-width: 46px; }
  .zbtn.help { cursor: help; }
  .zbtn.on { color: var(--topo-accent); }
  .zsep { width: 1px; height: 18px; margin: 0 2px; background: var(--topo-card-stroke); }
  .canvas-wrap { min-width: 0; }
  .grip {
    position: relative;
    display: flex;
    align-items: center;
    justify-content: center;
    gap: 8px;
    height: 18px;
    margin-top: 2px;
    cursor: ns-resize;
    touch-action: none;
    user-select: none;
    -webkit-user-select: none;
    color: var(--muted-foreground);
    font-size: 11px;
  }
  .grip-bar {
    width: 44px;
    height: 4px;
    border-radius: 2px;
    background: var(--border);
    transition: background 0.2s, width 0.2s;
  }
  .grip:hover .grip-bar,
  .grip.active .grip-bar { width: 64px; background: var(--primary); }
  .grip-text {
    position: absolute;
    left: calc(50% + 44px);
    white-space: nowrap;
    opacity: 0;
    transition: opacity 0.2s;
  }
  .grip:hover .grip-text,
  .grip.active .grip-text,
  .grip.custom .grip-text { opacity: 1; }
  .grip-reset {
    position: absolute;
    right: 0;
    padding: 0 6px;
    border: 0;
    background: none;
    color: var(--primary);
    font: inherit;
    cursor: pointer;
  }
  .graph { display: block; overflow: visible; font-family: inherit; }
  .cols text {
    font-size: 10.5px;
    letter-spacing: 0.08em;
    fill: var(--topo-sub);
    opacity: 0.8;
  }
  .rings circle { fill: none; stroke: var(--topo-grid); stroke-dasharray: 2 6; }
  /* 连线 */
  .edge-base {
    fill: none;
    stroke: var(--topo-line);
    stroke-linecap: round;
    transition: stroke 0.25s, opacity 0.25s;
  }
  .edge-flow {
    fill: none;
    stroke: var(--topo-accent);
    stroke-linecap: round;
    stroke-dasharray: 2 14;
    opacity: 0.85;
    animation: topo-flow 1.6s linear infinite;
  }
  .edge-flow.lv1 { animation-duration: 2.6s; opacity: 0.55; }
  .edge-flow.lv3 { animation-duration: 0.8s; }
  .no-motion .edge-flow { animation: none; }
  .edge.hot .edge-base { stroke: url(#topo-edge-hot); }
  .edge.dim { opacity: 0.18; }
  @keyframes topo-flow { to { stroke-dashoffset: -32; } }
  .edge-label {
    font-size: 10.5px;
    fill: var(--topo-sub);
    paint-order: stroke;
    stroke: var(--topo-bg);
    stroke-width: 3px;
    font-variant-numeric: tabular-nums;
    pointer-events: none;
  }
  .edge-label.hot { fill: var(--topo-text); }
  /* 节点 */
  .node { cursor: pointer; transition: opacity 0.25s; }
  .node.dim { opacity: 0.28; }
  .card {
    fill: var(--topo-card);
    stroke: var(--topo-card-stroke);
    stroke-width: 1;
    filter: drop-shadow(var(--topo-shadow));
    transition: fill 0.2s, stroke 0.2s;
  }
  .node:hover .card,
  .node.hot .card { fill: var(--topo-card-hi); stroke: var(--topo-card-stroke-hi); }
  .k-more .card { stroke-dasharray: 4 3; }
  .k-more .title { fill: var(--topo-accent); }
  .node.sel .card {
    stroke: var(--topo-accent);
    stroke-width: 1.5;
    filter: drop-shadow(0 0 8px color-mix(in oklab, var(--primary) 55%, transparent));
  }
  .card-accent { fill: var(--topo-off); }
  .st-ok .card-accent { fill: var(--topo-ok); }
  .st-down .card-accent { fill: var(--topo-down); }
  .st-expected .card-accent { fill: var(--topo-expected); }
  .icon-bg { fill: rgba(148, 163, 184, 0.1); }
  .st-ok .icon-bg { fill: color-mix(in oklab, var(--primary) 18%, transparent); }
  .st-down .icon-bg { fill: rgba(248, 113, 113, 0.16); }
  .node-icon { color: var(--topo-sub); }
  .st-ok .node-icon { color: var(--topo-accent); }
  .st-down .node-icon { color: var(--topo-down); }
  .title { font-size: 12.5px; font-weight: 600; fill: var(--topo-text); }
  .sub { font-size: 11px; fill: var(--topo-sub); font-variant-numeric: tabular-nums; }
  .st-off .title { fill: var(--topo-sub); }
  .dot { fill: var(--topo-off); }
  .st-ok .dot { fill: var(--topo-ok); }
  .st-down .dot { fill: var(--topo-down); animation: topo-blink 1.4s ease-in-out infinite; }
  .st-expected .dot { fill: var(--topo-expected); }
  .no-motion .st-down .dot { animation: none; }
  @keyframes topo-blink { 50% { opacity: 0.35; } }
  .fold rect,
  .fold circle { fill: color-mix(in oklab, var(--topo-sub) 16%, var(--topo-card)); }
  .fold:hover rect,
  .fold:hover circle { fill: color-mix(in oklab, var(--topo-sub) 30%, var(--topo-card)); }
  .fold { color: var(--topo-sub); cursor: pointer; }
  .ipbadge rect { fill: var(--topo-accent); }
  .ipbadge text { font-size: 9.5px; font-weight: 700; fill: #fff; }
  .hub {
    fill: url(#topo-core);
    stroke: color-mix(in oklab, var(--primary) 70%, transparent);
    stroke-width: 1.5;
  }
  .hub-halo {
    fill: none;
    stroke: color-mix(in oklab, var(--primary) 30%, transparent);
    stroke-width: 1;
    stroke-dasharray: 3 5;
    transform-box: fill-box;
    transform-origin: center;
    animation: topo-spin 24s linear infinite;
  }
  .no-motion .hub-halo { animation: none; }
  @keyframes topo-spin { to { transform: rotate(360deg); } }
  .k-gately .hub { stroke-width: 2; }
  .node.sel .hub { filter: drop-shadow(0 0 10px color-mix(in oklab, var(--primary) 70%, transparent)); }
  .hub-icon { color: var(--topo-hub-icon); }
  .st-off .hub-icon { color: var(--topo-sub); }
  .hub-title { font-size: 12.5px; font-weight: 600; fill: var(--topo-text); }
  .hub-sub { font-size: 10.5px; fill: var(--topo-sub); }
  /* 径向 */
  .rdot {
    fill: var(--topo-card);
    stroke: var(--topo-off);
    stroke-width: 1.5;
    transition: fill 0.2s;
  }
  .st-ok .rdot { stroke: var(--topo-ok); }
  .st-down .rdot { stroke: var(--topo-down); }
  .st-expected .rdot { stroke: var(--topo-expected); }
  .k-more .rdot { stroke: var(--topo-accent); stroke-dasharray: 3 2; }
  .node:hover .rdot,
  .node.hot .rdot { fill: var(--topo-card-hi); }
  .node.sel .rdot {
    stroke: var(--topo-accent);
    stroke-width: 2.5;
    filter: drop-shadow(0 0 6px color-mix(in oklab, var(--primary) 60%, transparent));
  }
  .rmore { font-size: 9px; font-weight: 700; fill: var(--topo-accent); }
  .rlabel {
    font-size: 11.5px;
    fill: var(--topo-text);
    paint-order: stroke;
    stroke: var(--topo-bg);
    stroke-width: 3px;
    stroke-linejoin: round;
  }
  .k-listener .rlabel { font-weight: 600; }
  .k-more .rlabel { fill: var(--topo-accent); }
  .st-off .rlabel { fill: var(--topo-sub); }
  .m-radial .edge.hot .edge-base { stroke: var(--topo-accent); stroke-opacity: 0.75; }
  /* 桑基 */
  .hit { fill: transparent; }
  .sbar { fill: var(--topo-off); transition: fill 0.2s; }
  .st-ok .sbar { fill: var(--topo-accent); }
  .st-down .sbar { fill: var(--topo-down); }
  .st-expected .sbar { fill: var(--topo-expected); }
  .k-more .sbar { fill: none; stroke: var(--topo-accent); stroke-dasharray: 3 2; }
  .node.sel .sbar { filter: drop-shadow(0 0 6px color-mix(in oklab, var(--primary) 70%, transparent)); }
  .halo { paint-order: stroke; stroke: var(--topo-bg); stroke-width: 3px; stroke-linejoin: round; }
  .edge-base.band { fill: var(--topo-accent); fill-opacity: 0.16; stroke: none; }
  .edge.hot .edge-base.band { stroke: none; fill-opacity: 0.38; }
  /* 旭日 */
  .sarc { fill: var(--topo-off); stroke: var(--topo-bg); stroke-width: 1; transition: fill 0.2s; }
  .st-ok .sarc { fill: var(--topo-accent); }
  .st-down .sarc { fill: var(--topo-down); }
  .st-expected .sarc { fill: var(--topo-expected); }
  .k-more .sarc { fill: var(--topo-accent); stroke: var(--topo-accent); stroke-dasharray: 3 2; }
  .node:hover .sarc,
  .node.hot .sarc { stroke: var(--topo-text); stroke-width: 1.2; }
  .node.sel .sarc { stroke: var(--topo-accent); stroke-width: 2.5; }
  .slabel {
    font-size: 11px;
    fill: var(--topo-text);
    pointer-events: none;
    paint-order: stroke;
    stroke: var(--topo-bg);
    stroke-width: 2.5px;
    stroke-linejoin: round;
  }
  .k-listener .slabel { font-weight: 600; }
  /* 树图 / 气泡 */
  .tm,
  .pk {
    fill: var(--topo-card);
    stroke: var(--topo-card-stroke);
    stroke-width: 1;
    transition: fill 0.2s, stroke 0.2s;
  }
  .tm.root,
  .pk.root { fill: color-mix(in oklab, var(--primary) 4%, transparent); stroke-dasharray: 4 4; }
  .k-listener .tm,
  .k-listener .pk { fill: color-mix(in oklab, var(--primary) 6%, var(--topo-card)); }
  .tm.leafbox,
  .pk.leafbox { fill: var(--topo-off); stroke: var(--topo-bg); }
  .st-ok .tm.leafbox,
  .st-ok .pk.leafbox { fill: var(--topo-accent); }
  .st-down .tm.leafbox,
  .st-down .pk.leafbox { fill: var(--topo-down); }
  .st-expected .tm.leafbox,
  .st-expected .pk.leafbox { fill: var(--topo-expected); }
  .k-more .tm,
  .k-more .pk { stroke: var(--topo-accent); stroke-dasharray: 3 2; }
  .node:hover .tm,
  .node.hot .tm,
  .node:hover .pk,
  .node.hot .pk { stroke: var(--topo-card-stroke-hi); }
  .node.sel .tm,
  .node.sel .pk { stroke: var(--topo-accent); stroke-width: 2; }
  .tm-head,
  .pk-head { font-size: 11.5px; font-weight: 600; fill: var(--topo-text); pointer-events: none; }
  .tm-title,
  .pk-title {
    font-size: 11px;
    font-weight: 600;
    fill: var(--topo-text);
    pointer-events: none;
    paint-order: stroke;
    stroke: var(--topo-card);
    stroke-width: 2.5px;
    stroke-linejoin: round;
  }
  .tm-sub { font-size: 10.5px; fill: var(--topo-text); opacity: 0.75; pointer-events: none; }
  /* 详情面板 */
  .panel {
    position: sticky;
    top: 16px;
    display: flex;
    flex-direction: column;
    gap: 10px;
    max-height: calc(100vh - 32px);
    overflow: auto;
    padding: 14px;
    border: 1px solid var(--border);
    border-radius: var(--radius);
    background: var(--card);
  }
  .topo.full .panel { top: 0; max-height: calc(100vh - 120px); }
  .panel-title { font-size: 15px; font-weight: 600; word-break: break-all; }
  .panel-sub {
    margin-top: 2px;
    font-size: 12px;
    color: var(--muted-foreground);
    word-break: break-all;
  }
  .tags { display: flex; flex-wrap: wrap; gap: 4px; margin-top: 8px; }
  .tag {
    padding: 1px 8px;
    border-radius: 999px;
    border: 1px solid var(--border);
    font-size: 11px;
    color: var(--muted-foreground);
    white-space: nowrap;
  }
  .tag.t-success { color: var(--success); border-color: color-mix(in oklab, var(--success) 40%, transparent); }
  .tag.t-warning { color: var(--warning); border-color: color-mix(in oklab, var(--warning) 40%, transparent); }
  .tag.t-danger { color: var(--destructive); border-color: color-mix(in oklab, var(--destructive) 40%, transparent); }
  .tag.t-primary { color: var(--primary); border-color: color-mix(in oklab, var(--primary) 40%, transparent); }
  .panel-error {
    margin-top: 8px;
    font-size: 12px;
    color: var(--destructive);
    word-break: break-all;
  }
  .metrics {
    display: grid;
    grid-template-columns: repeat(3, minmax(0, 1fr));
    gap: 8px;
  }
  .metric {
    padding: 8px;
    border-radius: 8px;
    background: var(--secondary);
  }
  .m-label { font-size: 11px; color: var(--muted-foreground); }
  .m-value {
    margin-top: 2px;
    font-size: 15px;
    font-weight: 600;
    font-variant-numeric: tabular-nums;
    word-break: break-all;
  }
  .m-value.bad { color: var(--destructive); }
  .m-value.warn { color: var(--warning); }
  .chart-title {
    display: flex;
    align-items: center;
    gap: 6px;
    font-size: 12px;
    font-weight: 600;
    color: var(--foreground);
  }
  .chart-meta { font-weight: 400; color: var(--muted-foreground); }
  .curve { display: block; }
  .rows { display: flex; flex-direction: column; }
  .row {
    display: flex;
    justify-content: space-between;
    gap: 12px;
    padding: 5px 0;
    border-bottom: 1px dashed var(--border);
    font-size: 12px;
  }
  .r-label { flex-shrink: 0; color: var(--muted-foreground); white-space: pre; }
  .r-value { text-align: right; word-break: break-all; font-variant-numeric: tabular-nums; }
  .r-value.bad { color: var(--destructive); }
  .r-value.warn { color: var(--warning); }
  .panel-actions { display: flex; flex-wrap: wrap; gap: 6px; }
  .ips-title { margin-top: 4px; }
  .ip-warn { font-size: 12px; color: var(--warning); }
  .ip-list {
    display: flex;
    flex-direction: column;
    max-height: 280px;
    overflow: auto;
    border: 1px solid var(--border);
    border-radius: 8px;
  }
  .ip-empty {
    padding: 14px;
    text-align: center;
    font-size: 12px;
    color: var(--muted-foreground);
  }
  .ip-row {
    display: flex;
    justify-content: space-between;
    gap: 8px;
    padding: 6px 10px;
    border: 0;
    border-bottom: 1px solid var(--border);
    background: none;
    color: inherit;
    font: inherit;
    font-size: 12px;
    cursor: pointer;
    text-align: left;
  }
  .ip-row:hover { background: color-mix(in oklab, var(--primary) 8%, transparent); }
  .ip-row:last-child { border-bottom: 0; }
  .ip-row .mono { font-family: var(--font-mono, monospace); }
  .ip-search { padding: 8px; border-bottom: 1px solid var(--border); }
  .ip-input {
    width: 100%;
    padding: 5px 8px;
    border: 1px solid var(--input);
    border-radius: 6px;
    background: var(--background);
    color: var(--foreground);
    font: inherit;
    font-size: 12.5px;
    outline: none;
  }
  .ip-input:focus { border-color: var(--primary); }
  .opt-meta {
    margin-left: auto;
    font-size: 11px;
    color: var(--muted-foreground);
    white-space: nowrap;
  }
  .mm-box {
    display: inline-flex;
    align-items: center;
    justify-content: center;
    width: 15px;
    height: 15px;
    margin-right: 4px;
    border: 1px solid var(--border);
    border-radius: 4px;
    flex-shrink: 0;
  }
  .mm-box.on {
    background: var(--primary);
    border-color: var(--primary);
    color: var(--primary-foreground);
  }
  .mm-radio {
    display: inline-block;
    width: 12px;
    height: 12px;
    margin-right: 4px;
    border: 1.5px solid var(--border);
    border-radius: 999px;
    flex-shrink: 0;
  }
  .mm-radio.on { border-color: var(--primary); box-shadow: inset 0 0 0 3px var(--primary); }
  .mm-sec {
    padding: 6px 8px 2px;
    font-size: 10.5px;
    color: var(--muted-foreground);
  }
  .mm-head { padding: 6px 8px 2px; font-size: 11px; color: var(--muted-foreground); }
  .layout-item.on { background: color-mix(in oklab, var(--primary) 8%, transparent); }
  .topo-foot {
    display: flex;
    justify-content: space-between;
    gap: 8px;
    font-size: 11px;
    color: var(--muted-foreground);
  }
</style>
