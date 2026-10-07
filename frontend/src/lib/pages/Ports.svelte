<script lang="ts">
  import { onMount } from 'svelte';
  import {
    RefreshCw, Plus, Search, Pencil, Trash2, Activity, BookMarked, Container,
    MonitorSmartphone, Globe2, ScanLine,
  } from '@lucide/svelte';
  import { Card } from '$lib/components/ui/card';
  import { Button } from '$lib/components/ui/button';
  import { Input } from '$lib/components/ui/input';
  import { Badge } from '$lib/components/ui/badge';
  import { Label } from '$lib/components/ui/label';
  import * as Dialog from '$lib/components/ui/dialog';
  import * as RadioGroup from '$lib/components/ui/radio-group';
  import { toast } from 'svelte-sonner';
  import EmptyState from '$lib/components/common/EmptyState.svelte';
  import ProtoBadge from '$lib/components/common/ProtoBadge.svelte';
  import StatCard from '$lib/components/common/StatCard.svelte';
  import { backend, type ListenPort, type PortRecord } from '$lib/api';
  import { relativeTime } from '$lib/utils';

  let listening = $state<ListenPort[]>([]);
  let records = $state<PortRecord[]>([]);
  let scanLoading = $state(false);
  let scanError = $state('');
  let recordsLoading = $state(true);
  let keyword = $state('');
  let quickBusy = $state('');

  async function loadScan() {
    scanLoading = true;
    scanError = '';
    try {
      listening = (await backend.listeningPorts()).items;
    } catch (e) {
      scanError = e instanceof Error ? e.message : String(e);
    } finally {
      scanLoading = false;
    }
  }

  async function loadRecords() {
    recordsLoading = true;
    try {
      records = (await backend.portRecords()).items;
    } catch (e) {
      toast.error(e instanceof Error ? e.message : String(e));
    } finally {
      recordsLoading = false;
    }
  }

  onMount(() => {
    loadScan();
    loadRecords();
  });

  /* ------- 状态联动 ------- */
  const extCount = $derived(listening.filter((p) => isExternal(p.address)).length);
  const localCount = $derived(listening.length - extCount);

  function isExternal(addr: string): boolean {
    return addr !== '127.0.0.1' && addr !== '::1' && addr !== '[::1]';
  }

  function addrLabel(p: ListenPort): string {
    if (p.address === '0.0.0.0' || p.address === '::' || p.address === '[::]' || p.address === '*') return '所有网卡';
    return p.address;
  }

  function recordOf(lp: ListenPort): PortRecord | undefined {
    return records.find((r) => r.port === lp.port && (r.proto === 'both' || r.proto === lp.proto));
  }

  function isListening(rec: PortRecord): boolean {
    return listening.some((p) => p.port === rec.port && (rec.proto === 'both' || p.proto === rec.proto));
  }

  const sortedListening = $derived([...listening].sort((a, b) => a.port - b.port || a.proto.localeCompare(b.proto)));

  /* ------- 一键记入台账 ------- */
  async function quickRecord(lp: ListenPort) {
    const key = `${lp.proto}:${lp.port}`;
    quickBusy = key;
    try {
      await backend.createPortRecord({
        name: lp.process || `端口 ${lp.port}`,
        proto: lp.proto,
        port: lp.port,
        group_tag: lp.docker ? 'docker' : '',
      });
      toast.success(`端口 ${lp.port}/${lp.proto} 已记入台账`);
      await loadRecords();
    } catch (e) {
      toast.error(e instanceof Error ? e.message : String(e));
    } finally {
      quickBusy = '';
    }
  }

  /* ------- 新增/编辑记录 ------- */
  let createOpen = $state(false);
  let editing = $state<PortRecord | null>(null);
  let submitting = $state(false);
  let form = $state({ name: '', proto: 'tcp' as PortRecord['proto'], port: '', group_tag: '', remark: '' });

  const valid = $derived(
    form.name.trim() !== '' && /^\d{1,5}$/.test(form.port.trim()) && +form.port >= 1 && +form.port <= 65535,
  );

  function openCreate() {
    editing = null;
    form = { name: '', proto: 'tcp', port: '', group_tag: '', remark: '' };
    createOpen = true;
  }

  function openEdit(r: PortRecord) {
    editing = r;
    form = { name: r.name, proto: r.proto, port: String(r.port), group_tag: r.group_tag, remark: r.remark };
    createOpen = true;
  }

  async function submit() {
    if (!valid || submitting) return;
    submitting = true;
    try {
      const payload = { name: form.name.trim(), proto: form.proto, port: +form.port, group_tag: form.group_tag.trim(), remark: form.remark.trim() };
      if (editing) await backend.updatePortRecord(editing.id, payload);
      else await backend.createPortRecord(payload);
      createOpen = false;
      await loadRecords();
      toast.success(editing ? '记录已更新' : '记录已添加');
    } catch (e) {
      toast.error(e instanceof Error ? e.message : String(e));
    } finally {
      submitting = false;
    }
  }

  async function remove(r: PortRecord) {
    try {
      await backend.deletePortRecord(r.id);
      records = records.filter((x) => x.id !== r.id);
      toast.success(`记录「${r.name}」已删除`);
    } catch (e) {
      toast.error(e instanceof Error ? e.message : String(e));
    }
  }

  const filtered = $derived.by(() => {
    const kw = keyword.trim().toLowerCase();
    if (!kw) return records;
    return records.filter(
      (r) =>
        r.name.toLowerCase().includes(kw) ||
        String(r.port).includes(kw) ||
        r.group_tag.toLowerCase().includes(kw) ||
        r.remark.toLowerCase().includes(kw),
    );
  });
