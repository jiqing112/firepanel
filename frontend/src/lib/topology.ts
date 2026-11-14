// Web 服务拓扑视图的数据类型与纯函数：布局、链路高亮、IP 筛选与打码。
// 1:1 移植自 gately web/src/topology.ts；组件 WebTopology.svelte 只负责渲染与交互，
// 这里的函数可在单测中直接调用。
import { hierarchy, pack, treemap, treemapSquarify, type HierarchyNode } from 'd3-hierarchy';

export interface Rate {
  qps: number;
  bps: number;
  qps60: number;
}

export interface RatePoint {
  t: number;
  requests: number;
  bytes: number;
}

export interface TopoUpstream {
  id: string;
  url: string;
  scope?: string;
  match?: string;
  weight: number;
  backup?: boolean;
  healthy?: boolean;
  conns: number;
  requests: number;
  bytes: number;
  rate: Rate;
}

export interface TopoSite {
  id: string;
  key: string;
  name: string;
  type: string;
  domains: string[];
  default?: boolean;
  enable: boolean;
  running: boolean;
  target?: string;
  guards: string[];
  accessLog?: boolean;
  requests: number;
  active: number;
  errors: number;
  closed: number;
  rate: Rate;
  status: Record<string, number>;
  blocked: Record<string, number>;
  upstreams: TopoUpstream[];
}

export interface TopoAction {
  id: string;
  kind: 'redirect' | 'notFound';
  requests: number;
  rate: Rate;
}

export interface TopoListener {
  id: string;
  key: string;
  name: string;
  enable: boolean;
  running: boolean;
  error?: string;
  network: string;
  listenIp: string;
  port: number;
  tls: boolean;
  http3: boolean;
  plainHttp: string;
  httpPort?: number;
  httpPortMode?: string;
  proxyProtocol: boolean;
  requests: number;
  active: number;
  rate: Rate;
  sites: TopoSite[];
  actions: TopoAction[];
}

export interface TopoIP {
  ip: string;
  nodes: string[];
  active: number;
  activeAt?: Record<string, number>;
  last: number;
}

export interface TopologyView {
  time: number;
  window: number;
  enabled: boolean;
  requests: number;
  active: number;
  rate: Rate;
  listeners: TopoListener[];
  ips: TopoIP[];
  ipTotal: number;
  ipTruncated: boolean;
  focus?: string;
  series?: RatePoint[];
}

// 节点种类：internet 互联网、gately 本实例、listener 监听、site 站点、action 监听级动作、
// upstream 后端、terminal 非代理站点的去向、more 子节点过多时收起其余子节点的汇总节点
export type NodeKind = 'internet' | 'gately' | 'listener' | 'site' | 'action' | 'upstream' | 'terminal' | 'more';

// 布局方式：tree 从左到右的树形，vertical 从上到下的竖向树，radial 以 Gately 为圆心的径向，
// sankey 按流量分配连线粗细的桑基图，sunburst 同心环形分区的旭日图，
// treemap 嵌套矩形的矩形树图，pack 嵌套圆形的气泡图
export type LayoutMode = 'tree' | 'vertical' | 'radial' | 'sankey' | 'sunburst' | 'treemap' | 'pack';
export const LAYOUT_MODES: LayoutMode[] = ['tree', 'vertical', 'radial', 'sankey', 'sunburst', 'treemap', 'pack'];

// isColumnar 判断布局是否按列从左到右排列（默认适应宽度、从顶部开始显示）
export function isColumnar(mode: LayoutMode): boolean {
  return mode === 'tree' || mode === 'sankey';
}

// MoreInfo 描述汇总节点：owner 为被收起子节点的父节点，ids 为收起的直接子节点，
// expanded 表示已展开全部（此时节点用于收起），rate 与 active 为被收起子节点的合计
export interface MoreInfo {
  owner: string;
  ids: string[];
  count: number;
  expanded: boolean;
  childKind: 'site' | 'upstream';
  rate: Rate;
  active: number;
}

export interface LayoutNode {
  id: string;
  kind: NodeKind;
  col: number;
  x: number;
  y: number;
  w: number;
  h: number;
  parent?: string;
  listener?: TopoListener;
  site?: TopoSite;
  action?: TopoAction;
  upstream?: TopoUpstream;
  more?: MoreInfo;
  collapsed?: boolean;
  hidden?: number;
  angle?: number;
  leaf?: boolean;
  shape?: string;
  label?: { x: number; y: number; angle: number; max: number; show: boolean };
}

export interface LayoutEdge {
  id: string;
  from: string;
  to: string;
  d: string;
  rate: Rate;
  labelX: number;
  labelY: number;
  band?: boolean;
  flow?: string;
}

export interface Layout {
  mode: LayoutMode;
  width: number;
  height: number;
  nodes: LayoutNode[];
  edges: LayoutEdge[];
  byId: Map<string, LayoutNode>;
  children: Map<string, string[]>;
  alias: Map<string, string>;
}

export interface LayoutOptions {
  mode?: LayoutMode;
  expanded?: Set<string>;
  limit?: number;
  upstreamLimit?: number;
}

// 画布尺寸：列的左边缘与宽度，行高
export const ROW = 60;
export const PAD_Y = 16;
export const COLS = [
  { x: 18, w: 56 }, // 互联网（圆形）
  { x: 112, w: 64 }, // Gately（圆形）
  { x: 220, w: 188 }, // 监听
  { x: 460, w: 226 }, // 站点 / 监听级动作
  { x: 740, w: 236 }, // 后端 / 去向
];
export const WIDTH = 1000;
const NODE_H = 46;

// 默认每个监听显示 8 个站点、每个站点显示 6 个后端，其余收进汇总节点
export const SITE_LIMIT = 8;
export const UPSTREAM_LIMIT = 6;

// siteMatches 判断站点是否匹配搜索词（名称、域名、后端地址）。
export function siteMatches(s: TopoSite, q: string): boolean {
  if (!q) {
    return true;
  }
  const k = q.toLowerCase();
  if ((s.name || '').toLowerCase().includes(k)) {
    return true;
  }
  if (s.domains.some((d) => d.toLowerCase().includes(k))) {
    return true;
  }
  return s.upstreams.some((u) => u.url.toLowerCase().includes(k));
}

