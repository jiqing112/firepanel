<script lang="ts">
  import { Moon, Sun, Menu, RefreshCw } from '@lucide/svelte';
  import { Button } from '$lib/components/ui/button';
  import { Badge } from '$lib/components/ui/badge';
  import { toggleTheme, theme } from '$lib/theme.svelte';
  import { layout } from './state.svelte';
  import { fwinfo, loadFwInfo } from '$lib/stores/fwinfo.svelte';
  import { session } from '$lib/stores/session.svelte';

  let { title, subtitle }: { title: string; subtitle?: string } = $props();

  let refreshing = $state(false);
  function refresh() {
    refreshing = true;
    setTimeout(() => (refreshing = false), 600);
  }

  $effect(() => {
    loadFwInfo();
  });
</script>

<header class="sticky top-0 z-20 border-b border-border/70 bg-background/95">
  <div class="flex h-14 items-center gap-3 px-5 lg:px-8">
    <Button variant="ghost" size="icon" class="lg:hidden" onclick={() => (layout.mobileMenuOpen = true)} aria-label="菜单">
      <Menu size={18} />
    </Button>

    <div class="min-w-0 flex-1">
      <h1 class="truncate text-[15px] font-semibold tracking-tight">{title}</h1>
      {#if subtitle}
        <p class="truncate text-xs text-muted-foreground">{subtitle}</p>
      {/if}
    </div>

    <div class="flex items-center gap-1.5">
      <Badge variant="secondary" class="hidden font-mono text-[11px] sm:inline-flex">{fwinfo.backend || '…'}</Badge>
      <Button variant="ghost" size="icon" class="text-muted-foreground" onclick={refresh} aria-label="刷新">
        <RefreshCw size={16} class={refreshing ? 'animate-spin' : ''} />
      </Button>
      <Button variant="ghost" size="icon" class="text-muted-foreground" onclick={toggleTheme} aria-label="切换主题">
        {#if theme.resolved === 'dark'}
          <Sun size={16} />
        {:else}
          <Moon size={16} />
        {/if}
      </Button>
      <div
        class="ml-1 flex h-8 w-8 items-center justify-center rounded-full bg-gradient-to-br from-sky-400 to-blue-600 text-xs font-semibold text-white shadow-soft select-none"
        title={session.user?.username || ''}
      >
        {(session.user?.username?.[0] || '?').toUpperCase()}
      </div>
    </div>
  </div>
</header>
