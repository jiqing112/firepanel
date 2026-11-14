/**
 * 类型化 API 客户端：REST + WebSocket。
 */
import type { ForwardRule, Protocol } from './types';

export class ApiError extends Error {
  constructor(
    public status: number,
    message: string,
    public data?: Record<string, unknown>,
  ) {
    super(message);
  }
}

let token: string | null = localStorage.getItem('firepanel-token');

export function setToken(value: string | null) {
  token = value;
  if (value) localStorage.setItem('firepanel-token', value);
  else localStorage.removeItem('firepanel-token');
}

export function getToken() {
  return token;
}

// 会话失效回调：由 session store 注册（清空内存中的 session.user）。
// 不在这里直接 import session，避免 api.ts ↔ session store 循环依赖。
let onUnauthorized: (() => void) | null = null;

export function setUnauthorizedHandler(fn: (() => void) | null) {
  onUnauthorized = fn;
}

/** 是否是「登录/初始化」入口：这些接口的 401 是业务结果（密码错），不能当会话失效处理。 */
function isAuthEntry(path: string) {
  return path.startsWith('/auth/login') || path.startsWith('/setup');
}

function handleUnauthorized() {
  setToken(null);
  onUnauthorized?.();
  if (!location.hash.startsWith('#/login')) location.hash = '#/login';
}

async function request<T>(path: string, init: RequestInit = {}): Promise<T> {
  const res = await fetch(`/api/v1${path}`, {
    ...init,
    headers: {
      'Content-Type': 'application/json',
      // JWT 走独立头：与整站 Basic Auth（浏览器自动附带的 Authorization: Basic）共存
      ...(token ? { 'X-Panel-Token': token } : {}),
      ...init.headers,
    },
  });
  const body = await res.json().catch(() => ({}));
  if (res.status === 401) {
    if (isAuthEntry(path)) {
      // 密码错误等业务 401：保留后端原始提示，不清会话、不跳转
      throw new ApiError(401, (body as { error?: string }).error ?? '用户名或密码错误', body);
    }
    handleUnauthorized();
    throw new ApiError(401, (body as { error?: string }).error ?? '未登录', body);
  }
  if (!res.ok) {
    throw new ApiError(res.status, (body as { error?: string }).error ?? `请求失败（${res.status}）`, body);
  }
  return body as T;
}

/* ------------------------- 类型 ------------------------- */

export interface SyncResult {
  applied: boolean;
  danger: boolean;
  token?: string;
  reason?: string;
  delay_sec?: number;
  confirm_sec?: number;
}

export interface User {
  id: number;
  username: string;
  role: 'admin' | 'viewer';
  created_at: string;
}

export interface SystemInfo {
  hostname: string;
  distro: string;
  kernel: string;
  arch: string;
  uptime: string;
  backend: string;
  drift: 'clean' | 'drifted' | 'unknown';
  needs_setup: boolean;
  ssh_ports: number[];
  detected: {
    IPTablesVariant: string;
    NftVersion: string;
    FirewalldActive: boolean;
    UfwActive: boolean;
    DockerPresent: boolean;
  };
}

export interface KernelRule {
  chain: string;
  tag: string;
  spec: string;
  counter: { packets: number; bytes: number };
}

export interface FirewallSnapshot {
  backend: string;
  chains: { name: string; exists: boolean; hooked: boolean; reference: string }[];
  rules: KernelRule[];
}

export interface DriftReport {
  drifted: boolean;
  missing: KernelRule[];
  extra: KernelRule[];
  modified: { tag: string; expected: string; actual: string }[];
}

export interface IPListEntry {
  id: number;
  list_type: 'black' | 'white';
  ip_or_cidr: string;
  group_tag: string;
  source: string;
  country: string;
  expires_at: string | null;
  remark: string;
  created_at: string;
}

export interface DashboardSummary {
  conn_count: number;
  rx_bps: number;
  tx_bps: number;
  traffic_today: number;
  samples: { ts: number; conn_count: number; rx_bps: number; tx_bps: number }[];
  hit_ranks: { rule_id: number; name: string; hits: number }[];
  backend: string;
}

export interface RateLimitRule {
  id: number;
  name: string;
  proto: 'tcp' | 'udp' | 'both';
  port: string;
  per_src: boolean;
  rate: number;
  rate_unit: 'second' | 'minute' | 'hour';
  burst: number;
  conn_limit: number;
  enabled: boolean;
  remark: string;
  created_at: string;
}

