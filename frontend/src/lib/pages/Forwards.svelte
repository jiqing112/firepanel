<script lang="ts">
  import { Search, Plus, MoreHorizontal, Pencil, Trash2, ArrowRight, Globe, ArrowDownToLine, ArrowUpFromLine, TriangleAlert } from '@lucide/svelte';
  import { fade } from 'svelte/transition';
  import { Card } from '$lib/components/ui/card';
  import { Button } from '$lib/components/ui/button';
  import { Input } from '$lib/components/ui/input';
  import { Switch } from '$lib/components/ui/switch';
  import { Badge } from '$lib/components/ui/badge';
  import * as Select from '$lib/components/ui/select';
  import * as DropdownMenu from '$lib/components/ui/dropdown-menu';
  import * as Dialog from '$lib/components/ui/dialog';
  import * as RadioGroup from '$lib/components/ui/radio-group';
  import { Label } from '$lib/components/ui/label';
  import { toast } from 'svelte-sonner';
  import EmptyState from '$lib/components/common/EmptyState.svelte';
  import ProtoBadge from '$lib/components/common/ProtoBadge.svelte';
  import StatusDot from '$lib/components/common/StatusDot.svelte';
  import DangerDialog from '$lib/components/common/DangerDialog.svelte';
  import { backend, type SyncResult } from '$lib/api';
  import type { ForwardRule, Protocol } from '$lib/types';
  import { fmtBytes, fmtNum, relativeTime } from '$lib/utils';

  let rules = $state<ForwardRule[]>([]);
  let loading = $state(true);
  let keyword = $state('');
  let protoFilter = $state<Protocol | 'all'>('all');

  let createOpen = $state(false);
  let danger = $state<{ sync: SyncResult } | null>(null);

  async function load() {
    loading = true;
    try {
      rules = (await backend.forwards()).items;
    } catch (e) {
      toast.error(e instanceof Error ? e.message : String(e));
    } finally {
      loading = false;
    }
  }

  $effect(() => {
    load();
  });

  const filtered = $derived.by(() => {
    const kw = keyword.trim().toLowerCase();
    return rules.filter((r) => {
      if (protoFilter !== 'all' && r.protocol !== protoFilter) return false;
      if (!kw) return true;
      return (
        r.name.toLowerCase().includes(kw) ||
        r.listen_port.includes(kw) ||
        r.dst_ip.toLowerCase().includes(kw) ||
        (r.dst_domain ?? '').toLowerCase().includes(kw)
      );
    });
  });

  // 命中计数映射（按 tag 前缀 fw:<id>，含包数与字节数）
  function statsOf(id: number): { packets: number; bytes: number } {
    return ruleStats[id] ?? { packets: 0, bytes: 0 };
  }
  let ruleStats = $state<Record<number, { packets: number; bytes: number }>>({});
  async function loadHits() {
    try {
      const { snapshot } = await backend.firewallRules();
      const map: Record<number, { packets: number; bytes: number }> = {};
      for (const r of snapshot.rules) {
        const m = r.tag.match(/^fp:fw:(\d+):/);
        if (m) {
          const id = +m[1];
          map[id] = map[id] || { packets: 0, bytes: 0 };
          map[id].packets += r.counter.packets;
          map[id].bytes += r.counter.bytes;
        }
      }
      ruleStats = map;
    } catch {
      /* 静默 */
    }
  }
  $effect(() => {
    loadHits();
  });

  async function toggleRule(rule: ForwardRule, enabled: boolean) {
    try {
      const { sync } = await backend.toggleForward(rule.id, enabled);
      rule.enabled = enabled;
      handleSync(sync, `规则「${rule.name}」已${enabled ? '启用' : '停用'}`);
    } catch (e) {
      toast.error(e instanceof Error ? e.message : String(e));
    }
  }

  async function removeRule(rule: ForwardRule) {
    try {
      const { sync } = await backend.deleteForward(rule.id);
      rules = rules.filter((r) => r.id !== rule.id);
      handleSync(sync, `规则「${rule.name}」已删除`);
    } catch (e) {
      toast.error(e instanceof Error ? e.message : String(e));
    }
  }

  function handleSync(sync: SyncResult | undefined, okMsg: string) {
    if (!sync) {
      toast.success(okMsg);
      return;
    }
    if (sync.danger) {
      danger = { sync };
    } else {
      toast.success(okMsg, { description: '已同步到内核' });
    }
  }

  /* ------- 新建/编辑表单 ------- */
  let editing = $state<ForwardRule | null>(null);
  let form = $state({
    name: '',
    protocol: 'tcp' as Protocol,
    listen_port: '',
    dst_type: 'ip' as 'ip' | 'domain',
    dst_ip: '',
    dst_domain: '',
    dst_port: '',
    src_ip: '',
    remark: '',
  });
  let submitting = $state(false);

  function openCreate() {
    editing = null;
    form = { name: '', protocol: 'tcp', listen_port: '', dst_type: 'ip', dst_ip: '', dst_domain: '', dst_port: '', src_ip: '', remark: '' };
    createOpen = true;
  }

  function openEdit(rule: ForwardRule) {
    editing = rule;
    form = {
      name: rule.name,
      protocol: rule.protocol,
      listen_port: rule.listen_port,
      dst_type: rule.dst_domain ? 'domain' : 'ip',
      dst_ip: rule.dst_ip,
      dst_domain: rule.dst_domain ?? '',
      dst_port: rule.dst_port,
      src_ip: rule.src_ip,
      remark: rule.remark ?? '',
    };
    createOpen = true;
  }

  const portValid = $derived(/^\d{1,5}(-\d{1,5})?$/.test(form.listen_port.trim()));
  const dstValid = $derived(
    form.dst_type === 'ip'
      ? form.dst_ip.trim().length > 6
      : /^[a-z0-9]([a-z0-9-]*[a-z0-9])?(\.[a-z0-9]([a-z0-9-]*[a-z0-9])?)+$/i.test(form.dst_domain.trim()),
  );
  const formValid = $derived(form.name.trim() !== '' && portValid && dstValid && /^\d{1,5}$/.test(form.dst_port.trim()));

  async function submit() {
    if (!formValid || submitting) return;
    submitting = true;
    const payload = {
      name: form.name.trim(),
      protocol: form.protocol,
      listen_port: form.listen_port.trim(),
      src_ip: form.src_ip.trim(),
      dst_ip: form.dst_type === 'ip' ? form.dst_ip.trim() : '',
      dst_domain: form.dst_type === 'domain' ? form.dst_domain.trim() : '',
      dst_port: form.dst_port.trim(),
      enabled: true,
      remark: form.remark.trim(),
    };
    try {
      const { sync } = editing
        ? await backend.updateForward(editing.id, payload)
        : await backend.createForward(payload);
      createOpen = false;
      await load();
      handleSync(sync, editing ? '规则已更新' : '转发规则已创建');
    } catch (e) {
      toast.error(e instanceof Error ? e.message : String(e));
    } finally {
      submitting = false;
    }
  }