// TopoFilter 为拓扑筛选条件：listeners 只显示这些监听，issues 只看运行异常，
// blocked 只看出现过这些原因拦截的站点，active 只看活动中的站点。
export interface TopoFilter {
  listeners?: string[];
  issues?: boolean;
  blocked?: string[];
  active?: boolean;
}

// rateActive 判断速率是否表示近期有流量。
function rateActive(r: Rate): boolean {
  return r.qps > 0 || r.qps60 > 0 || r.bps > 0;
}

// seenNodes 返回活动 IP 经过的节点编号。
export function seenNodes(v: TopologyView): Set<string> {
  const out = new Set<string>();
  for (const ip of v.ips) {
    for (const n of ip.nodes) {
      out.add(n);
    }
  }
  return out;
}

// siteActive 判断站点是否活动中。
export function siteActive(s: TopoSite, seen?: Set<string>): boolean {
  return s.active > 0 || rateActive(s.rate) || !!seen?.has(s.id);
}

// upstreamActive 判断后端是否活动中。
export function upstreamActive(u: TopoUpstream, seen?: Set<string>): boolean {
  return u.conns > 0 || rateActive(u.rate) || !!seen?.has(u.id);
}

// actionActive 判断监听级动作最近 60 秒是否处理过请求。
export function actionActive(a: TopoAction, seen?: Set<string>): boolean {
  return rateActive(a.rate) || !!seen?.has(a.id);
}

// countActive 统计视图中活动中的站点数。
export function countActive(v: TopologyView, query = ''): number {
  const seen = seenNodes(v);
  let n = 0;
  for (const l of v.listeners) {
    for (const s of l.sites) {
      if (siteMatches(s, query) && siteActive(s, seen)) {
        n++;
      }
    }
  }
  return n;
}

// BLOCK_REASONS 为站点拦截计数的原因，与后端 blocked 的键一致
export const BLOCK_REASONS = ['ip', 'rateLimit'];

// filterActive 判断筛选条件是否生效。
export function filterActive(f: TopoFilter): boolean {
  return !!(f.listeners?.length || f.issues || f.blocked?.length || f.active);
}

// listenerFailed 判断监听是否启动失败。
export function listenerFailed(l: TopoListener): boolean {
  return l.enable && (!l.running || !!l.error);
}

// siteIssue 判断站点是否运行异常。
export function siteIssue(s: TopoSite): boolean {
  if (!s.enable) {
    return false;
  }
  return !s.running || s.upstreams.some((u) => u.healthy === false);
}

// filterView 按筛选条件裁剪拓扑数据。
export function filterView(v: TopologyView, f: TopoFilter): TopologyView {
  if (!filterActive(f)) {
    return v;
  }
  const out = filterStatic(v, f);
  if (!f.active) {
    return out;
  }
  const seen = seenNodes(v);
  const listeners: TopoListener[] = [];
  for (const l of out.listeners) {
    const sites = l.sites
      .filter((s) => siteActive(s, seen))
      .map((s) => (s.upstreams.length ? { ...s, upstreams: s.upstreams.filter((u) => upstreamActive(u, seen)) } : s));
    const actions = l.actions.filter((a) => actionActive(a, seen));
    if (!sites.length && !actions.length) {
      continue;
    }
    listeners.push({ ...l, sites, actions });
  }
  return { ...out, listeners };
}

// filterStatic 按监听、运行异常与拦截原因筛选。
function filterStatic(v: TopologyView, f: TopoFilter): TopologyView {
  if (!(f.listeners?.length || f.issues || f.blocked?.length)) {
    return v;
  }
  const only = f.listeners?.length ? new Set(f.listeners) : undefined;
  const reasons = f.blocked ?? [];
  const listeners: TopoListener[] = [];
  for (const l of v.listeners) {
    if (only && !only.has(l.key)) {
      continue;
    }
    const failed = listenerFailed(l);
    let sites = l.sites;
    if (f.issues && !failed) {
      sites = l.enable ? sites.filter(siteIssue) : [];
    }
    if (reasons.length) {
      sites = sites.filter((s) => reasons.some((r) => (s.blocked?.[r] ?? 0) > 0));
    }
    const narrowed = f.issues || reasons.length > 0;
    if (narrowed && !sites.length && !(f.issues && failed && !reasons.length)) {
      continue;
    }
    listeners.push(narrowed ? { ...l, sites, actions: [] } : l);
  }
  return { ...v, listeners };
}

// terminalId 返回非代理站点的去向节点编号。
export function terminalId(siteId: string): string {
  return 't:' + siteId.slice(2);
}

// moreId 返回父节点下汇总节点的编号。
export function moreId(owner: string): string {
  return 'm:' + owner;
}

// curve 生成从 (x1,y1) 到 (x2,y2) 的水平三次贝塞尔曲线。
function curve(x1: number, y1: number, x2: number, y2: number): string {
  const dx = Math.max(24, (x2 - x1) * 0.5);
  return `M${x1},${y1} C${x1 + dx},${y1} ${x2 - dx},${y2} ${x2},${y2}`;
}

const zero: Rate = { qps: 0, bps: 0, qps60: 0 };

// TNode 为排版前的抽象树节点。
type TNode = Omit<LayoutNode, 'col' | 'x' | 'y' | 'w' | 'h'> & { kids: TNode[] };

// nodeRate 返回流经节点的速率。
function nodeRate(n: Omit<LayoutNode, 'col' | 'x' | 'y' | 'w' | 'h'>): Rate {
  return n.listener?.rate ?? n.action?.rate ?? n.upstream?.rate ?? n.more?.rate ?? n.site?.rate ?? zero;
}

