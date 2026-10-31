<script lang="ts">
  import { onMount } from 'svelte';
  import { Search, Plus, Pencil, Trash2, ShieldCheck, Info, CloudOff } from '@lucide/svelte';
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
  import DangerDialog from '$lib/components/common/DangerDialog.svelte';
  import { backend, type PortAllowRule, type SyncResult } from '$lib/api';

  type Proto = 'tcp' | 'udp' | 'both';

  let items = $state<PortAllowRule[]>([]);
  let loading = $state(true);
  let keyword = $state('');
  let danger = $state<{ sync: SyncResult } | null>(null);

  async function load() {
    loading = true;
    try {
      items = (await backend.portAllow()).items;
    } catch (e) {
      toast.error(e instanceof Error ? e.message : String(e));
    } finally {
      loading = false;
    }
  }
  onMount(() => {
    load();
  });

  const filtered = $derived(
    items.filter((r) => {
      const k = keyword.trim().toLowerCase();
      if (!k) return true;
      return (
        r.name.toLowerCase().includes(k) ||
        r.port.includes(k) ||
        r.src_cidr.toLowerCase().includes(k) ||
        r.remark.toLowerCase().includes(k)
      );
    })
  );

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

  /* ---------- 新增 / 编辑 ---------- */
  let createOpen = $state(false);
  let editing = $state<PortAllowRule | null>(null);
  let submitting = $state(false);
  let form = $state({ name: '', proto: 'tcp' as Proto, port: '', src_cidr: '', remark: '' });

  // 端口：单端口或 start-end 段
  const portValid = $derived(
    /^\d{1,5}(-\d{1,5})?$/.test(form.port.trim()) &&
      form.port
        .split('-')
        .map((v) => Number(v))
        .every((n) => n >= 1 && n <= 65535) &&
      (() => {
        const p = form.port.split('-').map(Number);
        return p.length === 1 || p[0] <= p[1];
      })()
  );
  // 来源留空 = 对所有人放行
  const srcValid = $derived(
    form.src_cidr.trim() === '' ||
      /^(\d{1,3}\.){3}\d{1,3}(\/\d{1,2})?$/.test(form.src_cidr.trim()) ||
      /^[0-9a-fA-F:]+\/\d{1,3}$/.test(form.src_cidr.trim())
  );
  const formValid = $derived(form.name.trim() !== '' && portValid && srcValid);

  function openCreate() {
    editing = null;
    form = { name: '', proto: 'tcp', port: '', src_cidr: '', remark: '' };
    createOpen = true;
  }

  function openEdit(r: PortAllowRule) {
    editing = r;
    form = { name: r.name, proto: r.proto, port: r.port, src_cidr: r.src_cidr, remark: r.remark };
    createOpen = true;
  }

  async function submit() {
    if (!formValid || submitting) return;
    submitting = true;
    try {
      const payload = {
        name: form.name.trim(),
        proto: form.proto,
        port: form.port.trim(),
        src_cidr: form.src_cidr.trim(),
        remark: form.remark.trim(),
        // 编辑时保留原启停状态，避免把已停用的规则静默重新下发
        enabled: editing ? editing.enabled : true,
      };
      const { sync } = editing
        ? await backend.updatePortAllow(editing.id, payload)
        : await backend.createPortAllow(payload);
      createOpen = false;
      await load();
      handleSync(sync, editing ? '放行规则已更新' : '放行规则已创建');
    } catch (e) {
      toast.error(e instanceof Error ? e.message : String(e));
    } finally {
      submitting = false;
    }
  }

  async function toggle(r: PortAllowRule, enabled: boolean) {
    try {
      const { sync } = await backend.togglePortAllow(r.id, enabled);
      r.enabled = enabled;
      handleSync(sync, `规则「${r.name}」已${enabled ? '启用' : '停用'}`);
    } catch (e) {
      toast.error(e instanceof Error ? e.message : String(e));
      await load();
    }
  }

  async function remove(r: PortAllowRule) {
    try {
      const { sync } = await backend.deletePortAllow(r.id);
      items = items.filter((x) => x.id !== r.id);
      handleSync(sync, `规则「${r.name}」已删除`);
    } catch (e) {
      toast.error(e instanceof Error ? e.message : String(e));
    }
  }
</script>