export interface NATRule {
  id: number;
  name: string;
  type: 'SNAT' | 'MASQ';
  src_cidr: string;
  out_iface: string;
  to_addr: string;
  enabled: boolean;
  remark: string;
  created_at: string;
}

export interface PortAllowRule {
  id: number;
  name: string;
  proto: 'tcp' | 'udp' | 'both';
  port: string;
  src_cidr: string;
  enabled: boolean;
  remark: string;
  created_at: string;
}

export interface ScheduledTask {
  id: number;
  name: string;
  cron_expr: string;
  action: 'forward_enable' | 'forward_disable' | 'iplist_expire' | 'sync';
  target_id: number;
  enabled: boolean;
  last_run: string | null;
  next_run: string | null;
  remark: string;
  created_at: string;
}

export interface AlertConfig {
  id: number;
  channel: 'telegram' | 'webhook' | 'smtp';
  config_json: string;
  events_json: string;
  enabled: boolean;
  created_at: string;
}

export interface JailConfig {
  id: number;
  name: string;
  source_type: 'file' | 'journald' | 'audit';
  log_path: string;
  unit: string;
  match_regex: string;
  threshold: number;
  find_time: number;
  ban_time: number;
  ignore_cidrs: string;
  enabled: boolean;
  remark: string;
  created_at: string;
}

export interface AuditEntry {
  id: number;
  username: string;
  action: string;
  target: string;
  detail: string;
  ip: string;
  created_at: string;
}

export interface WSHealth {
  enable: boolean;
  mode: 'http' | 'tcp';
  path: string;
  interval: number;
  timeout: number;
  fails: number;
  passes: number;
}

export interface WSLocation {
  prefix: string;
  targets: string[];
  strip_prefix: boolean;
  request_headers: string[];
}

export interface WSTimeouts {
  connect: number;
  tls_handshake: number;
  response_header: number;
}

export interface WSRateLimit {
  enable: boolean;
  rate: number;
  burst: number;
  max_conns: number;
}

/** 站点：字段与后端引擎模型一致（cert_pem/key_pem 仅提交时携带，不回读）。 */
export interface WSSite {
  id: number;
  listener_id: number;
  name: string;
  domains: string[];
  type: 'proxy' | 'redirect' | 'static' | 'text' | 'close';
  enabled: boolean;
  default: boolean;
  remark: string;
  tls_mode: 'auto' | 'manual' | 'none';
  challenge: '' | 'auto' | 'http' | 'alpn' | 'dns';
  cert_pem?: string;
  key_pem?: string;
  cert_status: 'none' | 'pending' | 'ok' | 'error';
  cert_error: string;
  cert_expires_at: string | null;
  created_at: string;
  targets: string[];
  lb_policy: '' | 'random' | 'ip_hash' | 'least_conn';
  health: WSHealth;
  locations: WSLocation[];
  use_target_host: boolean;
  forwarded_headers: boolean;
  insecure_skip_verify: boolean;
  timeouts: WSTimeouts;
  redirect_to: string;
  redirect_code: number;
  keep_path: boolean;
  root: string;
  list_dir: boolean;
  text: string;
  content_type: string;
  status_code: number;
  ip_mode: '' | 'whitelist' | 'blacklist';
  ip_list: string[];
  compress: boolean;
  rate_limit: WSRateLimit;
  request_headers: string[];
  response_headers: string[];
}

export type WSSiteInput = Partial<WSSite> & { domains: string[] };

export interface UpstreamStatus {
  url: string;
  weight: number;
  backup: boolean;
  healthy: boolean;
  conns: number;
  scope?: string;
  match?: string;
  requests: number;
  bytes: number;
}

export interface WSSiteStatus {
  id: number;
  listener: number;
  name: string;
  type: string;
  domains: string[];
  default: boolean;
  requests: number;
  active: number;
  errors: number;
  blocked: number;
  qps: number;
  rate60: number[];
  cert?: string;
  upstreams?: UpstreamStatus[];
}

export interface WSListenerStatus {
  id: number;
  name: string;
  port: number;
  tls: boolean;
  http_port: number;
  http_port_mode?: string;
  plain_http?: string;
  running: boolean;
  error?: string;
}

