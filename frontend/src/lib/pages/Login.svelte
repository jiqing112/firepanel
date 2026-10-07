<script lang="ts">
  import { Flame, CircleAlert } from '@lucide/svelte';
  import { fade, fly } from 'svelte/transition';
  import { cubicOut } from 'svelte/easing';
  import { Button } from '$lib/components/ui/button';
  import { Input } from '$lib/components/ui/input';
  import { Label } from '$lib/components/ui/label';
  import { BrandMark } from '$lib/components/common';
  import { login, session } from '$lib/stores/session.svelte';
  import { navigate } from '$lib/router.svelte';
  import { t } from '$lib/i18n';

  let username = $state('');
  let password = $state('');
  let error = $state('');
  let loading = $state(false);

  $effect(() => {
    if (session.user) navigate('/dashboard', true);
  });

  async function submit(e: SubmitEvent) {
    e.preventDefault();
    if (loading) return;
    error = '';
    loading = true;
    try {
      await login(username.trim(), password);
    } catch (err) {
      error = err instanceof Error ? err.message : t('login.error');
    } finally {
      loading = false;
    }
  }
</script>

<div class="relative flex min-h-svh items-center justify-center overflow-hidden px-4">
  <!-- 背景装饰：柔和渐变 + 点阵 -->
  <div
    class="pointer-events-none absolute inset-0"
    style="background:
      radial-gradient(720px 420px at 15% 8%, oklch(0.92 0.05 240 / 0.7), transparent 62%),
      radial-gradient(640px 480px at 88% 92%, oklch(0.93 0.04 210 / 0.55), transparent 60%);"
  ></div>
  <div
    class="pointer-events-none absolute inset-0 opacity-[0.35]"
    style="background-image: radial-gradient(circle, var(--border) 1px, transparent 1px); background-size: 22px 22px;"
  ></div>

  <div class="relative w-full max-w-[380px]" in:fly={{ y: 14, duration: 350, easing: cubicOut }}>
    <div class="mb-7 flex flex-col items-center gap-3 text-center">
      <BrandMark size={46} />
      <div>
        <h1 class="text-[19px] font-semibold tracking-tight">{t('app.name')}</h1>
        <p class="mt-1 text-[12.5px] text-muted-foreground">{t('app.slogan')}</p>
      </div>
    </div>

    <div class="rounded-2xl border border-border/70 bg-card p-6 shadow-lift">
      <h2 class="mb-5 text-[15px] font-semibold">{t('login.title')}</h2>

      <form class="space-y-4" onsubmit={submit}>
        <div class="grid gap-2">
          <Label for="username">{t('login.username')}</Label>
          <Input id="username" autocomplete="username" bind:value={username} class="h-10" />
        </div>
        <div class="grid gap-2">
          <Label for="password">{t('login.password')}</Label>
          <Input id="password" type="password" autocomplete="current-password" bind:value={password} class="h-10" />
        </div>

        {#if error}
          <div class="flex items-center gap-1.5 rounded-lg bg-destructive/8 px-3 py-2 text-[12.5px] text-destructive" transition:fade={{ duration: 120 }}>
            <CircleAlert size={14} />
            {error}
          </div>
        {/if}

        <Button type="submit" class="h-10 w-full text-[13.5px]" disabled={loading || !username || !password}>
          {#if loading}
            <span class="mr-1 inline-block size-3.5 animate-spin rounded-full border-2 border-current border-t-transparent"></span>
            登录中…
          {:else}
            {t('login.submit')}
          {/if}
        </Button>
      </form>
    </div>
  </div>
</div>
