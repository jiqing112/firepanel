<script lang="ts">
  import { Search, Plus, Trash2, Upload, Download, Globe, Ban, ShieldCheck } from '@lucide/svelte';
  import { Card } from '$lib/components/ui/card';
  import { Button } from '$lib/components/ui/button';
  import { Input } from '$lib/components/ui/input';
  import { Badge } from '$lib/components/ui/badge';
  import { Label } from '$lib/components/ui/label';
  import * as Dialog from '$lib/components/ui/dialog';
  import * as DropdownMenu from '$lib/components/ui/dropdown-menu';
  import { toast } from 'svelte-sonner';
  import EmptyState from '$lib/components/common/EmptyState.svelte';
  import DangerDialog from '$lib/components/common/DangerDialog.svelte';
  import { backend, type SyncResult, type IPListEntry } from '$lib/api';
  import { fmtDateTime, fmtNum } from '$lib/utils';

  let tab = $state<'black' | 'white'>('black');
  let items = $state<IPListEntry[]>([]);
  let loading = $state(true);
  let keyword = $state('');
  let danger = $state<{ sync: SyncResult } | null>(null);

  async function load() {
    loading = true;
    try {
      items = (await backend.ipLists(tab)).items;
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
    return items.filter(
      (e) => e.ip_or_cidr.toLowerCase().includes(kw) || e.group_tag.toLowerCase().includes(kw) || e.remark.toLowerCase().includes(kw),
    );
  });

  async function remove(e: IPListEntry) {
    try {
      const { sync } = await backend.deleteIPList(e.id);
      items = items.filter((x) => x.id !== e.id);
      if (sync?.danger) danger = { sync };
      else toast.success(`已移除 ${e.ip_or_cidr}`);
    } catch (err) {
      toast.error(err instanceof Error ? err.message : String(err));
    }
  }

  /* ------- 新增 ------- */
  let createOpen = $state(false);
  let form = $state({ ip: '', group: '', remark: '' });
  const ipValid = $derived(form.ip.trim().length > 0);
  let submitting = $state(false);

  async function submitCreate() {
    if (!ipValid || submitting) return;
    submitting = true;
    try {
      const { sync } = await backend.createIPList({
        list_type: tab,
        ip_or_cidr: form.ip.trim(),
        group_tag: form.group.trim(),
        remark: form.remark.trim(),
      });
      createOpen = false;
      form = { ip: '', group: '', remark: '' };
      await load();
      if (sync?.danger) danger = { sync };
      else toast.success('已添加并应用到内核');
    } catch (e) {
      toast.error(e instanceof Error ? e.message : String(e));
    } finally {
      submitting = false;
    }
  }

  /* ------- 批量导入 ------- */
  let importOpen = $state(false);
  let importText = $state('');
  let importing = $state(false);

  async function submitImport() {
    if (!importText.trim() || importing) return;
    importing = true;
    try {
      const r = await backend.importIPLists(tab, 'text', importText);
      importOpen = false;
      importText = '';
      await load();
      if (r.sync?.danger) danger = { sync: r.sync };
      toast.success(`导入 ${r.imported} 条，跳过 ${r.skipped} 条`);
    } catch (e) {
      toast.error(e instanceof Error ? e.message : String(e));
    } finally {
      importing = false;
    }
  }

  async function exportList(format: 'txt' | 'csv' | 'json') {
    try {
      const res = await fetch(`/api/v1/ip-lists/export?type=${tab}&format=${format}`, {
        headers: { Authorization: `Bearer ${localStorage.getItem('firepanel-token') ?? ''}` },
      });
      if (!res.ok) throw new Error(`导出失败（${res.status}）`);
      const blob = await res.blob();
      const url = URL.createObjectURL(blob);
      const a = document.createElement('a');
      a.href = url;
      a.download = `firepanel-${tab}-list.${format}`;
      a.click();
      URL.revokeObjectURL(url);
    } catch (e) {
      toast.error(e instanceof Error ? e.message : String(e));
    }
  }

  const stats = $derived({
    black: tab === 'black' ? items.length : undefined,
  });
</script>