export interface WSStatus {
  running: boolean;
  started_at: string | null;
  listeners: WSListenerStatus[];
  sites: WSSiteStatus[];
}

/** 监听：config 与后端引擎的监听字段一致。 */
export interface WSListenerConfig {
  listen_ip: string;
  port: number;
  tls: boolean;
  min_tls: string;
  plain_http: string;
  http_port: number;
  http_port_mode: string;
}

export interface WSListener {
  id: number;
  name: string;
  enabled: boolean;
  config: WSListenerConfig;
  remark: string;
  created_at: string;
  site_count?: number;
}

export type WSListenerInput = Partial<WSListener> & { config: Partial<WSListenerConfig> & { port: number } };

export interface PortProbe {
  port: number;
  bindable: boolean;
  occupier: string;
  detail: string;
}

export interface ProxyEnv {
  http: PortProbe;
  https: PortProbe;
  listen_http: string;
  listen_https: string;
  http_mode: string;
  recommended: { http_port: number; https_port: number; challenge: string; note: string };
}

export interface DNSProviderConfig {
  provider: '' | 'cloudflare' | 'alidns' | 'dnspod';
  api_token: string;
  secret_key: string;
}

export interface BasicAuthInfo {
  enabled: boolean;
  username: string;
  password_set: boolean;
  source: '' | 'file' | 'config';
}

export interface ListenPort {
  proto: 'tcp' | 'udp';
  port: number;
  address: string;
  process: string;
  pid: string;
  docker: boolean;
}

export interface PortRecord {
  id: number;
  name: string;
  proto: 'tcp' | 'udp' | 'both';
  port: number;
  group_tag: string;
  remark: string;
  created_at: string;
}

export interface IfaceRate {
  name: string;
  rx_bps: number;
  tx_bps: number;
  rx_total: number;
  tx_total: number;
}

export interface IfacePoint {
  ts: number;
  rx_bps: number;
  tx_bps: number;
}

export interface ConnRate {
  proto: 'tcp' | 'udp';
  local: string;
  remote: string;
  process: string;
  pid: string;
  count: number;
  remotes: number;
  rx_bps: number;
  tx_bps: number;
  rx_total: number;
  tx_total: number;
}

export interface TrafficOverview {
  interfaces: IfaceRate[];
  window: Record<string, IfacePoint[]>;
  conns: ConnRate[];
  conns_error?: string;
  ts: number;
  rx_bps: number;
  tx_bps: number;
}

/* ------------------------- API 方法 ------------------------- */

export const api = {
  get: <T>(path: string) => request<T>(path),
  post: <T>(path: string, body?: unknown) =>
    request<T>(path, { method: 'POST', body: body === undefined ? undefined : JSON.stringify(body) }),
  put: <T>(path: string, body?: unknown) =>
    request<T>(path, { method: 'PUT', body: body === undefined ? undefined : JSON.stringify(body) }),
  patch: <T>(path: string, body?: unknown) =>
    request<T>(path, { method: 'PATCH', body: body === undefined ? undefined : JSON.stringify(body) }),
  delete: <T>(path: string) => request<T>(path, { method: 'DELETE' }),
};

/**
 * 带鉴权头下载文件（备份/导出用）。
 * 走 fetch + Blob 而非直接 <a href>：确保携带 X-Panel-Token 头（否则 401），
 * 同时避免把 token 放进 URL 查询串。失败时抛出 ApiError 供调用方提示。
 */
export async function downloadFile(path: string, filename: string): Promise<void> {
  const res = await fetch(`/api/v1${path}`, {
    headers: token ? { 'X-Panel-Token': token } : {},
  });
  if (res.status === 401) {
    const body = await res.json().catch(() => ({}));
    handleUnauthorized();
    throw new ApiError(401, (body as { error?: string }).error ?? '未登录', body);
  }
  if (!res.ok) {
    const body = await res.json().catch(() => ({}));
    throw new ApiError(res.status, (body as { error?: string }).error ?? `下载失败（${res.status}）`, body);
  }
  const blob = await res.blob();
  const url = URL.createObjectURL(blob);
  const a = document.createElement('a');
  a.href = url;
  a.download = filename;
  document.body.appendChild(a);
  a.click();
  a.remove();
  URL.revokeObjectURL(url);
}

