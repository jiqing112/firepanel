<script lang="ts">
  import { Bell, Send, Trash2, MessageSquare, Webhook, Mail } from '@lucide/svelte';
  import { Card } from '$lib/components/ui/card';
  import { Button } from '$lib/components/ui/button';
  import { Input } from '$lib/components/ui/input';
  import { Badge } from '$lib/components/ui/badge';
  import { Label } from '$lib/components/ui/label';
  import { Switch } from '$lib/components/ui/switch';
  import * as Checkbox from '$lib/components/ui/checkbox';
  import { toast } from 'svelte-sonner';
  import EmptyState from '$lib/components/common/EmptyState.svelte';
  import { backend, type AlertConfig } from '$lib/api';

  const EVENT_OPTIONS = [
    { value: 'apply_fail', label: '规则应用失败' },
    { value: 'drift', label: '检测到内核漂移' },
    { value: 'danger_pending', label: '高危变更待确认' },
    { value: 'health_down', label: '反代上游不可达' },
    { value: 'rollback', label: '自动回滚' },
    { value: 'jail_ban', label: '防爆破自动封禁' },
  ];

  let items = $state<AlertConfig[]>([]);
  let loading = $state(true);

  async function load() {
    loading = true;
    try {
      items = (await backend.alerts()).items;
    } catch (e) {
      toast.error(e instanceof Error ? e.message : String(e));
    } finally {
      loading = false;
    }
  }
  $effect(() => {
    load();
  });

  // 编辑状态（每渠道一张表单）
  let tgForm = $state({ bot_token: '', chat_id: '' });
  let whForm = $state({ url: '', method: 'POST' });
  let smForm = $state({ host: '', port: 587, username: '', password: '', from: '', to: '', use_tls: true });
  let events = $state<string[]>(['apply_fail', 'drift', 'danger_pending', 'health_down', 'rollback']);
  let enabledMap = $state<Record<string, boolean>>({});
  let testing = $state<Record<string, boolean>>({});
  let saving = $state<Record<string, boolean>>({});

  // 载入已有配置到表单（仅首次加载时填充）
  let hydrated = $state(false);
  $effect(() => {
    if (!loading && !hydrated && items.length > 0) {
      hydrated = true;
      for (const a of items) {
        enabledMap[a.channel] = a.enabled;
        try {
          const cfg = JSON.parse(a.config_json);
          if (a.channel === 'telegram') tgForm = { bot_token: cfg.bot_token ?? '', chat_id: cfg.chat_id ?? '' };
          if (a.channel === 'webhook') whForm = { url: cfg.url ?? '', method: cfg.method ?? 'POST' };
          if (a.channel === 'smtp') smForm = { host: cfg.host ?? '', port: cfg.port ?? 587, username: cfg.username ?? '', password: cfg.password ?? '', from: cfg.from ?? '', to: cfg.to ?? '', use_tls: cfg.use_tls ?? true };
          const ev = JSON.parse(a.events_json);
          if (Array.isArray(ev) && ev.length > 0) events = ev;
        } catch { /* 忽略 */ }
      }
    }
  });

  function configOf(channel: string): unknown {
    if (channel === 'telegram') return tgForm;
    if (channel === 'webhook') return whForm;
    return smForm;
  }

  function validOf(channel: string): boolean {
    if (channel === 'telegram') return tgForm.bot_token.trim() !== '' && tgForm.chat_id.trim() !== '';
    if (channel === 'webhook') return whForm.url.startsWith('http');
    return smForm.host.trim() !== '' && smForm.to.trim() !== '';
  }

  async function save(channel: string) {
    if (!validOf(channel)) return;
    saving[channel] = true;
    try {
      await backend.upsertAlert({ channel, config: configOf(channel), events, enabled: enabledMap[channel] ?? false });
      await load();
      toast.success(`告警渠道「${channel}」已保存`);
    } catch (e) {
      toast.error(e instanceof Error ? e.message : String(e));
    } finally {
      saving[channel] = false;
    }
  }

  async function test(channel: string) {
    if (!validOf(channel)) return;
    testing[channel] = true;
    try {
      await backend.testAlert({ channel, config: configOf(channel) });
      toast.success('测试通知已发送，请查收');
    } catch (e) {
      toast.error(e instanceof Error ? e.message : String(e));
    } finally {
      testing[channel] = false;
    }
  }

  async function remove(a: AlertConfig) {
    try {
      await backend.deleteAlert(a.id);
      await load();
      toast.success('告警渠道已删除');
    } catch (e) {
      toast.error(e instanceof Error ? e.message : String(e));
    }
  }

  const channelMeta: Record<string, { label: string; icon: typeof Bell }> = {
    telegram: { label: 'Telegram', icon: MessageSquare },
    webhook: { label: 'Webhook', icon: Webhook },
    smtp: { label: '邮件 (SMTP)', icon: Mail },
  };
</script>

