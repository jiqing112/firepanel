<script lang="ts">
  import { Flame, ShieldCheck, ArrowRight } from '@lucide/svelte';
  import { fade, fly } from 'svelte/transition';
  import { cubicOut } from 'svelte/easing';
  import { Button } from '$lib/components/ui/button';
  import { Input } from '$lib/components/ui/input';
  import { Label } from '$lib/components/ui/label';
  import { BrandMark } from '$lib/components/common';
  import { backend } from '$lib/api';
  import { session } from '$lib/stores/session.svelte';
  import { navigate } from '$lib/router.svelte';
  import { t } from '$lib/i18n';

  let username = $state('');
  let password = $state('');
  let password2 = $state('');
  let error = $state('');
  let loading = $state(false);

  const strong = $derived(password.length >= 6);
  const match = $derived(password === password2);
  const valid = $derived(username.trim().length >= 2 && strong && match);

  async function submit(e: SubmitEvent) {
    e.preventDefault();
    if (!valid || loading) return;
    error = '';
    loading = true;
    try {
      const { token, user } = await backend.setup(username.trim(), password);
      localStorage.setItem('firepanel-token', token);
      session.user = user;
      navigate('/dashboard');
    } catch (err) {
      error = String(err instanceof Error ? err.message : err);
    } finally {
      loading = false;
    }
  }
</script>

<div class="relative flex min-h-svh items-center justify-center overflow-hidden px-4">
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

  <div class="relative w-full max-w-[400px]" in:fly={{ y: 14, duration: 350, easing: cubicOut }}>
    <div class="mb-7 flex flex-col items-center gap-3 text-center">
      <BrandMark size={46} />
      <div>
        <h1 class="text-[19px] font-semibold tracking-tight">初始化 {t('app.name')}</h1>
        <p class="mt-1 text-[12.5px] text-muted-foreground">创建管理员账号，开始管理这台服务器</p>
      </div>
    </div>

    <div class="rounded-2xl border border-border/70 bg-card p-6 shadow-lift">
      <form class="space-y-4" onsubmit={submit}>
        <div class="grid gap-2">
          <Label for="username">管理员用户名</Label>
          <Input id="username" autocomplete="username" bind:value={username} class="h-10" placeholder="admin" />
        </div>
        <div class="grid gap-2">
          <Label for="password">密码</Label>
          <Input id="password" type="password" autocomplete="new-password" bind:value={password} class="h-10" />
          {#if password && !strong}
            <p class="text-[11px] text-warning">至少 6 位字符</p>
          {/if}
        </div>
        <div class="grid gap-2">
          <Label for="password2">确认密码</Label>
          <Input id="password2" type="password" autocomplete="new-password" bind:value={password2} class="h-10" />
          {#if password2 && !match}
            <p class="text-[11px] text-destructive">两次输入不一致</p>
          {/if}
        </div>

        {#if error}
          <div class="flex items-center gap-1.5 rounded-lg bg-destructive/8 px-3 py-2 text-[12.5px] text-destructive" transition:fade={{ duration: 120 }}>
            {error}
          </div>
        {/if}

        <Button type="submit" class="h-10 w-full text-[13.5px]" disabled={loading || !valid}>
          {#if loading}
            <span class="mr-1 inline-block size-3.5 animate-spin rounded-full border-2 border-current border-t-transparent"></span>
            创建中…
          {:else}
            创建管理员
            <ArrowRight size={15} />
          {/if}
        </Button>
      </form>
    </div>

    <div class="mt-4 flex items-center justify-center gap-1.5 text-center text-[11.5px] text-muted-foreground/80">
      <ShieldCheck size={13} />
      密码以 bcrypt 加密存储，仅创建一次
    </div>
    <div class="mt-1 flex justify-center text-muted-foreground/40">
      <Flame size={12} />
    </div>
  </div>
</div>
