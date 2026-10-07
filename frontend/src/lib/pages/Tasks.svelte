<script lang="ts">
  import { CalendarClock, Plus, Trash2, Clock } from '@lucide/svelte';
  import { Card } from '$lib/components/ui/card';
  import { Button } from '$lib/components/ui/button';
  import { Input } from '$lib/components/ui/input';
  import { Badge } from '$lib/components/ui/badge';
  import { Label } from '$lib/components/ui/label';
  import { Switch } from '$lib/components/ui/switch';
  import * as Dialog from '$lib/components/ui/dialog';
  import * as Select from '$lib/components/ui/select';
  import { toast } from 'svelte-sonner';
  import EmptyState from '$lib/components/common/EmptyState.svelte';
  import { backend, type ScheduledTask } from '$lib/api';
  import { fmtDateTime } from '$lib/utils';

  let items = $state<ScheduledTask[]>([]);
  let forwards = $state<{ id: number; name: string }[]>([]);
  let loading = $state(true);

  async function load() {
    loading = true;
    try {
      items = (await backend.tasks()).items;
      const fwds = await backend.forwards();
      forwards = fwds.items.map((f) => ({ id: f.id, name: f.name }));
    } catch (e) {
      toast.error(e instanceof Error ? e.message : String(e));
    } finally {
      loading = false;
    }
  }
  $effect(() => {
    load();
  });

  const actionLabel: Record<string, string> = {
    forward_enable: '启用转发规则',
    forward_disable: '停用转发规则',
    iplist_expire: '清理过期名单',
    sync: '同步内核规则',
  };

  let createOpen = $state(false);
  let form = $state({ name: '', cron_expr: '0 2 * * *', action: 'forward_disable' as ScheduledTask['action'], target_id: 0, remark: '' });
  let submitting = $state(false);
  const valid = $derived(
    form.name.trim() !== '' &&
      /^[\d*,\-/\s]+$/.test(form.cron_expr) &&
      (form.action !== 'forward_enable' && form.action !== 'forward_disable' ? true : form.target_id > 0),
  );

  async function submit() {
    if (!valid || submitting) return;
    submitting = true;
    try {
      await backend.createTask({ ...form, enabled: true });
      createOpen = false;
      form = { name: '', cron_expr: '0 2 * * *', action: 'forward_disable', target_id: 0, remark: '' };
      await load();
      toast.success('定时任务已创建');
    } catch (e) {
      toast.error(e instanceof Error ? e.message : String(e));
    } finally {
      submitting = false;
    }
  }

  async function toggle(t: ScheduledTask, enabled: boolean) {
    try {
      await backend.updateTask(t.id, { ...t, enabled });
      await load();
      toast[enabled ? 'success' : 'info'](`任务「${t.name}」已${enabled ? '启用' : '停用'}`);
    } catch (e) {
      toast.error(e instanceof Error ? e.message : String(e));
    }
  }

  async function remove(t: ScheduledTask) {
    try {
      await backend.deleteTask(t.id);
      await load();
      toast.success(`任务「${t.name}」已删除`);
    } catch (e) {
      toast.error(e instanceof Error ? e.message : String(e));
    }
  }
</script>

