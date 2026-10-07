<script lang="ts">
  import { ScrollText, Search, Pause, Play, Radio } from '@lucide/svelte';
  import { fade } from 'svelte/transition';
  import { Card } from '$lib/components/ui/card';
  import { Button } from '$lib/components/ui/button';
  import { Input } from '$lib/components/ui/input';
  import { Badge } from '$lib/components/ui/badge';
  import { toast } from 'svelte-sonner';
  import EmptyState from '$lib/components/common/EmptyState.svelte';
  import { backend, onWSMessage, type AuditEntry } from '$lib/api';
  import { fmtDateTime } from '$lib/utils';

  let items = $state<AuditEntry[]>([]);
  let live = $state<AuditEntry[]>([]);
  let liveOn = $state(true);
  let keyword = $state('');
  let loading = $state(true);

  async function load() {
    loading = true;
    try {
      items = (await backend.audit(200)).items;
    } catch (e) {
      toast.error(e instanceof Error ? e.message : String(e));
    } finally {
      loading = false;
    }
  }
  $effect(() => {
    load();
    return onWSMessage((topic, payload) => {
      if (topic !== 'events' || !liveOn) return;
      const ev = payload as { type?: string; message?: string; detail?: string; timestamp?: string };
      live = [
        {
          id: Date.now(),
          username: 'system',
          action: ev.type ?? 'event',
          target: '',
          detail: ev.detail ? `${ev.message}（${ev.detail}）` : (ev.message ?? ''),
          ip: '',
          created_at: ev.timestamp ?? new Date().toISOString(),
        },
        ...live.slice(0, 99),
      ];
    });
  });

  const actionStyle: Record<string, string> = {
    create: 'bg-success/10 text-success',
    update: 'bg-info/10 text-info',
    delete: 'bg-destructive/10 text-destructive',
    login: 'bg-primary/10 text-primary',
    login_fail: 'bg-destructive/10 text-destructive',
    sync: 'bg-info/10 text-info',
    import: 'bg-warning/10 text-warning',
    restore: 'bg-warning/10 text-warning',
  };

  const filtered = $derived.by(() => {
    const kw = keyword.trim().toLowerCase();
    const base = items;
    if (!kw) return base;
    return base.filter((e) =>
      `${e.username} ${e.action} ${e.target} ${e.detail} ${e.ip}`.toLowerCase().includes(kw),
    );
  });
</script>

<div class="animate-fade-up grid grid-cols-1 gap-4 xl:grid-cols-3">
  <!-- 操作审计 -->
  <Card class="xl:col-span-2 shadow-soft">
    <div class="flex flex-wrap items-center gap-2.5 border-b border-border/60 px-5 py-3.5">
      <h2 class="text-sm font-semibold">操作审计</h2>
      <span class="font-num text-[11px] text-muted-foreground">最近 {filtered.length} 条</span>
      <div class="relative ml-auto min-w-[200px] flex-1 sm:max-w-xs">
        <Search size={14} class="pointer-events-none absolute top-1/2 left-3 -translate-y-1/2 text-muted-foreground" />
        <Input placeholder="搜索用户、动作、对象…" bind:value={keyword} class="h-8 pl-8 text-[12.5px]" />
      </div>
      <Button variant="ghost" size="sm" onclick={load}>刷新</Button>
    </div>

    {#if loading}
      <div class="space-y-2 p-5">
        {#each Array(8) as _, i (i)}
          <div class="h-9 animate-pulse rounded-lg bg-foreground/[0.04]"></div>
        {/each}
      </div>
    {:else if filtered.length === 0}
      <div class="p-5">
        <EmptyState compact title="暂无审计记录" desc="登录、规则变更、同步等操作都会记录在这里。" />
      </div>
    {:else}
      <div class="max-h-[560px] overflow-y-auto">
        <table class="w-full text-left text-[12.5px]">
          <thead class="sticky top-0 bg-card">
            <tr class="border-b border-border/60 text-[11px] text-muted-foreground">
              <th class="px-5 py-2 font-medium">时间</th>
              <th class="px-3 py-2 font-medium">用户</th>
              <th class="px-3 py-2 font-medium">动作</th>
              <th class="px-3 py-2 font-medium">对象</th>
              <th class="px-5 py-2 font-medium">来源 IP</th>
            </tr>
          </thead>
          <tbody>
            {#each filtered as e (e.id)}
              <tr class="border-b border-border/30 last:border-0 hover:bg-foreground/[0.02]">
                <td class="font-num px-5 py-2 text-muted-foreground">{fmtDateTime(e.created_at)}</td>
                <td class="px-3 py-2 font-medium">{e.username || '—'}</td>
                <td class="px-3 py-2">
                  <span class="inline-flex rounded px-1.5 py-0.5 text-[10.5px] font-medium {actionStyle[e.action] ?? 'bg-muted text-muted-foreground'}">
                    {e.action}
                  </span>
                </td>
                <td class="max-w-[260px] truncate px-3 py-2 font-mono text-[11.5px]" title={e.target + (e.detail ? ' · ' + e.detail : '')}>
                  {e.target || e.detail || '—'}
                </td>
                <td class="px-5 py-2 font-mono text-[11.5px] text-muted-foreground">{e.ip || '—'}</td>
              </tr>
            {/each}
          </tbody>
        </table>
      </div>
    {/if}
  </Card>

  <!-- 实时事件流 -->
  <Card class="shadow-soft">
    <div class="flex items-center gap-2.5 border-b border-border/60 px-5 py-3.5">
      <h2 class="flex items-center gap-1.5 text-sm font-semibold">
        <Radio size={14} class={liveOn ? 'text-success' : 'text-muted-foreground'} />
        实时事件
      </h2>
      <Button
        variant="outline" size="sm"
        class="ml-auto h-7 px-2 text-[11.5px]"
        onclick={() => (liveOn = !liveOn)}
      >
        {#if liveOn}<Pause size={12} /> 暂停{:else}<Play size={12} /> 继续{/if}
      </Button>
    </div>
    {#if live.length === 0}
      <div class="p-5">
        <EmptyState compact title="等待事件…" desc="规则变更、漂移、健康状态切换等事件会实时出现在这里。" />
      </div>
    {:else}
      <div class="max-h-[560px] space-y-1.5 overflow-y-auto p-4">
        {#each live as e (e.id)}
          <div class="rounded-lg border border-border/50 bg-foreground/[0.015] px-3 py-2 text-[12px]" in:fade={{ duration: 200 }}>
            <div class="flex items-center gap-2">
              <span class="font-num text-[10.5px] text-muted-foreground">{new Date(e.created_at).toLocaleTimeString('zh-CN', { hour12: false })}</span>
              <Badge variant="outline" class="h-4 bg-muted px-1.5 text-[9.5px] text-muted-foreground">{e.action}</Badge>
            </div>
            <p class="mt-1 leading-relaxed">{e.detail}</p>
          </div>
        {/each}
      </div>
    {/if}
  </Card>
</div>