// buildTree 按搜索词、折叠状态与数量上限生成要显示的监听子树。
function buildTree(v: TopologyView, collapsed: Set<string>, query: string, opts: LayoutOptions, alias: Map<string, string>): TNode[] {
  const expanded = opts.expanded ?? new Set<string>();
  const limit = opts.limit ?? SITE_LIMIT;
  const upLimit = opts.upstreamLimit ?? UPSTREAM_LIMIT;

  const aliasAll = (t: TNode, to: string) => {
    alias.set(t.id, to);
    for (const k of t.kids) {
      aliasAll(k, to);
    }
  };
  const seen = seenNodes(v);
  const busy = (t: TNode) => (t.kind === 'upstream' ? upstreamActive(t.upstream!, seen) : t.kind === 'site' ? siteActive(t.site!, seen) : false);
  const cap = (owner: string, list: TNode[], max: number, childKind: MoreInfo['childKind']): TNode[] => {
    if (list.length <= max + 1) {
      return list;
    }
    const rest = list.slice(max).filter((t) => !busy(t));
    if (rest.length <= 1) {
      return list;
    }
    const sum = rest.reduce((a, t) => {
      const r = nodeRate(t);
      return { qps: a.qps + r.qps, bps: a.bps + r.bps, qps60: a.qps60 + r.qps60 };
    }, zero);
    const active = rest.reduce((a, t) => a + (t.kind === 'upstream' ? (t.upstream?.conns ?? 0) : (t.site?.active ?? 0)), 0);
    const isOpen = expanded.has(owner);
    const m: TNode = {
      id: moreId(owner),
      kind: 'more',
      parent: owner,
      more: { owner, ids: rest.map((t) => t.id), count: rest.length, expanded: isOpen, childKind, rate: sum, active },
      kids: [],
    };
    if (isOpen) {
      return [...list, m];
    }
    const hidden = new Set(rest);
    for (const t of rest) {
      aliasAll(t, m.id);
    }
    return [...list.filter((t) => !hidden.has(t)), m];
  };

  const roots: TNode[] = [];
  for (const l of v.listeners) {
    const sites = l.sites.filter((s) => siteMatches(s, query));
    const selfMatch = !query || (l.name || '').toLowerCase().includes(query.toLowerCase()) || String(l.port) === query;
    if (query && !sites.length && !selfMatch) {
      continue;
    }
    const shown = selfMatch && query && !sites.length ? l.sites : sites;
    const isCollapsed = collapsed.has(l.key);
    let kids: TNode[] = [];
    if (!isCollapsed) {
      for (const s of shown) {
        let leaves: TNode[];
        if (s.type === 'proxy') {
          leaves = s.upstreams.map((u) => ({ id: u.id, kind: 'upstream' as const, parent: s.id, upstream: u, site: s, kids: [] }));
          leaves = cap(s.id, leaves, upLimit, 'upstream');
        } else {
          const ups: TNode[] = s.upstreams.map((u) => ({ id: u.id, kind: 'upstream' as const, parent: s.id, upstream: u, site: s, kids: [] }));
          leaves = [{ id: terminalId(s.id), kind: 'terminal', parent: s.id, site: s, kids: [] }, ...cap(s.id, ups, upLimit, 'upstream')];
        }
        kids.push({ id: s.id, kind: 'site', parent: l.id, site: s, kids: leaves });
      }
      kids = cap(l.id, kids, limit, 'site');
      for (const a of query && !selfMatch ? [] : l.actions) {
        kids.push({ id: a.id, kind: 'action', parent: l.id, action: a, kids: [] });
      }
    }
    const hidden = isCollapsed ? l.sites.length + l.actions.length : l.sites.length - shown.length + (query && !selfMatch ? l.actions.length : 0);
    roots.push({ id: l.id, kind: 'listener', parent: 'gately', listener: l, collapsed: isCollapsed, hidden, kids });
  }
  return roots;
}

// buildLayout 生成拓扑布局。
export function buildLayout(v: TopologyView, collapsed: Set<string>, query: string, opts: LayoutOptions = {}): Layout {
  const alias = new Map<string, string>();
  const roots = buildTree(v, collapsed, query, opts, alias);
  const out: Layout = { mode: opts.mode ?? 'tree', width: WIDTH, height: 0, nodes: [], edges: [], byId: new Map(), children: new Map(), alias };
  if (out.mode === 'radial') {
    layoutRadial(v, roots, out);
  } else if (out.mode === 'sankey') {
    layoutSankey(v, roots, out);
  } else if (out.mode === 'sunburst') {
    layoutSunburst(roots, out);
  } else if (out.mode === 'vertical') {
    layoutVertical(v, roots, out);
  } else if (out.mode === 'treemap' || out.mode === 'pack') {
    layoutNested(roots, out);
  } else {
    layoutTree(v, roots, out);
  }
  return out;
}

// addNode 把节点加入布局并登记父子关系。
function addNode(out: Layout, n: LayoutNode): LayoutNode {
  out.nodes.push(n);
  out.byId.set(n.id, n);
  if (n.parent) {
    const list = out.children.get(n.parent) ?? [];
    list.push(n.id);
    out.children.set(n.parent, list);
  }
  return n;
}

// strip 去掉抽象树节点的子节点列表。
function strip(t: TNode): Omit<LayoutNode, 'col' | 'x' | 'y' | 'w' | 'h'> {
  const { kids: _kids, ...base } = t;
  return base;
}

function layoutTree(v: TopologyView, roots: TNode[], out: Layout) {
  let row = 0;
  const rowY = (r: number) => PAD_Y + r * ROW + ROW / 2;
  const card = (col: number, y: number) => ({ col, x: COLS[col].x, w: COLS[col].w, y: y - NODE_H / 2, h: NODE_H });
  const place = (t: TNode, depth: number): number => {
    let y: number;
    if (t.kids.length) {
      const ys = t.kids.map((k) => place(k, depth + 1));
      y = (ys[0] + ys[ys.length - 1]) / 2;
    } else {
      y = rowY(row);
      row++;
    }
    addNode(out, { ...strip(t), ...card(2 + depth, y), leaf: !t.kids.length });
    return y;
  };
  const listenerYs = roots.map((t) => place(t, 0));
  const height = PAD_Y * 2 + Math.max(1, row) * ROW;
  const midY = listenerYs.length ? (listenerYs[0] + listenerYs[listenerYs.length - 1]) / 2 : rowY(0);
  addNode(out, { id: 'internet', kind: 'internet', col: 0, x: COLS[0].x, w: COLS[0].w, y: midY - COLS[0].w / 2, h: COLS[0].w });
  addNode(out, { id: 'gately', kind: 'gately', col: 1, x: COLS[1].x, w: COLS[1].w, y: midY - COLS[1].w / 2, h: COLS[1].w, parent: 'internet' });
  out.width = WIDTH;
  out.height = height;

  for (const n of out.nodes) {
    const p = n.parent ? out.byId.get(n.parent) : undefined;
    if (!p) {
      continue;
    }
    const x1 = p.x + p.w;
    const y1 = p.y + p.h / 2;
    const x2 = n.x;
    const y2 = n.y + n.h / 2;
    const rate = n.kind === 'gately' ? v.rate : nodeRate(n);
    out.edges.push({ id: p.id + '>' + n.id, from: p.id, to: n.id, d: curve(x1, y1, x2, y2), rate, labelX: (x1 + x2) / 2, labelY: (y1 + y2) / 2 - 5 });
  }
}