<div class="animate-fade-up space-y-4">
  <!-- 事件订阅 -->
  <Card class="p-5 shadow-soft">
    <h2 class="mb-1 text-sm font-semibold">事件订阅</h2>
    <p class="mb-3 text-[12px] text-muted-foreground">勾选需要推送的事件类型，对全部已启用渠道生效。</p>
    <div class="flex flex-wrap gap-x-6 gap-y-2.5">
      {#each EVENT_OPTIONS as opt (opt.value)}
        <label class="flex items-center gap-2 text-[13px]">
          <Checkbox.Root
            checked={events.includes(opt.value)}
            onCheckedChange={(v) => {
              events = v ? [...events, opt.value] : events.filter((x) => x !== opt.value);
            }}
          />
          {opt.label}
        </label>
      {/each}
    </div>
  </Card>

  {#if loading}
    <div class="grid gap-4 lg:grid-cols-3">
      {#each Array(3) as _, i (i)}
        <div class="h-64 animate-pulse rounded-xl bg-foreground/[0.04]"></div>
      {/each}
    </div>
  {:else}
    <div class="grid gap-4 lg:grid-cols-3">
      {#each ['telegram', 'webhook', 'smtp'] as channel (channel)}
        {@const existing = items.find((x) => x.channel === channel)}
        <Card class="flex flex-col p-5 shadow-soft">
          <div class="mb-4 flex items-center gap-2">
            <span class="flex h-9 w-9 items-center justify-center rounded-lg bg-primary/10 text-primary">
              {#if channel === 'telegram'}<MessageSquare size={16} />{:else if channel === 'webhook'}<Webhook size={16} />{:else}<Mail size={16} />{/if}
            </span>
            <div class="flex-1">
              <h3 class="text-sm font-semibold">{channelMeta[channel].label}</h3>
              {#if existing}
                <Badge variant="outline" class="mt-0.5 {existing.enabled ? 'bg-success/10 text-success ring-success/20' : 'bg-muted text-muted-foreground'}">
                  {existing.enabled ? '已启用' : '已停用'}
                </Badge>
              {/if}
            </div>
            {#if existing}
              <Button variant="ghost" size="icon" class="size-7 text-muted-foreground" onclick={() => remove(existing)} aria-label="删除">
                <Trash2 size={13} />
              </Button>
            {/if}
          </div>

          {#if channel === 'telegram'}
            <div class="grid gap-3">
              <div class="grid gap-1.5">
                <Label class="text-[11.5px]">Bot Token</Label>
                <Input placeholder="123456:ABC-DEF..." bind:value={tgForm.bot_token} class="h-8 font-mono text-[12px]" />
              </div>
              <div class="grid gap-1.5">
                <Label class="text-[11.5px]">Chat ID</Label>
                <Input placeholder="-1001234567890" bind:value={tgForm.chat_id} class="h-8 font-mono text-[12px]" />
              </div>
            </div>
          {:else if channel === 'webhook'}
            <div class="grid gap-3">
              <div class="grid gap-1.5">
                <Label class="text-[11.5px]">URL</Label>
                <Input placeholder="https://hooks.example.com/firepanel" bind:value={whForm.url} class="h-8 font-mono text-[12px]" />
              </div>
              <p class="text-[11px] text-muted-foreground">以 POST JSON 推送：type / message / detail / time / text</p>
            </div>
          {:else}
            <div class="grid gap-3">
              <div class="grid grid-cols-[1fr_72px] gap-2">
                <div class="grid gap-1.5">
                  <Label class="text-[11.5px]">SMTP 服务器</Label>
                  <Input placeholder="smtp.example.com" bind:value={smForm.host} class="h-8 font-mono text-[12px]" />
                </div>
                <div class="grid gap-1.5">
                  <Label class="text-[11.5px]">端口</Label>
                  <Input type="number" bind:value={smForm.port} class="h-8 font-num text-[12px]" />
                </div>
              </div>
              <div class="grid grid-cols-2 gap-2">
                <div class="grid gap-1.5">
                  <Label class="text-[11.5px]">用户名</Label>
                  <Input bind:value={smForm.username} class="h-8 text-[12px]" />
                </div>
                <div class="grid gap-1.5">
                  <Label class="text-[11.5px]">密码</Label>
                  <Input type="password" bind:value={smForm.password} class="h-8 text-[12px]" />
                </div>
              </div>
              <div class="grid gap-1.5">
                <Label class="text-[11.5px]">收件人（逗号分隔）</Label>
                <Input placeholder="ops@example.com" bind:value={smForm.to} class="h-8 font-mono text-[12px]" />
              </div>
            </div>
          {/if}

          <div class="mt-4 flex items-center gap-2 border-t border-border/50 pt-4">
            <Switch checked={enabledMap[channel] ?? false} onCheckedChange={(v) => (enabledMap[channel] = v)} />
            <span class="text-[12px] text-muted-foreground">启用</span>
            <Button variant="outline" size="sm" class="ml-auto h-7 text-[11.5px]" disabled={!validOf(channel) || testing[channel]} onclick={() => test(channel)}>
              <Send size={12} /> {testing[channel] ? '发送中…' : '测试'}
            </Button>
            <Button size="sm" class="h-7 text-[11.5px]" disabled={!validOf(channel) || saving[channel]} onclick={() => save(channel)}>
              {saving[channel] ? '保存中…' : '保存'}
            </Button>
          </div>
        </Card>
      {/each}
    </div>
  {/if}

  {#if !loading && items.length === 0}
    <EmptyState compact title="尚未配置告警渠道" desc="配置 Telegram / Webhook / 邮件后，异常事件会第一时间推送。" />
  {/if}
</div>
