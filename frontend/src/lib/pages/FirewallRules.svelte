<script lang="ts">
  import { RefreshCw, ShieldCheck, TriangleAlert, GitCompare, Download, Database } from '@lucide/svelte';
  import { Card } from '$lib/components/ui/card';
  import { Button } from '$lib/components/ui/button';
  import { Badge } from '$lib/components/ui/badge';
  import { toast } from 'svelte-sonner';
  import EmptyState from '$lib/components/common/EmptyState.svelte';
  import { backend, type FirewallSnapshot, type DriftReport } from '$lib/api';
  import { fmtBytes, fmtNum, fmtDateTime } from '$lib/utils';

  let snap = $state<FirewallSnapshot | null>(null);
  let drift = $state<DriftReport | null>(null);
  let loading = $state(true);
  let syncing = $state(false);
  let lastCheck = $state<Date | null>(null);

  async function load() {
    loading = true;
    try {
      const [s, d] = await Promise.all([backend.firewallRules(), backend.drift()]);
      snap = s.snapshot;
      drift = d.report;
      lastCheck = new Date();
    } catch (e) {
      toast.error(e instanceof Error ? e.message : String(e));
    } finally {
      loading = false;
    }
  }

  $effect(() => {
    load();
  });

  async function checkDrift() {
    try {
      const r = await backend.checkDrift();
      drift = r.report;
      lastCheck = new Date();
      toast[r.report.drifted ? 'warning' : 'success'](
        r.report.drifted ? '检测到规则漂移' : '内核规则与面板一致',
      );
    } catch (e) {
      toast.error(e instanceof Error ? e.message : String(e));
    }
  }

  async function forceSync() {
    syncing = true;
    try {
      await backend.forceSync();
      toast.success('已用面板期望状态覆盖内核规则');
      await load();
    } catch (e) {
      toast.error(e instanceof Error ? e.message : String(e));
    } finally {
      syncing = false;
    }
  }

  function exportSave(format: 'iptables-save' | 'nft' | 'json') {
    const a = document.createElement('a');
    a.href = `/api/v1/system/backup?format=${format}`;
    a.download = '';
    a.click();
  }

  const chainLabel: Record<string, string> = {
    input: 'INPUT（入站）',
    forward: 'FORWARD（转发）',
    dstnat: 'PREROUTING · DNAT',
    srcnat: 'POSTROUTING · SNAT',
  };
</script>