// 径向布局参数
export const RADIAL_RINGS = [180, 350, 500];
export const RADIAL_LABEL = 210;
const RADIAL_GAP = 1.1;
const RADIAL_MIN_ARC = 22;
const RADIAL_GROUP_GAP = 0.6;

function layoutRadial(v: TopologyView, roots: TNode[], out: Layout) {
  const leafCount = (t: TNode): number => (t.kids.length ? t.kids.reduce((n, k) => n + leafCount(k), 0) : 1);
  const units = roots.reduce((n, t) => n + leafCount(t), 0) + Math.max(0, roots.length - 1) * RADIAL_GROUP_GAP;
  const span = Math.PI * 2 - RADIAL_GAP;
  const k = Math.max(1, (RADIAL_MIN_ARC * units) / span / RADIAL_RINGS[2]);
  const rings = [RADIAL_RINGS[0] * Math.min(k, 1.6), RADIAL_RINGS[1] * k, RADIAL_RINGS[2] * k];
  const half = Math.round(rings[2] + RADIAL_LABEL);
  const c = half;
  const step = units ? span / units : 0;
  const start = Math.PI + RADIAL_GAP / 2;
  let pos = 0;
  const place = (t: TNode, depth: number): number => {
    let a: number;
    if (t.kids.length) {
      const as = t.kids.map((kid) => place(kid, depth + 1));
      a = (as[0] + as[as.length - 1]) / 2;
    } else {
      a = start + (pos + 0.5) * step;
      pos++;
    }
    const r = rings[depth];
    const nr = t.kind === 'listener' ? 15 : t.kind === 'more' ? 11 : 12;
    const x = c + r * Math.cos(a);
    const y = c + r * Math.sin(a);
    addNode(out, { ...strip(t), col: 2 + depth, x: x - nr, y: y - nr, w: nr * 2, h: nr * 2, angle: a, leaf: !t.kids.length });
    return a;
  };
  roots.forEach((t, i) => {
    if (i > 0) {
      pos += RADIAL_GROUP_GAP;
    }
    place(t, 0);
  });
  const gr = 34;
  const ir = 24;
  const ia = Math.PI;
  const id = rings[0] * 0.56;
  addNode(out, { id: 'internet', kind: 'internet', col: 0, x: c + id * Math.cos(ia) - ir, y: c + id * Math.sin(ia) - ir, w: ir * 2, h: ir * 2, angle: ia });
  addNode(out, { id: 'gately', kind: 'gately', col: 1, x: c - gr, y: c - gr, w: gr * 2, h: gr * 2, parent: 'internet' });
  out.width = half * 2;
  out.height = half * 2;

  const center = (n: LayoutNode) => ({ x: n.x + n.w / 2, y: n.y + n.h / 2 });
  for (const n of out.nodes) {
    const p = n.parent ? out.byId.get(n.parent) : undefined;
    if (!p) {
      continue;
    }
    const a = center(p);
    const b = center(n);
    const rate = n.kind === 'gately' ? v.rate : nodeRate(n);
    if (n.kind === 'gately') {
      out.edges.push({ id: p.id + '>' + n.id, from: p.id, to: n.id, d: `M${a.x},${a.y} L${b.x},${b.y}`, rate, labelX: (a.x + b.x) / 2, labelY: (a.y + b.y) / 2 - 8 });
      continue;
    }
    const rp = Math.hypot(a.x - c, a.y - c);
    const rc = Math.hypot(b.x - c, b.y - c);
    const ap = p.kind === 'gately' ? n.angle! : p.angle!;
    const ac = n.angle!;
    const rm = (rp + rc) / 2;
    const c1x = c + rm * Math.cos(ap);
    const c1y = c + rm * Math.sin(ap);
    const c2x = c + rm * Math.cos(ac);
    const c2y = c + rm * Math.sin(ac);
    const lx = (a.x + 3 * c1x + 3 * c2x + b.x) / 8;
    const ly = (a.y + 3 * c1y + 3 * c2y + b.y) / 8;
    out.edges.push({ id: p.id + '>' + n.id, from: p.id, to: n.id, d: `M${a.x},${a.y} C${c1x},${c1y} ${c2x},${c2y} ${b.x},${b.y}`, rate, labelX: lx, labelY: ly - 4 });
  }
}

// 桑基图参数
export const SANKEY_COLS = [20, 190, 360, 580, 820];
export const SANKEY_BAR = 12;
export const SANKEY_LABEL = 230;
export const SANKEY_ROW = 46;
const SANKEY_MIN = 6;