</script>

<div class="animate-fade-up space-y-4">
  <!-- 统计 -->
  <div class="grid gap-3 sm:grid-cols-2 lg:grid-cols-4">
    <StatCard label="监听端口" value={String(listening.length)} accent="primary">
      {#snippet icon()}<Activity size={17} />{/snippet}
    </StatCard>
    <StatCard label="对外监听" value={String(extCount)} accent="warning">
      {#snippet icon()}<Globe2 size={17} />{/snippet}
    </StatCard>
    <StatCard label="仅本机回环" value={String(localCount)} accent="info">
      {#snippet icon()}<MonitorSmartphone size={17} />{/snippet}
    </StatCard>
    <StatCard label="台账记录" value={String(records.length)} accent="success">
      {#snippet icon()}<BookMarked size={17} />{/snippet}
    </StatCard>
  </div>

  <!-- 监听扫描 -->
  <Card class="p-5 shadow-soft">
    <div class="mb-3.5 flex flex-wrap items-center gap-2.5">
      <div class="min-w-0">
        <h2 class="flex items-center gap-2 text-sm font-semibold">
          <ScanLine size={15} class="text-primary" />
          本机监听端口
        </h2>
        <p class="mt-0.5 text-[11.5px] text-muted-foreground">实时扫描 TCP / UDP 监听与占用进程，可一键记入下方台账</p>
      </div>
      <Button variant="outline" size="sm" class="ml-auto h-8" disabled={scanLoading} onclick={loadScan}>
        <RefreshCw size={13} class={scanLoading ? 'animate-spin' : ''} /> 刷新扫描
      </Button>
    </div>

    {#if scanError}
      <div class="rounded-lg border border-destructive/25 bg-destructive/[0.05] px-3.5 py-2.5 text-[12.5px] text-destructive">
        {scanError}
      </div>
    {:else if scanLoading && listening.length === 0}
      <div class="space-y-2">
        {#each Array(4) as _, i (i)}
          <div class="h-11 animate-pulse rounded-lg bg-foreground/[0.04]"></div>
        {/each}
      </div>
    {:else if sortedListening.length === 0}
      <EmptyState compact title="没有发现监听端口" desc="当前没有 TCP / UDP 服务在监听。">
        {#snippet action()}
          <Button variant="outline" size="sm" onclick={loadScan}><RefreshCw size={13} /> 重新扫描</Button>
        {/snippet}
      </EmptyState>
    {:else}
      <div class="divide-y divide-border/50">
        {#each sortedListening as lp (`${lp.proto}:${lp.port}`)}
          {@const rec = recordOf(lp)}
          <div class="group flex flex-wrap items-center gap-x-4 gap-y-1.5 py-2.5">
            <div class="flex min-w-[130px] items-center gap-2">
              <span class="font-num text-[14px] font-semibold">{lp.port}</span>
              <ProtoBadge protocol={lp.proto} />
            </div>
            <div class="min-w-[150px] flex-1 basis-40 text-[12.5px]">
              {#if lp.process}
                <span class="font-medium">{lp.process}</span>
                <span class="ml-1.5 font-num text-[11px] text-muted-foreground">PID {lp.pid}</span>
              {:else}
                <span class="text-muted-foreground/70">未知进程</span>
              {/if}
            </div>
            <div class="flex items-center gap-1.5">
              {#if lp.docker}
                <Badge variant="outline" class="bg-info/10 text-[10px] text-info ring-info/20">
                  <Container size={10} class="mr-0.5" /> Docker 映射
                </Badge>
              {/if}
              {#if isExternal(lp.address)}
                <Badge variant="outline" class="bg-warning/10 text-[10px] text-warning ring-warning/25">对外</Badge>
                <span class="font-mono text-[11px] text-muted-foreground">{addrLabel(lp)}</span>
              {:else}
                <Badge variant="outline" class="bg-muted/60 text-[10px] text-muted-foreground ring-border">仅本机</Badge>
              {/if}
            </div>
            <div class="ml-auto flex items-center gap-2">
              {#if rec}
                <span class="flex items-center gap-1.5 text-[11.5px] text-muted-foreground">
                  <BookMarked size={12} class="text-success" />
                  已记录：<span class="font-medium text-foreground">{rec.name}</span>
                </span>
              {:else}
                <Button
                  variant="outline"
                  size="sm"
                  class="h-7 px-2.5 text-[11.5px]"
                  disabled={quickBusy === `${lp.proto}:${lp.port}`}
                  onclick={() => quickRecord(lp)}
                >
                  <Plus size={12} /> {quickBusy === `${lp.proto}:${lp.port}` ? '记入中…' : '记入台账'}
                </Button>
              {/if}
            </div>
          </div>
        {/each}
      </div>
    {/if}
  </Card>

  <!-- 端口台账 -->
  <Card class="p-5 shadow-soft">
    <div class="mb-3.5 flex flex-wrap items-center gap-2.5">
      <div class="min-w-0">
        <h2 class="flex items-center gap-2 text-sm font-semibold">
          <BookMarked size={15} class="text-primary" />
          端口记录
        </h2>
        <p class="mt-0.5 text-[11.5px] text-muted-foreground">登记每个端口的用途与归属服务，与实时监听状态联动</p>
      </div>
      <div class="relative ml-auto">
        <Search size={13} class="absolute top-1/2 left-2.5 -translate-y-1/2 text-muted-foreground/60" />
        <Input bind:value={keyword} placeholder="搜索名称 / 端口 / 分组…" class="h-8 w-44 pl-7 text-[12px]" />
      </div>
      <Button size="sm" class="h-8" onclick={openCreate}><Plus size={14} /> 新增记录</Button>
    </div>

    {#if recordsLoading}
      <div class="space-y-2">
        {#each Array(3) as _, i (i)}
          <div class="h-11 animate-pulse rounded-lg bg-foreground/[0.04]"></div>
        {/each}
      </div>
    {:else if records.length === 0}
      <EmptyState
        title="还没有端口记录"
        desc="为常用端口登记用途（如 22 · SSH、6379 · Redis），方便交接与审计；也可从上方扫描结果一键记入。"
      >
        {#snippet icon()}<BookMarked size={22} />{/snippet}
        {#snippet action()}
          <Button onclick={openCreate}><Plus size={16} /> 新增记录</Button>
        {/snippet}
      </EmptyState>
    {:else if filtered.length === 0}
      <EmptyState compact title="没有匹配的记录" desc="换个关键词试试。">
        {#snippet icon()}<Search size={20} />{/snippet}
        {#snippet action()}
          <Button variant="outline" size="sm" onclick={() => (keyword = '')}>清除筛选</Button>
        {/snippet}
      </EmptyState>
    {:else}
      <div class="divide-y divide-border/50">
        {#each filtered as r (r.id)}
          <div class="group flex flex-wrap items-center gap-x-4 gap-y-1.5 py-2.5">
            <div class="flex min-w-[130px] items-center gap-2">
              <span class="font-num text-[14px] font-semibold">{r.port}</span>
              <ProtoBadge protocol={r.proto} />
            </div>
            <div class="min-w-[160px] flex-1 basis-44">
              <div class="flex items-center gap-2">
                <span class="text-[13px] font-medium">{r.name}</span>
                {#if r.group_tag}
                  <Badge variant="outline" class="bg-primary/[0.07] text-[10px] text-primary ring-primary/20">{r.group_tag}</Badge>
                {/if}
              </div>
              {#if r.remark}
                <p class="mt-0.5 truncate text-[11.5px] text-muted-foreground">{r.remark}</p>
              {/if}
            </div>
            <div class="min-w-[92px]">
              {#if isListening(r)}
                <span class="inline-flex items-center gap-1.5 text-[11.5px] font-medium text-success">
                  <span class="size-1.5 rounded-full bg-success"></span> 监听中
                </span>
              {:else}
                <span class="inline-flex items-center gap-1.5 text-[11.5px] text-muted-foreground">
                  <span class="size-1.5 rounded-full bg-muted-foreground/40"></span> 未监听
                </span>
              {/if}
            </div>
            <div class="hidden min-w-[64px] text-[11.5px] text-muted-foreground sm:block">
              {relativeTime(r.created_at)}
            </div>
            <div class="ml-auto flex items-center gap-1">
              <Button variant="ghost" size="icon" class="size-7 text-muted-foreground" onclick={() => openEdit(r)} aria-label="编辑">
                <Pencil size={13} />
              </Button>
              <Button variant="ghost" size="icon" class="size-7 text-muted-foreground opacity-0 transition-opacity hover:text-destructive group-hover:opacity-100" onclick={() => remove(r)} aria-label="删除">
                <Trash2 size={13} />
              </Button>
            </div>
          </div>
        {/each}
      </div>
    {/if}
  </Card>
</div>

<Dialog.Root bind:open={createOpen}>
  <Dialog.Content class="sm:max-w-md">
    <Dialog.Header>
      <Dialog.Title>{editing ? '编辑端口记录' : '新增端口记录'}</Dialog.Title>
      <Dialog.Description>登记端口的用途与归属服务，纯台账信息，不影响内核规则。</Dialog.Description>
    </Dialog.Header>
    <div class="grid gap-4">
      <div class="grid grid-cols-[1.4fr_1fr] gap-3">
        <div class="grid gap-1.5">
          <Label for="pr-name">用途名称</Label>
          <Input id="pr-name" placeholder="如：SSH 远程管理" bind:value={form.name} />
        </div>
        <div class="grid gap-1.5">
          <Label for="pr-port">端口号</Label>
          <Input id="pr-port" placeholder="22" bind:value={form.port} class="font-mono" inputmode="numeric" />
        </div>
      </div>
      <div class="grid gap-1.5">
        <Label>协议</Label>
        <RadioGroup.Root
          value={form.proto}
          onValueChange={(v) => (form.proto = v as PortRecord['proto'])}
          class="flex gap-2"
        >
          {#each [{ v: 'tcp', l: 'TCP' }, { v: 'udp', l: 'UDP' }, { v: 'both', l: 'TCP+UDP' }] as opt (opt.v)}
            <Label
              for="pr-proto-{opt.v}"
              class="flex flex-1 cursor-pointer items-center justify-center gap-2 rounded-lg border p-2.5 text-[12.5px] font-medium transition-[color,background-color,border-color,box-shadow,transform,opacity] duration-150
                {form.proto === opt.v ? 'border-primary/50 bg-primary/[0.06] ring-1 ring-primary/30' : 'border-border/60 hover:border-primary/30'}"
            >
              <RadioGroup.Item value={opt.v} id="pr-proto-{opt.v}" />
              {opt.l}
            </Label>
          {/each}
        </RadioGroup.Root>
      </div>
      <div class="grid grid-cols-2 gap-3">
        <div class="grid gap-1.5">
          <Label for="pr-group">分组 <span class="text-muted-foreground/70">（可选）</span></Label>
          <Input id="pr-group" placeholder="web / 数据库 / docker" bind:value={form.group_tag} />
        </div>
        <div class="grid gap-1.5">
          <Label for="pr-remark">备注 <span class="text-muted-foreground/70">（可选）</span></Label>
          <Input id="pr-remark" bind:value={form.remark} />
        </div>
      </div>
    </div>
    <Dialog.Footer class="gap-2 sm:justify-end">
      <Button variant="outline" onclick={() => (createOpen = false)}>取消</Button>
      <Button disabled={!valid || submitting} onclick={submit}>{submitting ? '保存中…' : editing ? '保存' : '添加'}</Button>
    </Dialog.Footer>
  </Dialog.Content>
</Dialog.Root>