<div class="animate-fade-up space-y-4">
  <!-- 工具栏：同步状态 -->
  <Card class="flex flex-wrap items-center gap-3 p-4 shadow-soft">
    <div class="flex items-center gap-2.5">
      {#if drift === null}
        <Badge variant="outline" class="bg-muted text-muted-foreground">漂移未检测</Badge>
      {:else if drift.drifted}
        <Badge variant="outline" class="gap-1 bg-warning/10 text-warning ring-warning/25">
          <TriangleAlert size={12} /> 内核规则有漂移
        </Badge>
      {:else}
        <Badge variant="outline" class="gap-1 bg-success/10 text-success ring-success/20">
          <ShieldCheck size={12} /> 内核与面板一致
        </Badge>
      {/if}
      {#if lastCheck}
        <span class="text-[11.5px] text-muted-foreground">检测于 {fmtDateTime(lastCheck)}</span>
      {/if}
    </div>
    <div class="ml-auto flex items-center gap-2">
      <Button variant="outline" size="sm" onclick={checkDrift}>
        <GitCompare size={14} /> 检测漂移
      </Button>
      <Button variant="outline" size="sm" disabled={syncing} onclick={forceSync}>
        <RefreshCw size={14} class={syncing ? 'animate-spin' : ''} /> 重新同步
      </Button>
    </div>
  </Card>

  {#if drift?.drifted}
    <Card class="border-warning/40 bg-warning/[0.04] p-4 shadow-soft">
      <h3 class="flex items-center gap-2 text-[13px] font-semibold text-warning">
        <TriangleAlert size={15} />
        检测到 {drift.missing.length + drift.extra.length + drift.modified.length} 处漂移
      </h3>
      <p class="mt-1 text-[12px] text-muted-foreground">
        可能是其他工具或人工修改了面板链。点击「重新同步」以面板数据为准覆盖；或在其他机器上确认来源后再处理。
      </p>
      <div class="mt-3 space-y-1.5 font-mono text-[11.5px]">
        {#each drift.modified as m (m.tag)}
          <div class="rounded bg-card px-3 py-1.5">
            <span class="text-destructive">改</span>
            <span class="text-muted-foreground">[{m.tag}]</span>
            期望 <span class="text-success">{m.expected}</span>
            实际 <span class="text-destructive">{m.actual}</span>
          </div>
        {/each}
        {#each drift.extra as e (e.tag + e.spec)}
          <div class="rounded bg-card px-3 py-1.5">
            <span class="text-warning">多</span>
            <span class="text-muted-foreground">[{e.tag}]</span>
            {e.chain} {e.spec}
          </div>
        {/each}
        {#each drift.missing as m (m.tag + m.spec)}
          <div class="rounded bg-card px-3 py-1.5">
            <span class="text-info">缺</span>
            <span class="text-muted-foreground">[{m.tag}]</span>
            {m.spec}
          </div>
        {/each}
      </div>
    </Card>
  {/if}

  <!-- 面板链规则表 -->
  {#if loading}
    <div class="space-y-2">
      {#each Array(6) as _, i (i)}
        <div class="h-12 animate-pulse rounded-lg bg-foreground/[0.04]"></div>
      {/each}
    </div>
  {:else if snap}
    <div class="grid grid-cols-1 gap-4 lg:grid-cols-3">
      <Card class="overflow-hidden shadow-soft lg:col-span-2">
        <div class="flex items-center justify-between border-b border-border/60 px-4 py-3">
          <div>
            <h2 class="text-sm font-semibold">面板链规则（内核实时）</h2>
            <p class="mt-0.5 text-[11.5px] text-muted-foreground">
              后端 {snap.backend} · 共 {snap.rules.length} 条
            </p>
          </div>
          <div class="flex gap-1.5">
            <Button variant="ghost" size="icon" class="size-8" onclick={load} aria-label="刷新">
              <RefreshCw size={14} />
            </Button>
            <Button variant="ghost" size="icon" class="size-8" onclick={() => exportSave('json')} aria-label="导出备份">
              <Download size={14} />
            </Button>
          </div>
        </div>
        {#if snap.rules.length === 0}
          <div class="p-4">
            <EmptyState compact title="面板链为空" desc="创建转发规则或黑名单后，内核规则会显示在这里。" />
          </div>
        {:else}
          <div class="overflow-x-auto">
            <table class="w-full text-left text-[12.5px]">
              <thead>
                <tr class="border-b border-border/70 text-[11px] text-muted-foreground">
                  <th class="py-2 pr-3 pl-4 font-medium">链</th>
                  <th class="py-2 pr-3 font-medium">规则</th>
                  <th class="py-2 pr-3 font-medium">标签</th>
                  <th class="py-2 pr-4 text-right font-medium">命中</th>
                </tr>
              </thead>
              <tbody>
                {#each snap.rules as r, i (i)}
                  <tr class="border-b border-border/30 last:border-0 hover:bg-foreground/[0.02]">
                    <td class="py-2 pr-3 pl-4">
                      <span class="font-mono text-[11px] text-muted-foreground">{r.chain}</span>
                    </td>
                    <td class="max-w-[380px] truncate py-2 pr-3 font-mono text-[11.5px]" title={r.spec}>{r.spec || '—'}</td>
                    <td class="py-2 pr-3">
                      {#if r.tag}
                        <span class="font-mono text-[10.5px] text-primary/80">{r.tag}</span>
                      {/if}
                    </td>
                    <td class="font-num py-2 pr-4 text-right text-muted-foreground">
                      {fmtNum(r.counter.packets)} 包 / {fmtBytes(r.counter.bytes)}
                    </td>
                  </tr>
                {/each}
              </tbody>
            </table>
          </div>
        {/if}
      </Card>

      <div class="space-y-4">
        <Card class="p-4 shadow-soft">
          <h3 class="mb-3 text-sm font-semibold">链挂接状态</h3>
          <ul class="space-y-2.5 text-[12.5px]">
            {#each snap.chains as c (c.name)}
              <li class="flex items-center gap-2.5">
                <span class="h-2 w-2 rounded-full {c.exists && c.hooked ? 'bg-success' : 'bg-destructive'}"></span>
                <div class="min-w-0 flex-1">
                  <div class="font-mono text-[12px] font-medium">{c.name}</div>
                  <div class="text-[10.5px] text-muted-foreground">{chainLabel[c.name] ?? c.reference}</div>
                </div>
                {#if c.exists && c.hooked}
                  <Badge variant="outline" class="bg-success/10 text-[10px] text-success ring-success/20">已挂接</Badge>
                {:else}
                  <Badge variant="outline" class="bg-destructive/10 text-[10px] text-destructive ring-destructive/20">异常</Badge>
                {/if}
              </li>
            {/each}
          </ul>
        </Card>

        <Card class="p-4 shadow-soft">
          <h3 class="mb-2 flex items-center gap-2 text-sm font-semibold">
            <Database size={14} class="text-muted-foreground" />
            备份导出
          </h3>
          <p class="mb-3 text-[11.5px] leading-relaxed text-muted-foreground">
            导出当前内核规则用于跨机迁移或灾备。
          </p>
          <div class="grid gap-2">
            <Button variant="outline" size="sm" onclick={() => exportSave('json')}>面板期望状态（JSON）</Button>
            {#if snap.backend === 'iptables'}
              <Button variant="outline" size="sm" onclick={() => exportSave('iptables-save')}>iptables-save 格式</Button>
            {:else}
              <Button variant="outline" size="sm" onclick={() => exportSave('nft')}>nft ruleset 格式</Button>
            {/if}
          </div>
        </Card>
      </div>
    </div>
  {/if}
</div>
