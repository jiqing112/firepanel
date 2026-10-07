<script lang="ts">
  import { Activity, ArrowDownToLine, ShieldCheck, Zap, Server, Cpu, Timer, Layers, WifiOff } from '@lucide/svelte';
  import { Card } from '$lib/components/ui/card';
  import { Button } from '$lib/components/ui/button';
  import { Badge } from '$lib/components/ui/badge';
  import StatCard from '$lib/components/common/StatCard.svelte';
  import TrafficArea from '$lib/components/charts/TrafficArea.svelte';
  import BarRank from '$lib/components/charts/BarRank.svelte';
  import Sparkline from '$lib/components/charts/Sparkline.svelte';
  import EmptyState from '$lib/components/common/EmptyState.svelte';
  import { backend, onWSMessage, api, type SyncResult } from '$lib/api';
  import type { DashboardSummary } from '$lib/api';
  import { fmtBytes, fmtNum } from '$lib/utils';
  import { toast } from 'svelte-sonner';
  import DangerDialog from '$lib/components/common/DangerDialog.svelte';

  let data = $state<DashboardSummary | null>(null);
  let error = $state('');
  let connTrend = $state<number[]>([]);

  async function load() {
    error = '';
    try {
      data = await backend.dashboard();
      connTrend = data.samples.slice(-40).map((s) => s.conn_count);
      if (connTrend.length === 0) connTrend = [0];
    } catch (e) {
      error = e instanceof Error ? e.message : String(e);
    }
  }

  $effect(() => {
    load();
    const off = onWSMessage((topic, payload) => {
      if (topic !== 'stats' || !data) return;
      const p = payload as { ts: number; conn_count: number; rx_bps: number; tx_bps: number };
      data.conn_count = p.conn_count;
      data.rx_bps = p.rx_bps;
      data.tx_bps = p.tx_bps;
      connTrend = [...connTrend.slice(-39), p.conn_count];
      // 流量图实时追加（5s 粒度 → 追加到最近采样点）
      if (data.samples.length > 0) {
        const last = data.samples[data.samples.length - 1];
        last.rx_bps = p.rx_bps;
        last.tx_bps = p.tx_bps;
        data.samples = [...data.samples];
      }
    });
    return off;
  });

  /* 危险确认弹窗（仪表盘上的封禁等操作会触发） */
  let danger = $state<{ sync: SyncResult; onClose: (confirmed: boolean) => void } | null>(null);

  async function block(ip: string) {
    try {
      await backend.createIPList({ list_type: 'black', ip_or_cidr: ip, remark: '仪表盘手动封禁' });
      toast.success(`已将 ${ip} 加入黑名单`, { description: '规则已下发内核，立即生效' });
      load();
    } catch (e) {
      const err = e as { status?: number; message: string };
      toast.error(err.message);
    }
  }

  const trafficPoints = $derived.by(() => {
    if (!data) return [];
    return data.samples.map((s) => ({ t: s.ts * 1000, rx: s.rx_bps, tx: s.tx_bps }));
  });
</script>

