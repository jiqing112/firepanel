<script lang="ts">
  import { onMount } from 'svelte';
  import {
    ArrowDownToLine, ArrowUpFromLine, Activity, ChevronDown, ChevronUp, Network, Pause, Play, Search, Waves, WifiOff,
  } from '@lucide/svelte';
  import { Card } from '$lib/components/ui/card';
  import { Button } from '$lib/components/ui/button';
  import { Input } from '$lib/components/ui/input';
  import { Badge } from '$lib/components/ui/badge';
  import * as Dialog from '$lib/components/ui/dialog';
  import StatCard from '$lib/components/common/StatCard.svelte';
  import TrafficArea from '$lib/components/charts/TrafficArea.svelte';
  import EmptyState from '$lib/components/common/EmptyState.svelte';
  import { backend, type TrafficOverview } from '$lib/api';
  import { fmtBytes, fmtRate } from '$lib/utils';

  let data = $state<TrafficOverview | null>(null);
  let error = $state('');
  let selected = $state('');
  let paused = $state(false);
  let keyword = $state('');
  let loading = $state(true);
  let ifaceAddrs = $state<Record<string, string[]>>({});

  const REFRESH_MS = 3000;

  async function load() {
    if (paused || (typeof document !== 'undefined' && document.hidden)) return;
    try {
      data = await backend.trafficOverview();
      error = '';
    } catch (e) {
      error = e instanceof Error ? e.message : String(e);
    } finally {
      loading = false;
    }
  }

  $effect(() => {
    load();
    const id = setInterval(load, REFRESH_MS);
    return () => clearInterval(id);
  });

  backend
    .interfaces()
    .then((r) => {
      const map: Record<string, string[]> = {};
      for (const i of r.items) map[i.name] = i.addrs ?? [];
      ifaceAddrs = map;
    })
    .catch(() => undefined);

  /* ---------- 网卡（nload 式） ---------- */
  const ifaces = $derived((data?.interfaces ?? []).slice().sort((a, b) => a.name.localeCompare(b.name)));
  const chartIfaces = $derived(ifaces.filter((i) => i.name !== 'lo'));

  const selectedIface = $derived.by(() => {
    if (selected && ifaces.some((i) => i.name === selected)) return selected;
    // 默认选中当前流量最大的网卡
    let best = '';
    let bestSum = -1;
    for (const i of chartIfaces) {
      const s = i.rx_bps + i.tx_bps;
      if (s > bestSum) {
        bestSum = s;
        best = i.name;
      }
    }
    return best;
  });

  const chartPoints = $derived.by(() => {
    const win = data?.window?.[selectedIface] ?? [];
    return win.map((p) => ({ t: p.ts * 1000, rx: p.rx_bps, tx: p.tx_bps }));
  });

  const peak = $derived.by(() => {
    let prx = 0;
    let ptx = 0;
    for (const p of chartPoints) {
      prx = Math.max(prx, p.rx);
      ptx = Math.max(ptx, p.tx);
    }
    return { rx: prx, tx: ptx };
  });

  /* ---------- 连接（iftop 式） ---------- */
  const conns = $derived(data?.conns ?? []);
  const filteredConns = $derived.by(() => {
    const kw = keyword.trim().toLowerCase();
    if (!kw) return conns;
    return conns.filter(
      (c) => c.remote.toLowerCase().includes(kw) || c.local.toLowerCase().includes(kw) || c.process.toLowerCase().includes(kw),
    );
  });
  const shownConns = $derived(filteredConns.slice(0, 30));
  const connCount = $derived(conns.length);

  function isLocal(addr: string): boolean {
    return addr.startsWith('127.') || addr.startsWith('[::1]');
  }

  // IPv4-mapped IPv6 地址（[::ffff:1.2.3.4]:5678）简化为 1.2.3.4:5678
  function fmtAddr(a: string): string {
    const m = a.match(/^\[::ffff:(\d{1,3}(?:\.\d{1,3}){3})\](.*)$/);
    return m ? `${m[1]}${m[2]}` : a;
  }

  /* ---------- 网卡进程流量（模态框） ---------- */
  let dialogIface = $state('');
  let expanded = $state(false);
  const VISIBLE_ROWS = 8;

  interface ProcRow {
    process: string;
    pids: string[];
    tcpConns: number;
    udpSessions: number;
    remotes: number;
    active: boolean;
    rx_bps: number;
    tx_bps: number;
    rx_total: number;
    tx_total: number;
  }

  /** 从 ss 的 local 地址提取 IP 与作用域（"10.0.185.3:22" / "[::ffff:1.2.3.4]:80" / "[fe80::1%ens17]:546"） */
  function ipOfLocal(local: string): { ip: string; scope: string } {
    if (local.startsWith('[')) {
      const end = local.indexOf(']');
      let inner = local.slice(1, end < 0 ? local.length : end);
      let scope = '';
      const pi = inner.indexOf('%');
      if (pi >= 0) {
        scope = inner.slice(pi + 1);
        inner = inner.slice(0, pi);
      }
      return { ip: inner, scope };
    }
    const i = local.lastIndexOf(':');
    return { ip: i < 0 ? local : local.slice(0, i), scope: '' };
  }

  function ipOf(local: string): string {
    const { ip, scope } = ipOfLocal(local);
    if (scope) return '';
    const m = ip.match(/^::ffff:(\d{1,3}(?:\.\d{1,3}){3})$/);
    return m ? m[1] : ip;
  }

  /**
   * 连接与网卡的归属匹配。
   * 通配监听（0.0.0.0 / ::）无法确定入站网卡，归入除 lo 外的所有网卡（服务进程确实可能经任一网卡收流量）。
   */
  function connMatchesIface(local: string, iface: string): boolean {
    const { ip, scope } = ipOfLocal(local);
    if (scope) return scope === iface;
    if (ip.startsWith('127.') || ip === '::1') return iface === 'lo';
    const m = ip.match(/^::ffff:(\d{1,3}(?:\.\d{1,3}){3})$/);
    const v4 = m ? m[1] : ip;
    if (v4 === '0.0.0.0' || v4 === '::') return iface !== 'lo';
    return (ifaceAddrs[iface] ?? []).includes(v4);
  }

  const procRows = $derived.by(() => {
    if (!data || !dialogIface) return [] as ProcRow[];
    const map = new Map<string, ProcRow>();
    for (const c of data.conns) {
      if (!connMatchesIface(c.local, dialogIface)) continue;
      const key = c.process || '(未知)';
      let row = map.get(key);
      if (!row) {
        row = {
          process: key, pids: [], tcpConns: 0, udpSessions: 0, remotes: 0,
          active: false, rx_bps: 0, tx_bps: 0, rx_total: 0, tx_total: 0,
        };
        map.set(key, row);
      }
      const n = Math.max(c.count, 1);
      if (c.proto === 'udp') {
        row.udpSessions += n;
        row.remotes += Math.max(c.remotes, 1);
      } else {
        row.tcpConns += n;
        row.remotes += n;
        row.rx_bps += c.rx_bps;
        row.tx_bps += c.tx_bps;
        if (c.rx_bps + c.tx_bps > 1) row.active = true;
      }
      row.rx_total += c.rx_total;
      row.tx_total += c.tx_total;
      if (c.pid && !row.pids.includes(c.pid)) row.pids.push(c.pid);
    }
    // 活跃优先：有速率的进程在前（速率降序）；其余按会话数
    return [...map.values()].sort((a, b) => {
      if (a.active !== b.active) return a.active ? -1 : 1;
      if (a.active && b.active) return b.rx_bps + b.tx_bps - (a.rx_bps + a.tx_bps);
      return b.tcpConns + b.udpSessions - (a.tcpConns + a.udpSessions);
    });
  });

  const dialogTotalSessions = $derived(
    procRows.reduce((s, r) => s + r.tcpConns + r.udpSessions, 0),
  );

  /** 未归类流量 = 网卡速率 − 已归类连接速率（UDP 无字节计数、转发过路、协议开销、瞬时连接） */
  const unaccounted = $derived.by(() => {
    const iface = ifaces.find((i) => i.name === dialogIface);
    if (!iface || !dialogIface) return null;
    let rx = 0;
    let tx = 0;
    for (const c of data?.conns ?? []) {
      if (!connMatchesIface(c.local, dialogIface)) continue;
      rx += c.rx_bps;
      tx += c.tx_bps;
    }
    const out = {
      rx: Math.max(iface.rx_bps - rx, 0),
      tx: Math.max(iface.tx_bps - tx, 0),
    };
    if (out.rx < 1024 && out.tx < 1024) return null;
    return out;
  });

  const dialogIfaceRate = $derived(ifaces.find((i) => i.name === dialogIface));

  function openIfaceDialog(name: string) {
    selected = name;
    dialogIface = name;
    expanded = false;
  }

  onMount(() => () => undefined);
