<script lang="ts">
  import { ShieldAlert, Plus, Trash2, Ban, Timer, Crosshair, Zap, Rocket, CheckCircle2, Pencil } from '@lucide/svelte';
  import { Card } from '$lib/components/ui/card';
  import { Button } from '$lib/components/ui/button';
  import { Input } from '$lib/components/ui/input';
  import { Badge } from '$lib/components/ui/badge';
  import { Label } from '$lib/components/ui/label';
  import { Switch } from '$lib/components/ui/switch';
  import * as Dialog from '$lib/components/ui/dialog';
  import * as Select from '$lib/components/ui/select';
  import { toast } from 'svelte-sonner';
  import EmptyState from '$lib/components/common/EmptyState.svelte';
  import { backend, onWSMessage, type JailConfig, type IPListEntry } from '$lib/api';
  import { fmtDateTime } from '$lib/utils';

  let jails = $state<(JailConfig & { hits: Record<string, number> })[]>([]);
  let banned = $state<IPListEntry[]>([]);
  let loading = $state(true);
  let enabling = $state<Record<string, boolean>>({});

  async function load() {
    loading = true;
    try {
      const r = await backend.jails();
      jails = r.items;
      banned = r.banned;
    } catch (e) {
      toast.error(e instanceof Error ? e.message : String(e));
    } finally {
      loading = false;
    }
  }
  $effect(() => {
    load();
    return onWSMessage((topic, payload) => {
      if (topic === 'events' && String((payload as { type?: string }).type) === 'jail_ban') load();
    });
  });

  /* ---------- 内置方案库（固定名称，按名称判定启用状态） ---------- */
  interface Builtin {
    key: string;
    name: string;
    desc: string;
    accent: string;
    source_type: JailConfig['source_type'];
    unit: string;
    log_path: string;
    match_regex: string;
    threshold: number;
    find_time: number;
    ban_time: number;
  }
  const SSH_RE = '(Failed password for|Invalid user).* from ([0-9]{1,3}([.][0-9]{1,3}){3})';
  const NGINX_RE = '^([0-9]{1,3}(?:\\.[0-9]{1,3}){3}) - \\S+ \\S+ \\[[^\\]]+\\] "[^"]*" 40[13] ';
  const BUILTINS: Builtin[] = [
    {
      key: 'sshd', name: 'SSH 爆破防护', accent: 'primary',
      desc: '监听 journald 的 ssh 单元，密码爆破达标即封禁（推荐常开）',
      source_type: 'journald', unit: 'ssh', log_path: '', match_regex: SSH_RE,
      threshold: 5, find_time: 600, ban_time: 1800,
    },
    {
      key: 'sshd-strict', name: 'SSH 严格防护', accent: 'destructive',
      desc: '更激进的阈值与 24 小时长封禁，适合暴露公网的服务器',
      source_type: 'journald', unit: 'ssh', log_path: '', match_regex: SSH_RE,
      threshold: 3, find_time: 300, ban_time: 86400,
    },
    {
      key: 'panel', name: '面板登录爆破防护', accent: 'info',
      desc: '统计面板自身登录失败（来自审计日志），保护管理入口',
      source_type: 'audit', unit: '', log_path: '', match_regex: '',
      threshold: 5, find_time: 600, ban_time: 3600,
    },
    {
      key: 'web401', name: 'Web 401/403 防护', accent: 'warning',
      desc: '解析 Nginx 访问日志中的 401/403（需已安装 Nginx 且路径存在）',
      source_type: 'file', unit: '', log_path: '/var/log/nginx/access.log', match_regex: NGINX_RE,
      threshold: 20, find_time: 60, ban_time: 600,
    },
  ];

  const enabledByName = $derived(new Map(jails.map((j) => [j.name, j])));

  /* ---------- 内置方案模态框 ---------- */
  let builtinOpen = $state(false);

  async function enableBuiltin(b: Builtin) {
    if (enabling[b.key]) return;
    enabling[b.key] = true;
    try {
      await backend.createJail({
        name: b.name, source_type: b.source_type, unit: b.unit, log_path: b.log_path,
        match_regex: b.match_regex, threshold: b.threshold, find_time: b.find_time,
        ban_time: b.ban_time,
        ignore_cidrs: '192.168.0.0/16,10.0.0.0/8,172.16.0.0/12,127.0.0.0/8',
        enabled: true, remark: `内置方案：${b.desc}`,
      });
      await load();
      toast.success(`已启用「${b.name}」`, { description: 'Jail 开始监听，达到阈值自动封禁' });
    } catch (e) {
      toast.error(e instanceof Error ? e.message : String(e));
    } finally {
      enabling[b.key] = false;
    }
  }

  /* ---------- 我的 Jail（自定义） ---------- */
  let createOpen = $state(false);
  let editing = $state<JailConfig | null>(null);
  let form = $state({
    name: '', source_type: 'journald' as JailConfig['source_type'],
    log_path: '', unit: 'ssh', match_regex: SSH_RE,
    threshold: 5, find_time: 600, ban_time: 1800,
    ignore_cidrs: '192.168.0.0/16,10.0.0.0/8,172.16.0.0/12,127.0.0.0/8', remark: '',
  });
  let preset = $state('sshd');
  let submitting = $state(false);
  const valid = $derived(
    form.name.trim() !== '' &&
      (form.source_type !== 'file' || form.log_path.trim() !== '') &&
      form.threshold > 0 && form.find_time >= 5 && form.ban_time >= 5,
  );

  const PRESETS = [
    { value: 'sshd', label: 'SSH 爆破（journald）', source: 'journald', unit: 'ssh', regex: SSH_RE, threshold: 5, find_time: 600, ban_time: 1800 },
    { value: 'panel', label: '面板登录爆破（审计）', source: 'audit', unit: '', regex: '', threshold: 5, find_time: 600, ban_time: 3600 },
    { value: 'custom', label: '自定义日志文件', source: 'file', unit: '', regex: 'client ([0-9]{1,3}(?:\\.[0-9]{1,3}){3})', threshold: 10, find_time: 60, ban_time: 600 },
  ];
  const sourceLabel: Record<string, string> = { journald: 'journald', audit: '面板审计', file: '日志文件' };

  function applyPreset(v: string) {
    preset = v;
    const p = PRESETS.find((x) => x.value === v);
    if (!p) return;
    form.source_type = p.source as JailConfig['source_type'];
    form.unit = p.unit;
    form.match_regex = p.regex;
    form.threshold = p.threshold;
    form.find_time = p.find_time;
    form.ban_time = p.ban_time;
  }

  function openCreate() {
    editing = null;
    preset = 'sshd';
    applyPreset('sshd');
    form.name = '';
    form.log_path = '';
    form.ignore_cidrs = '192.168.0.0/16,10.0.0.0/8,172.16.0.0/12,127.0.0.0/8';
    form.remark = '';
    createOpen = true;
  }

  function openEdit(j: JailConfig) {
    editing = j;
    form = {
      name: j.name, source_type: j.source_type, log_path: j.log_path, unit: j.unit,
      match_regex: j.match_regex, threshold: j.threshold, find_time: j.find_time,
      ban_time: j.ban_time, ignore_cidrs: j.ignore_cidrs, remark: j.remark,
    };
    createOpen = true;
  }

  async function submit() {
    if (!valid || submitting) return;
    submitting = true;
    try {
      const payload = { ...form, enabled: editing ? editing.enabled : true };
      if (editing) await backend.updateJail(editing.id, payload);
      else await backend.createJail(payload);
      createOpen = false;
      await load();
      toast.success(editing ? 'Jail 已更新' : 'Jail 已创建并开始监听');
    } catch (e) {
      toast.error(e instanceof Error ? e.message : String(e));
    } finally {
      submitting = false;
    }
  }

  async function toggle(j: JailConfig & { hits: Record<string, number> }, enabled: boolean) {
    try {
      await backend.updateJail(j.id, { ...j, enabled });
      await load();
      toast[enabled ? 'success' : 'info'](`Jail「${j.name}」已${enabled ? '启用' : '停用'}`);
    } catch (e) {
      toast.error(e instanceof Error ? e.message : String(e));
    }
  }

  async function removeJail(j: JailConfig) {
    try {
      await backend.deleteJail(j.id);
      await load();
      toast.success(`Jail「${j.name}」已删除`);
    } catch (e) {
      toast.error(e instanceof Error ? e.message : String(e));
    }
  }

  async function unban(e: IPListEntry) {
    try {
      await backend.unbanIP(e.id);
      await load();
      toast.success(`${e.ip_or_cidr} 已解封`);
    } catch (err) {
      toast.error(err instanceof Error ? err.message : String(err));
    }
  }

  function remainOf(e: IPListEntry): string {
    if (!e.expires_at) return '—';
    const ms = new Date(e.expires_at.replace(' ', 'T')).getTime() - Date.now();
    if (ms <= 0) return '到期中';
    const s = Math.floor(ms / 1000);
    return s > 90 ? `${Math.floor(s / 60)} 分钟` : `${s} 秒`;
  }

  function hitSummary(j: JailConfig & { hits: Record<string, number> }): string {
    const entries = Object.entries(j.hits);
    if (entries.length === 0) return '暂无命中';
    return entries.slice(0, 3).map(([ip, n]) => `${ip}(${n})`).join(' ') + (entries.length > 3 ? ` +${entries.length - 3}` : '');
  }

  const accentCls: Record<string, string> = {
    primary: 'bg-primary/10 text-primary',
    destructive: 'bg-destructive/10 text-destructive',
    info: 'bg-info/10 text-info',
    warning: 'bg-warning/15 text-warning',
  };