// layoutSankey 按行排版，竖条高度与连线带宽度随 QPS 变化。
function layoutSankey(v: TopologyView, roots: TNode[], out: Layout) {
  interface P {
    t: TNode;
    depth: number;
    cy: number;
    span: number;
    kids: P[];
  }
  let row = 0;
  const place = (t: TNode, depth: number): P => {
    if (t.kids.length) {
      const kids = t.kids.map((k) => place(k, depth + 1));
      const first = kids[0];
      const last = kids[kids.length - 1];
      const top = first.cy - first.span / 2;
      const bottom = last.cy + last.span / 2;
      return { t, depth, cy: (first.cy + last.cy) / 2, span: bottom - top, kids };
    }
    const cy = PAD_Y + row * SANKEY_ROW + SANKEY_ROW / 2;
    row++;
    return { t, depth, cy, span: SANKEY_ROW, kids: [] };
  };
  const ps = roots.map((t) => place(t, 0));
  const rows = Math.max(1, row);
  const spanAll = rows * SANKEY_ROW;
  const total = Math.max(0, v.rate.qps);
  const k = total > 0 ? (spanAll * 0.7) / total : 0;
  const barH = (qps: number, span: number) => Math.min(Math.max(SANKEY_MIN, span - 10), Math.max(SANKEY_MIN, k * qps));
  const addBar = (p: P) => {
    const h = barH(nodeRate(p.t).qps, p.span);
    addNode(out, { ...strip(p.t), col: 2 + p.depth, x: SANKEY_COLS[2 + p.depth], y: p.cy - h / 2, w: SANKEY_BAR, h, leaf: !p.kids.length });
    p.kids.forEach(addBar);
  };
  ps.forEach(addBar);
  const midY = ps.length ? (ps[0].cy + ps[ps.length - 1].cy) / 2 : PAD_Y + SANKEY_ROW / 2;
  const hubH = barH(total, spanAll);
  addNode(out, { id: 'internet', kind: 'internet', col: 0, x: SANKEY_COLS[0], y: midY - hubH / 2, w: SANKEY_BAR, h: hubH });
  addNode(out, { id: 'gately', kind: 'gately', col: 1, x: SANKEY_COLS[1], y: midY - hubH / 2, w: SANKEY_BAR, h: hubH, parent: 'internet' });
  out.width = SANKEY_COLS[4] + SANKEY_BAR + SANKEY_LABEL;
  out.height = PAD_Y * 2 + spanAll;

  for (const [pid, kids] of out.children) {
    const p = out.byId.get(pid);
    if (!p) {
      continue;
    }
    const nodes = kids.map((id) => out.byId.get(id)!).filter(Boolean);
    const qps = nodes.map((n) => Math.max(0, n.kind === 'gately' ? v.rate.qps : nodeRate(n).qps));
    const sum = qps.reduce((a, b) => a + b, 0);
    const eps = sum > 0 ? sum * 0.03 : 1;
    const weights = qps.map((q) => q + eps);
    const wsum = weights.reduce((a, b) => a + b, 0);
    let off = p.y;
    nodes.forEach((n, i) => {
      const th = (p.h * weights[i]) / wsum;
      const x1 = p.x + p.w;
      const x2 = n.x;
      const xm = (x1 + x2) / 2;
      const py0 = off;
      const py1 = off + th;
      off = py1;
      const cy0 = n.y;
      const cy1 = n.y + n.h;
      const d = `M${x1},${py0} C${xm},${py0} ${xm},${cy0} ${x2},${cy0} L${x2},${cy1} C${xm},${cy1} ${xm},${py1} ${x1},${py1} Z`;
      const a = (py0 + py1) / 2;
      const b = (cy0 + cy1) / 2;
      const flow = `M${x1},${a} C${xm},${a} ${xm},${b} ${x2},${b}`;
      const rate = n.kind === 'gately' ? v.rate : nodeRate(n);
      out.edges.push({ id: p.id + '>' + n.id, from: p.id, to: n.id, d, flow, band: true, rate, labelX: xm, labelY: (a + b) / 2 - 5 });
    });
  }
}

// 旭日图参数
export const SUN_RINGS: [number, number][] = [
  [64, 150],
  [154, 300],
  [304, 440],
];
export const SUN_HUB = 50;
const SUN_MIN_ARC = 10;
const SUN_GROUP_GAP = 0.3;

// arcPath 生成圆心 (c,c)、半径 r0~r1、角度 a0~a1 的环形扇区路径。
function arcPath(c: number, r0: number, r1: number, a0: number, a1: number): string {
  const end = Math.min(a1, a0 + Math.PI * 2 - 1e-4);
  const large = end - a0 > Math.PI ? 1 : 0;
  const pt = (r: number, a: number) => `${(c + r * Math.cos(a)).toFixed(2)},${(c + r * Math.sin(a)).toFixed(2)}`;
  return `M${pt(r1, a0)} A${r1},${r1} 0 ${large} 1 ${pt(r1, end)} L${pt(r0, end)} A${r0},${r0} 0 ${large} 0 ${pt(r0, a0)} Z`;
}

// layoutSunburst 以 Gately 为圆心，监听、站点、后端依次占据三层环。
function layoutSunburst(roots: TNode[], out: Layout) {
  const leafCount = (t: TNode): number => (t.kids.length ? t.kids.reduce((n, k) => n + leafCount(k), 0) : 1);
  const units = roots.reduce((n, t) => n + leafCount(t), 0) + Math.max(0, roots.length - 1) * SUN_GROUP_GAP;
  const span = Math.PI * 2;
  const outerMid = (SUN_RINGS[2][0] + SUN_RINGS[2][1]) / 2;
  const k = Math.max(1, (SUN_MIN_ARC * units) / span / outerMid);
  const rings = SUN_RINGS.map(([a, b], i) => (i === 0 ? [a, b] : [a * k, b * k]) as [number, number]);
  const half = Math.round(rings[2][1] + 16);
  const c = half;
  const step = units ? span / units : 0;
  const start = -Math.PI / 2;
  let pos = 0;
  const place = (t: TNode, depth: number): [number, number] => {
    let a0: number;
    let a1: number;
    if (t.kids.length) {
      const as = t.kids.map((kid) => place(kid, depth + 1));
      a0 = as[0][0];
      a1 = as[as.length - 1][1];
    } else {
      a0 = start + pos * step;
      a1 = a0 + step;
      pos++;
    }
    const [r0, r1] = rings[depth];
    const pad = Math.min((a1 - a0) / 4, 0.75 / r1);
    const am = (a0 + a1) / 2;
    const rm = (r0 + r1) / 2;
    const arcLen = (a1 - a0) * rm;
    addNode(out, {
      ...strip(t),
      col: 2 + depth,
      x: 0,
      y: 0,
      w: 0,
      h: 0,
      angle: am,
      leaf: !t.kids.length,
      shape: arcPath(c, r0, r1, a0 + pad, a1 - pad),
      label: { x: c + rm * Math.cos(am), y: c + rm * Math.sin(am), angle: am, max: r1 - r0 - 14, show: arcLen >= 13 },
    });
    return [a0, a1];
  };
  roots.forEach((t, i) => {
    if (i > 0) {
      pos += SUN_GROUP_GAP;
    }
    place(t, 0);
  });
  addNode(out, { id: 'gately', kind: 'gately', col: 1, x: c - SUN_HUB, y: c - SUN_HUB, w: SUN_HUB * 2, h: SUN_HUB * 2 });
  out.width = half * 2;
  out.height = half * 2;
}

