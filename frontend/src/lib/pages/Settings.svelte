<script lang="ts">
  import { Settings as SettingsIcon, UserPlus, Trash2, ShieldCheck, Server, Cpu, Terminal, HardDrive, Lock } from '@lucide/svelte';
  import { Card } from '$lib/components/ui/card';
  import { Button } from '$lib/components/ui/button';
  import { Input } from '$lib/components/ui/input';
  import { Badge } from '$lib/components/ui/badge';
  import { Label } from '$lib/components/ui/label';
  import { Switch } from '$lib/components/ui/switch';
  import * as Dialog from '$lib/components/ui/dialog';
  import * as Select from '$lib/components/ui/select';
  import { toast } from 'svelte-sonner';
  import { backend, onWSMessage, type User, type SystemInfo, type BasicAuthInfo } from '$lib/api';
  import { session } from '$lib/stores/session.svelte';
  import { fmtDateTime } from '$lib/utils';

  let info = $state<SystemInfo | null>(null);
  let users = $state<User[]>([]);
  let loading = $state(true);
  let baInfo = $state<BasicAuthInfo | null>(null);

  async function load() {
    loading = true;
    try {
      const [i, u, ba] = await Promise.all([
        backend.systemInfo(),
        backend.users().then((r) => r.items),
        backend.basicAuth().catch(() => null),
      ]);
      info = i;
      users = u;
      baInfo = ba;
      primeBA();
    } catch (e) {
      toast.error(e instanceof Error ? e.message : String(e));
    } finally {
      loading = false;
    }
  }
  $effect(() => {
    load();
    return onWSMessage((topic, payload) => {
      if (topic === 'events' && (payload as { type?: string }).type === 'basicauth_changed') {
        baLoadedFor = '';
        load();
      }
    });
  });

  /* Basic Auth */
  let baForm = $state({ enabled: false, username: '', password: '' });
  let baLoadedFor = $state('');
  let baSaving = $state(false);

  function primeBA() {
    if (!baInfo || baLoadedFor === 'done') return;
    baForm = { enabled: baInfo.enabled, username: baInfo.username ?? '', password: '' };
    baLoadedFor = 'done';
  }

  async function saveBasicAuth() {
    if (baSaving) return;
    baSaving = true;
    try {
      const r = await backend.setBasicAuth({ ...baForm, username: baForm.username.trim() });
      baLoadedFor = '';
      await load();
      primeBA();
      toast[baForm.enabled ? 'success' : 'info'](r.enabled ? 'Basic Auth 已开启并立即生效' : 'Basic Auth 已关闭');
    } catch (e) {
      toast.error(e instanceof Error ? e.message : String(e));
    } finally {
      baSaving = false;
    }
  }

  let createOpen = $state(false);
  let form = $state({ username: '', password: '', role: 'viewer' as 'admin' | 'viewer' });
  let submitting = $state(false);
  const valid = $derived(form.username.trim().length >= 2 && form.password.length >= 6);

  async function submitUser() {
    if (!valid || submitting) return;
    submitting = true;
    try {
      await backend.createUser({ ...form, username: form.username.trim() });
      createOpen = false;
      form = { username: '', password: '', role: 'viewer' };
      await load();
      toast.success('用户已创建');
    } catch (e) {
      toast.error(e instanceof Error ? e.message : String(e));
    } finally {
      submitting = false;
    }
  }

  async function removeUser(u: User) {
    try {
      await backend.deleteUser(u.id);
      await load();
      toast.success(`用户「${u.username}」已删除`);
    } catch (e) {
      toast.error(e instanceof Error ? e.message : String(e));
    }
  }
</script>

