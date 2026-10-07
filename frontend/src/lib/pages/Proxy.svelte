<script lang="ts">
  import { Plus, Globe, Trash2, Pencil, Server, MoreHorizontal, Radio, ScanSearch, KeyRound, FileCode2, Download, Copy, Check, ExternalLink } from '@lucide/svelte';
  import { Card } from '$lib/components/ui/card';
  import { Button } from '$lib/components/ui/button';
  import { Input } from '$lib/components/ui/input';
  import { Badge } from '$lib/components/ui/badge';
  import { Label } from '$lib/components/ui/label';
  import { Switch } from '$lib/components/ui/switch';
  import * as Dialog from '$lib/components/ui/dialog';
  import * as Select from '$lib/components/ui/select';
  import * as DropdownMenu from '$lib/components/ui/dropdown-menu';
  import { toast } from 'svelte-sonner';
  import EmptyState from '$lib/components/common/EmptyState.svelte';
  import { backend, onWSMessage, type ProxyDomain, type ProxyRoute, type ProxyEnv, type DNSProviderConfig, type CaddySiteOpts } from '$lib/api';
  import { fmtDateTime } from '$lib/utils';

  let items = $state<ProxyDomain[]>([]);
  let running = $state(false);
  let loading = $state(true);
  let env = $state<ProxyEnv | null>(null);
  let envOpen = $state(false);
  let dnsCfg = $state<DNSProviderConfig | null>(null);

  async function load() {
    loading = true;
    try {
      const r = await backend.proxyDomains();
      items = r.items;
      running = r.running;
    } catch (e) {
      toast.error(e instanceof Error ? e.message : String(e));
    } finally {
      loading = false;
    }
  }

  async function loadEnv() {
    try {
      env = (await backend.proxyEnv()).env;
    } catch {
      /* 探测失败不打断页面 */
    }
    try {
      dnsCfg = (await backend.dnsConfig()).config;
    } catch {
      /* 同上 */
    }
  }

  $effect(() => {
    load();
    loadEnv();
    return onWSMessage((topic, payload) => {
      if (topic === 'events' && String((payload as { type?: string }).type ?? '').startsWith('proxy')) load();
      if (topic === 'events' && String((payload as { type?: string }).type ?? '').startsWith('health')) load();
    });
  });

  const certBadge: Record<string, { label: string; cls: string }> = {
    ok: { label: '证书正常', cls: 'bg-success/10 text-success ring-success/20' },
    pending: { label: '申请中', cls: 'bg-info/10 text-info ring-info/20' },
    error: { label: '证书异常', cls: 'bg-destructive/10 text-destructive ring-destructive/20' },
    none: { label: '无 TLS', cls: 'bg-muted text-muted-foreground ring-border' },
  };
  const challengeLabel: Record<string, string> = {
    '': '自动',
    auto: '自动',
    http: 'HTTP-01',
    alpn: 'TLS-ALPN',
    dns: 'DNS-01',
  };

  /* 新增/编辑域名 */
  let createOpen = $state(false);
  let editingId = $state<number | null>(null);
  let dform = $state({ domain: '', tls_mode: 'auto' as ProxyDomain['tls_mode'], challenge: '' as ProxyDomain['challenge'], cert_pem: '', key_pem: '', remark: '', upstream: '', opts: { encode: true, security_headers: true, log_access: true } as CaddySiteOpts });
  let submitting = $state(false);
  let selfsigning = $state(false);
  const dValid = $derived(
    /^[a-z0-9]([a-z0-9-]*[a-z0-9])?(\.[a-z0-9]([a-z0-9-]*[a-z0-9])?)+$/i.test(dform.domain.trim()) ||
      /^\d{1,3}(\.\d{1,3}){3}$/.test(dform.domain.trim()) ||
      /^[0-9a-f:]+:[0-9a-f:]+$/i.test(dform.domain.trim()),
  );
  const isIPDomain = $derived(/^\d{1,3}(\.\d{1,3}){3}$/.test(dform.domain.trim()) || /^[0-9a-f]+:[0-9a-f:]+$/i.test(dform.domain.trim()));

  function openCreate() {
    editingId = null;
    dform = { domain: '', tls_mode: 'auto', challenge: '', cert_pem: '', key_pem: '', remark: '', upstream: '', opts: { encode: true, security_headers: true, log_access: true } };
    createOpen = true;
  }

  function openEdit(d: ProxyDomain) {
    editingId = d.id;
    dform = { domain: d.domain, tls_mode: d.tls_mode, challenge: d.challenge ?? '', cert_pem: '', key_pem: '', remark: d.remark, opts: parseOpts(d.caddy_opts) };
    createOpen = true;
  }

  async function generateSelfSign() {
    if (!dform.domain.trim() || selfsigning) return;
    selfsigning = true;
    try {
      const r = await backend.selfSignCert(dform.domain.trim());
      dform.tls_mode = 'manual';
      dform.cert_pem = r.cert_pem;
      dform.key_pem = r.key_pem;
      toast.success(`自签证书已生成（至 ${r.expires}）`, { description: '浏览器首次访问需手动信任该证书' });
    } catch (e) {
      toast.error(e instanceof Error ? e.message : String(e));
    } finally {
      selfsigning = false;
    }
  }

  const valid = $derived(
    dValid &&
    (editingId !== null || /^https?:\/\//.test(dform.upstream.trim())) &&
    (dform.tls_mode !== 'manual' || editingId !== null || (dform.cert_pem !== '' && dform.key_pem !== '')),
  );
  const dnsChallengeReady = $derived(!!dnsCfg?.provider);

  async function submitDomain() {
    if (!dValid || submitting) return;
    submitting = true;
    try {
      const payload: Record<string, unknown> = {
        domain: dform.domain.trim().toLowerCase(),
        tls_mode: dform.tls_mode,
        challenge: dform.tls_mode === 'auto' ? dform.challenge : '',
        caddy_opts: dform.opts,
        remark: dform.remark,
      };
      if (editingId === null) {
        payload.cert_pem = dform.cert_pem;
        payload.key_pem = dform.key_pem;
        const created = await backend.createProxyDomain(payload);
        // 一步到位：首个上游自动落为 "/" 路由
        if (dform.upstream.trim()) {
          await backend.createProxyRoute(created.item.id, { path_match: '/', upstream_url: dform.upstream.trim() });
        }
      } else {
        await backend.updateProxyDomain(editingId, payload);
      }
      createOpen = false;
      await load();
      toast.success(editingId === null ? '域名已添加，反代已热重载' : '域名已更新，反代已热重载');
    } catch (e) {
      toast.error(e instanceof Error ? e.message : String(e));
    } finally {
      submitting = false;
    }
  }

  /* DNS-01 凭据 */
  let dnsOpen = $state(false);
  let dnsForm = $state<DNSProviderConfig>({ provider: '', api_token: '', secret_key: '' });
  let dnsSaving = $state(false);

  function openDNS() {
    dnsForm = { ...(dnsCfg ?? { provider: '', api_token: '', secret_key: '' }) };
    dnsOpen = true;
  }

  async function saveDNS() {
    if (dnsSaving) return;
    dnsSaving = true;
    try {
      const r = await backend.setDNSConfig(dnsForm);
      dnsCfg = r.config;
      dnsOpen = false;
      toast.success('DNS-01 凭据已保存', { description: '选择 DNS-01 挑战的域名将自动应用' });
    } catch (e) {
      toast.error(e instanceof Error ? e.message : String(e));
    } finally {
      dnsSaving = false;
    }
  }

  /* 寄生前置片段 */
  let snippetOpen = $state(false);
  let snippetKind = $state<'nginx' | 'caddy'>('nginx');
  const snippetPort = $derived(env?.listen_http?.replace(/^.*:(\d+)$/, '$1') ?? '80');

  const nginxSnippet = $derived(`# Nginx（占用 80 的前置）— 加入对应 server {} 块
# 把 ACME 挑战路径转发给 FirePanel（反代 HTTP 监听 ${env?.listen_http ?? ':8080'}）
location /.well-known/acme-challenge/ {
    proxy_pass http://127.0.0.1:${snippetPort};
    proxy_set_header Host $host;
}`);

  const caddySnippet = $derived(`# Caddyfile（占用 80 的前置 Caddy）— 在对应站点块内加入
handle_path /.well-known/acme-challenge/* {
    reverse_proxy 127.0.0.1:${snippetPort}
}`);

  /* 引擎与监听切换 */
  let engineForm = $state({ engine: 'builtin', http: '', https: '' });
  let engineSaving = $state(false);

  function primeEngineForm() {
    if (!env) return;
    engineForm = {
      engine: env.engine,
      http: env.listen_http?.replace(/^.*:/, '') ?? '80',
      https: env.listen_https?.replace(/^.*:/, '') ?? '443',
    };
  }

  async function saveEngine() {
    if (engineSaving) return;
    engineSaving = true;
    try {
      await backend.setProxyEngine({
        engine: engineForm.engine,
        http_addr: engineForm.http ? `:${engineForm.http.replace(/^:/, '')}` : '',
        https_addr: engineForm.https ? `:${engineForm.https.replace(/^:/, '')}` : '',
      });
      toast.success('引擎与监听已切换并持久化', { description: '监听端口已按新配置重建；重启面板后保持' });
      await loadEnv();
      await load();
    } catch (e) {
      toast.error(e instanceof Error ? e.message : String(e));
    } finally {
      engineSaving = false;
    }
  }

  const engineDirty = $derived(
    !!env && (engineForm.engine !== env.engine || `:${engineForm.http.replace(/^:/, '')}` !== env.listen_http.replace(/^.*:/, ':') || `:${engineForm.https.replace(/^:/, '')}` !== env.listen_https.replace(/^.*:/, ':')),
  );

  /* Caddy 一键安装 */
  let caddyInstalling = $state(false);

  /* Caddyfile（完全接管，整文件编辑） */
  let cfOpen = $state(false);
  let cfText = $state(''); // 服务器当前生效内容
  let cfEdit = $state(''); // 编辑器内容
  let cfPath = $state('/etc/caddy/Caddyfile');
  let cfLoading = $state(false);
  let cfSaving = $state(false);
  const cfDirty = $derived(cfEdit !== cfText);

  async function openCaddyfile() {
    cfOpen = true;
    cfLoading = true;
    try {
      const cf = await backend.caddyfile();
      cfText = cf.caddyfile;
      cfEdit = cf.caddyfile;
      cfPath = cf.path;
    } catch (e) {
      toast.error(e instanceof Error ? e.message : String(e));
    } finally {
      cfLoading = false;
    }
  }

  async function saveCaddyfileEdit() {
    if (!cfDirty || cfSaving) return;
    cfSaving = true;
    try {
      await backend.saveCaddyfile(cfEdit);
      cfText = cfEdit;
      toast.success('Caddyfile 已保存并重载');
    } catch (e) {
      toast.error(e instanceof Error ? e.message : String(e));
    } finally {
      cfSaving = false;
    }
  }

  function downloadCaddyfile() {
    const blob = new Blob([cfEdit], { type: 'text/plain' });
    const a = document.createElement('a');
    a.href = URL.createObjectURL(blob);
    a.download = 'Caddyfile';
    a.click();
    URL.revokeObjectURL(a.href);
  }

  /* 域名 Caddy 站点选项 */
  function parseOpts(raw: string | undefined): { encode: boolean; security_headers: boolean; log_access: boolean } {
    const def = { encode: true, security_headers: true, log_access: true };
    if (raw) {
      try {
        return { ...def, ...JSON.parse(raw) };
      } catch { /* fallthrough */ }
    }
    return def;
  }

  async function installCaddy() {
    if (caddyInstalling) return;
    caddyInstalling = true;
    try {
      const r = await backend.installCaddy();
      toast.success(`Caddy ${r.version} 安装完成`, { description: '现在可以把引擎切换为 Caddy 托管了' });
      await loadEnv();
      primeEngineForm();
    } catch (e) {
      toast.error(e instanceof Error ? e.message : String(e));
    } finally {
      caddyInstalling = false;
    }
  }

  async function toggleDomain(d: ProxyDomain, enabled: boolean) {
    try {
      await backend.updateProxyDomain(d.id, { enabled });
      await load();
      toast[enabled ? 'success' : 'info'](`域名 ${d.domain} 已${enabled ? '启用' : '停用'}`);
    } catch (e) {
      toast.error(e instanceof Error ? e.message : String(e));
    }
  }

  async function removeDomain(d: ProxyDomain) {
    try {
      await backend.deleteProxyDomain(d.id);
      await load();
      toast.success(`域名 ${d.domain} 已删除`);
    } catch (e) {
      toast.error(e instanceof Error ? e.message : String(e));
    }
  }

  /* 新增路由 */
  let routeOpen = $state(false);
  let routeDomain = $state<ProxyDomain | null>(null);
  let rform = $state({ path_match: '/', upstream_url: 'http://', health_path: '', ws_enabled: true });
  let rSubmitting = $state(false);

  function openRoute(d: ProxyDomain) {
    routeDomain = d;
    rform = { path_match: '/', upstream_url: 'http://', health_path: '', ws_enabled: true };
    routeOpen = true;
  }

  async function submitRoute() {
    if (!routeDomain || !rform.upstream_url.startsWith('http') || rSubmitting) return;
    rSubmitting = true;
    try {
      await backend.createProxyRoute(routeDomain.id, rform);
      routeOpen = false;
      await load();
      toast.success('路由已添加');
    } catch (e) {
      toast.error(e instanceof Error ? e.message : String(e));
    } finally {
      rSubmitting = false;
    }
  }

  async function removeRoute(r: ProxyRoute) {
    try {
      await backend.deleteProxyRoute(r.id);
      await load();
      toast.success('路由已删除');
    } catch (e) {
      toast.error(e instanceof Error ? e.message : String(e));
    }
  }

  const healthDot: Record<string, string> = {
    ok: 'bg-success',
    down: 'bg-destructive',
    unknown: 'bg-muted-foreground/40',
  };
</script>

<div class="animate-fade-up space-y-4">
  <div class="flex flex-wrap items-center gap-2.5">
    <Badge variant="outline" class="gap-1.5 {running ? 'bg-success/10 text-success ring-success/20' : 'bg-muted text-muted-foreground'}">
      <Radio size={12} />
      {running ? '反代运行中' : '尚无域名，未启动监听'}
    </Badge>
    <span class="ml-auto"></span>
    {#if env?.engine === 'caddy'}
      <Button variant="outline" onclick={openCaddyfile}>
        <FileCode2 size={14} />
        Caddyfile
      </Button>
    {/if}
    <Button variant="outline" onclick={() => (dnsOpen = true)}>
      <KeyRound size={14} />
      DNS-01 凭据{dnsCfg?.provider ? ` · ${dnsCfg.provider}` : ''}
    </Button>
    <Button variant="outline" onclick={() => { envOpen = true; loadEnv().then(primeEngineForm); }}>
      <ScanSearch size={14} />
      环境探测
    </Button>
    <Button onclick={openCreate}>
      <Plus size={16} />
      添加域名
    </Button>
  </div>

  {#if loading}
    <div class="space-y-3">
      {#each Array(2) as _, i (i)}
        <div class="h-40 animate-pulse rounded-xl bg-foreground/[0.04]"></div>
      {/each}
    </div>
  {:else if items.length === 0}
    <EmptyState title="还没有反向代理" desc="三步把你的服务发布到公网：">
      {#snippet icon()}<Globe size={22} />{/snippet}
      {#snippet children()}
        <div class="mx-auto grid max-w-md gap-2 text-left text-[12.5px]">
          {#each [["1", "添加域名", "填访问域名和服务地址，一步创建"], ["2", "证书自动签发", "Let's Encrypt 自动续期，含 IP 短期证书"], ["3", "即插即用", "流量按域名转发，随时加路径路由"]] as [n, t, d2] (n)}
            <div class="flex items-center gap-3 rounded-lg border border-border/60 px-3.5 py-2.5">
              <span class="flex h-6 w-6 shrink-0 items-center justify-center rounded-full bg-primary/10 text-[11px] font-bold text-primary">{n}</span>
              <span class="font-medium">{t}</span>
              <span class="text-muted-foreground">{d2}</span>
            </div>
          {/each}
        </div>
      {/snippet}
      {#snippet action()}
        <Button onclick={() => (createOpen = true)}><Plus size={16} /> 添加反向代理</Button>
      {/snippet}
    </EmptyState>
  {:else}
    <div class="space-y-4">
      {#each items as d (d.id)}
        <Card class="overflow-hidden shadow-soft">
          <div class="flex flex-wrap items-center gap-x-4 gap-y-2 border-b border-border/60 px-5 py-3.5">
            <Globe size={16} class="text-primary" />
            <div class="min-w-0">
              <div class="flex items-center gap-2">
                <span class="text-[14px] font-semibold">{d.domain}</span>
                <a
                  href="{d.tls_mode === 'none' ? 'http://' : 'https://'}{d.domain}"
                  target="_blank"
                  rel="noreferrer"
                  class="text-muted-foreground/60 transition-colors hover:text-primary"
                  title="在新标签页打开"
                  onclick={(e) => e.stopPropagation()}
                >
                  <ExternalLink size={12} />
                </a>
                <Badge variant="outline" class="text-[10px] ring-inset {certBadge[d.cert_status]?.cls}">
                  {certBadge[d.cert_status]?.label}
                </Badge>
                {#if d.tls_mode === 'auto'}
                  <Badge variant="outline" class="bg-primary/5 text-[10px] text-primary ring-primary/20">Let's Encrypt</Badge>
                  <Badge variant="outline" class="bg-muted text-[10px] text-muted-foreground ring-border">{challengeLabel[d.challenge ?? ''] ?? '自动'}</Badge>
                {:else if d.tls_mode === 'manual'}
                  <Badge variant="outline" class="bg-info/5 text-[10px] text-info ring-info/20">手动证书</Badge>
                {/if}
              </div>
              {#if d.cert_error}
                <p class="mt-0.5 truncate text-[11px] text-destructive" title={d.cert_error}>{d.cert_error}</p>
              {:else if d.remark}
                <p class="mt-0.5 truncate text-[11.5px] text-muted-foreground">{d.remark}</p>
              {/if}
            </div>
            <div class="ml-auto flex items-center gap-3">
              <Switch checked={d.enabled} onCheckedChange={(v) => toggleDomain(d, v)} />
              <Button variant="outline" size="sm" onclick={() => openRoute(d)}><Plus size={14} /> 路由</Button>
              <DropdownMenu.Root>
                <DropdownMenu.Trigger>
                  {#snippet child({ props })}
                    <Button {...props} variant="ghost" size="icon" class="size-8 text-muted-foreground"><MoreHorizontal size={16} /></Button>
                  {/snippet}
                </DropdownMenu.Trigger>
                <DropdownMenu.Content align="end" class="w-36">
                  <DropdownMenu.Item onclick={() => openEdit(d)}>
                    <Pencil size={14} /> 编辑域名
                  </DropdownMenu.Item>
                  <DropdownMenu.Separator />
                  <DropdownMenu.Item variant="destructive" onclick={() => removeDomain(d)}>
                    <Trash2 size={14} /> 删除域名
                  </DropdownMenu.Item>
                </DropdownMenu.Content>
              </DropdownMenu.Root>
            </div>
          </div>

          {#if d.routes.length === 0}
            <div class="px-5 py-6">
              <EmptyState compact title="暂无路由" desc="添加至少一条路由，把该域名的流量转发到上游服务。">
                {#snippet icon()}<Server size={18} />{/snippet}
                {#snippet action()}
                  <Button variant="outline" size="sm" onclick={() => openRoute(d)}><Plus size={14} /> 添加路由</Button>
                {/snippet}
              </EmptyState>
            </div>
          {:else}
            <table class="w-full text-left text-[13px]">
              <thead>
                <tr class="border-b border-border/40 text-[11px] text-muted-foreground">
                  <th class="px-5 py-2 font-medium">路径</th>
                  <th class="px-3 py-2 font-medium">上游</th>
                  <th class="px-3 py-2 font-medium">WebSocket</th>
                  <th class="px-3 py-2 font-medium">健康</th>
                  <th class="px-5 py-2 text-right font-medium">操作</th>
                </tr>
              </thead>
              <tbody>
                {#each d.routes as r (r.id)}
                  <tr class="group border-b border-border/30 last:border-0 hover:bg-foreground/[0.02]">
                    <td class="px-5 py-2.5 font-mono text-[12px] font-medium">{r.path_match}</td>
                    <td class="px-3 py-2.5 font-mono text-[12px]">{r.upstream_url}</td>
                    <td class="px-3 py-2.5">
                      {#if r.ws_enabled}
                        <Badge variant="outline" class="bg-success/5 text-[10px] text-success ring-success/20">支持</Badge>
                      {:else}
                        <span class="text-muted-foreground/60 text-[11px]">—</span>
                      {/if}
                    </td>
                    <td class="px-3 py-2.5">
                      <span class="inline-flex items-center gap-1.5 text-[11.5px]">
                        <span class="h-[7px] w-[7px] rounded-full {healthDot[r.health_status]}"></span>
                        {r.health_status === 'ok' ? '健康' : r.health_status === 'down' ? '不可达' : '未检测'}
                        {#if r.health_checked_at}
                          <span class="text-muted-foreground/70">{fmtDateTime(r.health_checked_at)}</span>
                        {/if}
                      </span>
                    </td>
                    <td class="px-5 py-2.5 text-right">
                      <Button
                        variant="ghost" size="icon"
                        class="size-7 text-muted-foreground opacity-0 transition-opacity hover:text-destructive group-hover:opacity-100"
                        onclick={() => removeRoute(r)} aria-label="删除路由"
                      >
                        <Trash2 size={13} />
                      </Button>
                    </td>
                  </tr>
                {/each}
              </tbody>
            </table>
          {/if}
        </Card>
      {/each}
    </div>
  {/if}
</div>

<!-- 添加/编辑域名（一步向导：域名 + 上游即可用，高级项折叠） -->
<Dialog.Root bind:open={createOpen}>
  <Dialog.Content class="sm:max-w-md">
    <Dialog.Header>
      <Dialog.Title>{editingId === null ? '添加反向代理' : '编辑域名'}</Dialog.Title>
      <Dialog.Description>
        {editingId === null
          ? '填两处即可：访问域名 + 服务地址。证书自动签发，流量按域名转发到你的服务。'
          : '修改域名与证书设置；路由转发在域名卡片中管理。'}
      </Dialog.Description>
    </Dialog.Header>
    <div class="grid gap-3.5">
      <div class="grid gap-2">
        <Label for="pd-domain">① 访问域名</Label>
        <Input id="pd-domain" placeholder="app.example.com 或 203.0.113.9" bind:value={dform.domain} class="font-mono" />
        {#if isIPDomain && editingId === null}
          <p class="text-[11px] text-info">检测到 IP：将签发 Let's Encrypt 6.7 天短期证书（自动续期）；内网 IP 请在高级里选手动证书。</p>
        {/if}
      </div>
      {#if editingId === null}
        <div class="grid gap-2">
          <Label for="pd-upstream">② 服务地址</Label>
          <Input id="pd-upstream" placeholder="http://127.0.0.1:8080" bind:value={dform.upstream} class="font-mono" />
          <p class="text-[11px] text-muted-foreground">要被代理的服务地址，自动创建 "/" 路由；更多路径稍后可在域名卡片中添加。</p>
        </div>
      {/if}
      <label class="flex items-center gap-2.5 rounded-lg border border-border/60 px-3 py-2.5 text-[13px]">
        <Switch
          checked={dform.tls_mode !== 'none'}
          onCheckedChange={(v) => (dform.tls_mode = v ? 'auto' : 'none')}
        />
        <span class="font-medium">HTTPS 自动证书</span>
        <span class="text-[11px] text-muted-foreground">Let's Encrypt 自动签发与续期</span>
      </label>

      <details open={editingId !== null} class="rounded-lg border border-border/60">
        <summary class="cursor-pointer select-none px-3 py-2 text-[12px] font-medium text-muted-foreground hover:text-foreground">
          高级选项{editingId !== null ? '（含手动证书）' : ''}
        </summary>
        <div class="grid gap-3.5 border-t border-border/50 p-3.5">
          <div class="grid gap-2">
            <Label>TLS 模式</Label>
            <Select.Root type="single" bind:value={dform.tls_mode}>
              <Select.Trigger class="w-full">
                {dform.tls_mode === 'auto' ? "自动（Let's Encrypt，域名 90 天 / IP 6.7 天）" : dform.tls_mode === 'manual' ? '手动证书' : '无 TLS（仅 HTTP）'}
              </Select.Trigger>
              <Select.Content>
                <Select.Item value="auto">自动（Let's Encrypt）</Select.Item>
                <Select.Item value="manual">手动证书</Select.Item>
                <Select.Item value="none">无 TLS（仅 HTTP）</Select.Item>
              </Select.Content>
            </Select.Root>
          </div>
          {#if dform.tls_mode === 'auto'}
            <div class="grid gap-2">
              <Label>ACME 挑战方式</Label>
              <Select.Root type="single" bind:value={dform.challenge}>
                <Select.Trigger class="w-full">
                  {dform.challenge === '' || dform.challenge === 'auto'
                    ? '自动（按端口探测结果选择）'
                    : dform.challenge === 'http' ? '仅 HTTP-01（80 端口校验）'
                    : dform.challenge === 'alpn' ? '仅 TLS-ALPN-01（443 端口校验）'
                    : 'DNS-01（无需 80/443，泛域名必选）'}
                </Select.Trigger>
                <Select.Content>
                  <Select.Item value="auto">自动（按端口探测结果选择）</Select.Item>
                  <Select.Item value="http">仅 HTTP-01（80 端口校验）</Select.Item>
                  <Select.Item value="alpn">仅 TLS-ALPN-01（443 端口校验）</Select.Item>
                  <Select.Item value="dns">DNS-01（无需 80/443，泛域名必选）</Select.Item>
                </Select.Content>
              </Select.Root>
              {#if dform.challenge === 'dns'}
                {#if dnsChallengeReady}
                  <p class="text-[11px] text-success">DNS-01 凭据已配置（{dnsCfg?.provider}），保存后自动生效。</p>
                {:else}
                  <p class="text-[11px] text-warning">尚未配置 DNS-01 凭据：
                    <button type="button" class="underline hover:text-foreground" onclick={openDNS}>立即配置</button>
                  </p>
                {/if}
              {:else if env && !env.http.bindable && !env.https.bindable && dform.challenge === ''}
                <p class="text-[11px] text-warning">探测到 80/443 均被占用：自动挑战可能失败，建议选 DNS-01，或用「环境探测」生成寄生前置配置。</p>
              {/if}
            </div>
          {/if}
          {#if dform.tls_mode === 'manual' && editingId === null}
            <Button variant="outline" size="sm" class="justify-start" disabled={selfsigning || !dform.domain.trim()} onclick={generateSelfSign}>
              {selfsigning ? '生成中…' : '为该域名/IP 自动生成自签证书（10 年）'}
            </Button>
            <div class="grid gap-2">
              <Label for="pd-cert">证书 PEM</Label>
              <textarea id="pd-cert" class="h-24 w-full resize-none rounded-lg border border-input bg-card px-3 py-2 font-mono text-[11px] outline-none focus:ring-2 focus:ring-ring/40" placeholder="-----BEGIN CERTIFICATE-----" bind:value={dform.cert_pem}></textarea>
            </div>
            <div class="grid gap-2">
              <Label for="pd-key">私钥 PEM</Label>
              <textarea id="pd-key" class="h-24 w-full resize-none rounded-lg border border-input bg-card px-3 py-2 font-mono text-[11px] outline-none focus:ring-2 focus:ring-ring/40" placeholder="-----BEGIN PRIVATE KEY-----" bind:value={dform.key_pem}></textarea>
            </div>
          {/if}
          <div class="grid gap-2">
            <Label>Caddy 站点选项 <span class="text-muted-foreground/70">（Caddy 引擎下生效）</span></Label>
            <div class="grid gap-1.5 sm:grid-cols-3">
              <label class="flex items-center gap-2 rounded-lg border border-border/60 px-2.5 py-2 text-[12px]">
                <Switch checked={dform.opts.encode} onCheckedChange={(v) => (dform.opts = { ...dform.opts, encode: v })} />
                压缩
              </label>
              <label class="flex items-center gap-2 rounded-lg border border-border/60 px-2.5 py-2 text-[12px]">
                <Switch checked={dform.opts.security_headers} onCheckedChange={(v) => (dform.opts = { ...dform.opts, security_headers: v })} />
                安全头
              </label>
              <label class="flex items-center gap-2 rounded-lg border border-border/60 px-2.5 py-2 text-[12px]">
                <Switch checked={dform.opts.log_access} onCheckedChange={(v) => (dform.opts = { ...dform.opts, log_access: v })} />
                访问日志
              </label>
            </div>
          </div>
          <div class="grid gap-2">
            <Label for="pd-remark">备注 <span class="text-muted-foreground/70">（可选）</span></Label>
            <Input id="pd-remark" placeholder="用途说明" bind:value={dform.remark} />
          </div>
        </div>
      </details>
    </div>
    <Dialog.Footer class="gap-2 sm:justify-end">
      <Button variant="outline" onclick={() => (createOpen = false)}>取消</Button>
      <Button disabled={!valid || submitting} onclick={submitDomain}>
        {submitting ? '保存中…' : editingId === null ? '创建并生效' : '保存'}
      </Button>
    </Dialog.Footer>
  </Dialog.Content>
</Dialog.Root>

<!-- 添加路由 -->
<Dialog.Root bind:open={routeOpen}>
  <Dialog.Content class="sm:max-w-md">
    <Dialog.Header>
      <Dialog.Title>添加路由{routeDomain ? ` · ${routeDomain.domain}` : ''}</Dialog.Title>
      <Dialog.Description>按最长路径前缀匹配；WebSocket 升级自动透传。</Dialog.Description>
    </Dialog.Header>
    <div class="grid gap-3.5">
      <div class="grid grid-cols-2 gap-3">
        <div class="grid gap-2">
          <Label for="pr-path">路径前缀</Label>
          <Input id="pr-path" placeholder="/ 或 /api" bind:value={rform.path_match} class="font-mono" />
        </div>
        <div class="grid gap-2">
          <Label for="pr-health">健康检查路径 <span class="text-muted-foreground/70">（可选）</span></Label>
          <Input id="pr-health" placeholder="/healthz" bind:value={rform.health_path} class="font-mono" />
        </div>
      </div>
      <div class="grid gap-2">
        <Label for="pr-up">上游地址</Label>
        <Input id="pr-up" placeholder="http://192.168.1.100:8080" bind:value={rform.upstream_url} class="font-mono" />
      </div>
      <label class="flex items-center gap-2.5 text-[13px]">
        <Switch checked={rform.ws_enabled} onCheckedChange={(v) => (rform.ws_enabled = v)} />
        允许 WebSocket 透传
      </label>
    </div>
    <Dialog.Footer class="gap-2 sm:justify-end">
      <Button variant="outline" onclick={() => (routeOpen = false)}>取消</Button>
      <Button disabled={!rform.upstream_url.startsWith('http') || rSubmitting} onclick={submitRoute}>
        {rSubmitting ? '添加中…' : '添加'}
      </Button>
    </Dialog.Footer>
  </Dialog.Content>
</Dialog.Root>

<!-- 环境探测 -->
<Dialog.Root bind:open={envOpen}>
  <Dialog.Content class="max-h-[92vh] w-full max-w-[calc(100vw-2rem)] overflow-x-hidden overflow-y-auto sm:max-w-lg">
    <Dialog.Header>
      <Dialog.Title>反代环境探测</Dialog.Title>
      <Dialog.Description>80/443 可用性决定证书挑战方式；推荐值可在添加域名时手动覆盖。</Dialog.Description>
    </Dialog.Header>
    {#if env}
      <div class="min-w-0 space-y-3">
        <!-- 当前引擎横幅 -->
        <div class="flex items-center gap-3 rounded-xl border border-primary/30 bg-gradient-to-r from-primary/[0.08] to-info/[0.05] px-4 py-3">
          <div class="flex h-10 w-10 shrink-0 items-center justify-center rounded-lg bg-primary/15 text-primary">
            {#if env.engine === 'caddy'}<Globe size={20} />{:else}<Server size={20} />{/if}
          </div>
          <div class="min-w-0 flex-1">
            <div class="flex flex-wrap items-center gap-2">
              <span class="text-[12px] text-muted-foreground">当前引擎</span>
              <span class="text-[15px] font-bold text-foreground">{env.engine === 'caddy' ? 'Caddy 托管' : '内置引擎'}</span>
              <Badge variant="outline" class="text-[10px] {env.engine === 'caddy' ? 'bg-info/10 text-info ring-info/25' : 'bg-primary/10 text-primary ring-primary/25'}">
                {env.engine === 'caddy' ? '模式 3' : '模式 1 / 2'}
              </Badge>
            </div>
            <p class="mt-0.5 truncate text-[11.5px] text-muted-foreground">
              {env.engine === 'caddy' ? 'Caddy 承担反代与证书，面板下发配置与观测' : '面板直接监听并承担反代与证书（零外部依赖）'}
              <span class="font-mono">· HTTP {env.listen_http || '未配置'} · HTTPS {env.listen_https || '未配置'}</span>
              {#if env.caddy_installed}<span class="font-mono"> · Caddy {env.caddy_version}</span>{/if}
            </p>
          </div>
        </div>

        <div class="grid grid-cols-2 gap-3">
          <div class="rounded-lg border border-border/60 p-3">
            <div class="flex items-center gap-2 text-[12.5px] font-medium">
              端口 80
              <Badge variant="outline" class="ml-auto text-[10px] {env.http.bindable ? 'bg-success/10 text-success ring-success/20' : 'bg-warning/10 text-warning ring-warning/25'}">
                {env.http.bindable ? '可用' : `被占用${env.http.occupier ? `（${env.http.occupier}）` : ''}`}
              </Badge>
            </div>
          </div>
          <div class="rounded-lg border border-border/60 p-3">
            <div class="flex items-center gap-2 text-[12.5px] font-medium">
              端口 443
              <Badge variant="outline" class="ml-auto text-[10px] {env.https.bindable ? 'bg-success/10 text-success ring-success/20' : 'bg-warning/10 text-warning ring-warning/25'}">
                {env.https.bindable ? '可用' : `被占用${env.https.occupier ? `（${env.https.occupier}）` : ''}`}
              </Badge>
            </div>
          </div>
        </div>

        <!-- 引擎与监听切换（模式 1/2/3 的正交配置） -->
        <div class="rounded-lg border border-border/60 p-3.5">
          <div class="mb-2.5 flex items-center gap-2 text-[12.5px] font-semibold">
            引擎与监听
            <span class="text-[10.5px] font-normal text-muted-foreground">切换立即生效并持久化（重启保持）</span>
          </div>
          <div class="grid gap-2.5 sm:grid-cols-3">
            <div class="grid gap-1.5">
              <Label class="text-[11.5px]">引擎</Label>
              <Select.Root
                type="single"
                value={engineForm.engine}
                onValueChange={(v) => (engineForm.engine = v)}
              >
                <Select.Trigger class="w-full text-[12px]">
                  {engineForm.engine === 'caddy' ? 'Caddy 托管' : '内置引擎'}
                </Select.Trigger>
                <Select.Content>
                  <Select.Item value="builtin">内置引擎（零依赖）</Select.Item>
                  <Select.Item value="caddy">Caddy 托管</Select.Item>
                </Select.Content>
              </Select.Root>
            </div>
            <div class="grid gap-1.5">
              <Label class="text-[11.5px]">HTTP 端口</Label>
              <Input class="h-8 font-mono text-[12px]" inputmode="numeric" bind:value={engineForm.http} placeholder="80" />
            </div>
            <div class="grid gap-1.5">
              <Label class="text-[11.5px]">HTTPS 端口</Label>
              <Input class="h-8 font-mono text-[12px]" inputmode="numeric" bind:value={engineForm.https} placeholder="443" />
            </div>
          </div>
          {#if engineForm.engine === 'caddy' && env && !env.caddy_installed}
            <p class="mt-2 text-[11px] text-warning">
              未检测到 Caddy：可一键安装（Debian/Ubuntu 走发行版或官方仓库，约 1-3 分钟），或参考 caddyserver.com/docs/install。
            </p>
            <Button
              size="sm"
              variant="outline"
              class="mt-1.5 h-7 text-[11.5px]"
              disabled={caddyInstalling}
              onclick={installCaddy}
            >
              {caddyInstalling ? '安装中，请勿关闭页面…' : '一键安装 Caddy'}
            </Button>
          {:else if engineForm.engine === 'caddy'}
            <p class="mt-2 text-[11px] text-muted-foreground">切换后由 Caddy 监听上述端口并负责反代与证书；面板继续提供配置下发与观测。</p>
          {:else}
            <p class="mt-2 text-[11px] text-muted-foreground">端口变更会先释放旧监听再绑定新端口；80/443 保持不变即对应「直接接管」形态。</p>
          {/if}
          <Button size="sm" class="mt-2.5 h-8 w-full text-[12px]" disabled={engineSaving || !engineDirty} onclick={saveEngine}>
            {engineSaving ? '应用中…' : engineDirty ? '保存并应用（重建监听）' : '无变更'}
          </Button>
        </div>
        <div class="rounded-lg border border-primary/30 bg-primary/[0.05] p-3 text-[12px]">
          <div class="font-medium text-primary">推荐：{env.recommended.challenge} · 监听 {env.recommended.http_port}/{env.recommended.https_port}</div>
          <p class="mt-1 text-[11.5px] leading-relaxed text-muted-foreground">{env.recommended.note}</p>
        </div>
        {#if !env.http.bindable}
          <Button variant="outline" size="sm" class="w-full" onclick={() => (snippetOpen = true)}>
            <FileCode2 size={14} /> 生成寄生前置配置片段（Nginx / Caddy）
          </Button>
        {/if}
      </div>
    {:else}
      <div class="h-32 animate-pulse rounded-lg bg-foreground/[0.04]"></div>
    {/if}
    <Dialog.Footer class="sm:justify-end">
      <Button variant="outline" onclick={() => (envOpen = false)}>关闭</Button>
    </Dialog.Footer>
  </Dialog.Content>
</Dialog.Root>

<!-- DNS-01 凭据 -->
<Dialog.Root bind:open={dnsOpen}>
  <Dialog.Content class="sm:max-w-md">
    <Dialog.Header>
      <Dialog.Title>DNS-01 挑战凭据</Dialog.Title>
      <Dialog.Description>
        80/443 均不可用或签发泛域名证书时使用。凭据保存在面板本机数据库，仅用于添加/删除 _acme-challenge TXT 记录。
      </Dialog.Description>
    </Dialog.Header>
    <div class="grid gap-3.5">
      <div class="grid gap-2">
        <Label>DNS 服务商</Label>
        <Select.Root type="single" bind:value={dnsForm.provider}>
          <Select.Trigger class="w-full">
            {dnsForm.provider === 'cloudflare' ? 'Cloudflare' : dnsForm.provider === 'alidns' ? '阿里云（万网）DNS' : dnsForm.provider === 'dnspod' ? 'DNSPod / 腾讯云' : '选择服务商…'}
          </Select.Trigger>
          <Select.Content>
            <Select.Item value="cloudflare">Cloudflare</Select.Item>
            <Select.Item value="alidns">阿里云（万网）DNS</Select.Item>
            <Select.Item value="dnspod">DNSPod / 腾讯云</Select.Item>
          </Select.Content>
        </Select.Root>
      </div>
      {#if dnsForm.provider === 'cloudflare'}
        <div class="grid gap-2">
          <Label for="dns-token">API Token</Label>
          <Input id="dns-token" type="password" placeholder="Zone.DNS:Edit 权限的 API Token" bind:value={dnsForm.api_token} class="font-mono" />
        </div>
      {:else if dnsForm.provider === 'alidns'}
        <div class="grid gap-2">
          <Label for="dns-ak">AccessKey ID</Label>
          <Input id="dns-ak" type="password" bind:value={dnsForm.api_token} class="font-mono" />
        </div>
        <div class="grid gap-2">
          <Label for="dns-sk">AccessKey Secret</Label>
          <Input id="dns-sk" type="password" bind:value={dnsForm.secret_key} class="font-mono" />
        </div>
      {:else if dnsForm.provider === 'dnspod'}
        <div class="grid gap-2">
          <Label for="dns-sid">SecretId</Label>
          <Input id="dns-sid" type="password" bind:value={dnsForm.api_token} class="font-mono" />
        </div>
        <div class="grid gap-2">
          <Label for="dns-skey">SecretKey</Label>
          <Input id="dns-skey" type="password" bind:value={dnsForm.secret_key} class="font-mono" />
        </div>
      {/if}
    </div>
    <Dialog.Footer class="gap-2 sm:justify-end">
      <Button variant="outline" onclick={() => (dnsOpen = false)}>取消</Button>
      <Button disabled={!dnsForm.provider || dnsSaving} onclick={saveDNS}>{dnsSaving ? '保存中…' : '保存'}</Button>
    </Dialog.Footer>
  </Dialog.Content>
</Dialog.Root>

<!-- 寄生前置片段 -->
<Dialog.Root bind:open={snippetOpen}>
  <Dialog.Content class="sm:max-w-lg">
    <Dialog.Header>
      <Dialog.Title>寄生前置配置片段</Dialog.Title>
      <Dialog.Description>
        让占用 80 端口的前置反代把 ACME 挑战路径转发给 FirePanel，HTTP-01 即可正常签发（含 IP 证书）。
      </Dialog.Description>
    </Dialog.Header>
    <div class="flex gap-1.5">
      <button
        type="button"
        onclick={() => (snippetKind = 'nginx')}
        class="rounded-lg border px-3 py-1 text-[12px] font-medium transition-[color,background-color,border-color] {snippetKind === 'nginx' ? 'border-primary/50 bg-primary/[0.08] text-primary ring-1 ring-primary/30' : 'border-border/60 text-muted-foreground hover:text-foreground'}"
      >Nginx</button>
      <button
        type="button"
        onclick={() => (snippetKind = 'caddy')}
        class="rounded-lg border px-3 py-1 text-[12px] font-medium transition-[color,background-color,border-color] {snippetKind === 'caddy' ? 'border-primary/50 bg-primary/[0.08] text-primary ring-1 ring-primary/30' : 'border-border/60 text-muted-foreground hover:text-foreground'}"
      >Caddy</button>
    </div>
    <pre class="max-h-72 overflow-auto rounded-lg border border-border/60 bg-foreground/[0.03] p-3.5 font-mono text-[11.5px] leading-relaxed">{snippetKind === 'nginx' ? nginxSnippet : caddySnippet}</pre>
    <Dialog.Footer class="gap-2 sm:justify-end">
      <Button variant="outline" onclick={() => { navigator.clipboard.writeText(snippetKind === 'nginx' ? nginxSnippet : caddySnippet); toast.success('已复制到剪贴板'); }}>复制</Button>
      <Button variant="outline" onclick={() => (snippetOpen = false)}>关闭</Button>
    </Dialog.Footer>
  </Dialog.Content>
</Dialog.Root>

<!-- Caddyfile 管理：直接编辑完整文件，保存时校验语法并自动重载 -->
<Dialog.Root bind:open={cfOpen}>
  <Dialog.Content class="max-h-[92vh] overflow-y-auto sm:max-w-2xl">
    <Dialog.Header>
      <Dialog.Title class="flex items-center gap-2">
        <FileCode2 size={15} class="text-primary" />
        编辑 Caddyfile
        <Badge variant="outline" class="ml-1 bg-muted font-mono text-[10px] font-normal text-muted-foreground ring-border">{cfPath}</Badge>
      </Dialog.Title>
      <Dialog.Description>直接修改完整文件，保存时自动校验语法并重载 Caddy（约 2 秒生效）。</Dialog.Description>
    </Dialog.Header>

    <!-- 管理范围说明 -->
    <div class="rounded-lg border border-info/30 bg-info/[0.05] px-3.5 py-2.5 text-[11.5px] leading-relaxed text-muted-foreground">
      <p><span class="font-medium text-foreground">谁管理哪部分：</span>
        每个域名的站点块由面板根据「添加域名」自动生成——在面板增删域名会重写这些块；
        <span class="font-medium text-foreground">{'{ }'}</span> 全局块里的
        <code class="rounded bg-foreground/[0.06] px-1 font-mono">email</code>
        行是证书通知邮箱，直接改这里即可。</p>
      <p class="mt-1"><span class="font-medium text-foreground">「# ==== 自定义配置 ====」</span>
        注释以下的内容<span class="font-medium text-foreground">永远不会被面板修改</span>——反向代理之外的站点、snippets 都写在那里。</p>
    </div>

    {#if cfLoading}
      <div class="h-72 animate-pulse rounded-lg bg-foreground/[0.04]"></div>
    {:else}
      <textarea
        class="h-96 w-full resize-none rounded-lg border border-input bg-card p-3.5 font-mono text-[12px] leading-relaxed outline-none focus:ring-2 focus:ring-ring/40"
        spellcheck="false"
        bind:value={cfEdit}
      ></textarea>

      <div class="flex flex-wrap items-center gap-2">
        <Button size="sm" class="h-8 text-[12px]" disabled={!cfDirty || cfSaving} onclick={saveCaddyfileEdit}>
          {cfSaving ? '校验并保存中…' : cfDirty ? '保存并重载' : '已保存'}
        </Button>
        <Button size="sm" variant="outline" class="h-8 text-[12px]" disabled={!cfDirty} onclick={() => (cfEdit = cfText)} title="放弃手改，回到面板生成的版本">
          恢复面板生成版本
        </Button>
        <span class="text-[10.5px] text-muted-foreground">
          {#if cfDirty}有未保存修改{:else}与服务器一致{/if}
          · 语法错误会在保存时提示且不生效
        </span>
      </div>
    {/if}

    <Dialog.Footer class="gap-2 sm:justify-end">
      <Button variant="outline" onclick={() => { navigator.clipboard.writeText(cfEdit); toast.success('已复制到剪贴板'); }}>
        <Copy size={13} /> 复制
      </Button>
      <Button variant="outline" disabled={!cfEdit} onclick={downloadCaddyfile}>
        <Download size={13} /> 导出
      </Button>
      <Button variant="outline" onclick={() => (cfOpen = false)}>关闭</Button>
    </Dialog.Footer>
  </Dialog.Content>
</Dialog.Root>