// 竖向树参数
export const VERT_CARD = 168;
const VERT_SLOT = 182;
const VERT_PAD = 16;
export const VERT_LEVELS = [8, 118, 240, 340, 440];

// layoutVertical 按“互联网 → Gately → 监听 → 站点 → 后端”从上到下排列。
function layoutVertical(v: TopologyView, roots: TNode[], out: Layout) {
  let col = 0;
  const colX = (c: number) => VERT_PAD + c * VERT_SLOT + VERT_SLOT / 2;
  const place = (t: TNode, depth: number): number => {
    let x: number;
    if (t.kids.length) {
      const xs = t.kids.map((k) => place(k, depth + 1));
      x = (xs[0] + xs[xs.length - 1]) / 2;
    } else {
      x = colX(col);
      col++;
    }
    addNode(out, { ...strip(t), col: 2 + depth, x: x - VERT_CARD / 2, y: VERT_LEVELS[2 + depth], w: VERT_CARD, h: NODE_H, leaf: !t.kids.length });
    return x;
  };
  const xs = roots.map((t) => place(t, 0));
  const cols = Math.max(1, col);
  const midX = xs.length ? (xs[0] + xs[xs.length - 1]) / 2 : colX(0);
  addNode(out, { id: 'internet', kind: 'internet', col: 0, x: midX - COLS[0].w / 2, y: VERT_LEVELS[0], w: COLS[0].w, h: COLS[0].w });
  addNode(out, { id: 'gately', kind: 'gately', col: 1, x: midX - COLS[1].w / 2, y: VERT_LEVELS[1], w: COLS[1].w, h: COLS[1].w, parent: 'internet' });
  out.width = Math.max(VERT_PAD * 2 + cols * VERT_SLOT, midX + 240);
  out.height = VERT_LEVELS[4] + NODE_H + VERT_PAD;

  for (const n of out.nodes) {
    const p = n.parent ? out.byId.get(n.parent) : undefined;
    if (!p) {
      continue;
    }
    const x1 = p.x + p.w / 2;
    const y1 = p.y + p.h;
    const x2 = n.x + n.w / 2;
    const y2 = n.y;
    const dy = Math.max(16, (y2 - y1) * 0.5);
    const rate = n.kind === 'gately' ? v.rate : nodeRate(n);
    out.edges.push({ id: p.id + '>' + n.id, from: p.id, to: n.id, d: `M${x1},${y1} C${x1},${y1 + dy} ${x2},${y2 - dy} ${x2},${y2}`, rate, labelX: (x1 + x2) / 2 + 4, labelY: (y1 + y2) / 2 });
  }
}

// 矩形树图与气泡图的尺寸
const NEST_LEAF_AREA = 15000;
export const NEST_HEAD = [24, 22, 20];

// layoutNested 生成矩形树图（treemap）或气泡图（pack）。
function layoutNested(roots: TNode[], out: Layout) {
  const root: TNode = { id: 'gately', kind: 'gately', kids: roots };
  const h = hierarchy<TNode>(root, (d) => d.kids)
    .sum((d) => (d.kids.length ? 0 : 1));
  const leaves = Math.max(1, h.value ?? 0);
  const add = (d: HierarchyNode<TNode>, x: number, y: number, w: number, hh: number) => {
    const t = d.data;
    if (t.id === 'gately') {
      addNode(out, { id: 'gately', kind: 'gately', col: 1, x, y, w, h: hh });
      return;
    }
    addNode(out, { ...strip(t), col: 1 + d.depth, x, y, w, h: hh, leaf: !t.kids.length });
  };
  if (out.mode === 'treemap') {
    const area = leaves * NEST_LEAF_AREA;
    const width = Math.max(900, Math.round(Math.sqrt(area * 1.6)));
    const height = Math.max(420, Math.round(area / width) + 120);
    const tm = treemap<TNode>()
      .size([width, height])
      .tile(treemapSquarify.ratio(1.4))
      .paddingOuter(4)
      .paddingTop((d) => (d.children ? NEST_HEAD[Math.min(d.depth, 2)] : 0))
      .paddingInner(3)
      .round(true);
    const r = tm(h);
    for (const d of r.descendants()) {
      add(d, d.x0, d.y0, d.x1 - d.x0, d.y1 - d.y0);
    }
    out.width = width;
    out.height = height;
    return;
  }
  const size = Math.max(640, Math.round(Math.sqrt(leaves) * 110));
  const pk = pack<TNode>()
    .size([size, size])
    .padding((d: HierarchyNode<TNode>) => (d.depth === 0 ? 14 : 6));
  const r = pk(h);
  for (const d of r.descendants()) {
    add(d, d.x - d.r, d.y - d.r, d.r * 2, d.r * 2);
  }
  out.width = size;
  out.height = size;
}

// resolveId 返回节点在布局中实际显示的编号。
export function resolveId(layout: Layout, id: string): string {
  let cur = id;
  for (let i = 0; i < 4; i++) {
    if (layout.byId.has(cur)) {
      return cur;
    }
    const next = layout.alias.get(cur);
    if (!next) {
      return '';
    }
    cur = next;
  }
  return layout.byId.has(cur) ? cur : '';
}

// ancestors 返回节点及其全部上游节点。
export function ancestors(layout: Layout, id: string): string[] {
  const out: string[] = [];
  let cur: string | undefined = id;
  while (cur) {
    out.push(cur);
    cur = layout.byId.get(cur)?.parent;
  }
  return out;
}

// descendants 返回节点的全部下游节点（不含自身）。
export function descendants(layout: Layout, id: string): string[] {
  const out: string[] = [];
  const stack = [...(layout.children.get(id) ?? [])];
  while (stack.length) {
    const c = stack.pop()!;
    out.push(c);
    stack.push(...(layout.children.get(c) ?? []));
  }
  return out;
}

