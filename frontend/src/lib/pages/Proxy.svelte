<script lang="ts">
  // Web 服务页：监听器卡片 + 站点列表 + 编辑对话框。1:1 复刻自 gately web/src/views/Web.vue。
  // 复刻范围：监听器卡片（标签/启停/站点行/域名链接）、监听器对话框、站点对话框（基础/安全/流量/头）。
  // 未复刻：排序拖拽/批量/Nginx 导入/站点级认证与 WAF（引擎未含）。
  import {
    Plus, Search, Server, Trash2, Pencil, ChevronsDownUp, ChevronsUpDown,
    Workflow, ListFilter, ShieldCheck, ShieldAlert, CopyPlus, X, MoreHorizontal, ExternalLink,
  } from '@lucide/svelte';
  import { Card } from '$lib/components/ui/card';
  import { Button } from '$lib/components/ui/button';
  import { Input } from '$lib/components/ui/input';
  import { Badge } from '$lib/components/ui/badge';
  import { Label } from '$lib/components/ui/label';
  import { Switch } from '$lib/components/ui/switch';
  import * as Dialog from '$lib/components/ui/dialog';
  import * as Select from '$lib/components/ui/select';
  import * as Tabs from '$lib/components/ui/tabs';
  import * as DropdownMenu from '$lib/components/ui/dropdown-menu';
  import * as Tooltip from '$lib/components/ui/tooltip';
  import { toast } from 'svelte-sonner';
  import EmptyState from '$lib/components/common/EmptyState.svelte';
  import { backend, onWSMessage, type WSSite, type WSSiteInput, type WSListener, type WSListenerInput, type WSStatus, type UpstreamStatus } from '$lib/api';
  import { fmtCompact } from '$lib/utils';

  type SiteType = WSSite['type'];

  let listeners = $state<WSListener[]>([]);
  let items = $state<WSSite[]>([]);
  let status = $state<WSStatus | null>(null);
  let loading = $state(true);
  let saving = $state(false);
  let filterText = $state('');
  let compact = $state(false);
  let collapsedListeners = $state<Set<string>>(new Set());
  let confirmingDelete = $state('');

  /* 状态快照轮询（统计列 + 后端健康） */
  const live = $state<Record<number, { qps: number; requests: number; active: number; errors: number; upstreams: UpstreamStatus[] }>>({});

  async function load() {
    loading = true;
    try {
      const [ls, sites] = await Promise.all([backend.wsListeners(), backend.wsSites()]);
      listeners = ls.items;
      items = sites.items;
    } catch (e) {
      toast.error(e instanceof Error ? e.message : String(e));
    } finally {
      loading = false;
    }
  }

  async function loadStatus() {
    try {
      const r = await backend.wsStatus();
      status = r.status;
      const next: typeof live = {};
      for (const s of r.status.sites) {
        next[s.id] = { qps: s.qps, requests: s.requests, active: s.active, errors: s.errors, upstreams: [] };
      }
      for (const k of Object.keys(live)) if (!(Number(k) in next)) delete live[Number(k)];
      Object.assign(live, next);
    } catch { /* 轮询失败不打断页面 */ }
  }

  $effect(() => {
    load();
    loadStatus();
    // 编辑器或对话框打开时暂停轮询：数字跳动会打断填写
    if (listenerOpen || siteOpen) return;
    const timer = setInterval(loadStatus, 5000);
    const off = onWSMessage((topic, payload) => {
      if (topic !== 'events') return;
      const t = String((payload as { type?: string }).type ?? '');
      if (t.startsWith('proxy') || t.startsWith('health')) loadStatus();
    });
    return () => { clearInterval(timer); off(); };
  });

  const running = $derived(status?.running ?? false);

  function liveOf(id: number) { return live[id]; }
  function healthOf(id: number, url: string): boolean | undefined {
    const ups = live[id]?.upstreams;
    if (!ups) return undefined;
    const u = ups.find((x) => x.url === url);
    return u ? u.healthy : undefined;
  }
  function sitesOf(l: WSListener): WSSite[] {
    return items.filter((s) => s.listener_id === l.id);
  }
  function downCount(s: WSSite): number {
    let n = 0;
    for (const t of s.targets) {
      const url = t.trim().split(/\s+/)[0];
      if (healthOf(s.id, url) === false) n++;
    }
    return n;
  }
  function parseTarget(t: string): { url: string; weight: number; backup: boolean } {
    const parts = t.trim().split(/\s+/);
    let weight = 1, backup = false;
    for (const p of parts.slice(1)) {
      if (p === 'backup') backup = true;
      else if (p.startsWith('weight=')) weight = Math.max(1, parseInt(p.slice(7), 10) || 1);
    }
    return { url: parts[0] ?? '', weight, backup };
  }
  function summary(s: WSSite): string {
    switch (s.type) {
      case 'proxy': return s.targets.map((t) => parseTarget(t).url).join(' · ') || '—';
      case 'redirect': return s.redirect_to;
      case 'static': return s.root;
      case 'text': return (s.text || '—').slice(0, 60);
      case 'close': return '连接直接断开';
    }
    return '';
  }
  const typeLabels: Record<SiteType, string> = { proxy: '代理', redirect: '重定向', static: '静态', text: '文本', close: '断开' };
  function listenerAddr(l: WSListener) {
    const host = l.config.listen_ip || '0.0.0.0';
    return (host.includes(':') ? `[${host}]` : host) + ':' + l.config.port;
  }
  function listenerError(l: WSListener): string {
    const st = status?.listeners.find((x) => x.id === l.id);
    return st?.error ?? '';
  }
  function listenerRunning(l: WSListener): boolean {
    const st = status?.listeners.find((x) => x.id === l.id);
    return st?.running ?? false;
  }

  /* ---------------- 监听器对话框 ---------------- */
  let listenerOpen = $state(false);
  let editingListenerId = $state<number | null>(null);
  let lform = $state({ name: '', listen_ip: '', port: 443, tls: true, min_tls: '1.2', plain_http: '', http_port: 80, http_port_mode: 'serve', remark: '', enabled: true });
  let copyListenerId = $state<number | null>(null);

  function openListener(l?: WSListener, copy = false) {
    copyListenerId = copy && l ? l.id : null;
    if (l && !copy) {
      editingListenerId = l.id;
      lform = { name: l.name, listen_ip: l.config.listen_ip, port: l.config.port, tls: l.config.tls, min_tls: l.config.min_tls || '1.2', plain_http: l.config.plain_http, http_port: l.config.http_port, http_port_mode: l.config.http_port_mode || (l.config.tls ? 'redirect' : 'serve'), remark: l.remark, enabled: l.enabled };
    } else {
      editingListenerId = null;
      lform = { name: '', listen_ip: '', port: 8443, tls: true, min_tls: '1.2', plain_http: '', http_port: 8080, http_port_mode: 'redirect', remark: '', enabled: true };
    }
    listenerOpen = true;
  }

  async function saveListener() {
    if (saving) return;
    saving = true;
    try {
      const payload: WSListenerInput = {
        name: lform.name.trim(),
        config: {
          listen_ip: lform.listen_ip.trim(),
          port: Number(lform.port) || 0,
          tls: lform.tls,
          min_tls: lform.tls ? lform.min_tls : '',
          plain_http: lform.tls ? lform.plain_http : '',
          http_port: Number(lform.http_port) || 0,
          http_port_mode: Number(lform.http_port) > 0 ? lform.http_port_mode : '',
        },
        remark: lform.remark,
        enabled: lform.enabled,
      };
      if (editingListenerId === null) {
        const created = await backend.createWSListener(payload);
        if (copyListenerId !== null) {
          const srcListener = listeners.find((x) => x.id === copyListenerId);
          if (srcListener) {
            for (const s of sitesOf(srcListener)) {
              const { id: _id, created_at: _c, cert_expires_at: _e, ...rest } = s;
              await backend.createWSSite({ ...rest, listener_id: created.id, enabled: false, default: false } as WSSiteInput);
            }
            toast.success('监听与站点已复制（站点已停用，确认后启用）');
          }
        } else {
          toast.success('监听已添加');
        }
      } else {
        await backend.updateWSListener(editingListenerId, payload);
        toast.success('监听已保存');
      }
      listenerOpen = false;
      await load();
    } catch (e) {
      toast.error(e instanceof Error ? e.message : String(e));
    } finally {
      saving = false;
    }
  }

  async function toggleListener(l: WSListener, enabled: boolean) {
    try {
      await backend.setWSListenerEnabled(l.id, enabled);
      await load();
    } catch (e) {
      toast.error(e instanceof Error ? e.message : String(e));
    }
  }

  async function removeListener(l: WSListener) {
    if (confirmingDelete !== 'l' + l.id) {
      confirmingDelete = 'l' + l.id;
      setTimeout(() => { if (confirmingDelete === 'l' + l.id) confirmingDelete = ''; }, 3000);
      return;
    }
    confirmingDelete = '';
    try {
      await backend.deleteWSListener(l.id);
      await load();
      toast.success('监听已删除');
    } catch (e) {
      toast.error(e instanceof Error ? e.message : String(e));
    }
  }

  /* ---------------- 站点对话框 ---------------- */
  let siteOpen = $state(false);
  let editingSiteId = $state<number | null>(null);
  let siteIsDefault = $state(false);
  let siteListenerId = $state(0);
  let siteTab = $state<'basic' | 'security' | 'traffic' | 'headers'>('basic');
  let draft = $state<WSSite>(blankSite());
  let domainsText = $state('');
  let targetsText = $state('');
  let locRows = $state<{ prefix: string; targets: string; strip: boolean }[]>([]);
  let ipText = $state('');
  let reqHText = $state('');
  let resHText = $state('');
  let copySiteId = $state<number | null>(null);

  function blankSite(): WSSite {
    return {
      id: 0, listener_id: 0, name: '', domains: [], type: 'proxy', enabled: true, default: false, remark: '',
      tls_mode: 'auto', challenge: '', cert_pem: '', key_pem: '',
      cert_status: 'pending', cert_error: '', cert_expires_at: null, created_at: '',
      targets: [], lb_policy: '', health: { enable: false, mode: 'http', path: '/', interval: 30, timeout: 5, fails: 3, passes: 2 },
      locations: [], use_target_host: false, forwarded_headers: true, insecure_skip_verify: false,
      timeouts: { connect: 0, tls_handshake: 0, response_header: 0 },
      redirect_to: '', redirect_code: 302, keep_path: true,
      root: '', list_dir: false, text: '', content_type: 'text/plain; charset=utf-8', status_code: 200,
      ip_mode: '', ip_list: [], compress: false, rate_limit: { enable: false, rate: 50, burst: 100, max_conns: 0 },
      request_headers: [], response_headers: [],
    };
  }

  function openSite(l: WSListener, s?: WSSite, copy = false, isDefault = false) {
    copySiteId = copy && s ? s.id : null;
    siteListenerId = s?.listener_id || l.id;
    siteIsDefault = isDefault || (!!s?.default && !copy);
    if (s && !copy) {
      editingSiteId = s.id;
      draft = { ...blankSite(), ...s, cert_pem: s.cert_pem ?? '', key_pem: s.key_pem ?? '' };
    } else if (s && copy) {
      editingSiteId = null;
      draft = { ...blankSite(), ...s, id: 0, name: s.name ? s.name + ' - 副本' : '', default: false, enabled: false };
    } else {
      editingSiteId = null;
      draft = blankSite();
    }
    domainsText = draft.domains.join(', ');
    targetsText = draft.targets.join('\n');
    locRows = draft.locations.map((x) => ({ prefix: x.prefix, targets: x.targets.join('\n'), strip: x.strip_prefix }));
    ipText = draft.ip_list.join('\n');
    reqHText = draft.request_headers.join('\n');
    resHText = draft.response_headers.join('\n');
    siteTab = 'basic';
    siteOpen = true;
  }

  function openDefaultSite(l: WSListener) {
    const s = sitesOf(l).find((x) => x.default);
    if (s) {
      openSite(l, s, false, true);
      return;
    }
    editingSiteId = null;
    siteIsDefault = true;
    siteListenerId = l.id;
    draft = blankSite();
    domainsText = '';
    targetsText = 'http://127.0.0.1:80';
    locRows = [];
    ipText = reqHText = resHText = '';
    siteTab = 'basic';
    siteOpen = true;
  }

  const domains = $derived(domainsText.split(/[\s,，]+/).map((d) => d.trim()).filter(Boolean));

  const formValid = $derived.by(() => {
    if (!siteIsDefault && domains.length === 0) return false;
    switch (draft.type) {
      case 'proxy':
        return targetsText.split('\n').some((t) => /^https?:\/\/.+/.test(t.trim()));
      case 'redirect':
        return draft.redirect_to.trim() !== '' && (draft.redirect_to.includes('://') || draft.redirect_to.includes('{host}'));
      case 'static':
        return draft.root.trim() !== '';
      default:
        return true;
    }
  });

  function buildPayload(): WSSiteInput {
    const targets = targetsText.split('\n').map((t) => t.trim()).filter(Boolean);
    const locations = locRows
      .filter((l) => l.prefix.trim().startsWith('/') && l.targets.trim() !== '')
      .map((l) => ({
        prefix: l.prefix.trim(),
        targets: l.targets.split('\n').map((t) => t.trim()).filter(Boolean),
        strip_prefix: l.strip,
        request_headers: [] as string[],
      }));
    return {
      name: draft.name.trim(),
      domains: siteIsDefault ? (domains.length ? domains : ['*']) : domains,
      type: draft.type,
      enabled: draft.enabled,
      default: siteIsDefault,
      remark: draft.remark,
      listener_id: siteListenerId,
      tls_mode: draft.tls_mode,
      challenge: draft.challenge,
      cert_pem: draft.cert_pem,
      key_pem: draft.key_pem,
      targets: draft.type === 'proxy' ? targets : [],
      lb_policy: draft.lb_policy,
      health: { ...draft.health },
      locations: draft.type === 'proxy' ? locations : [],
      use_target_host: draft.use_target_host,
      forwarded_headers: draft.forwarded_headers,
      insecure_skip_verify: draft.insecure_skip_verify,
      timeouts: draft.timeouts,
      redirect_to: draft.redirect_to.trim(),
      redirect_code: Number(draft.redirect_code) || 302,
      keep_path: draft.keep_path,
      root: draft.root.trim(),
      list_dir: draft.list_dir,
      text: draft.text,
      content_type: draft.content_type,
      status_code: Number(draft.status_code) || 200,
      ip_mode: draft.ip_mode,
      ip_list: ipText.split('\n').map((x) => x.trim()).filter(Boolean),
      compress: draft.compress,
      rate_limit: { ...draft.rate_limit },
      request_headers: reqHText.split('\n').map((x) => x.trim()).filter(Boolean),
      response_headers: resHText.split('\n').map((x) => x.trim()).filter(Boolean),
    };
  }

  async function saveSite() {
    if (!formValid || saving) return;
    saving = true;
    try {
      const payload = buildPayload();
      if (editingSiteId === null) {
        await backend.createWSSite(payload);
        toast.success(copySiteId !== null ? '站点已复制（已停用，确认后启用）' : '站点已添加');
      } else {
        await backend.updateWSSite(editingSiteId, payload);
        toast.success('站点已保存');
      }
      siteOpen = false;
      await load();
      loadStatus();
    } catch (e) {
      toast.error(e instanceof Error ? e.message : String(e));
    } finally {
      saving = false;
    }
  }

  async function toggleSite(s: WSSite, enabled: boolean) {
    try {
      await backend.setWSSiteEnabled(s.id, enabled);
      await load();
      loadStatus();
    } catch (e) {
      toast.error(e instanceof Error ? e.message : String(e));
    }
  }

  async function removeSite(s: WSSite) {
    if (confirmingDelete !== 's' + s.id) {
      confirmingDelete = 's' + s.id;
      setTimeout(() => { if (confirmingDelete === 's' + s.id) confirmingDelete = ''; }, 3000);
      return;
    }
    confirmingDelete = '';
    try {
      await backend.deleteWSSite(s.id);
      await load();
      loadStatus();
      toast.success('站点已删除');
    } catch (e) {
      toast.error(e instanceof Error ? e.message : String(e));
    }
  }

  async function resetDefaultSite(l: WSListener) {
    const s = sitesOf(l).find((x) => x.default);
    if (!s) return;
    try {
      await backend.deleteWSSite(s.id);
      await load();
      toast.success('默认站点已重置');
    } catch (e) {
      toast.error(e instanceof Error ? e.message : String(e));
    }
  }

  /* 域名链接：点击打开新页，工具提示复制完整地址 */
  function domainLink(l: WSListener, domain: string): { href: string; text: string } | null {
    if (domain.includes('*') || domain === '*') return null;
    const scheme = l.config.tls ? 'https' : 'http';
    const port = l.config.tls ? (l.config.port === 443 ? '' : ':' + l.config.port) : l.config.port === 80 ? '' : ':' + l.config.port;
    return { href: `${scheme}://${domain}${port}`, text: `${scheme}://${domain}${port}` };
  }

  async function copyText(text: string) {
    try {
      await navigator.clipboard.writeText(text);
      toast.success('已复制');
    } catch {
      toast.error('复制失败');
    }
  }

  function setCollapsed(l: WSListener) {
    const next = new Set(collapsedListeners);
    if (next.has(String(l.id))) next.delete(String(l.id)); else next.add(String(l.id));
    collapsedListeners = next;
  }

  const filteredSitesOf = (l: WSListener) => {
    const q = filterText.trim().toLowerCase();
    let list = sitesOf(l);
    if (q) {
      list = list.filter((s) =>
        (s.name || '').toLowerCase().includes(q) ||
        s.domains.some((d) => d.toLowerCase().includes(q)) ||
        s.targets.some((t) => t.toLowerCase().includes(q)),
      );
    }
    return list;
  };