<div class="animate-fade-up grid grid-cols-1 gap-4 xl:grid-cols-3">
  <!-- 系统信息 -->
  <Card class="p-5 shadow-soft">
    <h2 class="mb-4 flex items-center gap-2 text-sm font-semibold">
      <Server size={15} class="text-primary" />
      系统与后端
    </h2>
    {#if info}
      <dl class="space-y-3 text-[13px]">
        <div class="flex items-center justify-between gap-3">
          <dt class="text-muted-foreground">主机名</dt>
          <dd class="font-medium">{info.hostname}</dd>
        </div>
        <div class="flex items-center justify-between gap-3">
          <dt class="text-muted-foreground">发行版</dt>
          <dd class="max-w-[60%] truncate text-right font-medium" title={info.distro}>{info.distro}</dd>
        </div>
        <div class="flex items-center justify-between gap-3">
          <dt class="flex items-center gap-1.5 text-muted-foreground"><Cpu size={13} /> 内核</dt>
          <dd class="font-mono text-[11.5px] font-medium">{info.kernel}</dd>
        </div>
        <div class="flex items-center justify-between gap-3">
          <dt class="flex items-center gap-1.5 text-muted-foreground"><Terminal size={13} /> 防火墙后端</dt>
          <dd class="flex items-center gap-1.5">
            <Badge variant="outline" class="bg-success/10 text-success ring-success/20">{info.backend}</Badge>
            {#if info.detected?.IPTablesVariant}
              <span class="text-[10.5px] text-muted-foreground">iptables: {info.detected.IPTablesVariant}</span>
            {/if}
          </dd>
        </div>
        <div class="flex items-center justify-between gap-3">
          <dt class="text-muted-foreground">共存检测</dt>
          <dd class="flex gap-1.5">
            {#if info.detected?.FirewalldActive}<Badge variant="outline" class="text-[10px]">firewalld</Badge>{/if}
            {#if info.detected?.UfwActive}<Badge variant="outline" class="text-[10px]">ufw</Badge>{/if}
            {#if info.detected?.DockerPresent}<Badge variant="outline" class="text-[10px]">docker</Badge>{/if}
            {#if !info.detected?.FirewalldActive && !info.detected?.UfwActive && !info.detected?.DockerPresent}
              <span class="text-[11px] text-muted-foreground">无</span>
            {/if}
          </dd>
        </div>
        <div class="flex items-center justify-between gap-3">
          <dt class="flex items-center gap-1.5 text-muted-foreground"><ShieldCheck size={13} /> SSH 保护端口</dt>
          <dd class="font-mono text-[12px] font-medium">{info.ssh_ports?.join(', ')}</dd>
        </div>
        <div class="flex items-center justify-between gap-3">
          <dt class="text-muted-foreground">规则同步</dt>
          <dd>
            {#if info.drift === 'clean'}
              <Badge variant="outline" class="bg-success/10 text-success ring-success/20">与内核一致</Badge>
            {:else if info.drift === 'drifted'}
              <Badge variant="outline" class="bg-warning/10 text-warning ring-warning/25">有漂移</Badge>
            {:else}
              <Badge variant="outline" class="text-muted-foreground">未知</Badge>
            {/if}
          </dd>
        </div>
      </dl>
    {:else}
      <div class="animate-pulse space-y-4">
        {#each Array(6) as _, i (i)}
          <div class="h-8 rounded-lg bg-foreground/[0.04]"></div>
        {/each}
      </div>
    {/if}
  </Card>

  <!-- 用户管理 -->
  <Card class="p-5 shadow-soft xl:col-span-2">
    <div class="mb-4 flex items-center gap-2">
      <h2 class="flex items-center gap-2 text-sm font-semibold">
        <SettingsIcon size={15} class="text-primary" />
        用户与权限
      </h2>
      <span class="font-num text-[11px] text-muted-foreground">{users.length} 个账号</span>
      <Button size="sm" class="ml-auto h-8" onclick={() => (createOpen = true)}>
        <UserPlus size={14} /> 新增用户
      </Button>
    </div>

    <div class="overflow-x-auto">
      <table class="w-full text-left text-[13px]">
        <thead>
          <tr class="border-b border-border/70 text-[11.5px] text-muted-foreground">
            <th class="py-2.5 pr-3 font-medium">用户名</th>
            <th class="py-2.5 pr-3 font-medium">角色</th>
            <th class="py-2.5 pr-3 font-medium">创建时间</th>
            <th class="py-2.5 pr-1 text-right font-medium">操作</th>
          </tr>
        </thead>
        <tbody>
          {#each users as u (u.id)}
            <tr class="group border-b border-border/40 last:border-0 hover:bg-foreground/[0.02]">
              <td class="py-2.5 pr-3 font-medium">
                {u.username}
                {#if u.username === session.user?.username}
                  <Badge variant="outline" class="ml-1.5 bg-primary/10 text-[10px] text-primary ring-primary/20">当前</Badge>
                {/if}
              </td>
              <td class="py-2.5 pr-3">
                <Badge variant="outline" class={u.role === 'admin' ? 'bg-primary/10 text-[10.5px] text-primary ring-primary/20' : 'bg-muted text-[10.5px] text-muted-foreground'}>
                  {u.role === 'admin' ? '管理员' : '只读'}
                </Badge>
              </td>
              <td class="py-2.5 pr-3 text-muted-foreground">{fmtDateTime(u.created_at)}</td>
              <td class="py-2.5 pr-1 text-right">
                {#if u.username !== session.user?.username}
                  <Button variant="ghost" size="icon" class="size-7 text-muted-foreground opacity-0 transition-opacity hover:text-destructive group-hover:opacity-100" onclick={() => removeUser(u)} aria-label="删除">
                    <Trash2 size={13} />
                  </Button>
                {/if}
              </td>
            </tr>
          {/each}
        </tbody>
      </table>
    </div>
    <p class="mt-4 text-[11.5px] leading-relaxed text-muted-foreground">
      管理员可变更全部配置；只读用户仅可查看面板与日志。全部变更都会记录在
      <a class="text-primary hover:underline" href="#/logs">操作审计</a> 中。
    </p>
  </Card>

  <!-- 访问防护（Basic Auth） -->
  <Card class="p-5 shadow-soft xl:col-span-3">
    <div class="flex flex-wrap items-center gap-x-4 gap-y-2.5">
      <h2 class="flex items-center gap-2 text-sm font-semibold">
        <Lock size={15} class="text-primary" />
        整站 Basic Auth
      </h2>
      <Switch
        checked={baForm.enabled}
        onCheckedChange={(v) => {
          baForm.enabled = v;
          baLoadedFor = 'done';
        }}
      />
      {#if baInfo?.source === 'file'}
        <Badge variant="outline" class="bg-muted text-[10px] text-muted-foreground ring-border">basicauth.yaml</Badge>
      {/if}
      <Input
        class="h-9 w-40 font-mono"
        placeholder="用户名"
        autocomplete="off"
        bind:value={baForm.username}
        disabled={!baForm.enabled}
      />
      <Input
        class="h-9 w-44 font-mono"
        type="password"
        autocomplete="new-password"
        placeholder={baInfo?.password_set ? '密码（留空保持）' : '密码'}
        bind:value={baForm.password}
        disabled={!baForm.enabled}
      />
      <Button
        class="h-9"
        disabled={baSaving || (baForm.enabled && (!baForm.username.trim() || (!baInfo?.password_set && !baForm.password)))}
        onclick={saveBasicAuth}
      >
        {baSaving ? '应用中…' : '保存'}
      </Button>
      <span class="text-[11px] text-muted-foreground">
        立即生效 · 扫描器只见 401 · 凭据在
        <code class="rounded bg-foreground/[0.06] px-1 font-mono">basicauth.yaml</code>
        （手改约 2 秒自动生效）
      </span>
    </div>
  </Card>
</div>

<Dialog.Root bind:open={createOpen}>
  <Dialog.Content class="sm:max-w-sm">
    <Dialog.Header>
      <Dialog.Title>新增用户</Dialog.Title>
      <Dialog.Description>密码以 bcrypt 加密存储。</Dialog.Description>
    </Dialog.Header>
    <div class="grid gap-3.5">
      <div class="grid gap-2">
        <Label for="u-name">用户名</Label>
        <Input id="u-name" bind:value={form.username} />
      </div>
      <div class="grid gap-2">
        <Label for="u-pass">密码</Label>
        <Input id="u-pass" type="password" bind:value={form.password} />
        {#if form.password && form.password.length < 6}
          <p class="text-[11px] text-warning">至少 6 位</p>
        {/if}
      </div>
      <div class="grid gap-2">
        <Label>角色</Label>
        <Select.Root type="single" bind:value={form.role}>
          <Select.Trigger class="w-full">{form.role === 'admin' ? '管理员' : '只读'}</Select.Trigger>
          <Select.Content>
            <Select.Item value="admin">管理员</Select.Item>
            <Select.Item value="viewer">只读</Select.Item>
          </Select.Content>
        </Select.Root>
      </div>
    </div>
    <Dialog.Footer class="gap-2 sm:justify-end">
      <Button variant="outline" onclick={() => (createOpen = false)}>取消</Button>
      <Button disabled={!valid || submitting} onclick={submitUser}>{submitting ? '创建中…' : '创建'}</Button>
    </Dialog.Footer>
  </Dialog.Content>
</Dialog.Root>