<div class="animate-fade-up space-y-4">
  <div class="flex flex-wrap items-center gap-2.5">
    <div class="inline-flex items-center gap-1 rounded-lg bg-muted p-1">
      <button
        type="button"
        class="flex-none rounded-md px-3 py-1 text-[12.5px] font-medium transition-[color,background-color,border-color,box-shadow,transform,opacity] duration-150 flex items-center gap-1.5
          {tab === 'black' ? 'bg-card text-foreground shadow-sm' : 'text-muted-foreground hover:text-foreground'}"
        onclick={() => { tab = 'black'; keyword = ''; }}
      >
        <Ban size={14} class="text-destructive" /> 黑名单
      </button>
      <button
        type="button"
        class="flex-none rounded-md px-3 py-1 text-[12.5px] font-medium transition-[color,background-color,border-color,box-shadow,transform,opacity] duration-150 flex items-center gap-1.5
          {tab === 'white' ? 'bg-card text-foreground shadow-sm' : 'text-muted-foreground hover:text-foreground'}"
        onclick={() => { tab = 'white'; keyword = ''; }}
      >
        <ShieldCheck size={14} class="text-success" /> 白名单
      </button>
    </div>

      <div class="relative min-w-[200px] flex-1 sm:max-w-xs">
        <Search size={15} class="pointer-events-none absolute top-1/2 left-3 -translate-y-1/2 text-muted-foreground" />
        <Input placeholder="搜索 IP、标签或备注…" bind:value={keyword} class="pl-9" />
      </div>

      <span class="font-num ml-auto hidden text-xs text-muted-foreground sm:block">共 {filtered.length} 条</span>

      <DropdownMenu.Root>
        <DropdownMenu.Trigger>
          {#snippet child({ props })}
            <Button {...props} variant="outline"><Download size={15} /> 导出</Button>
          {/snippet}
        </DropdownMenu.Trigger>
        <DropdownMenu.Content align="end">
          <DropdownMenu.Item onclick={() => exportList('txt')}>文本（每行一个）</DropdownMenu.Item>
          <DropdownMenu.Item onclick={() => exportList('csv')}>CSV</DropdownMenu.Item>
          <DropdownMenu.Item onclick={() => exportList('json')}>JSON</DropdownMenu.Item>
        </DropdownMenu.Content>
      </DropdownMenu.Root>

      <Button variant="outline" onclick={() => (importOpen = true)}><Upload size={15} /> 导入</Button>
      <Button onclick={() => (createOpen = true)}><Plus size={16} /> 新增{tab === 'black' ? '拉黑' : '放行'}</Button>
    </div>

    <div class="mt-4">
      {#if loading}
        <div class="space-y-2">
          {#each Array(6) as _, i (i)}
            <div class="h-12 animate-pulse rounded-lg bg-foreground/[0.04]"></div>
          {/each}
        </div>
      {:else if filtered.length === 0}
        {#if keyword}
          <EmptyState compact title="没有匹配的条目" desc="换个关键词试试。">
            {#snippet icon()}<Search size={20} />{/snippet}
          </EmptyState>
        {:else}
          <EmptyState
            title={tab === 'black' ? '黑名单为空' : '白名单为空'}
            desc={tab === 'black'
              ? '拉黑恶意 IP 或网段后，其所有入站连接将被直接丢弃。'
              : '放行可信网段后，这些来源的连接将优先允许。'}
          >
            {#snippet icon()}<Globe size={22} />{/snippet}
            {#snippet action()}
              <Button onclick={() => (createOpen = true)}><Plus size={16} /> 新增{tab === 'black' ? '拉黑' : '放行'}</Button>
            {/snippet}
          </EmptyState>
        {/if}
      {:else}
        <Card class="overflow-hidden shadow-soft">
          <div class="overflow-x-auto">
            <table class="w-full text-left text-[13px]">
              <thead>
                <tr class="border-b border-border/70 bg-foreground/[0.02] text-[11.5px] text-muted-foreground">
                  <th class="py-2.5 pr-3 pl-4 font-medium">IP / 网段</th>
                  <th class="py-2.5 pr-3 font-medium">标签</th>
                  <th class="py-2.5 pr-3 font-medium">来源</th>
                  <th class="py-2.5 pr-3 font-medium">过期时间</th>
                  <th class="py-2.5 pr-3 font-medium">备注</th>
                  <th class="py-2.5 pr-4 text-right font-medium">操作</th>
                </tr>
              </thead>
              <tbody>
                {#each filtered as e (e.id)}
                  <tr class="group border-b border-border/40 transition-colors last:border-0 hover:bg-foreground/[0.02]">
                    <td class="py-2.5 pr-3 pl-4">
                      <span class="font-mono text-[12.5px] font-medium">{e.ip_or_cidr}</span>
                    </td>
                    <td class="py-2.5 pr-3">
                      {#if e.group_tag}
                        <Badge variant="outline" class="bg-primary/5 text-[10.5px] text-primary ring-primary/20">{e.group_tag}</Badge>
                      {:else}
                        <span class="text-muted-foreground/50">—</span>
                      {/if}
                    </td>
                    <td class="py-2.5 pr-3 text-muted-foreground">{e.source}</td>
                    <td class="py-2.5 pr-3 text-muted-foreground">
                      {e.expires_at ? fmtDateTime(e.expires_at) : '—'}
                    </td>
                    <td class="max-w-[220px] truncate py-2.5 pr-3 text-muted-foreground">{e.remark || '—'}</td>
                    <td class="py-2.5 pr-4 text-right">
                      <Button
                        variant="ghost"
                        size="icon"
                        class="size-7 text-muted-foreground opacity-0 transition-opacity hover:text-destructive group-hover:opacity-100"
                        onclick={() => remove(e)}
                        aria-label="删除"
                      >
                        <Trash2 size={14} />
                      </Button>
                    </td>
                  </tr>
                {/each}
              </tbody>
            </table>
          </div>
        </Card>
      {/if}
    </div>
</div>

<!-- 新增对话框 -->
<Dialog.Root bind:open={createOpen}>
  <Dialog.Content class="sm:max-w-sm">
    <Dialog.Header>
      <Dialog.Title>新增{tab === 'black' ? '拉黑' : '放行'}</Dialog.Title>
      <Dialog.Description>支持单个 IP 或 CIDR 网段，写入后立即生效。</Dialog.Description>
    </Dialog.Header>
    <div class="grid gap-3.5">
      <div class="grid gap-2">
        <Label for="ip-addr">IP / CIDR</Label>
        <Input id="ip-addr" placeholder="203.0.113.9 或 10.0.0.0/24" bind:value={form.ip} class="font-mono" />
      </div>
      <div class="grid gap-2">
        <Label for="ip-group">标签 <span class="text-muted-foreground/70">（可选）</span></Label>
        <Input id="ip-group" placeholder="如 cc-attack、scanner" bind:value={form.group} />
      </div>
      <div class="grid gap-2">
        <Label for="ip-remark">备注 <span class="text-muted-foreground/70">（可选）</span></Label>
        <Input id="ip-remark" placeholder="说明" bind:value={form.remark} />
      </div>
    </div>
    <Dialog.Footer class="gap-2 sm:justify-end">
      <Button variant="outline" onclick={() => (createOpen = false)}>取消</Button>
      <Button disabled={!ipValid || submitting} onclick={submitCreate}>
        {submitting ? '应用中…' : '添加并应用'}
      </Button>
    </Dialog.Footer>
  </Dialog.Content>
</Dialog.Root>

<!-- 批量导入对话框 -->
<Dialog.Root bind:open={importOpen}>
  <Dialog.Content class="sm:max-w-md">
    <Dialog.Header>
      <Dialog.Title>批量导入到{tab === 'black' ? '黑名单' : '白名单'}</Dialog.Title>
      <Dialog.Description>每行一个 IP 或 CIDR；# 开头为注释；已存在的条目自动去重。</Dialog.Description>
    </Dialog.Header>
    <textarea
      class="h-48 w-full resize-none rounded-lg border border-input bg-card px-3 py-2 font-mono text-[12.5px] outline-none transition focus:ring-2 focus:ring-ring/40"
      placeholder={'203.0.113.0/24\n45.148.10.85  # 端口扫描源\n91.240.118.0/24'}
      bind:value={importText}
    ></textarea>
    <Dialog.Footer class="gap-2 sm:justify-end">
      <Button variant="outline" onclick={() => (importOpen = false)}>取消</Button>
      <Button disabled={!importText.trim() || importing} onclick={submitImport}>
        {importing ? '导入中…' : '导入'}
      </Button>
    </Dialog.Footer>
  </Dialog.Content>
</Dialog.Root>

{#if danger}
  <DangerDialog sync={danger.sync} onClose={() => { danger = null; load(); }} />
{/if}
