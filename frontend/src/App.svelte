<script lang="ts">
  import { initTheme } from '$lib/theme.svelte';
  import { router, navigate, parsePath } from '$lib/router.svelte';
  import { session, initSession } from '$lib/stores/session.svelte';
  import { navGroups } from '$lib/nav';
  import { t, type I18nKey } from '$lib/i18n';
  import AppShell from '$lib/components/layout/AppShell.svelte';
  import Dashboard from '$lib/pages/Dashboard.svelte';
  import Forwards from '$lib/pages/Forwards.svelte';
  import Ports from '$lib/pages/Ports.svelte';
  import Traffic from '$lib/pages/Traffic.svelte';
  import IPLists from '$lib/pages/IPLists.svelte';
  import FirewallRules from '$lib/pages/FirewallRules.svelte';
  import Proxy from '$lib/pages/Proxy.svelte';
  import Logs from '$lib/pages/Logs.svelte';
  import Backup from '$lib/pages/Backup.svelte';
  import RateLimit from '$lib/pages/RateLimit.svelte';
  import NAT from '$lib/pages/NAT.svelte';
  import Fail2ban from '$lib/pages/Fail2ban.svelte';
  import Tasks from '$lib/pages/Tasks.svelte';
  import Alerts from '$lib/pages/Alerts.svelte';
  import Settings from '$lib/pages/Settings.svelte';
  import Login from '$lib/pages/Login.svelte';
  import Setup from '$lib/pages/Setup.svelte';
  import Placeholder from '$lib/pages/Placeholder.svelte';
  import { Toaster } from '$lib/components/ui/sonner';
  import { TooltipProvider } from '$lib/components/ui/tooltip';
  import { fade } from 'svelte/transition';

  let booted = $state(false);
  let needsSetup = $state(false);

  $effect(() => {
    initTheme();
    initRouterSafe();
  });

  async function initRouterSafe() {
    const { initRouter } = await import('$lib/router.svelte');
    initRouter();
    const r = await initSession();
    needsSetup = r.needsSetup;
    booted = true;
  }

  const { pathname } = $derived(parsePath(router.path));

  const isAuthPage = $derived(pathname === '/login' || pathname === '/setup');

  // 路由守卫
  $effect(() => {
    if (!booted) return;
    if (needsSetup && pathname !== '/setup') {
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
    }
  });

  function pageMeta(path: string): { comp: unknown; titleKey: string; subKey?: string } {
    switch (path) {
      case '/dashboard':
        return { comp: Dashboard, titleKey: 'dashboard.title', subKey: '系统运行概览与实时流量' };
      case '/forwards':
        return { comp: Forwards, titleKey: 'forwards.title', subKey: 'TCP / UDP 端口映射与域名目标（DDNS）' };
      case '/ports':
        return { comp: Ports, titleKey: 'nav.ports', subKey: '监听端口扫描 · 端口记录台账' };
      case '/traffic':
        return { comp: Traffic, titleKey: 'nav.traffic', subKey: '网卡实时速率 · 连接级流量排行' };
      case '/iplists':
        return { comp: IPLists, titleKey: 'nav.iplists', subKey: '批量黑白名单管理与来源过滤' };
      case '/firewall':
        return { comp: FirewallRules, titleKey: 'nav.firewall', subKey: '内核规则视图 · 漂移检测 · 同步' };
      case '/proxy':
        return { comp: Proxy, titleKey: 'nav.proxy', subKey: 'HTTP/HTTPS 域名路由 · WebSocket 透传 · 自动证书' };
      case '/logs':
        return { comp: Logs, titleKey: 'nav.logs', subKey: '操作审计与实时事件流' };
      case '/nat':
        return { comp: NAT, titleKey: 'nav.nat', subKey: 'SNAT / MASQUERADE 出口策略' };
      case '/ratelimit':
        return { comp: RateLimit, titleKey: 'nav.ratelimit', subKey: '限速与连接数限制 · 防 CC / 扫描' };
      case '/fail2ban':
        return { comp: Fail2ban, titleKey: 'nav.fail2ban', subKey: '失败阈值自动封禁 · 到期自动解封' };
      case '/tasks':
        return { comp: Tasks, titleKey: 'nav.tasks', subKey: '计划启停规则与周期同步' };
      case '/alerts':
        return { comp: Alerts, titleKey: 'nav.alerts', subKey: 'Telegram / Webhook / 邮件推送' };
      case '/settings':
        return { comp: Settings, titleKey: 'nav.settings', subKey: '系统信息 · 用户与权限' };
      case '/backup':
        return { comp: Backup, titleKey: 'nav.backup', subKey: '备份导出 · 恢复 · 跨机迁移' };
      default: {
        const item = navGroups.flatMap((g) => g.items).find((i) => i.path === path);
        return { comp: Placeholder, titleKey: (item?.labelKey ?? 'nav.dashboard') as string };
      }
    }
  }

  const meta = $derived(pageMeta(pathname));
</script>

<TooltipProvider delayDuration={200}>
{#if booted}
  {#if pathname === '/setup' && needsSetup}
    <Setup />
  {:else if !session.user}
    <Login />
  {:else}
    <AppShell title={t(meta.titleKey as I18nKey)} subtitle={meta.subKey}>
      <!-- 不用 key+过渡 包裹路由页：后台标签页 rAF 节流时过渡回调不执行，
           会遗留僵尸 DOM，导致页面停在加载骨架且交互失效 -->
      <div>
          {#if meta.comp === Dashboard}
            <Dashboard />
          {:else if meta.comp === Forwards}
            <Forwards />
          {:else if meta.comp === Ports}
            <Ports />
          {:else if meta.comp === Traffic}
            <Traffic />
          {:else if meta.comp === IPLists}
            <IPLists />
          {:else if meta.comp === FirewallRules}
            <FirewallRules />
          {:else if meta.comp === Proxy}
            <Proxy />
          {:else if meta.comp === Logs}
            <Logs />
          {:else if meta.comp === NAT}
            <NAT />
          {:else if meta.comp === RateLimit}
            <RateLimit />
          {:else if meta.comp === Fail2ban}
            <Fail2ban />
          {:else if meta.comp === Tasks}
            <Tasks />
          {:else if meta.comp === Alerts}
            <Alerts />
          {:else if meta.comp === Settings}
            <Settings />
          {:else if meta.comp === Backup}
            <Backup />
          {:else}
            <Placeholder title={t(meta.titleKey as I18nKey)} />
          {/if}
      </div>
    </AppShell>
  {/if}
{/if}
</TooltipProvider>

<Toaster position="top-center" richColors closeButton />