export const backend = {
  needsSetup: () => api.get<{ needs_setup: boolean }>('/bootstrap').then((r) => r.needs_setup).catch(() => false),

  setup: (username: string, password: string) =>
    api.post<{ token: string; user: User }>('/setup', { username, password }),

  login: (username: string, password: string) =>
    api.post<{ token: string; user: User }>('/auth/login', { username, password }),

  me: () => api.get<{ uid: number; username: string; role: string }>('/me'),

  systemInfo: () => api.get<SystemInfo>('/system/info'),
  interfaces: () => api.get<{ items: { name: string; addrs: string[] }[] }>('/system/interfaces'),

  dashboard: () => api.get<DashboardSummary>('/dashboard/summary'),

  /* 转发 */
  forwards: () => api.get<{ items: ForwardRule[] }>('/forwards'),
  createForward: (r: Partial<ForwardRule>) => api.post<{ item: ForwardRule; sync: SyncResult }>('/forwards', r),
  updateForward: (id: number, r: Partial<ForwardRule>) =>
    api.put<{ item: ForwardRule; sync: SyncResult }>(`/forwards/${id}`, r),
  toggleForward: (id: number, enabled: boolean) =>
    api.patch<{ ok: boolean; sync: SyncResult }>(`/forwards/${id}/enabled`, { enabled }),
  deleteForward: (id: number) => api.delete<{ ok: boolean; sync: SyncResult }>(`/forwards/${id}`),

  /* IP 名单 */
  ipLists: (type: '' | 'black' | 'white' = '') =>
    api.get<{ items: IPListEntry[] }>(`/ip-lists${type ? `?type=${type}` : ''}`),
  createIPList: (e: Partial<IPListEntry>) => api.post<{ item: IPListEntry; sync: SyncResult }>('/ip-lists', e),
  updateIPList: (id: number, e: Partial<IPListEntry>) => api.put<{ item: IPListEntry }>(`/ip-lists/${id}`, e),
  deleteIPList: (id: number) => api.delete<{ ok: boolean; sync: SyncResult }>(`/ip-lists/${id}`),
  importIPLists: (listType: string, format: 'text' | 'json', content: string) =>
    api.post<{ imported: number; skipped: number; sync: SyncResult }>('/ip-lists/import', {
      list_type: listType,
      format,
      content,
    }),

  /* 防火墙 / 系统 */
  firewallRules: () => api.get<{ snapshot: FirewallSnapshot }>('/firewall/rules'),
  drift: () => api.get<{ report: DriftReport | null }>('/system/drift'),
  checkDrift: () => api.post<{ report: DriftReport }>('/system/drift/check'),
  forceSync: () => api.post<{ sync: SyncResult }>('/system/sync'),
  confirmDanger: (token: string, stage: number) =>
    api.post<{ sync: SyncResult }>('/system/danger/confirm', { token, stage }),
  cancelDanger: (token: string) => api.post<{ cancelled: boolean }>('/system/danger/cancel', { token }),

  /* Web 服务 */
  wsSites: () => api.get<{ items: WSSite[]; running: boolean }>('/ws/sites'),
  wsListeners: () => api.get<{ items: WSListener[]; running: boolean }>('/ws/listeners'),
  createWSListener: (l: WSListenerInput) => api.post<{ id: number }>('/ws/listeners', l),
  updateWSListener: (id: number, l: WSListenerInput) => api.put<{ ok: boolean }>(`/ws/listeners/${id}`, l),
  setWSListenerEnabled: (id: number, enabled: boolean) =>
    api.patch<{ ok: boolean }>(`/ws/listeners/${id}/enabled`, { enabled }),
  deleteWSListener: (id: number) => api.delete<{ ok: boolean }>(`/ws/listeners/${id}`),
  wsTopology: (focus = '', ip = '') => {
    const q = new URLSearchParams();
    if (focus) q.set('focus', focus);
    if (ip) q.set('ip', ip);
    return api.get<{ view: import('./topology').TopologyView }>(`/ws/topology?${q.toString()}`);
  },
  createWSSite: (d: WSSiteInput) => api.post<{ id: number }>('/ws/sites', d),
  updateWSSite: (id: number, d: WSSiteInput) => api.put<{ ok: boolean }>(`/ws/sites/${id}`, d),
  setWSSiteEnabled: (id: number, enabled: boolean) =>
    api.patch<{ ok: boolean }>(`/ws/sites/${id}/enabled`, { enabled }),
  deleteWSSite: (id: number) => api.delete<{ ok: boolean }>(`/ws/sites/${id}`),
  wsStatus: () => api.get<{ status: WSStatus }>('/ws/status'),
  wsEnv: () => api.get<{ env: ProxyEnv }>('/ws/env'),
  dnsConfig: () => api.get<{ config: DNSProviderConfig }>('/ws/dns-config'),
  setDNSConfig: (c: DNSProviderConfig) => api.post<{ config: DNSProviderConfig }>('/ws/dns-config', c),

  audit: (limit = 200) => api.get<{ items: AuditEntry[] }>(`/audit?limit=${limit}`),

  /* 限速 */
  rateLimits: () => api.get<{ items: RateLimitRule[] }>('/rate-limits'),
  createRateLimit: (r: Partial<RateLimitRule>) => api.post<{ item: RateLimitRule; sync: SyncResult }>('/rate-limits', r),
  updateRateLimit: (id: number, r: Partial<RateLimitRule>) => api.put<{ item: RateLimitRule; sync: SyncResult }>(`/rate-limits/${id}`, r),
  deleteRateLimit: (id: number) => api.delete<{ ok: boolean; sync: SyncResult }>(`/rate-limits/${id}`),

  /* NAT */
  natRules: () => api.get<{ items: NATRule[]; ip_forward: boolean }>('/nat-rules'),
  enableIPForward: () =>
    api.post<{ ip_forward: boolean; persisted: boolean; warning?: string }>('/system/ip-forward/enable'),
  createNAT: (r: Partial<NATRule>) => api.post<{ item: NATRule; sync: SyncResult }>('/nat-rules', r),
  updateNAT: (id: number, r: Partial<NATRule>) => api.put<{ item: NATRule; sync: SyncResult }>(`/nat-rules/${id}`, r),
  deleteNAT: (id: number) => api.delete<{ ok: boolean; sync: SyncResult }>(`/nat-rules/${id}`),

  /* 入站放行（FPANEL_INPUT ACCEPT） */
  portAllow: () => api.get<{ items: PortAllowRule[] }>('/port-allow'),
  createPortAllow: (r: Partial<PortAllowRule>) =>
    api.post<{ item: PortAllowRule; sync: SyncResult }>('/port-allow', r),
  updatePortAllow: (id: number, r: Partial<PortAllowRule>) =>
    api.put<{ item: PortAllowRule; sync: SyncResult }>(`/port-allow/${id}`, r),
  togglePortAllow: (id: number, enabled: boolean) =>
    api.patch<{ sync: SyncResult }>(`/port-allow/${id}/enabled`, { enabled }),
  deletePortAllow: (id: number) => api.delete<{ sync: SyncResult }>(`/port-allow/${id}`),

  /* 端口（监听扫描 + 记录台账） */
  listeningPorts: () => api.get<{ items: ListenPort[] }>('/ports/listening'),
  trafficOverview: () => api.get<TrafficOverview>('/traffic/overview'),
  portRecords: () => api.get<{ items: PortRecord[] }>('/port-records'),
  createPortRecord: (r: Partial<PortRecord>) => api.post<{ item: PortRecord }>('/port-records', r),
  updatePortRecord: (id: number, r: Partial<PortRecord>) => api.put<{ item: PortRecord }>(`/port-records/${id}`, r),
  deletePortRecord: (id: number) => api.delete<{ ok: boolean }>(`/port-records/${id}`),

  /* 定时任务 */
  tasks: () => api.get<{ items: ScheduledTask[] }>('/tasks'),
  createTask: (t: Partial<ScheduledTask>) => api.post<{ item: ScheduledTask }>('/tasks', t),
  updateTask: (id: number, t: Partial<ScheduledTask>) => api.put<{ item: ScheduledTask }>(`/tasks/${id}`, t),
  deleteTask: (id: number) => api.delete<{ ok: boolean }>(`/tasks/${id}`),

  /* 告警 */
  alerts: () => api.get<{ items: AlertConfig[] }>('/alerts'),
  upsertAlert: (a: { channel: string; config: unknown; events: string[]; enabled: boolean }) =>
    api.post<{ item: AlertConfig }>('/alerts', a),
  deleteAlert: (id: number) => api.delete<{ ok: boolean }>(`/alerts/${id}`),
  testAlert: (a: { channel: string; config: unknown }) =>
    api.post<{ ok: boolean }>('/alerts/test', a),

  /* 防爆破 */
  jails: () =>
    api.get<{ items: (JailConfig & { hits: Record<string, number> })[]; banned: IPListEntry[] }>('/jails'),
  createJail: (j: Partial<JailConfig>) => api.post<{ item: JailConfig }>('/jails', j),
  updateJail: (id: number, j: Partial<JailConfig>) => api.put<{ item: JailConfig }>(`/jails/${id}`, j),
  deleteJail: (id: number) => api.delete<{ ok: boolean }>(`/jails/${id}`),
  unbanIP: (ipListId: number) => api.delete<{ ok: boolean }>(`/jails/bans/${ipListId}`),

  /* 自签证书 */
  selfSignCert: (hosts: string) =>
    api.post<{ cert_pem: string; key_pem: string; expires: string }>('/ws/selfsign', { hosts }),
  restore: (payload: unknown) =>
    api.post<{ sync: SyncResult; forwards: number; ip_lists: number }>('/system/restore', payload),
  users: () => api.get<{ items: User[] }>('/users'),
  basicAuth: () => api.get<BasicAuthInfo>('/settings/basicauth'),
  setBasicAuth: (p: { enabled: boolean; username: string; password: string }) =>
    api.post<{ ok: boolean; enabled: boolean }>('/settings/basicauth', p),
  panelPorts: () =>
    api.get<{ listen: string; https_port: string; https_enabled: boolean; source_listen: string; source_https: string }>('/settings/ports'),
  setPanelPorts: (p: { listen: string; https_port: string }) => api.post<{ ok: boolean; need_restart: boolean }>('/settings/ports', p),
  restartPanel: () => api.post<{ ok: boolean; message: string }>('/system/restart'),
  createUser: (u: { username: string; password: string; role: string }) =>
    api.post<{ item: User }>('/users', u),
  deleteUser: (id: number) => api.delete<{ ok: boolean }>(`/users/${id}`),
};