</script>

<div class="animate-fade-up space-y-4">
  <div class="flex flex-wrap items-center gap-2.5">
    <div class="relative min-w-[220px] flex-1 sm:max-w-xs">
      <Search size={15} class="pointer-events-none absolute top-1/2 left-3 -translate-y-1/2 text-muted-foreground" />
      <Input placeholder="搜索规则名称、端口或目标…" bind:value={keyword} class="pl-9" />
    </div>

    <Select.Root type="single" bind:value={protoFilter}>
      <Select.Trigger class="w-[130px]">
        {protoFilter === 'all' ? '全部协议' : protoFilter.toUpperCase()}
      </Select.Trigger>
      <Select.Content>
        <Select.Item value="all">全部协议</Select.Item>
        <Select.Item value="tcp">TCP</Select.Item>
        <Select.Item value="udp">UDP</Select.Item>
        <Select.Item value="both">TCP+UDP</Select.Item>
      </Select.Content>
    </Select.Root>

    <span class="font-num ml-auto hidden text-xs text-muted-foreground sm:block">
      共 {filtered.length} 条规则
    </span>

    <Button onclick={openCreate}>
      <Plus size={16} />
      新增转发
    </Button>
  </div>

  {#if loading}
    <div class="space-y-2.5">
      {#each Array(5) as _, i (i)}
        <div class="h-[64px] animate-pulse rounded-xl bg-foreground/[0.04]"></div>
      {/each}
    </div>
  {:else if filtered.length === 0}
    {#if keyword || protoFilter !== 'all'}
      <EmptyState compact title="没有匹配的规则" desc="换个关键词，或清除筛选条件试试。">
        {#snippet icon()}<Search size={20} />{/snippet}
        {#snippet action()}
          <Button variant="outline" size="sm" onclick={() => { keyword = ''; protoFilter = 'all'; }}>清除筛选</Button>
        {/snippet}
      </EmptyState>
    {:else}
      <EmptyState
        title="还没有端口转发规则"
        desc="创建第一条规则，将公网端口的流量转发到内网服务。支持 TCP / UDP / 双栈，以及域名目标自动解析。"
      >
        {#snippet icon()}<Globe size={22} />{/snippet}
        {#snippet action()}
          <Button onclick={openCreate}><Plus size={16} /> 新增转发</Button>
        {/snippet}
      </EmptyState>
    {/if}
  {:else}
    <div class="space-y-2.5" out:fade={{ duration: 120 }}>
      {#each filtered as rule (rule.id)}
        <Card class="lift-card group p-4">
          <div class="flex flex-wrap items-center gap-x-6 gap-y-3">
            <!-- 名称 + 备注/来源限制 -->
            <div class="min-w-[180px] flex-1 basis-48">
              <div class="flex flex-wrap items-center gap-2">
                <span class="text-[13.5px] font-medium">{rule.name}</span>
                {#if rule.dst_domain}
                  <Badge variant="outline" class="bg-info/10 text-[10px] text-info ring-info/20">DDNS</Badge>
                {/if}
                {#if rule.expires_at}
                  <Badge variant="outline" class="bg-warning/10 text-[10px] text-warning ring-warning/25">临时</Badge>
                {/if}
              </div>
              {#if rule.remark}
                <p class="mt-0.5 truncate text-[11.5px] text-muted-foreground">{rule.remark}</p>
              {:else if rule.src_ip}
                <p class="mt-0.5 truncate text-[11px] text-muted-foreground/70">
                  <span class="font-mono">{rule.src_ip}</span> · 仅允许该来源
                </p>
              {:else}
                <p class="mt-0.5 text-[11px] text-muted-foreground/50">创建于 {relativeTime(rule.created_at)}</p>
              {/if}
            </div>

            <!-- 流向 -->
            <div class="flex min-w-[250px] flex-1 basis-64 items-center gap-2.5 text-[12.5px]">
              <span class="font-mono"><span class="text-muted-foreground">:</span>{rule.listen_port}</span>
              <span class="text-muted-foreground/60"><ArrowRight size={13} /></span>
              <span class="font-mono truncate {rule.enabled ? 'text-foreground' : 'text-muted-foreground'}">
                {rule.dst_domain || rule.dst_ip}{rule.dst_port ? `:${rule.dst_port}` : ''}
              </span>
              <ProtoBadge protocol={rule.protocol} />
            </div>

            <!-- 流量：命中包数 + 字节数（内核计数器） -->
            <div class="hidden min-w-[120px] text-right md:block">
              <div class="font-num text-[12.5px] font-medium">{fmtNum(statsOf(rule.id).packets)} <span class="text-[10.5px] font-normal text-muted-foreground">包</span></div>
              <div class="font-num text-[10.5px] text-muted-foreground">{fmtBytes(statsOf(rule.id).bytes)}</div>
            </div>

            <!-- 创建时间 -->
            <div class="hidden min-w-[76px] text-right text-[10.5px] text-muted-foreground lg:block">
              {relativeTime(rule.created_at)}
            </div>

            <div class="flex items-center gap-2.5">
              <Switch checked={rule.enabled} onCheckedChange={(v) => toggleRule(rule, v)} />
              <StatusDot state={rule.enabled ? 'active' : 'disabled'} />
            </div>

            <DropdownMenu.Root>
              <DropdownMenu.Trigger>
                {#snippet child({ props })}
                  <Button {...props} variant="ghost" size="icon" class="size-8 text-muted-foreground">
                    <MoreHorizontal size={16} />
                  </Button>
                {/snippet}
              </DropdownMenu.Trigger>
              <DropdownMenu.Content align="end" class="w-36">
                <DropdownMenu.Item onclick={() => openEdit(rule)}>
                  <Pencil size={14} /> 编辑规则
                </DropdownMenu.Item>
                <DropdownMenu.Separator />
                <DropdownMenu.Item variant="destructive" onclick={() => removeRule(rule)}>
                  <Trash2 size={14} /> 删除
                </DropdownMenu.Item>
              </DropdownMenu.Content>
            </DropdownMenu.Root>
          </div>
        </Card>
      {/each}
    </div>
  {/if}
</div>

<!-- 新建/编辑模态框 -->
<Dialog.Root bind:open={createOpen}>
  <Dialog.Content class="max-h-[88vh] overflow-y-auto sm:max-w-lg">
    <Dialog.Header>
      <Dialog.Title>{editing ? '编辑转发规则' : '新增端口转发'}</Dialog.Title>
      <Dialog.Description>
        配置后将自动写入内核并立即生效；涉及 SSH 端口的规则需要二次确认。
      </Dialog.Description>
    </Dialog.Header>

    <div class="flex flex-1 flex-col gap-4.5">
      <div class="grid gap-2">
        <Label for="f-name">规则名称</Label>
        <Input id="f-name" placeholder="例如：Web 服务" bind:value={form.name} />
      </div>

      <div class="grid gap-2">
        <Label>协议</Label>
        <RadioGroup.Root
          value={form.protocol}
          onValueChange={(v) => (form.protocol = v as Protocol)}
          class="grid grid-cols-3 gap-2"
        >
          {#each ['tcp', 'udp', 'both'] as const as p}
            {@const id = `proto-${p}`}
            <Label
              for={id}
              class="flex cursor-pointer items-center gap-2 rounded-lg border px-3 py-2.5 transition-colors duration-150
                {form.protocol === p ? 'border-primary/50 bg-primary/[0.04]' : 'bg-card hover:bg-muted/60'}"
            >
              <RadioGroup.Item value={p} id={id} />
              <span class="font-mono text-xs font-medium {form.protocol === p ? 'text-foreground' : 'text-muted-foreground'}">
                {p === 'both' ? 'TCP+UDP' : p.toUpperCase()}
              </span>
            </Label>
          {/each}
        </RadioGroup.Root>
      </div>

      <div class="grid grid-cols-2 gap-3">
        <div class="grid gap-2">
          <Label for="f-listen">监听端口</Label>
          <Input id="f-listen" placeholder="80 或 30000-30010" bind:value={form.listen_port} class="font-mono" />
          {#if form.listen_port && !portValid}
            <p class="text-[11px] text-destructive">端口格式：单端口或起始-结束</p>
          {/if}
        </div>
        <div class="grid gap-2">
          <Label for="f-dport">目标端口</Label>
          <Input id="f-dport" placeholder="8080" bind:value={form.dst_port} class="font-mono" />
        </div>
      </div>

      <div class="grid gap-2">
        <Label>目标类型</Label>
        <div class="grid grid-cols-2 gap-1 rounded-lg bg-muted p-1">
          <button
            type="button"
            class="rounded-md py-1.5 text-xs font-medium transition-[color,background-color,border-color,box-shadow,transform,opacity] duration-150
              {form.dst_type === 'ip' ? 'bg-card text-foreground shadow-soft' : 'text-muted-foreground hover:text-foreground'}"
            onclick={() => (form.dst_type = 'ip')}
          >
            IP 地址
          </button>
          <button
            type="button"
            class="rounded-md py-1.5 text-xs font-medium transition-[color,background-color,border-color,box-shadow,transform,opacity] duration-150
              {form.dst_type === 'domain' ? 'bg-card text-foreground shadow-soft' : 'text-muted-foreground hover:text-foreground'}"
            onclick={() => (form.dst_type = 'domain')}
          >
            域名（DDNS）
          </button>
        </div>
      </div>

      {#if form.dst_type === 'ip'}
        <div class="grid gap-2">
          <Label for="f-dst">目标 IP</Label>
          <Input id="f-dst" placeholder="192.168.1.100" bind:value={form.dst_ip} class="font-mono" />
        </div>
      {:else}
        <div class="grid gap-2">
          <Label for="f-dstdomain">目标域名</Label>
          <Input id="f-dstdomain" placeholder="nas.example.net" bind:value={form.dst_domain} class="font-mono" />
          <p class="text-[11px] text-muted-foreground">面板将按周期自动解析域名并更新转发规则</p>
        </div>
      {/if}

      <div class="grid gap-2">
        <Label for="f-src">来源限制 <span class="text-muted-foreground/70">（可选，IP/CIDR）</span></Label>
        <Input id="f-src" placeholder="如 192.168.0.0/16，留空不限制" bind:value={form.src_ip} class="font-mono" />
      </div>

      <div class="grid gap-2">
        <Label for="f-remark">备注 <span class="text-muted-foreground/70">（可选）</span></Label>
        <Input id="f-remark" placeholder="用途说明" bind:value={form.remark} />
      </div>

      <div class="mt-1 flex items-start gap-2 rounded-lg bg-info/5 px-3 py-2.5 text-[11.5px] text-muted-foreground">
        <TriangleAlert size={14} class="mt-0.5 shrink-0 text-info" />
        规则写入面板自有链（FPANEL_*），不触碰系统与其他服务的规则。
      </div>
    </div>

    <Dialog.Footer class="gap-2 sm:justify-end">
      <Button variant="outline" onclick={() => (createOpen = false)}>取消</Button>
      <Button disabled={!formValid || submitting} onclick={submit}>
        {#if submitting}
          <span class="mr-1 inline-block size-3 animate-spin rounded-full border-[1.5px] border-current border-t-transparent"></span>
          应用中…
        {:else}
          {editing ? '保存并应用' : '创建并应用'}
        {/if}
      </Button>
    </Dialog.Footer>
  </Dialog.Content>
</Dialog.Root>

{#if danger}
  <DangerDialog sync={danger.sync} onClose={() => { danger = null; load(); }} />
{/if}