</script>

<div class="animate-fade-up space-y-4">
  <!-- 当前封禁 -->
  {#if banned.length > 0}
    <Card class="border-destructive/30 p-4 shadow-soft">
      <h2 class="mb-3 flex items-center gap-2 text-sm font-semibold text-destructive">
        <Ban size={15} />
        当前封禁（到期自动解封）
      </h2>
      <div class="overflow-x-auto">
        <table class="w-full text-left text-[12.5px]">
          <thead>
            <tr class="border-b border-border/60 text-[11px] text-muted-foreground">
              <th class="py-2 pr-3 font-medium">IP</th>
              <th class="py-2 pr-3 font-medium">Jail</th>
              <th class="py-2 pr-3 font-medium">封禁至</th>
              <th class="py-2 pr-3 font-medium">剩余</th>
              <th class="py-2 pr-1 text-right font-medium">操作</th>
            </tr>
          </thead>
          <tbody>
            {#each banned as e (e.id)}
              <tr class="border-b border-border/30 last:border-0">
                <td class="py-2 pr-3 font-mono font-medium">{e.ip_or_cidr}</td>
                <td class="py-2 pr-3 text-muted-foreground">{(e.source ?? '').replace('jail:', '')}</td>
                <td class="py-2 pr-3 text-muted-foreground">{fmtDateTime(e.expires_at)}</td>
                <td class="font-num py-2 pr-3">{remainOf(e)}</td>
                <td class="py-2 pr-1 text-right">
                  <Button variant="outline" size="sm" class="h-6 px-2 text-[11px]" onclick={() => unban(e)}>解封</Button>
                </td>
              </tr>
            {/each}
          </tbody>
        </table>
      </div>
    </Card>
  {/if}

  <!-- 我的 Jail -->
  <div>
    <div class="mb-2.5 flex items-center gap-2.5">
      <h2 class="text-sm font-semibold">我的 Jail</h2>
      <span class="font-num text-[11.5px] text-muted-foreground">{jails.length} 个</span>
      <Button variant="outline" size="sm" class="ml-auto h-8" onclick={() => (builtinOpen = true)}>
        <Rocket size={14} />
        启用内置防护方案
      </Button>
      <Button size="sm" class="h-8" onclick={openCreate}><Plus size={14} /> 自定义 Jail</Button>
    </div>

    {#if loading}
      <div class="space-y-2.5">
        {#each Array(2) as _, i (i)}
          <div class="h-16 animate-pulse rounded-xl bg-foreground/[0.04]"></div>
        {/each}
      </div>
    {:else if jails.length === 0}
      <EmptyState
        title="尚未启用任何 Jail"
        desc="从内置防护方案一键启用（SSH 爆破 / 面板登录 / Web 401-403），或创建自定义 Jail。"
      >
        {#snippet icon()}<ShieldAlert size={22} />{/snippet}
        {#snippet action()}
          <Button variant="outline" onclick={() => (builtinOpen = true)}>
            <Rocket size={14} /> 启用内置防护方案
          </Button>
          <Button onclick={openCreate}><Plus size={16} /> 自定义 Jail</Button>
        {/snippet}
      </EmptyState>
    {:else}
      <div class="space-y-2.5">
        {#each jails as j (j.id)}
          <Card class="lift-card group p-4">
            <div class="flex flex-wrap items-center gap-x-6 gap-y-3">
              <div class="min-w-[160px] flex-1 basis-40">
                <span class="text-[13.5px] font-medium">{j.name}</span>
                <p class="mt-0.5 text-[11.5px] text-muted-foreground">
                  {sourceLabel[j.source_type]}{j.unit ? ` · ${j.unit}` : ''}{j.log_path ? ` · ${j.log_path}` : ''}
                </p>
              </div>
              <div class="flex items-center gap-1.5 text-[12px] text-muted-foreground">
                <Crosshair size={13} class="text-destructive/70" />
                <span class="font-num font-medium text-foreground">{j.threshold}</span> 次 /
                <span class="font-num">{j.find_time}s</span>
                <span class="mx-1 text-border">|</span>
                <Timer size={13} class="text-warning" />
                封 <span class="font-num font-medium text-foreground">{j.ban_time}s</span>
              </div>
              <div class="hidden min-w-[180px] text-[11.5px] text-muted-foreground lg:block">
                命中：{hitSummary(j)}
              </div>
              <div class="ml-auto flex items-center gap-2.5">
                <Switch checked={j.enabled} onCheckedChange={(v) => toggle(j, v)} />
                <Button variant="ghost" size="icon" class="size-7 text-muted-foreground" onclick={() => openEdit(j)} aria-label="编辑">
                  <Pencil size={13} />
                </Button>
                <Button variant="ghost" size="icon" class="size-7 text-muted-foreground opacity-0 transition-opacity hover:text-destructive group-hover:opacity-100" onclick={() => removeJail(j)} aria-label="删除">
                  <Trash2 size={13} />
                </Button>
              </div>
            </div>
          </Card>
        {/each}
      </div>
    {/if}
  </div>
</div>

<!-- 启用内置防护方案 -->
<Dialog.Root bind:open={builtinOpen}>
  <Dialog.Content class="max-h-[86vh] overflow-y-auto sm:max-w-xl">
    <Dialog.Header>
      <Dialog.Title class="flex items-center gap-2">
        <Rocket size={16} class="text-primary" />
        启用内置防护方案
      </Dialog.Title>
      <Dialog.Description>
        一键启用的方案会出现在「我的 Jail」中，参数可随时调整。
      </Dialog.Description>
    </Dialog.Header>

    <div class="grid gap-2.5">
      {#each BUILTINS as b (b.key)}
        {@const enabledJail = enabledByName.get(b.name)}
        <div
          class="flex items-start gap-3.5 rounded-xl border p-3.5 transition-[color,background-color,border-color,box-shadow,transform,opacity] duration-150
            {enabledJail ? 'border-success/30 bg-success/[0.04]' : 'border-border/70 bg-card hover:border-primary/30 hover:bg-primary/[0.02]'}"
        >
          <span class="flex h-9 w-9 shrink-0 items-center justify-center rounded-lg {accentCls[b.accent]}">
            <ShieldAlert size={16} />
          </span>
          <div class="min-w-0 flex-1">
            <div class="flex items-center gap-2">
              <span class="text-[13.5px] font-semibold">{b.name}</span>
              {#if enabledJail}
                <Badge variant="outline" class="gap-1 bg-success/10 text-[10px] text-success ring-success/20">
                  <CheckCircle2 size={10} /> 已启用
                </Badge>
              {/if}
            </div>
            <p class="mt-0.5 text-[11.5px] leading-relaxed text-muted-foreground">{b.desc}</p>
            <div class="mt-1.5 flex items-center gap-1.5 text-[10.5px] text-muted-foreground">
              <Zap size={11} class="text-warning" />
              <span class="font-num font-medium text-foreground">{b.threshold}</span> 次/
              <span class="font-num">{b.find_time}s</span>
              <span class="mx-0.5 text-border">·</span>
              <Timer size={11} class="text-info" />
              封 <span class="font-num font-medium text-foreground">
                {b.ban_time >= 3600 ? `${b.ban_time / 3600} 小时` : `${b.ban_time}s`}
              </span>
              {#if b.log_path}
                <span class="ml-1 truncate font-mono">{b.log_path}</span>
              {/if}
            </div>
          </div>
          <div class="shrink-0 self-center">
            {#if enabledJail}
              <Button variant="outline" size="sm" class="h-7 text-[11.5px]" onclick={() => { builtinOpen = false; openEdit(enabledJail); }}>
                调整
              </Button>
            {:else}
              <Button size="sm" class="h-7 min-w-[64px] text-[11.5px]" disabled={enabling[b.key]} onclick={() => enableBuiltin(b)}>
                {enabling[b.key] ? '启用中…' : '开启'}
              </Button>
            {/if}
          </div>
        </div>
      {/each}
    </div>

    <Dialog.Footer class="sm:justify-start">
      <p class="text-[11px] leading-relaxed text-muted-foreground">
        默认忽略私网/回环网段防止误封管理地址；启用后可在「我的 Jail」中调整阈值、窗口与封禁时长。
      </p>
    </Dialog.Footer>
  </Dialog.Content>
</Dialog.Root>

<!-- 自定义 Jail -->
<Dialog.Root bind:open={createOpen}>
  <Dialog.Content class="max-h-[88vh] overflow-y-auto sm:max-w-lg">
    <Dialog.Header>
      <Dialog.Title>{editing ? '编辑 Jail' : '自定义 Jail'}</Dialog.Title>
      <Dialog.Description>窗口内失败达到阈值即自动封禁，到期由面板自动解封。</Dialog.Description>
    </Dialog.Header>
    <div class="grid gap-3.5">
      {#if !editing}
        <div class="grid gap-2">
          <Label>模板</Label>
          <Select.Root type="single" value={preset} onValueChange={(v) => applyPreset(v)}>
            <Select.Trigger class="w-full">{PRESETS.find((p) => p.value === preset)?.label}</Select.Trigger>
            <Select.Content>
              {#each PRESETS as p (p.value)}
                <Select.Item value={p.value}>{p.label}</Select.Item>
              {/each}
            </Select.Content>
          </Select.Root>
        </div>
      {/if}

      <div class="grid grid-cols-2 gap-3">
        <div class="grid gap-2">
          <Label for="j-name">名称</Label>
          <Input id="j-name" placeholder="sshd 爆破防护" bind:value={form.name} />
        </div>
        <div class="grid gap-2">
          <Label for="j-unit">journald 单元 {#if form.source_type !== 'journald'}<span class="text-muted-foreground/60">（仅 journald）</span>{/if}</Label>
          <Input id="j-unit" placeholder="ssh" bind:value={form.unit} class="font-mono" disabled={form.source_type !== 'journald'} />
        </div>
      </div>

      {#if form.source_type === 'file'}
        <div class="grid gap-2">
          <Label for="j-path">日志文件路径</Label>
          <Input id="j-path" placeholder="/var/log/nginx/access.log" bind:value={form.log_path} class="font-mono" />
        </div>
      {/if}

      {#if form.source_type !== 'audit'}
        <div class="grid gap-2">
          <Label for="j-regex">匹配正则（捕获 IP 的组会被采用）</Label>
          <Input id="j-regex" bind:value={form.match_regex} class="font-mono text-[12px]" />
        </div>
      {/if}

      <div class="grid grid-cols-3 gap-3">
        <div class="grid gap-2">
          <Label for="j-threshold">阈值（次）</Label>
          <Input id="j-threshold" type="number" min="1" bind:value={form.threshold} class="font-num" />
        </div>
        <div class="grid gap-2">
          <Label for="j-find">窗口（秒）</Label>
          <Input id="j-find" type="number" min="5" bind:value={form.find_time} class="font-num" />
        </div>
        <div class="grid gap-2">
          <Label for="j-ban">封禁（秒）</Label>
          <Input id="j-ban" type="number" min="5" bind:value={form.ban_time} class="font-num" />
        </div>
      </div>

      <div class="grid gap-2">
        <Label for="j-ignore">忽略网段 <span class="text-muted-foreground/70">（逗号分隔 CIDR，防误封管理地址）</span></Label>
        <Input id="j-ignore" bind:value={form.ignore_cidrs} class="font-mono text-[12px]" />
      </div>
    </div>
    <Dialog.Footer class="gap-2 sm:justify-end">
      <Button variant="outline" onclick={() => (createOpen = false)}>取消</Button>
      <Button disabled={!valid || submitting} onclick={submit}>{submitting ? '创建中…' : editing ? '保存' : '创建并监听'}</Button>
    </Dialog.Footer>
  </Dialog.Content>
</Dialog.Root>