// pathOf 返回选中节点所在的整条链路。
export function pathOf(layout: Layout, id: string): Set<string> | null {
  if (!layout.byId.has(id) || id === 'internet' || id === 'gately') {
    return null;
  }
  return new Set([...ancestors(layout, id), ...descendants(layout, id)]);
}

// ipPath 返回一个访客地址走过的链路。
export function ipPath(layout: Layout, ip: TopoIP | undefined): Set<string> {
  const out = new Set<string>();
  if (!ip) {
    return out;
  }
  const ups = new Set(ip.nodes.filter((n) => n.startsWith('u:')).map((n) => resolveId(layout, n)));
  for (const raw of ip.nodes) {
    const n = resolveId(layout, raw);
    if (!n) {
      continue;
    }
    for (const a of ancestors(layout, n)) {
      out.add(a);
    }
    if (n.startsWith('s:')) {
      const kids = layout.children.get(n) ?? [];
      const known = kids.filter((k) => ups.has(k));
      for (const k of known.length ? known : kids) {
        out.add(k);
      }
    }
  }
  return out;
}

// nodeCovers 判断地址的节点列表是否经过节点 id。
export function nodeCovers(ip: TopoIP, id: string, more?: string[]): boolean {
  if (id === 'internet' || id === 'gately') {
    return true;
  }
  if (id.startsWith('m:')) {
    return (more ?? []).some((c) => nodeCovers(ip, c));
  }
  if (id.startsWith('l:')) {
    const key = id.slice(2);
    return ip.nodes.some((n) => n.startsWith('s:' + key + '/') || n.startsWith('a:' + key + '/'));
  }
  if (id.startsWith('t:')) {
    return ip.nodes.includes('s:' + id.slice(2));
  }
  return ip.nodes.includes(id);
}

// activeAt 返回地址在节点 id 上进行中的请求数。
export function activeAt(ip: TopoIP, id: string, more?: string[]): number {
  if (id === 'internet' || id === 'gately') {
    return ip.active;
  }
  if (id.startsWith('m:')) {
    return (more ?? []).reduce((n, c) => n + activeAt(ip, c), 0);
  }
  const m = ip.activeAt ?? {};
  if (id.startsWith('l:')) {
    const prefix = 's:' + id.slice(2) + '/';
    return Object.entries(m).reduce((n, [k, v]) => (k.startsWith(prefix) ? n + v : n), 0);
  }
  if (id.startsWith('t:')) {
    return m['s:' + id.slice(2)] ?? 0;
  }
  return m[id] ?? 0;
}

// maskIP 把 IPv4 末段、IPv6 后 64 位替换为 *，用于截图分享时隐藏地址。
export function maskIP(ip: string): string {
  if (!ip || ip === '0.0.0.0' || ip === '::') {
    return ip;
  }
  if (ip.includes(':')) {
    const [head, tail] = ip.split('::');
    const h = head ? head.split(':') : [];
    let groups = h;
    if (tail !== undefined) {
      const tl = tail ? tail.split(':') : [];
      groups = [...h, ...Array(Math.max(0, 8 - h.length - tl.length)).fill('0'), ...tl];
    }
    return groups.slice(0, 4).join(':') + ':*';
  }
  const p = ip.split('.');
  if (p.length === 4) {
    return `${p[0]}.${p[1]}.${p[2]}.*`;
  }
  return ip;
}

const ipv4Re = /^\d{1,3}(\.\d{1,3}){3}$/;

// isIPLiteral 判断主机名是否为 IP 地址。
export function isIPLiteral(host: string): boolean {
  const h = host.replace(/^\[|\]$/g, '');
  return ipv4Re.test(h) || (h.includes(':') && /^[0-9a-fA-F:.]+$/.test(h));
}

// isValidIP 严格校验 IP 地址。
export function isValidIP(s: string): boolean {
  if (ipv4Re.test(s)) {
    return s.split('.').every((p) => Number(p) <= 255);
  }
  if (!s.includes(':') || !/^[0-9a-fA-F:.]+$/.test(s)) {
    return false;
  }
  try {
    new URL(`http://[${s}]/`);
    return true;
  } catch {
    return false;
  }
}

// maskText 打码自由文本中出现的 IP 地址。
export function maskText(text: string): string {
  return text
    .replace(/\[([0-9a-fA-F:.]+)\]/g, (m, ip: string) => (ip.includes(':') ? '[' + maskIP(ip) + ']' : m))
    .replace(/(?<![\w.:])\d{1,3}(?:\.\d{1,3}){3}(?![\w.])/g, (ip) => maskIP(ip))
    .replace(/(?<![\w:[])(?:[0-9a-fA-F]{0,4}:){2,7}[0-9a-fA-F]{0,4}(?![\w:\]])/g, (ip) => (isValidIP(ip) ? maskIP(ip) : ip));
}

// maskHost 打码地址中的 IP（主机名不变）。
export function maskHost(host: string): string {
  if (!isIPLiteral(host)) {
    return host;
  }
  if (host.startsWith('[')) {
    return '[' + maskIP(host.slice(1, -1)) + ']';
  }
  return maskIP(host);
}

// maskDomain 打码域名：保留第一段与顶级域，中间替换为 ***。
export function maskDomain(host: string): string {
  if (!host || isIPLiteral(host)) {
    return host;
  }
  const wild = host.startsWith('*.');
  const labels = (wild ? host.slice(2) : host).split('.');
  if (labels.length < 2) {
    return host;
  }
  const tld = labels[labels.length - 1];
  const masked = labels.length === 2 ? labels[0].slice(0, 1) + '***.' + tld : labels[0] + '.***.' + tld;
  return (wild ? '*.' : '') + masked;
}

const domainInText = /(?<![\w.*-])(?:[a-z0-9_-]*\*[a-z0-9_-]*\.)?(?:[a-z0-9_-]+\.)+[a-z][a-z0-9-]*[a-z](?![\w.-])/gi;

// maskDomainText 打码自由文本中出现的域名。
export function maskDomainText(text: string): string {
  return text.replace(domainInText, (d) => maskDomain(d));
}

