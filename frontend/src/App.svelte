<script lang="ts">
  import { initTheme } from '$lib/theme.svelte';
  import { router, navigate, parsePath, initRouter } from '$lib/router.svelte';
  import { session, initSession } from '$lib/stores/session.svelte';
  import { navGroups } from '$lib/nav';
  import { t, type I18nKey } from '$lib/i18n';
  import type { Component } from 'svelte';
  import AppShell from '$lib/components/layout/AppShell.svelte';
  import Dashboard from '$lib/pages/Dashboard.svelte';
  import Forwards from '$lib/pages/Forwards.svelte';
  import Ports from '$lib/pages/Ports.svelte';
  import Traffic from '$lib/pages/Traffic.svelte';
  import IPLists from '$lib/pages/IPLists.svelte';
  import FirewallRules from '$lib/pages/FirewallRules.svelte';
  import Proxy from '$lib/pages/Proxy.svelte';
  import ProxyTopology from '$lib/pages/ProxyTopology.svelte';
  import Logs from '$lib/pages/Logs.svelte';
  import Backup from '$lib/pages/Backup.svelte';
  import RateLimit from '$lib/pages/RateLimit.svelte';
  import NAT from '$lib/pages/NAT.svelte';
  import Allow from '$lib/pages/Allow.svelte';
  import Fail2ban from '$lib/pages/Fail2ban.svelte';
  import Tasks from '$lib/pages/Tasks.svelte';
  import Alerts from '$lib/pages/Alerts.svelte';
  import Settings from '$lib/pages/Settings.svelte';
  import Login from '$lib/pages/Login.svelte';
  import Setup from '$lib/pages/Setup.svelte';
  import { Toaster } from '$lib/components/ui/sonner';
  import { TooltipProvider } from '$lib/components/ui/tooltip';
  import { fade } from 'svelte/transition';

  let booted = $state(false);

  $effect(() => {
    initTheme();
    initRouterSafe();
  });

  async function initRouterSafe() {
    initRouter();
    await initSession();
    booted = true;
  }

  const { pathname } = $derived(parsePath(router.path));

  const isAuthPage = $derived(pathname === '/login' || pathname === '/setup');

  const knownPaths = navGroups.flatMap((g) => g.items.map((i) => i.path));

  // 路由守卫
  $effect(() => {
    if (!booted) return;
    if (session.needsSetup && pathname !== '/setup') {
      navigate('/setup', true);
      return;
    }
    if (!session.user && !isAuthPage) {
      navigate('/login', true);
      return;
    }
    // 已登录访问登录页 → 回仪表盘
    if (session.user && (pathname === '/login' || pathname === '/')) {
      navigate('/dashboard', true);
      return;
    }
    // 未知路径 → 回仪表盘
    if (session.user && !knownPaths.includes(pathname)) {
      navigate('/dashboard', true);
    }
  });

  function pageMeta(path: string): { comp: Component; titleKey: I18nKey } {
    switch (path) {
      case '/dashboard':
        return { comp: Dashboard, titleKey: 'nav.dashboard' };
      case '/forwards':
        return { comp: Forwards, titleKey: 'nav.forwards' };
      case '/ports':
        return { comp: Ports, titleKey: 'nav.ports' };
      case '/traffic':
        return { comp: Traffic, titleKey: 'nav.traffic' };
      case '/iplists':
        return { comp: IPLists, titleKey: 'nav.iplists' };
      case '/firewall':
        return { comp: FirewallRules, titleKey: 'nav.firewall' };
      case '/proxy':
        return { comp: Proxy, titleKey: 'nav.proxy' };
      case '/proxy/topology':
        return { comp: ProxyTopology, titleKey: 'nav.topology' };
      case '/logs':
        return { comp: Logs, titleKey: 'nav.logs' };
      case '/nat':
        return { comp: NAT, titleKey: 'nav.nat' };
      case '/allow':
        return { comp: Allow, titleKey: 'nav.allow' };
      case '/ratelimit':
        return { comp: RateLimit, titleKey: 'nav.ratelimit' };
      case '/fail2ban':
        return { comp: Fail2ban, titleKey: 'nav.fail2ban' };
      case '/tasks':
        return { comp: Tasks, titleKey: 'nav.tasks' };
      case '/alerts':
        return { comp: Alerts, titleKey: 'nav.alerts' };
      case '/settings':
        return { comp: Settings, titleKey: 'nav.settings' };
      case '/backup':
        return { comp: Backup, titleKey: 'nav.backup' };
      default:
        return { comp: Dashboard, titleKey: 'nav.dashboard' };
    }
  }

  const meta = $derived(pageMeta(pathname));
  const Page = $derived(meta.comp);
</script>

<TooltipProvider delayDuration={200}>
{#if booted}
  {#if pathname === '/setup' && session.needsSetup}
    <Setup />
  {:else if !session.user}
    <Login />
  {:else}
    <AppShell title={t(meta.titleKey)}>
      <!-- 不用 key+过渡 包裹路由页：后台标签页 rAF 节流时过渡回调不执行，
           会遗留僵尸 DOM，导致页面停在加载骨架且交互失效 -->
      <div>
          <Page />
      </div>
    </AppShell>
  {/if}
{/if}
</TooltipProvider>

<Toaster position="top-center" richColors closeButton />