</script>

<div class="animate-fade-up space-y-4">
  <!-- 概览 -->
  <div class="grid gap-3 sm:grid-cols-2 lg:grid-cols-4">
    <StatCard label="下载速率" value={data ? fmtBytes(data.rx_bps) : '—'} unit={data ? '/s' : ''} accent="primary">
      {#snippet icon()}<ArrowDownToLine size={17} />{/snippet}
    </StatCard>
    <StatCard label="上传速率" value={data ? fmtBytes(data.tx_bps) : '—'} unit={data ? '/s' : ''} accent="warning">
      {#snippet icon()}<ArrowUpFromLine size={17} />{/snippet}
    </StatCard>
    <StatCard label="活跃连接" value={data ? String(connCount) : '—'} accent="info">
      {#snippet icon()}<Activity size={17} />{/snippet}
    </StatCard>
    <StatCard label="监控网卡" value={data ? String(chartIfaces.length) : '—'} accent="success">
      {#snippet icon()}<Network size={17} />{/snippet}
    </StatCard>
  </div>

  {#if error}
    <div class="rounded-lg border border-destructive/30 bg-destructive/5 px-4 py-2.5 text-[12.5px] text-destructive">
      采样失败：{error}
    </div>
  {/if}

  <!-- 网卡实时流量 -->
  <Card class="p-5 shadow-soft">
    <div class="mb-3.5 flex flex-wrap items-center gap-2.5">
      <div class="min-w-0">
        <h2 class="flex items-center gap-2 text-sm font-semibold">
          <Waves size={15} class="text-primary" />
          网卡实时流量
        </h2>
        <p class="mt-0.5 text-[11.5px] text-muted-foreground">按网卡统计收发速率，窗口约 5 分钟（2s 粒度）</p>
      </div>
      <div class="ml-auto flex flex-wrap items-center gap-1.5">
        {#each chartIfaces as i (i.name)}
          <button
            type="button"
            onclick={() => (selected = i.name)}
            class="rounded-lg border px-2.5 py-1 font-mono text-[11.5px] font-medium transition-[color,background-color,border-color,box-shadow,transform,opacity] duration-150
              {selectedIface === i.name
                ? 'border-primary/50 bg-primary/[0.08] text-primary ring-1 ring-primary/30'
                : 'border-border/60 text-muted-foreground hover:border-primary/30 hover:text-foreground'}"
          >
            {i.name}
          </button>
        {/each}
      </div>
    </div>

    <div class="mb-1 flex items-center gap-4 text-[11.5px]">
      <span class="flex items-center gap-1.5">
        <span class="h-2 w-2 rounded-full bg-chart-1"></span>
        下载
        <span class="font-num text-muted-foreground">{fmtRate(ifaces.find((i) => i.name === selectedIface)?.rx_bps ?? 0)}</span>
      </span>
      <span class="flex items-center gap-1.5">
        <span class="h-2 w-2 rounded-full bg-chart-2"></span>
        上传
        <span class="font-num text-muted-foreground">{fmtRate(ifaces.find((i) => i.name === selectedIface)?.tx_bps ?? 0)}</span>
      </span>
      <span class="font-num ml-auto text-[10.5px] text-muted-foreground/70">
        峰值 ↓{fmtRate(peak.rx)} · ↑{fmtRate(peak.tx)}
      </span>
    </div>

    {#if data && chartPoints.length >= 2}
      <TrafficArea data={chartPoints} height={230} />
    {:else}
      <div class="flex animate-pulse items-center justify-center rounded-lg bg-foreground/[0.03] text-[12px] text-muted-foreground" style="height:230px">
        {loading ? '正在建立采样基线…' : '采样数据积累中，约 4 秒后出现曲线'}
      </div>
    {/if}

    <!-- 全部网卡列表 -->
    {#if ifaces.length > 0}
      <div class="mt-4 divide-y divide-border/50 border-t border-border/50">
        {#each ifaces as i (i.name)}
          <button
            type="button"
            onclick={() => openIfaceDialog(i.name)}
            title="点击查看 {i.name} 上的进程流量"
            class="group flex w-full flex-wrap items-center gap-x-5 gap-y-1 py-2 text-left transition-colors hover:bg-foreground/[0.02]"
          >
            <div class="flex min-w-[110px] items-center gap-2">
              <span class="font-mono text-[12.5px] font-semibold {selectedIface === i.name ? 'text-primary' : ''} group-hover:text-primary">{i.name}</span>
              {#if i.name === 'lo'}
                <Badge variant="outline" class="bg-muted/60 text-[9.5px] text-muted-foreground ring-border">回环</Badge>
              {/if}
            </div>
            <div class="font-num min-w-[92px] text-[12.5px] text-chart-1">↓ {fmtRate(i.rx_bps)}</div>
            <div class="font-num min-w-[92px] text-[12.5px] text-chart-2">↑ {fmtRate(i.tx_bps)}</div>
            <div class="font-num ml-auto hidden text-[11.5px] text-muted-foreground sm:block">
              累计 ↓{fmtBytes(i.rx_total)} · ↑{fmtBytes(i.tx_total)}
            </div>
          </button>
        {/each}
      </div>
    {/if}
  </Card>

  <!-- 实时连接 -->
  <Card class="p-5 shadow-soft">
    <div class="mb-3.5 flex flex-wrap items-center gap-2.5">
      <div class="min-w-0">
        <h2 class="flex items-center gap-2 text-sm font-semibold">
          <Activity size={15} class="text-primary" />
          实时连接
        </h2>
        <p class="mt-0.5 text-[11.5px] text-muted-foreground">TCP 连接按速率排序、UDP 会话按进程与对端聚合，3 秒自动刷新</p>
      </div>
      <div class="relative ml-auto">
        <Search size={13} class="absolute top-1/2 left-2.5 -translate-y-1/2 text-muted-foreground/60" />
        <Input bind:value={keyword} placeholder="搜索远端 / 进程…" class="h-8 w-44 pl-7 text-[12px]" />
      </div>
      <Button variant="outline" size="sm" class="h-8" onclick={() => (paused = !paused)}>
        {#if paused}<Play size={13} /> 继续{:else}<Pause size={13} /> 暂停{/if}
      </Button>
    </div>

    {#if data?.conns_error}
      <div class="rounded-lg border border-warning/30 bg-warning/[0.06] px-3.5 py-2.5 text-[12px] text-warning">
        连接级流量不可用：{data.conns_error}
      </div>
    {:else if loading && !data}
      <div class="space-y-2">
        {#each Array(5) as _, i (i)}
          <div class="h-10 animate-pulse rounded-lg bg-foreground/[0.04]"></div>
        {/each}
      </div>
    {:else if filteredConns.length === 0}
      <EmptyState
        compact
        title={keyword ? '没有匹配的连接' : '当前没有活跃 TCP 连接'}
        desc={keyword ? '换个关键词试试。' : '有连接产生流量后会实时出现在这里。'}
      >
        {#snippet icon()}<WifiOff size={20} />{/snippet}
        {#snippet action()}
          {#if keyword}
            <Button variant="outline" size="sm" onclick={() => (keyword = '')}>清除筛选</Button>
          {/if}
        {/snippet}
      </EmptyState>
    {:else}
      <div class="divide-y divide-border/50">
        {#each shownConns as c (`${c.proto}:${c.local}->${c.remote}:${c.count}`)}
          <div class="flex flex-wrap items-center gap-x-4 gap-y-1 py-2.5">
            <div class="min-w-[190px] flex-1 basis-56">
              <div class="flex items-center gap-1.5 font-mono text-[12.5px]">
                <span class="rounded px-1 py-px text-[9.5px] font-sans font-semibold uppercase ring-1 ring-inset {c.proto === 'udp' ? 'bg-warning/10 text-warning ring-warning/25' : 'bg-primary/10 text-primary ring-primary/20'}">{c.proto}</span>
                {#if c.proto === 'udp' && c.count > 1}
                  <span class="truncate font-medium">{c.count} 条会话 · {c.remotes} 个对端</span>
                  <span class="truncate text-muted-foreground">{fmtAddr(c.local)}</span>
                {:else}
                  <span class="truncate font-medium {isLocal(c.remote) ? 'text-muted-foreground' : ''}">{fmtAddr(c.remote)}</span>
                  <span class="text-muted-foreground/50">⇄</span>
                  <span class="truncate text-muted-foreground">{fmtAddr(c.local)}</span>
                {/if}
              </div>
            </div>
            <div class="min-w-[110px]">
              {#if c.process}
                <span class="text-[12px] font-medium">{c.process}</span>
                {#if c.proto === 'tcp' && c.pid}
                  <span class="font-num ml-1.5 text-[10.5px] text-muted-foreground">PID {c.pid}</span>
                {:else if c.proto === 'udp' && c.count === 1 && c.pid}
                  <span class="font-num ml-1.5 text-[10.5px] text-muted-foreground">PID {c.pid}</span>
                {/if}
              {:else}
                <span class="text-[11.5px] text-muted-foreground/70">—</span>
              {/if}
            </div>
            <div class="font-num min-w-[96px] text-[12.5px] font-medium {c.proto === 'udp' ? 'text-muted-foreground/50' : 'text-chart-1'}">{c.proto === 'udp' ? '↓ —' : `↓ ${fmtRate(c.rx_bps)}`}</div>
            <div class="font-num min-w-[96px] text-[12.5px] font-medium {c.proto === 'udp' ? 'text-muted-foreground/50' : 'text-chart-2'}">{c.proto === 'udp' ? '↑ —' : `↑ ${fmtRate(c.tx_bps)}`}</div>
            <div class="font-num ml-auto hidden text-[11px] text-muted-foreground md:block">
              {#if c.proto === 'tcp'}累计 ↓{fmtBytes(c.rx_total)} ↑{fmtBytes(c.tx_total)}{:else}速率未知{/if}
            </div>
          </div>
        {/each}
      </div>
      {#if filteredConns.length > shownConns.length}
        <p class="mt-3 border-t border-border/50 pt-2.5 text-center text-[11px] text-muted-foreground">
          仅显示前 {shownConns.length} 行（共 {filteredConns.length} 行），可搜索缩小范围
        </p>
      {/if}
    {/if}
  </Card>
</div>

<Dialog.Root open={dialogIface !== ''} onOpenChange={(o) => { if (!o) dialogIface = ''; }}>
  <Dialog.Content class="max-h-[90vh] overflow-y-auto sm:max-w-lg">
    <Dialog.Header>
      <Dialog.Title class="flex items-center gap-2">
        <Waves size={15} class="text-primary" />
        <span class="font-mono">{dialogIface}</span> 上的进程流量
      </Dialog.Title>
      <Dialog.Description>本机连接按进程聚合，活跃进程置顶，实时刷新（3 秒）。</Dialog.Description>
    </Dialog.Header>

    {#if dialogIfaceRate}
      <div class="flex items-center gap-4 rounded-lg border border-border/60 bg-foreground/[0.02] px-3.5 py-2 text-[12px]">
        <span class="font-num text-chart-1">↓ {fmtRate(dialogIfaceRate.rx_bps)}/s</span>
        <span class="font-num text-chart-2">↑ {fmtRate(dialogIfaceRate.tx_bps)}/s</span>
        <span class="font-num ml-auto text-[11px] text-muted-foreground">
          累计 ↓{fmtBytes(dialogIfaceRate.rx_total)} · ↑{fmtBytes(dialogIfaceRate.tx_total)}
        </span>
      </div>
    {/if}

    {#if procRows.length === 0}
      <EmptyState
        compact
        title="这块网卡上当前没有进程在跑流量"
        desc="统计口径为本机连接（socket 计数差分）；DNAT 转发的过路流量与本机进程无关，不计入。"
      />
    {:else}
      <div class="max-h-[46vh] space-y-1 overflow-y-auto pr-1">
        {#each (expanded ? procRows : procRows.slice(0, VISIBLE_ROWS)) as r (r.process)}
          <div class="rounded-lg border border-transparent px-3 py-2 {r.active ? 'border-primary/25 bg-primary/[0.06]' : 'bg-foreground/[0.02] opacity-70'}">
            <!-- 行 1：进程名 + 速率 -->
            <div class="flex items-baseline gap-2">
              <span class="truncate text-[13px] font-medium">{r.process}</span>
              {#if r.pids.length === 1}
                <span class="font-num shrink-0 text-[10.5px] text-muted-foreground">PID {r.pids[0]}</span>
              {:else if r.pids.length > 1}
                <span class="font-num shrink-0 text-[10.5px] text-muted-foreground">{r.pids.length} 个进程</span>
              {/if}
              {#if r.tcpConns > 0}
                <span class="font-num ml-auto shrink-0 text-[12.5px] font-medium">
                  <span class="text-chart-1">↓ {fmtRate(r.rx_bps)}/s</span>
                  <span class="mx-1 text-muted-foreground/40">·</span>
                  <span class="text-chart-2">↑ {fmtRate(r.tx_bps)}/s</span>
                </span>
              {:else}
                <span class="ml-auto shrink-0 text-[11px] text-muted-foreground/60">速率未知</span>
              {/if}
            </div>
            <!-- 行 2：连接构成 + 累计 -->
            <div class="mt-0.5 flex items-baseline gap-2 text-[11px] text-muted-foreground">
              <span class="truncate">
                {r.tcpConns > 0 ? `${r.tcpConns} 条 TCP` : ''}
                {r.tcpConns > 0 && r.udpSessions > 0 ? ' · ' : ''}
                {r.udpSessions > 0 ? `${r.udpSessions} UDP` : ''}
                {#if r.remotes > 1}<span class="text-muted-foreground/70"> → {r.remotes} 对端</span>{/if}
              </span>
              {#if r.tcpConns > 0}
                <span class="font-num ml-auto shrink-0">累计 ↓{fmtBytes(r.rx_total)} ↑{fmtBytes(r.tx_total)}</span>
              {/if}
            </div>
          </div>
        {/each}
      </div>

      {#if procRows.length > VISIBLE_ROWS}
        <Button variant="ghost" size="sm" class="mx-auto mt-1 h-7 text-[11.5px] text-muted-foreground" onclick={() => (expanded = !expanded)}>
          {expanded ? '收起' : `展开其余 ${procRows.length - VISIBLE_ROWS} 个进程`}
          {#if !expanded}<ChevronDown size={12} class="ml-0.5" />{:else}<ChevronUp size={12} class="ml-0.5" />{/if}
        </Button>
      {/if}

      <p class="mt-1 text-[10.5px] text-muted-foreground/70">
        共 {procRows.length} 个进程 · {dialogTotalSessions} 条连接/会话（3 秒刷新；活跃进程置顶高亮）
      </p>

      {#if unaccounted}
        <div class="mt-1 flex flex-wrap items-center gap-x-3 gap-y-1 rounded-lg border border-warning/30 bg-warning/[0.06] px-3.5 py-2 text-[12px]">
          <span class="font-medium text-warning">未归类流量 ↓{fmtRate(unaccounted.rx)}/s · ↑{fmtRate(unaccounted.tx)}/s</span>
          <span class="text-[11px] text-muted-foreground">来自 UDP 会话（内核不提供 UDP 字节计数）、转发过路流量、TCP/IP 包头开销与采样间隙的短连接</span>
        </div>
      {/if}
      <p class="text-[10.5px] leading-relaxed text-muted-foreground/70">
        进程归属依据连接地址与网卡的匹配（通配监听归入所有网卡）；UDP 速率受内核限制无法按进程统计。
      </p>
    {/if}

    <Dialog.Footer class="sm:justify-end">
      <Button variant="outline" onclick={() => (dialogIface = '')}>关闭</Button>
    </Dialog.Footer>
  </Dialog.Content>
</Dialog.Root>
