<script lang="ts">
  import { Gauge, Plus, Trash2, Search, Zap, Link2 } from '@lucide/svelte';
  import { Card } from '$lib/components/ui/card';
  import { Button } from '$lib/components/ui/button';
  import { Input } from '$lib/components/ui/input';
  import { Badge } from '$lib/components/ui/badge';
  import { Label } from '$lib/components/ui/label';
  import { Switch } from '$lib/components/ui/switch';
  import * as Dialog from '$lib/components/ui/dialog';
  import * as RadioGroup from '$lib/components/ui/radio-group';
  import { toast } from 'svelte-sonner';
  import EmptyState from '$lib/components/common/EmptyState.svelte';
  import ProtoBadge from '$lib/components/common/ProtoBadge.svelte';
  import DangerDialog from '$lib/components/common/DangerDialog.svelte';
  import { backend, type RateLimitRule, type Protocol, type SyncResult } from '$lib/api';

  let items = $state<RateLimitRule[]>([]);
  let loading = $state(true);
  let keyword = $state('');
  let danger = $state<{ sync: SyncResult } | null>(null);

  async function load() {
    loading = true;
    try {
      items = (await backend.rateLimits()).items;
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
    if (!kw) return items;
    return items.filter((r) => `${r.name} ${r.port} ${r.remark}`.toLowerCase().includes(kw));
  });

  const unitLabel: Record<string, string> = { second: '秒', minute: '分', hour: '时' };

  let createOpen = $state(false);
  let editing = $state<RateLimitRule | null>(null);
  let form = $state({ name: '', proto: 'tcp' as Protocol, port: '', per_src: true, rate: 30, rate_unit: 'minute' as RateLimitRule['rate_unit'], burst: 10, conn_limit: 0, remark: '' });
  let submitting = $state(false);
  const valid = $derived(form.name.trim() !== '' && form.rate > 0 && (form.port === '' || /^\d{1,5}(-\d{1,5})?$/.test(form.port)));

  function openCreate() {
    editing = null;
    form = { name: '', proto: 'tcp', port: '', per_src: true, rate: 30, rate_unit: 'minute', burst: 10, conn_limit: 0, remark: '' };
    createOpen = true;
  }
  function openEdit(r: RateLimitRule) {
    editing = r;
    form = { name: r.name, proto: r.proto, port: r.port, per_src: r.per_src, rate: r.rate, rate_unit: r.rate_unit, burst: r.burst, conn_limit: r.conn_limit, remark: r.remark };
    createOpen = true;
  }

  async function submit() {
    if (!valid || submitting) return;
    submitting = true;
    try {
      const payload = { ...form, port: form.port.trim(), enabled: true };
      if (editing) await backend.updateRateLimit(editing.id, payload);
      else await backend.createRateLimit(payload);
      createOpen = false;
      await load();
      toast.success(editing ? '限速规则已更新' : '限速规则已创建');
    } catch (e) {
      toast.error(e instanceof Error ? e.message : String(e));
    } finally {
      submitting = false;
    }
  }

  async function toggle(r: RateLimitRule, enabled: boolean) {
    try {
      await backend.updateRateLimit(r.id, { ...r, enabled });
      await load();
      toast[enabled ? 'success' : 'info'](`规则「${r.name}」已${enabled ? '启用' : '停用'}`);
    } catch (e) {
      toast.error(e instanceof Error ? e.message : String(e));
    }
  }

  async function remove(r: RateLimitRule) {
    try {
      const { sync } = await backend.deleteRateLimit(r.id);
      await load();
      if (sync?.danger) danger = { sync };
      else toast.success(`规则「${r.name}」已删除`);
    } catch (e) {
      toast.error(e instanceof Error ? e.message : String(e));
    }
  }
</script>