// maskURL 打码后端地址中的 IP 主机。
export function maskURL(raw: string, names = false): string {
  const m = /^([a-z][a-z0-9+.-]*:\/\/)(\[[^\]]+\]|[^/:?#]+)(.*)$/i.exec(raw);
  if (!m) {
    return raw;
  }
  const host = isIPLiteral(m[2]) ? maskHost(m[2]) : names ? maskDomain(m[2]) : m[2];
  return m[1] + host + m[3];
}

// 打码方式：stars 以 * 遮住地址末段；alias 把整个值换成代号。
export type MaskStyle = 'stars' | 'alias';

// Aliases 为代号映射表：同一类别内同一原始值始终得到同一个编号。
export class Aliases {
  private maps = new Map<string, Map<string, string>>();

  token(prefix: string, raw: string): string {
    let m = this.maps.get(prefix);
    if (!m) {
      m = new Map();
      this.maps.set(prefix, m);
    }
    let v = m.get(raw);
    if (!v) {
      v = prefix + '-' + String(m.size + 1).padStart(2, '0');
      m.set(raw, v);
    }
    return v;
  }
}

// 常见的二级公共后缀
const SECOND_LEVEL = new Set(['com', 'net', 'org', 'gov', 'edu', 'ac', 'co', 'ne', 'or', 'go']);

// splitDomain 把域名拆成主机部分、可注册部分与公共后缀。
function splitDomain(host: string): [string, string, string] | undefined {
  const labels = host.split('.');
  if (labels.length < 2) {
    return undefined;
  }
  let n = 1;
  if (labels.length >= 3 && labels[labels.length - 1].length === 2 && SECOND_LEVEL.has(labels[labels.length - 2])) {
    n = 2;
  }
  const suffix = labels.slice(-n).join('.');
  const reg = labels[labels.length - n - 1];
  const sub = labels.slice(0, labels.length - n - 1).join('.');
  return [sub, reg, suffix];
}

// aliasIP 把 IP 换成 prefix 类别的代号。
export function aliasIP(a: Aliases, ip: string, prefix: string): string {
  if (!ip || ip === '0.0.0.0' || ip === '::') {
    return ip;
  }
  return a.token(prefix, ip.toLowerCase());
}

// aliasHost 换掉地址中的 IP。
export function aliasHost(a: Aliases, host: string, prefix: string): string {
  if (!isIPLiteral(host)) {
    return host;
  }
  if (host.startsWith('[')) {
    return '[' + aliasIP(a, host.slice(1, -1), prefix) + ']';
  }
  return aliasIP(a, host, prefix);
}

// aliasDomain 把域名换成代号。
export function aliasDomain(a: Aliases, host: string): string {
  if (!host || isIPLiteral(host) || host === '*') {
    return host;
  }
  const wild = host.startsWith('*.');
  const h = (wild ? host.slice(2) : host).toLowerCase();
  const parts = splitDomain(h);
  if (!parts) {
    return (wild ? '*.' : '') + a.token('host', h);
  }
  const [sub, reg, suffix] = parts;
  const base = a.token('domain', reg + '.' + suffix) + '.' + suffix;
  return (wild ? '*.' : '') + (sub ? a.token('host', h) + '.' : '') + base;
}

// aliasDomainText 换掉自由文本中出现的域名。
export function aliasDomainText(a: Aliases, text: string): string {
  return text.replace(domainInText, (d) => aliasDomain(a, d));
}

// aliasText 换掉自由文本中出现的 IP 地址。
export function aliasText(a: Aliases, text: string, prefix: string): string {
  return text
    .replace(/\[([0-9a-fA-F:.]+)\]/g, (m, ip: string) => (ip.includes(':') ? '[' + aliasIP(a, ip, prefix) + ']' : m))
    .replace(/(?<![\w.:])\d{1,3}(?:\.\d{1,3}){3}(?![\w.])/g, (ip) => aliasIP(a, ip, prefix))
    .replace(/(?<![\w:[])(?:[0-9a-fA-F]{0,4}:){2,7}[0-9a-fA-F]{0,4}(?![\w:\]])/g, (ip) => (isValidIP(ip) ? aliasIP(a, ip, prefix) : ip));
}

// aliasURL 把后端或跳转地址的主机换成代号。
export function aliasURL(a: Aliases, raw: string, prefix: string): string {
  const m = /^([a-z][a-z0-9+.-]*:\/\/)(\[[^\]]+\]|[^/:?#]+)(.*)$/i.exec(raw);
  if (!m) {
    return raw;
  }
  const host = isIPLiteral(m[2]) ? aliasHost(a, m[2], prefix) : aliasDomain(a, m[2]);
  return m[1] + host + m[3];
}

// formatQps 把每秒请求数格式化为简短字符串。
export function formatQps(n: number): string {
  if (!n) {
    return '0';
  }
  if (n < 0.1) {
    return '<0.1';
  }
  if (n < 10) {
    return n.toFixed(1);
  }
  if (n < 1000) {
    return Math.round(n).toString();
  }
  return (n / 1000).toFixed(n < 10000 ? 1 : 0) + 'k';
}

// formatCount 把累计数格式化为简短字符串。
export function formatCount(n: number): string {
  if (n < 10000) {
    return String(n);
  }
  if (n < 1e6) {
    return (n / 1000).toFixed(n < 1e5 ? 1 : 0) + 'k';
  }
  if (n < 1e9) {
    return (n / 1e6).toFixed(n < 1e7 ? 1 : 0) + 'M';
  }
  return (n / 1e9).toFixed(1) + 'G';
}

// flowLevel 把 QPS 映射为连线动画档位：0 不动，1 慢，2 中，3 快。
export function flowLevel(r: Rate): number {
  const q = Math.max(r.qps, r.qps60);
  if (q <= 0 && r.bps <= 0) {
    return 0;
  }
  if (q < 1) {
    return 1;
  }
  if (q < 20) {
    return 2;
  }
  return 3;
}

// edgeWidth 按 QPS 对数增长的连线宽度。
export function edgeWidth(r: Rate): number {
  return 1.4 + Math.min(4, Math.log10(1 + Math.max(r.qps, r.qps60)) * 1.6);
}