</script>

<div class="animate-fade-up space-y-4">
  <!-- 页头动作 -->
  <div class="flex flex-wrap items-center gap-2">
    <Badge variant="outline" class="gap-1.5 {running ? 'bg-success/10 text-success ring-success/20' : 'bg-muted text-muted-foreground'}">
      <Server size={12} />
      {running ? `${status?.listeners.filter((x) => x.running).length ?? 0}/${status?.listeners.length ?? 0} 监听运行中` : '未运行'}
    </Badge>
    <span class="ml-auto"></span>
    <Button variant="outline" size="sm" class="h-8 gap-1.5" onclick={() => (location.hash = '#/proxy/topology')}>
      <Workflow size={14} /> 网络拓扑
    </Button>
    {#if listeners.length}
      <Button variant="outline" size="sm" class="h-8 gap-1.5" onclick={() => {
        const all = listeners.every((l) => collapsedListeners.has(String(l.id)));
        collapsedListeners = new Set(all ? [] : listeners.map((l) => String(l.id)));
      }}>
        {#if listeners.every((l) => collapsedListeners.has(String(l.id)))}<ChevronsUpDown size={14} />{:else}<ChevronsDownUp size={14} />{/if}
        {listeners.every((l) => collapsedListeners.has(String(l.id))) ? '全部展开' : '全部折叠'}
      </Button>
    {/if}
    {#if items.length}
      <div class="relative">
        <Search size={13} class="pointer-events-none absolute left-2.5 top-1/2 -translate-y-1/2 text-muted-foreground" />
        <Input class="h-8 w-52 pl-8 text-[12.5px]" placeholder="搜索站点…" bind:value={filterText} />
      </div>
    {/if}
    <label class="flex cursor-pointer items-center gap-1.5 rounded-lg border border-border/60 px-2.5 py-1.5 text-[12px] text-muted-foreground">
      <Switch class="scale-90" checked={compact} onCheckedChange={(v) => (compact = v)} />
      紧凑
    </label>
    <Button size="sm" class="h-8 gap-1.5" onclick={() => openListener()}>
      <Plus size={14} /> 添加监听
    </Button>
  </div>

  {#if loading}
    <div class="space-y-3">
      {#each Array(2) as _, i (i)}
        <div class="h-40 animate-pulse rounded-xl bg-foreground/[0.04]"></div>
      {/each}
    </div>
  {:else if listeners.length === 0}
    <EmptyState title="添加第一个监听">
      {#snippet icon()}<Server size={22} />{/snippet}
      desc="监听一个端口（HTTP 或 HTTPS），站点挂在监听下，按域名路由。" 
      {#snippet action()}
        <Button onclick={() => openListener()}><Plus size={16} /> 添加监听</Button>
      {/snippet}
    </EmptyState>
  {:else}
    <div class="space-y-4">
      {#each listeners as l (l.id)}
        {@const sites = filteredSitesOf(l)}
        {@const lRunning = listenerRunning(l)}
        {@const lError = listenerError(l)}
        <Card class="lift-card overflow-hidden {l.enabled ? '' : 'opacity-70'}">
          <div class="flex flex-wrap items-center gap-x-3 gap-y-1.5 border-b border-border/60 px-5 py-3.5">
            <Switch checked={l.enabled} onCheckedChange={(v) => toggleListener(l, v)} />
            <div class="min-w-0">
              <div class="flex flex-wrap items-center gap-2">
                <span class="text-[14.5px] font-semibold tracking-tight">{l.name || '未命名监听'}</span>
                <Badge variant="outline" class="h-[19px] font-mono text-[10.5px]">{l.config.tls ? 'https' : 'http'}://{listenerAddr(l)}</Badge>
                {#if l.config.tls && l.config.plain_http === 'redirect'}
                  <Badge variant="outline" class="h-[19px] text-[10.5px] text-muted-foreground">明文跳转 HTTPS</Badge>
                {:else if l.config.tls && l.config.plain_http === 'serve'}
                  <Badge variant="outline" class="h-[19px] text-[10.5px] text-muted-foreground">明文直接提供</Badge>
                {/if}
                {#if l.config.http_port}
                  <Badge variant="outline" class="h-[19px] font-mono text-[10.5px] text-muted-foreground" title="额外明文端口">
                    http:{l.config.http_port} {l.config.tls && l.config.http_port_mode === 'redirect' ? '跳转' : '提供'}
                  </Badge>
                {/if}
                {#if l.enabled && !lRunning && lError}
                  <Badge variant="outline" class="h-[19px] border-destructive/40 bg-destructive/[0.06] text-[10.5px] text-destructive">启动失败</Badge>
                {/if}
              </div>
              {#if l.remark}
                <div class="mt-0.5 text-[11.5px] text-muted-foreground">{l.remark}</div>
              {/if}
              {#if l.enabled && !lRunning && lError}
                <div class="mt-0.5 font-mono text-[11px] text-destructive">{lError}</div>
              {/if}
            </div>
            <div class="ml-auto flex items-center gap-2">
              <span class="font-num text-[11.5px] text-muted-foreground">{sitesOf(l).length} 站点</span>
              <button type="button" class="rounded-md p-1 text-muted-foreground transition-colors hover:bg-foreground/[0.05] hover:text-foreground" onclick={() => setCollapsed(l)} aria-label="折叠">
                {#if collapsedListeners.has(String(l.id))}<ChevronsUpDown size={14} />{:else}<ChevronsDownUp size={14} />{/if}
              </button>
            </div>
          </div>

          {#if !collapsedListeners.has(String(l.id))}
            <div class={compact ? '' : 'divide-y divide-border/40'}>
              {#each sites as s (s.id)}
                {@const lv = liveOf(s.id)}
                {@const downs = downCount(s)}
                <div class="group transition-colors hover:bg-foreground/[0.02] {s.enabled ? '' : 'opacity-60'}">
                  {#if compact}
                    <div class="flex items-center gap-2.5 px-5 py-1.5">
                      <Switch class="scale-90" checked={s.enabled} onCheckedChange={(v) => toggleSite(s, v)} />
                      <span class="truncate font-mono text-[12.5px]">{s.default ? '默认站点' : s.domains[0]}</span>
                      <span class="truncate font-mono text-[11.5px] text-muted-foreground">{summary(s)}</span>
                      {#if lv}
                        <span class="ml-auto whitespace-nowrap font-num text-[11.5px] tabular-nums text-muted-foreground">
                          {fmtCompact(lv.requests)}{lv.active > 0 ? ` · ${lv.active}` : ''}
                        </span>
                      {/if}
                      {@render SiteMenu(s, l, () => openSite(l, s), () => openSite(l, s, true), () => removeSite(s), () => resetDefaultSite(l))}
                    </div>
                  {:else}
                    <div class="grid grid-cols-[auto_minmax(0,1.25fr)_minmax(0,1.15fr)_auto_auto_auto] items-center gap-3 px-5 py-2.5">
                      <Switch checked={s.enabled} onCheckedChange={(v) => toggleSite(s, v)} />
                      <div class="min-w-0">
                        {#if s.default}
                          <div class="flex items-center gap-2">
                            <Badge variant="outline" class="h-[19px] border-warning/40 bg-warning/[0.06] text-[10.5px] text-warning">默认</Badge>
                            <span class="text-[11.5px] text-muted-foreground">未匹配任何站点域名的请求</span>
                          </div>
                        {:else}
                          <div class="flex flex-wrap items-center gap-x-1.5 gap-y-0.5 font-mono text-[12.5px]">
                            {#each s.domains.slice(0, 3) as d, i (d)}
                              {@const link = domainLink(l, d)}
                              {#if i > 0}<span class="text-muted-foreground/40">·</span>{/if}
                              {#if link}
                                <Tooltip.Root>
                                  <Tooltip.Trigger
                                    class="inline-flex max-w-[220px] cursor-pointer items-center gap-1 truncate hover:text-primary"
                                    onclick={(e) => { e.preventDefault(); window.open(link.href, '_blank'); }}
                                  >
                                    <span class="truncate">{d}</span><ExternalLink size={10} class="opacity-0 transition-opacity group-hover:opacity-100" />
                                  </Tooltip.Trigger>
                                  <Tooltip.Content class="flex items-center gap-2">
                                    <span class="font-mono text-[11px]">{link.text}</span>
                                    <button type="button" class="text-primary hover:underline" onclick={(e) => { e.stopPropagation(); copyText(link.text); }}>复制</button>
                                  </Tooltip.Content>
                                </Tooltip.Root>
                              {:else}
                                <span class="text-muted-foreground">{d}</span>
                              {/if}
                            {/each}
                            {#if s.domains.length > 3}
                              <span class="text-[11px] text-muted-foreground/70">+{s.domains.length - 3}</span>
                            {/if}
                          </div>
                        {/if}
                        {#if s.name}
                          <div class="mt-0.5 text-[11.5px] text-muted-foreground">{s.name}</div>
                        {/if}
                      </div>
                      <div class="flex min-w-0 items-center gap-2">
                        <Badge variant="outline" class="h-[19px] shrink-0 text-[10.5px]">{typeLabels[s.type]}</Badge>
                        <span class="truncate font-mono text-[11.5px] text-muted-foreground">{summary(s)}</span>
                      </div>
                      <div class="flex flex-wrap items-center gap-1.5">
                        {#if !s.enabled}
                          <Badge variant="outline" class="h-[19px] text-[10.5px] text-muted-foreground">已停用</Badge>
                        {/if}
                        {#if s.locations.length}
                          <Badge variant="outline" class="h-[19px] text-[10.5px] text-muted-foreground">分路径 {s.locations.length}</Badge>
                        {/if}
                        {#if s.ip_mode}
                          <Badge variant="outline" class="h-[19px] text-[10.5px] text-muted-foreground">{s.ip_mode === 'whitelist' ? '白名单' : '黑名单'}</Badge>
                        {/if}
                        {#if s.rate_limit.enable}
                          <Badge variant="outline" class="h-[19px] text-[10.5px] text-muted-foreground">限流</Badge>
                        {/if}
                        {#if s.insecure_skip_verify}
                          <Badge variant="outline" class="h-[19px] text-[10.5px] text-muted-foreground">跳过校验</Badge>
                        {/if}
                        {#if s.type === 'proxy' && downs > 0}
                          <Badge variant="outline" class="h-[19px] border-destructive/40 bg-destructive/[0.06] text-[10.5px] text-destructive">{downs} 后端不可用</Badge>
                        {/if}
                      </div>
                      <div class="font-num whitespace-nowrap text-right text-[11.5px] tabular-nums text-muted-foreground" title="请求数 · 后端错误 · 活跃">
                        {#if lv}
                          {fmtCompact(lv.requests)}{#if lv.errors > 0}<span class="text-destructive"> · {fmtCompact(lv.errors)}</span>{/if}{#if lv.active > 0}<span> · {lv.active} 活跃</span>{/if}
                        {:else}
                          —
                        {/if}
                      </div>
                      {@render SiteMenu(s, l, () => openSite(l, s), () => openSite(l, s, true), () => removeSite(s), () => resetDefaultSite(l))}
                    </div>
                  {/if}
                </div>
              {/each}
              {#if sites.length === 0}
                <div class="px-5 py-3 text-[12px] text-muted-foreground">{filterText ? '没有匹配的站点' : '暂无站点'}</div>
              {/if}
            </div>
          {:else}
            <div class="px-5 py-2 text-[12px] text-muted-foreground">已折叠 {sitesOf(l).length} 个站点</div>
          {/if}

          <div class="flex items-center gap-1 border-t border-border/60 px-3.5 py-2">
            <Button variant="ghost" size="sm" class="h-7 gap-1 text-[12px] text-primary" onclick={() => openSite(l)}>
              <Plus size={13} /> 添加站点
            </Button>
            {#if !sitesOf(l).some((x) => x.default)}
              <Button variant="ghost" size="sm" class="h-7 gap-1 text-[12px]" onclick={() => openDefaultSite(l)}>
                <ShieldCheck size={13} /> 设置默认站点
              </Button>
            {/if}
            <span class="ml-auto"></span>
            <Button variant="ghost" size="sm" class="h-7 gap-1 text-[12px]" onclick={() => openListener(l)}><Pencil size={13} /> 编辑监听</Button>
            <Button variant="ghost" size="sm" class="h-7 gap-1 text-[12px]" onclick={() => openListener(l, true)}><CopyPlus size={13} /> 复制</Button>
            <Button variant="ghost" size="sm" class="h-7 gap-1 text-[12px] text-destructive hover:text-destructive" onclick={() => removeListener(l)}>
              {#if confirmingDelete === 'l' + l.id}<ShieldAlert size={13} /> 确认删除{:else}<Trash2 size={13} /> 删除{/if}
            </Button>
          </div>
        </Card>
      {/each}
    </div>
  {/if}
</div>

{#if listeners.length === 0 && !loading}
  <div class="rounded-xl border border-dashed border-border/70 bg-card/50 px-6 py-4 text-[12.5px] leading-relaxed text-muted-foreground">
    推荐配置：监听 443（HTTPS）+ 额外 HTTP 端口 80（跳转 HTTPS）。站点填域名与后端即可，证书自动签发。
  </div>
{/if}

<!-- ============ 监听器对话框 ============ -->
<Dialog.Root bind:open={listenerOpen}>
  <Dialog.Content class="sm:max-w-lg">
    <Dialog.Header>
      <Dialog.Title>{editingListenerId === null ? (copyListenerId !== null ? '复制监听' : '添加监听') : '编辑监听'}</Dialog.Title>
    </Dialog.Header>
    <div class="grid gap-3.5">
      <div class="grid grid-cols-[110px_1fr] items-center gap-3">
        <Label class="text-[12px]">名称</Label>
        <Input class="h-8 text-[12.5px]" placeholder="可选" bind:value={lform.name} />
      </div>
      <div class="grid grid-cols-[110px_1fr] items-center gap-3">
        <Label class="text-[12px]">监听地址</Label>
        <Input class="h-8 font-mono text-[12.5px]" placeholder="留空监听全部地址" bind:value={lform.listen_ip} />
      </div>
      <div class="grid grid-cols-[110px_1fr] items-center gap-3">
        <Label class="text-[12px]">端口</Label>
        <Input class="h-8 w-28 font-num text-[13px]" inputmode="numeric" bind:value={lform.port} />
      </div>
      <div class="grid grid-cols-[110px_1fr] items-center gap-3">
        <Label class="text-[12px]">HTTPS</Label>
        <div class="flex items-center gap-3">
          <Switch checked={lform.tls} onCheckedChange={(v) => (lform.tls = v)} />
          <span class="text-[11.5px] text-muted-foreground">证书按站点域名自动签发</span>
        </div>
      </div>
      {#if lform.tls}
        <div class="grid grid-cols-[110px_1fr] items-center gap-3">
          <Label class="text-[12px]">额外 HTTP 端口</Label>
          <div class="flex items-center gap-2">
            <Input class="h-8 w-24 font-num text-[13px]" inputmode="numeric" bind:value={lform.http_port} placeholder="不开" />
            {#if Number(lform.http_port) > 0}
              <Select.Root type="single" value={lform.http_port_mode} onValueChange={(v) => (lform.http_port_mode = v ?? 'serve')}>
                <Select.Trigger class="h-8 w-32 text-[12px]">
                  {lform.http_port_mode === 'serve' ? '直接提供' : '跳转 HTTPS'}
                </Select.Trigger>
                <Select.Content>
                  <Select.Item value="redirect">跳转 HTTPS</Select.Item>
                  <Select.Item value="serve">直接提供</Select.Item>
                </Select.Content>
              </Select.Root>
            {/if}
          </div>
        </div>
        <div class="grid grid-cols-[110px_1fr] items-center gap-3">
          <Label class="text-[12px]">明文请求</Label>
          <Select.Root type="single" value={lform.plain_http} onValueChange={(v) => (lform.plain_http = v ?? '')}>
            <Select.Trigger class="h-8 w-44 text-[12px]">
              {lform.plain_http === 'serve' ? '直接提供站点' : lform.plain_http === 'redirect' ? '跳转 HTTPS' : '不接受'}
            </Select.Trigger>
            <Select.Content>
              <Select.Item value="">不接受</Select.Item>
              <Select.Item value="redirect">跳转 HTTPS</Select.Item>
              <Select.Item value="serve">直接提供站点</Select.Item>
            </Select.Content>
          </Select.Root>
        </div>
        <div class="grid grid-cols-[110px_1fr] items-center gap-3">
          <Label class="text-[12px]">最低 TLS</Label>
          <Select.Root type="single" value={lform.min_tls} onValueChange={(v) => (lform.min_tls = v ?? '1.2')}>
            <Select.Trigger class="h-8 w-28 text-[12px]">TLS {lform.min_tls || '1.2'}</Select.Trigger>
            <Select.Content>
              {#each ['1.0', '1.1', '1.2', '1.3'] as v (v)}
                <Select.Item value={v}>TLS {v}</Select.Item>
              {/each}
            </Select.Content>
          </Select.Root>
        </div>
      {/if}
      <div class="grid grid-cols-[110px_1fr] items-center gap-3">
        <Label class="text-[12px]">备注</Label>
        <Input class="h-8 text-[12.5px]" bind:value={lform.remark} />
      </div>
      <div class="grid grid-cols-[110px_1fr] items-center gap-3">
        <Label class="text-[12px]">启用</Label>
        <Switch checked={lform.enabled} onCheckedChange={(v) => (lform.enabled = v)} />
      </div>
      {#if copyListenerId !== null}
        <p class="text-[11.5px] text-muted-foreground">保存后会把原监听的站点一并复制（站点为停用状态）。</p>
      {/if}
    </div>
    <Dialog.Footer class="gap-2 sm:justify-end">
      <Button variant="outline" size="sm" class="h-8" onclick={() => (listenerOpen = false)}>取消</Button>
      <Button size="sm" class="h-8" disabled={saving || !(Number(lform.port) >= 1 && Number(lform.port) <= 65535)} onclick={saveListener}>
        {saving ? '保存中…' : '保存'}
      </Button>
    </Dialog.Footer>
  </Dialog.Content>
</Dialog.Root>

<!-- ============ 站点对话框（Tab：基础 / 安全 / 流量 / 头） ============ -->
<Dialog.Root bind:open={siteOpen}>
  <Dialog.Content class="max-h-[92vh] overflow-y-auto sm:max-w-3xl">
    <Dialog.Header>
      <Dialog.Title class="flex items-center gap-2">
        {#if siteIsDefault}默认站点{:else}{editingSiteId === null ? (copySiteId !== null ? '复制站点' : '添加站点') : '编辑站点'}{/if}
        {#if siteIsDefault}
          <Badge variant="outline" class="h-[19px] border-warning/40 bg-warning/[0.06] text-[10.5px] text-warning">未匹配域名兜底</Badge>
        {/if}
      </Dialog.Title>
    </Dialog.Header>
    <Tabs.Root value={siteTab} onValueChange={(v) => (siteTab = v as typeof siteTab)}>
      <Tabs.List class="grid w-full grid-cols-4">
        <Tabs.Trigger value="basic">基础</Tabs.Trigger>
        {#if draft.type !== 'close'}
          <Tabs.Trigger value="security">安全</Tabs.Trigger>
          <Tabs.Trigger value="traffic">流量</Tabs.Trigger>
          <Tabs.Trigger value="headers">头</Tabs.Trigger>
        {:else}
          <Tabs.Trigger value="security" disabled>安全</Tabs.Trigger>
          <Tabs.Trigger value="traffic" disabled>流量</Tabs.Trigger>
          <Tabs.Trigger value="headers" disabled>头</Tabs.Trigger>
        {/if}
      </Tabs.List>

      <Tabs.Content value="basic" class="mt-3 grid gap-3.5">
        {#if !siteIsDefault}
          <div class="grid gap-1.5">
            <Label class="text-[12px]">域名</Label>
            <textarea rows="2" class="w-full resize-none rounded-lg border border-input bg-card px-3 py-2 font-mono text-[12.5px] leading-relaxed outline-none placeholder:text-muted-foreground/40 focus:ring-2 focus:ring-ring/30" placeholder="app.example.com, *.example.com, 203.0.113.9" bind:value={domainsText}></textarea>
          </div>
        {/if}
        <div class="grid grid-cols-[110px_1fr] items-center gap-3">
          <Label class="text-[12px]">名称</Label>
          <Input class="h-8 text-[12.5px]" placeholder="可选" bind:value={draft.name} />
        </div>
        <div class="grid grid-cols-[110px_1fr] items-center gap-3">
          <Label class="text-[12px]">类型</Label>
          <div class="flex flex-wrap gap-1.5">
            {#each Object.entries(typeLabels) as [t, label] (t)}
              <button
                type="button"
                onclick={() => (draft.type = t as SiteType)}
                class="rounded-lg border px-3 py-1.5 text-[12px] font-medium transition-all {draft.type === t
                  ? 'border-primary/50 bg-primary/[0.06] text-primary'
                  : 'border-border/60 text-muted-foreground hover:text-foreground'}"
              >{label}</button>
            {/each}
          </div>
        </div>
        {#if draft.type === 'proxy'}
          <div class="grid gap-1.5">
            <Label class="text-[12px]">后端</Label>
            <textarea rows="3" class="w-full resize-none rounded-lg border border-input bg-card px-3 py-2 font-mono text-[12.5px] leading-relaxed outline-none placeholder:text-muted-foreground/40 focus:ring-2 focus:ring-ring/30" placeholder={"http://127.0.0.1:8080\nhttp://10.0.0.2:8080 weight=3\nhttp://10.0.0.3:8080 backup"} bind:value={targetsText}></textarea>
            <div class="text-[11.5px] text-muted-foreground">每行一个；可加 weight=N 与 backup。多项时按负载均衡分发。</div>
            {#if targetsText.split('\n').filter((t) => /^https?:\/\/.+/.test(t.trim())).length > 1}
              <Select.Root type="single" bind:value={draft.lb_policy}>
                <Select.Trigger class="h-8 w-full text-[12px]">
                  {draft.lb_policy === 'random' ? '随机' : draft.lb_policy === 'ip_hash' ? 'IP 哈希' : draft.lb_policy === 'least_conn' ? '最少连接' : '加权轮询'}
                </Select.Trigger>
                <Select.Content>
                  <Select.Item value="">加权轮询</Select.Item>
                  <Select.Item value="random">随机</Select.Item>
                  <Select.Item value="ip_hash">IP 哈希（同源同后端）</Select.Item>
                  <Select.Item value="least_conn">最少连接</Select.Item>
                </Select.Content>
              </Select.Root>
            {/if}
          </div>
          <div class="grid gap-1.5">
            <div class="flex items-center justify-between">
              <Label class="text-[12px]">分路径</Label>
              <Button variant="ghost" size="sm" class="h-6 gap-0.5 px-1.5 text-[11.5px] text-muted-foreground" onclick={() => (locRows = [...locRows, { prefix: '/', targets: '', strip: false }])}><Plus size={12} /> 路径</Button>
            </div>
            {#if locRows.length === 0}
              <p class="text-[11.5px] text-muted-foreground/70">未分流的路径走站点后端。</p>
            {:else}
              <div class="space-y-1.5">
                {#each locRows as loc, i (i)}
                  <div class="rounded-lg border border-border/60 p-2.5">
                    <div class="flex items-center gap-1.5">
                      <Input class="h-8 w-28 shrink-0 font-mono text-[12px]" placeholder="/api/" bind:value={loc.prefix} />
                      <textarea rows="1" class="min-h-8 flex-1 resize-y rounded-lg border border-input bg-card px-3 py-1.5 font-mono text-[12px] outline-none focus:ring-2 focus:ring-ring/30" placeholder="http://10.0.0.3:9000, http://10.0.0.4:9000" bind:value={loc.targets}></textarea>
                      <Button variant="ghost" size="icon" class="size-7 shrink-0 text-muted-foreground hover:text-destructive" onclick={() => (locRows = locRows.filter((_, j) => j !== i))}>
                        <X size={13} />
                      </Button>
                    </div>
                    <label class="mt-1.5 flex cursor-pointer items-center gap-2 text-[11.5px] text-muted-foreground">
                      <Switch class="scale-90" checked={loc.strip} onCheckedChange={(v) => (loc.strip = v)} />
                      去掉路径前缀（/api/zyqd/abc 发往后端为 /abc）
                    </label>
                  </div>
                {/each}
              </div>
            {/if}
          </div>
        {:else if draft.type === 'redirect'}
          <div class="grid gap-1.5">
            <Label class="text-[12px]">重定向目标</Label>
            <Input class="font-mono text-[12.5px]" placeholder="https://example.com 或 https://{'{host}'}:8443" bind:value={draft.redirect_to} />
            <div class="flex items-center gap-3">
              <Label class="text-[12px]">状态码</Label>
              <Select.Root type="single" value={String(draft.redirect_code)} onValueChange={(v) => (draft.redirect_code = parseInt(v, 10) || 302)}>
                <Select.Trigger class="h-8 w-28 text-[12px]">
                  {draft.redirect_code === 301 ? '301 永久' : draft.redirect_code === 307 ? '307 临时' : draft.redirect_code === 308 ? '308 永久' : '302 临时'}
                </Select.Trigger>
                <Select.Content>
                  <Select.Item value="301">301 永久</Select.Item>
                  <Select.Item value="302">302 临时</Select.Item>
                  <Select.Item value="307">307 临时</Select.Item>
                  <Select.Item value="308">308 永久</Select.Item>
                </Select.Content>
              </Select.Root>
              <label class="flex cursor-pointer items-center gap-2 text-[12.5px]">
                <Switch checked={draft.keep_path} onCheckedChange={(v) => (draft.keep_path = v)} />
                保留路径
              </label>
            </div>
          </div>
        {:else if draft.type === 'static'}
          <div class="grid grid-cols-[110px_1fr] items-center gap-3">
            <Label class="text-[12px]">目录</Label>
            <Input class="font-mono text-[12.5px]" placeholder="/data/www" bind:value={draft.root} />
          </div>
          <div class="grid grid-cols-[110px_1fr] items-center gap-3">
            <Label class="text-[12px]">列目录</Label>
            <Switch checked={draft.list_dir} onCheckedChange={(v) => (draft.list_dir = v)} />
          </div>
        {:else if draft.type === 'text'}
          <div class="grid grid-cols-[110px_1fr] items-center gap-3">
            <Label class="text-[12px]">状态码</Label>
            <Input class="h-8 w-24 font-num text-[13px]" inputmode="numeric" bind:value={draft.status_code} />
          </div>
          <div class="grid grid-cols-[110px_1fr] items-center gap-3">
            <Label class="text-[12px]">Content-Type</Label>
            <Input class="h-8 font-mono text-[12px]" bind:value={draft.content_type} />
          </div>
          <div class="grid gap-1.5">
            <Label class="text-[12px]">内容</Label>
            <textarea rows="4" class="w-full resize-none rounded-lg border border-input bg-card px-3 py-2 font-mono text-[12.5px] outline-none focus:ring-2 focus:ring-ring/30" bind:value={draft.text}></textarea>
          </div>
        {:else if draft.type === 'close'}
          <div class="rounded-lg border border-destructive/25 bg-destructive/[0.04] px-3.5 py-3">
            <span class="font-num text-[13px] font-semibold text-destructive">444</span>
            <span class="ml-2 text-[12px] text-muted-foreground">连接直接断开，不返回任何响应。适合默认站点拒绝未知域名的扫描。</span>
          </div>
        {/if}
        <div class="grid grid-cols-[110px_1fr] items-center gap-3">
          <Label class="text-[12px]">启用</Label>
          <Switch checked={draft.enabled} onCheckedChange={(v) => (draft.enabled = v)} />
        </div>
      </Tabs.Content>

      <Tabs.Content value="security" class="mt-3 grid gap-3.5">
        <div class="grid gap-1.5">
          <Label class="text-[12px]">访问控制</Label>
          <div class="grid grid-cols-3 gap-1.5">
            {#each [['', '不限'], ['whitelist', '白名单'], ['blacklist', '黑名单']] as [m, label] (m)}
              <button
                type="button"
                onclick={() => (draft.ip_mode = m as WSSite['ip_mode'])}
                class="rounded-lg border py-1.5 text-[12px] font-medium transition-all {draft.ip_mode === m
                  ? 'border-primary/50 bg-primary/[0.06] text-primary'
                  : 'border-border/60 text-muted-foreground hover:text-foreground'}"
              >{label}</button>
            {/each}
          </div>
          {#if draft.ip_mode}
            <textarea rows="3" class="w-full resize-none rounded-lg border border-input bg-card px-3 py-2 font-mono text-[11.5px] outline-none focus:ring-2 focus:ring-ring/30" placeholder={'10.0.0.0/8\n192.168.1.1'} bind:value={ipText}></textarea>
          {/if}
        </div>
        <div class="rounded-lg border border-border/60 p-3">
          <label class="flex cursor-pointer items-center gap-2.5">
            <Switch checked={draft.rate_limit.enable} onCheckedChange={(v) => (draft.rate_limit = { ...draft.rate_limit, enable: v })} />
            <span class="text-[12.5px] font-medium">限流</span>
            {#if draft.rate_limit.enable}
              <span class="font-num ml-auto text-[10.5px] text-muted-foreground">{draft.rate_limit.rate}/s · burst {draft.rate_limit.burst}{draft.rate_limit.max_conns > 0 ? ` · 单 IP ${draft.rate_limit.max_conns} 并发` : ''}</span>
            {/if}
          </label>
          {#if draft.rate_limit.enable}
            <div class="mt-2.5 grid grid-cols-3 gap-2">
              <label class="grid gap-1 text-[11px] text-muted-foreground">每秒请求<input type="number" min="0.1" step="0.1" class="h-7 rounded-md border border-input bg-card px-2 font-num text-[12px] outline-none" bind:value={draft.rate_limit.rate} /></label>
              <label class="grid gap-1 text-[11px] text-muted-foreground">突发<input type="number" min="1" class="h-7 rounded-md border border-input bg-card px-2 font-num text-[12px] outline-none" bind:value={draft.rate_limit.burst} /></label>
              <label class="grid gap-1 text-[11px] text-muted-foreground">单 IP 并发<input type="number" min="0" class="h-7 rounded-md border border-input bg-card px-2 font-num text-[12px] outline-none" bind:value={draft.rate_limit.max_conns} /></label>
            </div>
          {/if}
        </div>
      </Tabs.Content>

      <Tabs.Content value="traffic" class="mt-3 grid gap-3.5">
        <div class="grid grid-cols-[110px_1fr] items-center gap-3">
          <Label class="text-[12px]">gzip 压缩</Label>
          <Switch checked={draft.compress} onCheckedChange={(v) => (draft.compress = v)} />
        </div>
        {#if draft.type === 'proxy'}
          <div class="grid grid-cols-[110px_1fr] items-center gap-3">
            <Label class="text-[12px]">转发真实 IP</Label>
            <Switch checked={draft.forwarded_headers} onCheckedChange={(v) => (draft.forwarded_headers = v)} />
          </div>
          <div class="grid grid-cols-[110px_1fr] items-center gap-3">
            <Label class="text-[12px]">Host 用后端地址</Label>
            <Switch checked={draft.use_target_host} onCheckedChange={(v) => (draft.use_target_host = v)} />
          </div>
          <div class="grid grid-cols-[110px_1fr] items-center gap-3">
            <Label class="text-[12px]">跳过后端证书校验</Label>
            <Switch checked={draft.insecure_skip_verify} onCheckedChange={(v) => (draft.insecure_skip_verify = v)} />
          </div>
        {/if}
        <div class="grid gap-1.5">
          <Label class="text-[12px]">后端超时（秒，0 为默认）</Label>
          <div class="grid grid-cols-3 gap-2">
            <label class="grid gap-1 text-[11px] text-muted-foreground">连接<input type="number" min="0" max="300" class="h-7 rounded-md border border-input bg-card px-2 font-num text-[12px] outline-none" bind:value={draft.timeouts.connect} /></label>
            <label class="grid gap-1 text-[11px] text-muted-foreground">TLS 握手<input type="number" min="0" max="300" class="h-7 rounded-md border border-input bg-card px-2 font-num text-[12px] outline-none" bind:value={draft.timeouts.tls_handshake} /></label>
            <label class="grid gap-1 text-[11px] text-muted-foreground">响应头<input type="number" min="0" class="h-7 rounded-md border border-input bg-card px-2 font-num text-[12px] outline-none" bind:value={draft.timeouts.response_header} /></label>
          </div>
        </div>
      </Tabs.Content>

      <Tabs.Content value="headers" class="mt-3 grid gap-3.5">
        <label class="grid gap-1.5 text-[12px] text-muted-foreground">
          请求头（Key: Value，每行一条，值为空删除）
          <textarea rows="4" class="w-full resize-none rounded-lg border border-input bg-card px-3 py-2 font-mono text-[11.5px] outline-none focus:ring-2 focus:ring-ring/30" bind:value={reqHText}></textarea>
        </label>
        <label class="grid gap-1.5 text-[12px] text-muted-foreground">
          响应头
          <textarea rows="4" class="w-full resize-none rounded-lg border border-input bg-card px-3 py-2 font-mono text-[11.5px] outline-none focus:ring-2 focus:ring-ring/30" bind:value={resHText}></textarea>
        </label>
      </Tabs.Content>
    </Tabs.Root>
    <Dialog.Footer class="gap-2 sm:justify-end">
      <Button variant="outline" size="sm" class="h-8" onclick={() => (siteOpen = false)}>取消</Button>
      <Button size="sm" class="h-8" disabled={!formValid || saving} onclick={saveSite}>
        {saving ? '保存中…' : '保存'}
      </Button>
    </Dialog.Footer>
  </Dialog.Content>
</Dialog.Root>

<!-- 站点行菜单（共享片段组件） -->
{#snippet SiteMenu(s: WSSite, l: WSListener, onedit: () => void, oncopy: () => void, onremove: () => void, onreset: () => void)}
  <DropdownMenu.Root>
    <DropdownMenu.Trigger>
      {#snippet child({ props })}
        <Button {...props} variant="ghost" size="icon" class="size-7 text-muted-foreground"><MoreHorizontal size={15} /></Button>
      {/snippet}
    </DropdownMenu.Trigger>
    <DropdownMenu.Content align="end" class="w-36">
      <DropdownMenu.Item onclick={onedit}><Pencil size={13} /> 编辑</DropdownMenu.Item>
      <DropdownMenu.Item onclick={oncopy} disabled={s.default}><CopyPlus size={13} /> 复制</DropdownMenu.Item>
      {#if s.default}
        <DropdownMenu.Item variant="destructive" onclick={onreset}><Trash2 size={13} /> 重置默认站点</DropdownMenu.Item>
      {:else}
        <DropdownMenu.Item variant="destructive" onclick={onremove}>
          {#if confirmingDelete === 's' + s.id}<ShieldCheck size={13} /> 确认删除{:else}<Trash2 size={13} /> 删除{/if}
        </DropdownMenu.Item>
      {/if}
    </DropdownMenu.Content>
  </DropdownMenu.Root>
{/snippet}
