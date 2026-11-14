<script lang="ts">
  // 网络拓扑独立页：互联网 → 引擎 → 监听 → 站点 → 后端。复刻自 gately views/WebTopologyPage.vue。
  import { Search, Server, Workflow } from '@lucide/svelte';
  import { Button } from '$lib/components/ui/button';
  import { Input } from '$lib/components/ui/input';
  import WebTopology from '$lib/components/WebTopology.svelte';

  let filterText = $state('');

  // 拓扑页的编辑按钮跳回 Web 服务页并原地打开对话框
  function editSite(listenerId: number, siteId: number) {
    location.hash = `/proxy?editListener=${listenerId}&editSite=${siteId}`;
  }
  function editListener(listenerId: number) {
    location.hash = `/proxy?editListener=${listenerId}`;
  }
</script>

<div class="animate-fade-up space-y-4">
  <div class="flex flex-wrap items-center gap-2.5">
    <div class="flex h-9 w-9 items-center justify-center rounded-xl bg-primary/10 text-primary">
      <Workflow size={18} />
    </div>
    <div class="min-w-0 flex-1">
      <h1 class="text-[16px] font-semibold tracking-tight">网络拓扑</h1>
      <p class="text-[12px] text-muted-foreground">实时流量拓扑与访客链路，每 2 秒刷新</p>
    </div>
    <div class="relative">
      <Search size={13} class="pointer-events-none absolute left-2.5 top-1/2 -translate-y-1/2 text-muted-foreground" />
      <Input class="h-8 w-52 pl-8 text-[12.5px]" placeholder="搜索站点…" bind:value={filterText} />
    </div>
    <Button variant="outline" size="sm" class="h-8 gap-1.5" onclick={() => (location.hash = '#/proxy')}>
      <Server size={14} /> Web 服务
    </Button>
  </div>

  <WebTopology {filterText} onedit={editSite} oneditlistener={editListener} />
</div>