/* ------------------------- WebSocket ------------------------- */

export type WSTopic = 'stats' | 'events' | 'logs';
type WSHandler = (topic: string, data: unknown) => void;

let ws: WebSocket | null = null;
let wsReconnect = 0;
const wsHandlers = new Set<WSHandler>();

export function onWSMessage(fn: WSHandler): () => void {
  wsHandlers.add(fn);
  return () => wsHandlers.delete(fn);
}

export function wsConnected() {
  return ws !== null && ws.readyState === WebSocket.OPEN;
}

export function connectWS() {
  if (ws && (ws.readyState === WebSocket.OPEN || ws.readyState === WebSocket.CONNECTING)) return;
  const proto = location.protocol === 'https:' ? 'wss' : 'ws';
  const t = getToken();
  if (!t) return;
  try {
    // 凭据走 Sec-WebSocket-Protocol 子协议（"fp.<JWT>"），不放进 URL：
    // 查询串会被反代/网关写进访问日志，等于把会话令牌留痕。
    ws = new WebSocket(`${proto}://${location.host}/api/ws`, [`fp.${t}`]);
  } catch {
    scheduleReconnect();
    return;
  }
  ws.onopen = () => {
    wsReconnect = 0;
  };
  ws.onmessage = (ev) => {
    try {
      const msg = JSON.parse(ev.data) as { topic: string; data: unknown };
      wsHandlers.forEach((fn) => fn(msg.topic, msg.data));
    } catch {
      /* 忽略坏帧 */
    }
  };
  ws.onclose = (ev) => {
    ws = null;
    // 1008/403 等鉴权失败不再重连（token 无效，等用户重新登录）
    if (ev.code === 1008 || ev.code === 1006) {
      if (wsReconnect > 3) return;
    }
    scheduleReconnect();
  };
  ws.onerror = () => ws?.close();
}

function scheduleReconnect() {
  if (wsReconnect > 10) return;
  const delay = Math.min(1000 * 2 ** wsReconnect, 15000);
  wsReconnect++;
  setTimeout(() => {
    if (getToken()) connectWS();
  }, delay);
}

export type { ForwardRule, Protocol } from './types';