<div class="animate-fade-up space-y-5">
  <div class="grid grid-cols-1 gap-4 sm:grid-cols-2 xl:grid-cols-4">
    <StatCard label="活跃连接" value={data ? fmtNum(data.conn_count) : '—'} accent="primary">
      {#snippet icon()}<Activity size={17} strokeWidth={2} />{/snippet}
    </StatCard>
    <StatCard label="今日流量" value={data ? fmtBytes(data.traffic_today) : '—'} accent="info">
      {#snippet icon()}<ArrowDownToLine size={17} strokeWidth={2} />{/snippet}
    </StatCard>
    <StatCard label="入站速率" value={data ? fmtBytes(data.rx_bps) : '—'} unit={data ? '/s' : ''} accent="success">
      {#snippet icon()}<Zap size={17} strokeWidth={2} />{/snippet}
    </StatCard>
    <StatCard label="出站速率" value={data ? fmtBytes(data.tx_bps) : '—'} unit={data ? '/s' : ''} accent="warning">
      {#snippet icon()}<ShieldCheck size={17} strokeWidth={2} />{/snippet}
    </StatCard>
  </div>

  <div class="grid grid-cols-1 gap-4 xl:grid-cols-3">
    <Card class="p-5 shadow-soft xl:col-span-2">
      <div class="mb-4 flex items-center justify-between gap-3">
        <div>
          <h2 class="text-sm font-semibold tracking-tight">流量趋势</h2>
          <p class="mt-0.5 text-xs text-muted-foreground">今日采样 · 实时更新</p>
        </div>
        <div class="flex items-center gap-4 text-[11.5px]">
          <span class="flex items-center gap-1.5">
            <span class="h-2 w-2 rounded-full bg-chart-1"></span>
            入站
            <span class="font-num ml-0.5 text-muted-foreground">{fmtBytes(data?.rx_bps ?? 0)}/s</span>
          </span>
          <span class="flex items-center gap-1.5">
            <span class="h-2 w-2 rounded-full bg-chart-2"></span>
            出站
          </span>
        </div>
      </div>
      {#if data && trafficPoints.length >= 2}
        <TrafficArea data={trafficPoints} height={264} />
      {:else if data}
        <EmptyState
          compact
          title="采样数据积累中"
          desc="面板启动后每分钟记录一次流量样本，稍后回来即可看到曲线。"
        >
          {#snippet icon()}<WifiOff size={20} />{/snippet}
        </EmptyState>
      {:else}
        <div class="animate-pulse rounded-lg bg-foreground/[0.04]" style="height:264px"></div>
      {/if}
    </Card>

    <Card class="p-5 shadow-soft">
      <div class="mb-4 flex items-center justify-between">
        <div>
          <h2 class="text-sm font-semibold tracking-tight">规则命中排行</h2>
          <p class="mt-0.5 text-xs text-muted-foreground">按内核计数器累计</p>
        </div>
      </div>
      {#if data}
        {#if data.hit_ranks.length > 0}
          <BarRank items={data.hit_ranks.map((r) => ({ label: r.name, value: r.hits }))} />
        {:else}
          <EmptyState compact title="暂无命中数据" desc="创建转发规则并产生流量后，这里会显示命中排行。" />
        {/if}
      {:else}
        <div class="animate-pulse space-y-4 pt-2">
          {#each Array(5) as _, i (i)}
            <div class="h-8 rounded-lg bg-foreground/[0.04]"></div>
          {/each}
        </div>
      {/if}
    </Card>
  </div>

  <div class="grid grid-cols-1 gap-4 xl:grid-cols-3">
    <Card class="p-5 shadow-soft xl:col-span-2">
      <div class="mb-3 flex items-center justify-between">
        <div>
          <h2 class="text-sm font-semibold tracking-tight">规则命中明细</h2>
          <p class="mt-0.5 text-xs text-muted-foreground">来自防火墙计数器（数据包数）</p>
        </div>
        <Button variant="ghost" size="sm" onclick={load}>刷新</Button>
      </div>
      {#if data && data.hit_ranks.length > 0}
        <div class="overflow-x-auto">
          <table class="w-full text-left text-[13px]">
            <thead>
              <tr class="border-b border-border/70 text-[11.5px] text-muted-foreground">
                <th class="py-2.5 pr-3 pl-1 font-medium">规则</th>
                <th class="py-2.5 pr-3 font-medium">命中包数</th>
                <th class="py-2.5 pr-1 text-right font-medium">占比</th>
              </tr>
            </thead>
            <tbody>
              {#each data.hit_ranks.slice(0, 8) as h (h.rule_id)}
                <tr class="border-b border-border/40 transition-colors last:border-0 hover:bg-foreground/[0.02]">
                  <td class="py-2.5 pr-3 pl-1 font-medium">{h.name}</td>
                  <td class="font-num py-2.5 pr-3">{fmtNum(h.hits)}</td>
                  <td class="font-num py-2.5 pr-1 text-right text-muted-foreground">
                    {data.hit_ranks[0].hits > 0 ? Math.round((h.hits / data.hit_ranks[0].hits) * 100) : 0}%
                  </td>
                </tr>
              {/each}
            </tbody>
          </table>
        </div>
      {:else if data}
        <EmptyState compact title="还没有规则命中" desc="转发或黑名单规则被流量命中后，会出现在这里。" />
      {:else}
        <div class="animate-pulse space-y-3">
          {#each Array(4) as _, i (i)}
            <div class="h-9 rounded-lg bg-foreground/[0.04]"></div>
          {/each}
        </div>
      {/if}
    </Card>

    <Card class="p-5 shadow-soft">
      <h2 class="mb-4 text-sm font-semibold tracking-tight">系统信息</h2>
      {#if data}
        <dl class="space-y-3.5 text-[13px]">
          <div class="flex items-center gap-3">
            <span class="flex h-8 w-8 items-center justify-center rounded-lg bg-primary/10 text-primary"><Server size={15} /></span>
            <div class="min-w-0 flex-1">
              <dt class="text-[11.5px] text-muted-foreground">防火墙后端</dt>
              <dd class="font-medium capitalize">{data.backend}</dd>
            </div>
            <Badge variant="outline" class="bg-success/10 text-success ring-success/20">运行中</Badge>
          </div>
          <div class="flex items-center gap-3">
            <span class="flex h-8 w-8 items-center justify-center rounded-lg bg-info/10 text-info"><Cpu size={15} /></span>
            <div class="min-w-0 flex-1">
              <dt class="text-[11.5px] text-muted-foreground">采样窗口</dt>
              <dd class="font-num font-medium">{data.samples.length} 个样本</dd>
            </div>
          </div>
          <div class="flex items-center gap-3">
            <span class="flex h-8 w-8 items-center justify-center rounded-lg bg-success/10 text-success"><Timer size={15} /></span>
            <div class="min-w-0 flex-1">
              <dt class="text-[11.5px] text-muted-foreground">实时连接</dt>
              <dd class="font-num font-medium">{fmtNum(data.conn_count)} 条</dd>
            </div>
          </div>
          <div class="flex items-center gap-3">
            <span class="flex h-8 w-8 items-center justify-center rounded-lg bg-warning/15 text-warning"><Layers size={15} /></span>
            <div class="min-w-0 flex-1">
              <dt class="text-[11.5px] text-muted-foreground">出站峰值（今日）</dt>
              <dd class="font-num font-medium">
                {fmtBytes(Math.max(...data.samples.map((s) => s.tx_bps), 0))}/s
              </dd>
            </div>
          </div>
        </dl>
        <div class="mt-5 border-t border-border/60 pt-4">
          <div class="mb-2 flex items-center justify-between text-[11.5px] text-muted-foreground">
            <span>连接数走势</span>
            <span class="font-num">实时</span>
          </div>
          <Sparkline values={connTrend} />
        </div>
      {:else}
        <div class="animate-pulse space-y-4">
          {#each Array(4) as _, i (i)}
            <div class="h-10 rounded-lg bg-foreground/[0.04]"></div>
          {/each}
        </div>
      {/if}
    </Card>
  </div>

  {#if error}
    <div class="rounded-lg border border-destructive/30 bg-destructive/5 p-4 text-sm text-destructive">
      加载失败：{error}
      <Button variant="outline" size="sm" class="ml-3" onclick={load}>重试</Button>
    </div>
  {/if}
</div>