<div class="animate-fade-up space-y-4">
  <div class="flex flex-wrap items-center gap-2.5">
    <div class="relative min-w-[220px] flex-1 sm:max-w-xs">
      <Search size={15} class="pointer-events-none absolute top-1/2 left-3 -translate-y-1/2 text-muted-foreground" />
      <Input placeholder="搜索名称、端口或来源…" bind:value={keyword} class="pl-9" />
    </div>

    <span class="font-num ml-auto hidden text-xs text-muted-foreground sm:block">
      共 {filtered.length} 条规则
    </span>

    <Button onclick={openCreate}>
      <Plus size={16} />
      新增放行
    </Button>
  </div>

  <div class="flex items-start gap-2 rounded-lg border border-border/60 bg-muted/30 px-3 py-2 text-xs leading-relaxed text-muted-foreground">
    <Info size={14} class="mt-0.5 shrink-0 text-info" />
    <span>
      放行规则写在面板链 <span class="font-mono">FPANEL_INPUT</span>，用于 INPUT 默认策略为 DROP 的机器上打开对外服务端口。
      端口转发走的是 FORWARD 链，不受这里的规则影响。
    </span>
  </div>

  <div class="flex items-start gap-2 rounded-lg border border-warning/30 bg-warning/[0.06] px-3 py-2 text-xs leading-relaxed text-warning">
    <CloudOff size={14} class="mt-0.5 shrink-0" />
    <span>
      云厂商安全组（AWS Security Group、阿里云安全组、Oracle NSG 等）在虚拟机外面，面板改不了，需要到控制台单独放行。
    </span>
  </div>

  {#if loading}
    <div class="space-y-2.5">
      {#each Array(4) as _, i (i)}
        <div class="h-[64px] animate-pulse rounded-xl bg-foreground/[0.04]"></div>
      {/each}
    </div>
  {:else if filtered.length === 0}
    <EmptyState
      title={keyword ? '没有匹配的规则' : '还没有放行规则'}
      desc={keyword
        ? '换个关键词试试。'
        : '如果服务器 INPUT 策略是 DROP，新增一条放行规则就能打开对外端口。'}
    >
      {#snippet icon()}<ShieldCheck size={22} />{/snippet}
      {#snippet action()}
        <Button onclick={openCreate}><Plus size={16} /> 新增放行</Button>
      {/snippet}
    </EmptyState>
  {:else}
    <div class="space-y-2.5">
      {#each filtered as rule (rule.id)}
        <Card class="lift-card group p-4">
          <div class="flex flex-wrap items-center gap-x-6 gap-y-3">
            <div class="min-w-[180px] flex-1 basis-48">
              <div class="flex flex-wrap items-center gap-2">
                <span class="text-[13.5px] font-medium">{rule.name}</span>
                {#if rule.src_cidr === ''}
                  <Badge variant="outline" class="text-[10px]">任意来源</Badge>
                {/if}
              </div>
              {#if rule.remark}
                <p class="mt-0.5 truncate text-[11.5px] text-muted-foreground">{rule.remark}</p>
              {/if}
            </div>

            <div class="flex items-center gap-2">
              <span class="font-mono text-xs text-muted-foreground">
                {rule.proto === 'both' ? 'TCP+UDP' : rule.proto.toUpperCase()}
              </span>
              <span class="font-num text-[13.5px] font-medium">{rule.port}</span>
            </div>

            <div class="min-w-[120px]">
              <span class="font-mono text-[11.5px] text-muted-foreground">
                {rule.src_cidr || '0.0.0.0/0'}
              </span>
            </div>

            <div class="ml-auto flex items-center gap-2.5">
              <Switch checked={rule.enabled} onCheckedChange={(v: boolean) => toggle(rule, v)} />
              <Button variant="ghost" size="icon" onclick={() => openEdit(rule)} aria-label="编辑">
                <Pencil size={15} />
              </Button>
              <Button variant="ghost" size="icon" onclick={() => remove(rule)} aria-label="删除">
                <Trash2 size={15} class="text-destructive" />
              </Button>
            </div>
          </div>
        </Card>
      {/each}
    </div>
  {/if}
</div>

<Dialog.Root bind:open={createOpen}>
  <Dialog.Content class="max-h-[88vh] overflow-y-auto sm:max-w-lg">
    <Dialog.Header>
      <Dialog.Title>{editing ? '编辑放行规则' : '新增端口放行'}</Dialog.Title>
      <Dialog.Description>
        规则会写入面板链并立即生效。仅放行指定端口，不改动机器的默认策略。
      </Dialog.Description>
    </Dialog.Header>

    <div class="flex flex-1 flex-col gap-4.5">
      <div class="grid gap-2">
        <Label for="a-name">规则名称</Label>
        <Input id="a-name" placeholder="例如：Web 服务" bind:value={form.name} />
      </div>

      <div class="grid gap-2">
        <Label>协议</Label>
        <RadioGroup.Root
          value={form.proto}
          onValueChange={(v) => (form.proto = v as Proto)}
          class="grid grid-cols-3 gap-2"
        >
          {#each ['tcp', 'udp', 'both'] as const as p}
            {@const id = `a-proto-${p}`}
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

      <div class="grid grid-cols-2 gap-3">
        <div class="grid gap-2">
          <Label for="a-port">端口</Label>
          <Input id="a-port" placeholder="80 或 8000-8010" bind:value={form.port} class="font-mono" />
          {#if form.port && !portValid}
            <p class="text-[11px] text-destructive">格式：1-65535 单端口，或起始-结束</p>
          {/if}
        </div>

        <div class="grid gap-2">
          <Label for="a-src">来源限制</Label>
          <Input id="a-src" placeholder="留空 = 所有人" bind:value={form.src_cidr} class="font-mono" />
          {#if form.src_cidr && !srcValid}
            <p class="text-[11px] text-destructive">填合法 IP 或 CIDR</p>
          {/if}
        </div>
      </div>

      <div class="grid gap-2">
        <Label for="a-remark">备注</Label>
        <Input id="a-remark" placeholder="可选" bind:value={form.remark} />
      </div>
    </div>

    <Dialog.Footer>
      <Button variant="outline" onclick={() => (createOpen = false)}>取消</Button>
      <Button onclick={submit} disabled={!formValid || submitting}>
        {submitting ? '保存中…' : editing ? '保存' : '创建'}
      </Button>
    </Dialog.Footer>
  </Dialog.Content>
</Dialog.Root>

{#if danger}
  <DangerDialog sync={danger.sync} onClose={() => { danger = null; load(); }} />
{/if}