<div class="animate-fade-up space-y-4">
  <div class="flex items-center gap-2.5">
    <span class="font-num text-xs text-muted-foreground">共 {items.length} 个任务</span>
    <span class="ml-auto"></span>
    <Button onclick={() => (createOpen = true)}><Plus size={16} /> 新增任务</Button>
  </div>

  {#if loading}
    <div class="space-y-2.5">
      {#each Array(3) as _, i (i)}
        <div class="h-16 animate-pulse rounded-xl bg-foreground/[0.04]"></div>
      {/each}
    </div>
  {:else if items.length === 0}
    <EmptyState
      title="还没有定时任务"
      desc="按 cron 计划自动启停转发规则、清理过期名单或同步内核。例如：工作时间停用游戏端口转发。"
    >
      {#snippet icon()}<CalendarClock size={22} />{/snippet}
      {#snippet action()}
        <Button onclick={() => (createOpen = true)}><Plus size={16} /> 新增任务</Button>
      {/snippet}
    </EmptyState>
  {:else}
    <div class="space-y-2.5">
      {#each items as t (t.id)}
        <Card class="lift-card group p-4">
          <div class="flex flex-wrap items-center gap-x-6 gap-y-3">
            <div class="min-w-[150px] flex-1 basis-40">
              <span class="text-[13.5px] font-medium">{t.name}</span>
              {#if t.remark}
                <p class="mt-0.5 truncate text-[11.5px] text-muted-foreground">{t.remark}</p>
              {/if}
            </div>
            <div class="flex items-center gap-2 text-[12px]">
              <Badge variant="outline" class="bg-primary/5 font-mono text-[11px] text-primary ring-primary/20">{t.cron_expr}</Badge>
              <span class="text-muted-foreground">
                {actionLabel[t.action]}
                {#if t.target_id > 0}
                  <span class="font-mono">#{t.target_id}</span>
                  {#if forwards.find((f) => f.id === t.target_id)}
                    （{forwards.find((f) => f.id === t.target_id)?.name}）
                  {/if}
                {/if}
              </span>
            </div>
            <div class="hidden items-center gap-1.5 text-[11px] text-muted-foreground lg:flex">
              <Clock size={12} />
              {#if t.last_run}
                上次 {fmtDateTime(t.last_run)}
              {:else}
                未执行
              {/if}
              {#if t.next_run}
                · 下次 {fmtDateTime(t.next_run)}
              {/if}
            </div>
            <div class="ml-auto flex items-center gap-2.5">
              <Switch checked={t.enabled} onCheckedChange={(v) => toggle(t, v)} />
              <Button variant="ghost" size="icon" class="size-7 text-muted-foreground opacity-0 transition-opacity hover:text-destructive group-hover:opacity-100" onclick={() => remove(t)} aria-label="删除">
                <Trash2 size={13} />
              </Button>
            </div>
          </div>
        </Card>
      {/each}
    </div>
  {/if}
</div>

<Dialog.Root bind:open={createOpen}>
  <Dialog.Content class="sm:max-w-md">
    <Dialog.Header>
      <Dialog.Title>新增定时任务</Dialog.Title>
      <Dialog.Description>使用 5 段 cron 表达式：分 时 日 月 周（如 0 9 * * 1-5 = 工作日每天 9 点）。</Dialog.Description>
    </Dialog.Header>
    <div class="grid gap-3.5">
      <div class="grid gap-2">
        <Label for="tk-name">任务名称</Label>
        <Input id="tk-name" bind:value={form.name} />
      </div>
      <div class="grid grid-cols-2 gap-3">
        <div class="grid gap-2">
          <Label for="tk-cron">cron 表达式</Label>
          <Input id="tk-cron" placeholder="0 2 * * *" bind:value={form.cron_expr} class="font-mono" />
        </div>
        <div class="grid gap-2">
          <Label for="tk-target">目标规则 <span class="text-muted-foreground/70">（转发类动作）</span></Label>
          <Select.Root type="single" bind:value={form.target_id as unknown as string}>
            <Select.Trigger class="w-full">
              {form.target_id > 0 ? `#${form.target_id} ${forwards.find((f) => f.id === form.target_id)?.name ?? ''}` : '仅同步/清理类需要'}
            </Select.Trigger>
            <Select.Content>
              {#each forwards as f (f.id)}
                <Select.Item value={String(f.id)}>#{f.id} {f.name}</Select.Item>
              {/each}
            </Select.Content>
          </Select.Root>
        </div>
      </div>
      <div class="grid gap-2">
        <Label>动作</Label>
        <Select.Root type="single" bind:value={form.action}>
          <Select.Trigger class="w-full">{actionLabel[form.action]}</Select.Trigger>
          <Select.Content>
            {#each Object.entries(actionLabel) as [k, v] (k)}
              <Select.Item value={k}>{v}</Select.Item>
            {/each}
          </Select.Content>
        </Select.Root>
      </div>
      <div class="grid gap-2">
        <Label for="tk-remark">备注 <span class="text-muted-foreground/70">（可选）</span></Label>
        <Input id="tk-remark" bind:value={form.remark} />
      </div>
    </div>
    <Dialog.Footer class="gap-2 sm:justify-end">
      <Button variant="outline" onclick={() => (createOpen = false)}>取消</Button>
      <Button disabled={!valid || submitting} onclick={submit}>{submitting ? '创建中…' : '创建任务'}</Button>
    </Dialog.Footer>
  </Dialog.Content>
</Dialog.Root>
