<script lang="ts">
  import { fade, fly } from 'svelte/transition';
  import { cubicOut } from 'svelte/easing';
  import { navGroups } from '$lib/nav';
  import { t } from '$lib/i18n';
  import { router, navigate } from '$lib/router.svelte';
  import { BrandMark } from '$lib/components/common';

  let { open = $bindable(false) }: { open?: boolean } = $props();

  const isActive = (path: string) => router.path === path || router.path.startsWith(path + '?');
</script>

{#snippet navList()}
  <nav class="flex flex-1 flex-col gap-6 px-3 py-4">
    {#each navGroups as group (group.labelKey)}
      <div>
        <div class="px-3 pb-2 text-[11px] font-medium tracking-wider text-muted-foreground/70 uppercase">
          {t(group.labelKey as never)}
        </div>
        <ul class="space-y-0.5">
          {#each group.items as item (item.path)}
            <li>
              <a
                href="#{item.path}"
                class="group relative flex items-center gap-2.5 rounded-lg px-3 py-2 text-sm transition-colors duration-150
                  {isActive(item.path)
                    ? 'bg-accent font-medium text-accent-foreground'
                    : 'text-sidebar-foreground hover:bg-foreground/[0.04] hover:text-foreground'}"
                onclick={(e) => {
                  e.preventDefault();
                  navigate(item.path);
                  open = false;
                }}
              >
                {#if isActive(item.path)}
                  <span class="absolute top-1/2 left-0 h-4 w-[3px] -translate-y-1/2 rounded-r-full bg-primary"></span>
                {/if}
                <item.icon size={17} strokeWidth={1.9} class="shrink-0 transition-transform duration-150 group-hover:scale-105" />
                <span>{t(item.labelKey as never)}</span>
              </a>
            </li>
          {/each}
        </ul>
      </div>
    {/each}
  </nav>
{/snippet}

{#snippet brand()}
  <a
    href="#/dashboard"
    class="flex items-center gap-2.5 px-5 pt-5 pb-2"
    onclick={(e) => {
      e.preventDefault();
      navigate('/dashboard');
      open = false;
    }}
  >
    <BrandMark size={30} />
    <div class="text-[15px] font-semibold tracking-tight">{t('app.name')}</div>
  </a>
{/snippet}

<!-- 桌面端侧栏 -->
<aside class="fixed inset-y-0 left-0 z-30 hidden w-[236px] flex-col border-r border-sidebar-border bg-sidebar lg:flex">
  {@render brand()}
  {@render navList()}
</aside>

<!-- 移动端抽屉 -->
{#if open}
  <div class="fixed inset-0 z-40 lg:hidden">
    <button
      class="absolute inset-0 bg-slate-900/45"
      aria-label="关闭菜单"
      onclick={() => (open = false)}
      transition:fade={{ duration: 150 }}
    ></button>
    <aside
      class="absolute inset-y-0 left-0 flex w-[272px] flex-col border-r border-sidebar-border bg-sidebar shadow-lift"
      transition:fly={{ x: -280, duration: 220, easing: cubicOut }}
    >
      {@render brand()}
      {@render navList()}
    </aside>
  </div>
{/if}
