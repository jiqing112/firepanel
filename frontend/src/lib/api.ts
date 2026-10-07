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
  if (res.status === 401) {
    setToken(null);
    if (!location.hash.startsWith('#/login')) location.hash = '#/login';
    throw new ApiError(401, '未登录');
  }
  const body = await res.json().catch(() => ({}));
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

export interface ProxyRoute {
  id: number;
  domain_id: number;
  path_match: string;
  upstream_url: string;
  ws_enabled: boolean;
  health_path: string;
  enabled: boolean;
  health_status: 'unknown' | 'ok' | 'down';
  health_checked_at: string | null;
  created_at: string;
}

export interface ProxyDomain {
  id: number;
  domain: string;
  enabled: boolean;
  tls_mode: 'auto' | 'manual' | 'none';
  challenge: '' | 'auto' | 'http' | 'alpn' | 'dns';
  caddy_opts: string;
  cert_status: 'none' | 'pending' | 'ok' | 'error';
  cert_error: string;
  cert_expires_at: string | null;
  remark: string;
  created_at: string;
  routes: ProxyRoute[];
}

export interface CaddySiteOpts {
  encode: boolean;
  security_headers: boolean;
  log_access: boolean;
}

export interface CaddyGlobalSettings {
  email: string;
  debug: boolean;
}

export interface PortProbe {
  port: number;
  bindable: boolean;
  occupier: string;
  detail: string;
}

export interface ProxyEnv {
  http: PortProbe;
  https: PortProbe;
  engine: string;
  listen_http: string;
  listen_https: string;
  recommended: { http_port: number; https_port: number; challenge: string; note: string };
  caddy_installed: boolean;
  caddy_version: string;
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
  source: '' | 'settings' | 'config';
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

  /* 反向代理 */
  proxyDomains: () => api.get<{ items: ProxyDomain[]; running: boolean }>('/proxy/domains'),
  createProxyDomain: (d: Partial<ProxyDomain> & { domain: string }) =>
    api.post<{ item: ProxyDomain }>('/proxy/domains', d),
  updateProxyDomain: (id: number, d: Partial<ProxyDomain>) => api.put<{ item: ProxyDomain }>(`/proxy/domains/${id}`, d),
  deleteProxyDomain: (id: number) => api.delete<{ ok: boolean }>(`/proxy/domains/${id}`),
  createProxyRoute: (domainId: number, r: Partial<ProxyRoute>) =>
    api.post<{ item: ProxyRoute }>(`/proxy/domains/${domainId}/routes`, r),
  updateProxyRoute: (id: number, r: Partial<ProxyRoute>) => api.put<{ item: ProxyRoute }>(`/proxy/routes/${id}`, r),
  deleteProxyRoute: (id: number) => api.delete<{ ok: boolean }>(`/proxy/routes/${id}`),
  proxyEnv: () => api.get<{ env: ProxyEnv }>('/proxy/env'),
  setProxyEngine: (p: { engine: string; http_addr: string; https_addr: string }) =>
    api.post<{ ok: boolean }>('/proxy/engine', p),
  installCaddy: () => api.post<{ version: string }>('/proxy/caddy/install'),
  caddyfile: () => api.get<{ caddyfile: string; custom: string; path: string }>('/proxy/caddyfile'),
  saveCaddyfile: (content: string) => api.post<{ ok: boolean }>('/proxy/caddyfile', { content }),
  setCaddyCustom: (custom: string) => api.post<{ ok: boolean }>('/proxy/caddy-custom', { custom }),
  caddyGlobal: () => api.get<{ config: CaddyGlobalSettings }>('/proxy/caddy-global'),
  setCaddyGlobal: (g: CaddyGlobalSettings) => api.post<{ ok: boolean }>('/proxy/caddy-global', g),
  dnsConfig: () => api.get<{ config: DNSProviderConfig }>('/proxy/dns-config'),
  setDNSConfig: (c: DNSProviderConfig) => api.post<{ config: DNSProviderConfig }>('/proxy/dns-config', c),

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
    api.post<{ cert_pem: string; key_pem: string; expires: string }>('/proxy/selfsign', { hosts }),
  restore: (payload: unknown) =>
    api.post<{ sync: SyncResult; forwards: number; ip_lists: number }>('/system/restore', payload),
  users: () => api.get<{ items: User[] }>('/users'),
  basicAuth: () => api.get<BasicAuthInfo>('/settings/basicauth'),
  setBasicAuth: (p: { enabled: boolean; username: string; password: string }) =>
    api.post<{ ok: boolean; enabled: boolean }>('/settings/basicauth', p),
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
    ws = new WebSocket(`${proto}://${location.host}/api/ws?token=${encodeURIComponent(t)}`);
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
  ws.onclose = () => {
    ws = null;
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