<div class="animate-fade-up space-y-4">
  <div class="flex flex-wrap items-center gap-2.5">
    <div class="relative min-w-[200px] flex-1 sm:max-w-xs">
      <Search size={15} class="pointer-events-none absolute top-1/2 left-3 -translate-y-1/2 text-muted-foreground" />
      <Input placeholder="搜索规则…" bind:value={keyword} class="pl-9" />
    </div>
    <span class="font-num ml-auto hidden text-xs text-muted-foreground sm:block">共 {filtered.length} 条</span>
    <Button onclick={openCreate}><Plus size={16} /> 新增限速</Button>
  </div>

  {#if loading}
    <div class="space-y-2.5">
      {#each Array(4) as _, i (i)}
        <div class="h-16 animate-pulse rounded-xl bg-foreground/[0.04]"></div>
      {/each}
    </div>
  {:else if filtered.length === 0}
    <EmptyState
      title="还没有限速规则"
      desc="按 IP 或总量限制端口的新建连接速率与并发数，防 CC 攻击与端口扫描。规则作用于入站方向。"
    >
      {#snippet icon()}<Gauge size={22} />{/snippet}
      {#snippet action()}
        <Button onclick={openCreate}><Plus size={16} /> 新增限速</Button>
      {/snippet}
    </EmptyState>
  {:else}
    <div class="space-y-2.5">
      {#each filtered as r (r.id)}
        <Card class="lift-card group p-4">
          <div class="flex flex-wrap items-center gap-x-6 gap-y-3">
            <div class="min-w-[150px] flex-1 basis-40">
              <span class="text-[13.5px] font-medium">{r.name}</span>
              {#if r.remark}
                <p class="mt-0.5 truncate text-[11.5px] text-muted-foreground">{r.remark}</p>
              {/if}
            </div>
            <div class="flex min-w-[220px] flex-1 basis-56 items-center gap-2 text-[12.5px]">
              <ProtoBadge protocol={r.proto} />
              <span class="font-mono">{r.port || '全部端口'}</span>
            </div>
            <div class="flex min-w-[200px] items-center gap-1.5 text-[12px] text-muted-foreground">
              <Zap size={13} class="text-warning" />
              <span class="font-num font-medium text-foreground">{r.rate}/{unitLabel[r.rate_unit]}</span>
              <span>· 突发 {r.burst}</span>
              {#if r.conn_limit > 0}
                <span class="flex items-center gap-1"><Link2 size={12} class="text-info" /> <span class="font-num">并发 {r.conn_limit}</span></span>
              {/if}
              <Badge variant="outline" class="text-[10px]">{r.per_src ? '按 IP' : '总量'}</Badge>
            </div>
            <div class="flex items-center gap-2.5">
              <Switch checked={r.enabled} onCheckedChange={(v) => toggle(r, v)} />
              <Button variant="ghost" size="icon" class="size-7 text-muted-foreground" onclick={() => openEdit(r)} aria-label="编辑">
                <Gauge size={13} />
              </Button>
              <Button variant="ghost" size="icon" class="size-7 text-muted-foreground opacity-0 transition-opacity hover:text-destructive group-hover:opacity-100" onclick={() => remove(r)} aria-label="删除">
                <Trash2 size={13} />
              </Button>
            </div>
          </div>
        </Card>
      {/each}
    </div>
  {/if}
</div>

<Dialog.Root bind:open={createOpen}>
  <Dialog.Content class="sm:max-w-md">
    <Dialog.Header>
      <Dialog.Title>{editing ? '编辑限速规则' : '新增限速规则'}</Dialog.Title>
      <Dialog.Description>超过速率的新建连接将被丢弃；可选同时限制单 IP 并发连接数。</Dialog.Description>
    </Dialog.Header>
    <div class="grid gap-3.5">
      <div class="grid grid-cols-2 gap-3">
        <div class="grid gap-2">
          <Label for="rl-name">规则名称</Label>
          <Input id="rl-name" placeholder="web-cc-guard" bind:value={form.name} />
        </div>
        <div class="grid gap-2">
          <Label for="rl-port">端口 <span class="text-muted-foreground/70">（空=全部）</span></Label>
          <Input id="rl-port" placeholder="80 或 30000-30010" bind:value={form.port} class="font-mono" />
        </div>
      </div>
      <div class="grid gap-2">
        <Label>协议</Label>
        <RadioGroup.Root
          value={form.proto}
          onValueChange={(v) => (form.proto = v as Protocol)}
          class="grid grid-cols-3 gap-2"
        >
          {#each ['tcp', 'udp', 'both'] as const as p}
            {@const id = `rl-proto-${p}`}
            <Label
              for={id}
              class="flex cursor-pointer items-center gap-2 rounded-lg border px-3 py-2.5 transition-colors duration-150
                {form.proto === p ? 'border-primary/50 bg-primary/[0.04]' : 'bg-card hover:bg-muted/60'}"
            >
              <RadioGroup.Item value={p} id={id} />
              <span class="font-mono text-xs font-medium {form.proto === p ? 'text-foreground' : 'text-muted-foreground'}">
                {p === 'both' ? 'TCP+UDP' : p.toUpperCase()}
              </span>
            </Label>
          {/each}
        </RadioGroup.Root>
      </div>
      <div class="grid grid-cols-3 gap-3">
        <div class="grid col-span-1 gap-2">
          <Label for="rl-rate">速率</Label>
          <Input id="rl-rate" type="number" min="1" bind:value={form.rate} class="font-num" />
        </div>
        <div class="grid gap-2">
          <Label>单位</Label>
          <div class="grid grid-cols-3 gap-1 rounded-lg bg-muted p-1">
            {#each ['second', 'minute', 'hour'] as const as u}
              <button type="button" class="rounded-md py-1.5 text-[11px] font-medium transition-[color,background-color,border-color,box-shadow,transform,opacity] {form.rate_unit === u ? 'bg-card shadow-soft' : 'text-muted-foreground'}" onclick={() => (form.rate_unit = u)}>
                {unitLabel[u]}
              </button>
            {/each}
          </div>
        </div>
        <div class="grid gap-2">
          <Label for="rl-burst">突发</Label>
          <Input id="rl-burst" type="number" min="0" bind:value={form.burst} class="font-num" />
        </div>
      </div>
      <div class="grid grid-cols-2 gap-3">
        <div class="grid gap-2">
          <Label for="rl-conn">并发连接限制 <span class="text-muted-foreground/70">（0=不限）</span></Label>
          <Input id="rl-conn" type="number" min="0" bind:value={form.conn_limit} class="font-num" />
        </div>
        <div class="grid gap-2">
          <Label>限速对象</Label>
          <RadioGroup.Root
            value={form.per_src ? 'ip' : 'total'}
            onValueChange={(v) => (form.per_src = v === 'ip')}
            class="grid grid-cols-2 gap-2"
          >
            {@const perOptions = [{ v: 'ip', label: '按 IP' }, { v: 'total', label: '总量' }]}
            {#each perOptions as opt}
              {@const id = `rl-per-${opt.v}`}
              <Label
                for={id}
                class="flex cursor-pointer items-center gap-2 rounded-lg border px-3 py-2.5 transition-colors duration-150
                  {(form.per_src ? 'ip' : 'total') === opt.v ? 'border-primary/50 bg-primary/[0.04]' : 'bg-card hover:bg-muted/60'}"
              >
                <RadioGroup.Item value={opt.v} id={id} />
                <span class="text-xs font-medium {(form.per_src ? 'ip' : 'total') === opt.v ? 'text-foreground' : 'text-muted-foreground'}">
                  {opt.label}
                </span>
              </Label>
            {/each}
          </RadioGroup.Root>
        </div>
      </div>
      <div class="grid gap-2">
        <Label for="rl-remark">备注 <span class="text-muted-foreground/70">（可选）</span></Label>
        <Input id="rl-remark" bind:value={form.remark} />
      </div>
    </div>
    <Dialog.Footer class="gap-2 sm:justify-end">
      <Button variant="outline" onclick={() => (createOpen = false)}>取消</Button>
      <Button disabled={!valid || submitting} onclick={submit}>{submitting ? '应用中…' : editing ? '保存并应用' : '创建并应用'}</Button>
    </Dialog.Footer>
  </Dialog.Content>
</Dialog.Root>

{#if danger}
  <DangerDialog sync={danger.sync} onClose={() => { danger = null; load(); }} />
{/if}
