<script lang="ts">
  import { onMount } from 'svelte';
  import { Network, Plus, Trash2, Info, Pencil, Globe, ArrowRight, Play } from '@lucide/svelte';
  import { Card } from '$lib/components/ui/card';
  import { Button } from '$lib/components/ui/button';
  import { Input } from '$lib/components/ui/input';
  import { Badge } from '$lib/components/ui/badge';
  import { Label } from '$lib/components/ui/label';
  import { Switch } from '$lib/components/ui/switch';
  import * as Dialog from '$lib/components/ui/dialog';
  import * as Select from '$lib/components/ui/select';
  import * as RadioGroup from '$lib/components/ui/radio-group';
  import * as Tooltip from '$lib/components/ui/tooltip';
  import { toast } from 'svelte-sonner';
  import EmptyState from '$lib/components/common/EmptyState.svelte';
  import DangerDialog from '$lib/components/common/DangerDialog.svelte';
  import { backend, type NATRule, type SyncResult } from '$lib/api';

  let items = $state<NATRule[]>([]);
  let ipForward = $state(false);
  let ifaces = $state<{ name: string; addrs: string[] }[]>([]);
  let loading = $state(true);
  let danger = $state<{ sync: SyncResult } | null>(null);

  async function load() {
    loading = true;
    try {
      const r = await backend.natRules();
      items = r.items;
      ipForward = r.ip_forward;
      ifaces = (await backend.interfaces()).items;
    } catch (e) {
      toast.error(e instanceof Error ? e.message : String(e));
    } finally {
      loading = false;
    }
  }
  onMount(() => {
    load();
  });

  /* ---------- ip_forward 一键开启 ---------- */
  let enablingForward = $state(false);
  async function enableForward() {
    enablingForward = true;
    try {
      const r = await backend.enableIPForward();
      if (r.ip_forward) {
        ipForward = true;
        toast.success('IPv4 转发已开启并写入 /etc/sysctl.conf，NAT 规则现在可以生效');
      } else {
        toast.error('开启失败，请手动执行 sysctl -w net.ipv4.ip_forward=1');
      }
      if (r.warning) toast.warning(r.warning);
    } catch (e) {
      toast.error(e instanceof Error ? e.message : String(e));
    } finally {
      enablingForward = false;
    }
  }

  /* ---------- 新增/编辑 ---------- */
  let createOpen = $state(false);
  let editing = $state<NATRule | null>(null);
  let form = $state({ name: '', type: 'MASQ' as NATRule['type'], src_cidr: '', out_iface: '', to_addr: '', remark: '' });
  let submitting = $state(false);

  const cidrValid = $derived(/^(\d{1,3}\.){3}\d{1,3}\/\d{1,2}$/.test(form.src_cidr.trim()));
  const ipValid = $derived(form.type !== 'SNAT' || /^(\d{1,3}\.){3}\d{1,3}$/.test(form.to_addr.trim()));
  const valid = $derived(form.name.trim() !== '' && cidrValid && form.out_iface.trim() !== '' && ipValid);

  function openCreate() {
    editing = null;
    // 预填第一个带 IPv4 的网卡作为出口，减少输入成本
    const suggested = ifaces.find((i) => i.addrs.length > 0)?.name ?? '';
    form = { name: '', type: 'MASQ', src_cidr: '', out_iface: suggested, to_addr: '', remark: '' };
    createOpen = true;
  }
  function openEdit(r: NATRule) {
    editing = r;
    form = { name: r.name, type: r.type, src_cidr: r.src_cidr, out_iface: r.out_iface, to_addr: r.to_addr, remark: r.remark };
    createOpen = true;
  }

  async function submit() {
    if (!valid || submitting) return;
    submitting = true;
    try {
      const payload = { ...form, src_cidr: form.src_cidr.trim(), out_iface: form.out_iface.trim(), to_addr: form.to_addr.trim(), enabled: true };
      if (editing) await backend.updateNAT(editing.id, payload);
      else await backend.createNAT(payload);
      createOpen = false;
      await load();
      toast.success(editing ? 'NAT 规则已更新' : 'NAT 规则已创建');
    } catch (e) {
      toast.error(e instanceof Error ? e.message : String(e));
    } finally {
      submitting = false;
    }
  }

  async function toggle(r: NATRule, enabled: boolean) {
    try {
      await backend.updateNAT(r.id, { ...r, enabled });
      await load();
      toast[enabled ? 'success' : 'info'](`规则「${r.name}」已${enabled ? '启用' : '停用'}`);
    } catch (e) {
      toast.error(e instanceof Error ? e.message : String(e));
    }
  }

  async function remove(r: NATRule) {
    try {
      const { sync } = await backend.deleteNAT(r.id);
      await load();
      if (sync?.danger) danger = { sync };
      else toast.success(`规则「${r.name}」已删除`);
    } catch (e) {
      toast.error(e instanceof Error ? e.message : String(e));
    }
  }

  function ifaceAddr(name: string): string {
    const i = ifaces.find((x) => x.name === name);
    return i && i.addrs.length > 0 ? i.addrs[0] : '';
  }
</script>

<div class="animate-fade-up space-y-4">
  <!-- ip_forward 提示 -->
  {#if !ipForward}
    <div class="flex flex-wrap items-center gap-x-3 gap-y-2 rounded-xl border border-warning/30 bg-warning/[0.06] px-4 py-3 text-[12.5px]">
      <Info size={15} class="shrink-0 text-warning" />
      <span class="font-medium text-warning">内核 ip_forward 未开启，NAT 规则不会生效</span>
      <Button size="sm" class="h-7 px-3 text-[12px]" disabled={enablingForward} onclick={enableForward}>
        <Play size={12} class="mr-1" />
        {enablingForward ? '开启中…' : '开始NAT转发'}
      </Button>
      <span class="text-[11.5px] text-muted-foreground">一键执行 <code class="rounded bg-foreground/[0.06] px-1.5 py-0.5 font-mono text-[11px]">sysctl -w net.ipv4.ip_forward=1</code> 并写入 <code class="rounded bg-foreground/[0.06] px-1.5 py-0.5 font-mono text-[11px]">/etc/sysctl.conf</code> 持久化</span>
    </div>
  {/if}

  <!-- 列表 -->
  <div class="flex items-center gap-2.5">
    <span class="font-num text-xs text-muted-foreground">共 {items.length} 条</span>
    <span class="ml-auto"></span>
    <Button onclick={openCreate}><Plus size={16} /> 新增 NAT</Button>
  </div>

  {#if loading}
    <div class="space-y-2.5">
      {#each Array(3) as _, i (i)}
        <div class="h-16 animate-pulse rounded-xl bg-foreground/[0.04]"></div>
      {/each}
    </div>
  {:else if items.length === 0}
    <EmptyState
      title="还没有 NAT 规则"
      desc="让内网网段经指定网卡出网：出口 IP 动态（拨号/DHCP）选 MASQUERADE，固定 IP 选 SNAT。"
    >
      {#snippet icon()}<Network size={22} />{/snippet}
      {#snippet action()}
        <Button onclick={openCreate}><Plus size={16} /> 新增 NAT</Button>
      {/snippet}
    </EmptyState>
  {:else}
    <div class="space-y-2.5">
      {#each items as r (r.id)}
        <Card class="lift-card group p-4">
          <div class="flex flex-wrap items-center gap-x-6 gap-y-3">
            <div class="min-w-[140px] flex-1 basis-40">
              <span class="text-[13.5px] font-medium">{r.name}</span>
              {#if r.remark}
                <p class="mt-0.5 truncate text-[11.5px] text-muted-foreground">{r.remark}</p>
              {/if}
            </div>
            <Badge variant="outline" class={r.type === 'MASQ' ? 'bg-primary/10 text-primary ring-primary/20' : 'bg-info/10 text-info ring-info/20'}>
              {r.type === 'MASQ' ? '动态伪装' : '固定转换'}
            </Badge>
            <div class="flex items-center gap-2 text-[12.5px]">
              <Tooltip.Root>
                <Tooltip.Trigger class="font-mono underline decoration-muted-foreground/40 decoration-dotted underline-offset-4">
                  {r.src_cidr}
                </Tooltip.Trigger>
                <Tooltip.Content class="text-xs">这些来源地址出网时会被改写地址</Tooltip.Content>
              </Tooltip.Root>
              <ArrowRight size={13} class="text-muted-foreground" />
              <Tooltip.Root>
                <Tooltip.Trigger class="font-mono text-primary underline decoration-primary/40 decoration-dotted underline-offset-4">
                  {r.out_iface}{r.type === 'SNAT' ? `(${r.to_addr})` : ''}
                </Tooltip.Trigger>
                <Tooltip.Content class="text-xs">
                  {r.type === 'MASQ' ? '自动采用此网卡当前地址伪装' : '出网地址固定为此 IP'}
                </Tooltip.Content>
              </Tooltip.Root>
            </div>
            <div class="ml-auto flex items-center gap-2.5">
              <Switch checked={r.enabled} onCheckedChange={(v) => toggle(r, v)} />
              <Button variant="ghost" size="icon" class="size-7 text-muted-foreground" onclick={() => openEdit(r)} aria-label="编辑">
                <Pencil size={13} />
              </Button>
              <Button variant="ghost" size="icon" class="size-7 text-muted-foreground opacity-0 transition-opacity hover:text-destructive group-hover:opacity-100" onclick={() => remove(r)} aria-label="删除">
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
  <Dialog.Content class="max-h-[92vh] overflow-hidden p-0 sm:max-w-4xl">
    <div class="grid md:grid-cols-[1.1fr_1fr] md:overflow-y-auto" style="max-height: 92vh;">
      <!-- 左栏：NAT 原理解释 -->
      <div class="flex flex-col gap-4 border-b border-border/60 bg-gradient-to-br from-primary/[0.06] via-transparent to-info/[0.05] p-6 md:border-b-0 md:border-r">
        <Dialog.Header class="space-y-1.5">
          <Dialog.Title>{editing ? '编辑 NAT 规则' : '新增 NAT 规则'}</Dialog.Title>
          <Dialog.Description>NAT（网络地址转换）：让内网设备共享出口上网。</Dialog.Description>
        </Dialog.Header>

        <p class="text-[12.5px] leading-relaxed text-muted-foreground">
          当<b class="text-foreground">内网设备</b>（如 192.168.1.x 的机器）访问外网时，出口路由器会把它的
          「来源地址」改写成<b class="text-foreground">出口网卡的地址</b>——否则外网不认识私有地址，回包也回不来。
        </p>
        <p class="text-[12.5px] leading-relaxed text-muted-foreground">
          一条 NAT 规则就是回答：
          <span class="font-medium text-foreground">「哪个内网网段，从哪块网卡出去，出去时用什么地址」</span>。
        </p>

        <!-- 流程示意 -->
        <div class="flex items-center gap-2 rounded-xl border border-border/60 bg-background/60 px-4 py-3 font-mono text-[11px] leading-relaxed">
          <div class="text-center">
            <div class="text-muted-foreground">内网设备</div>
            <div class="font-medium text-foreground">192.168.1.10</div>
          </div>
          <ArrowRight size={14} class="text-muted-foreground" />
          <div class="text-center">
            <div class="text-muted-foreground">本机出口</div>
            <div class="font-medium text-primary">{form.out_iface || 'eth0'}</div>
          </div>
          <ArrowRight size={14} class="text-muted-foreground" />
          <div class="text-center">
            <div class="text-muted-foreground">互联网看到的地址</div>
            <div class="font-medium text-success">{form.type === 'SNAT' && form.to_addr ? form.to_addr : '203.0.113.9'}</div>
          </div>
        </div>

        <!-- MASQ / SNAT 单选（显眼圆点 + 卡片） -->
        <RadioGroup.Root
          value={form.type}
          onValueChange={(v) => (form.type = v as 'MASQ' | 'SNAT')}
          class="mt-1 space-y-2.5"
        >
          <Label
            for="nat-type-masq"
            class="flex cursor-pointer items-start gap-2.5 rounded-lg border p-3 transition-[color,background-color,border-color,box-shadow,transform,opacity] duration-150
              {form.type === 'MASQ' ? 'border-primary/50 bg-primary/[0.06] ring-1 ring-primary/30' : 'border-border/60 bg-card hover:border-primary/30'}"
          >
            <RadioGroup.Item value="MASQ" id="nat-type-masq" class="mt-0.5" />
            <div class="min-w-0">
              <div class="text-[12.5px] font-semibold text-foreground">MASQUERADE（动态伪装）</div>
              <p class="mt-0.5 text-[11.5px] leading-relaxed text-muted-foreground">
                出口 IP 不固定时用：自动采用出口网卡当前地址。家庭宽带拨号（PPPoE）、DHCP 动态公网 IP 场景选这个，换 IP 也不用改规则。
              </p>
            </div>
          </Label>
          <Label
            for="nat-type-snat"
            class="flex cursor-pointer items-start gap-2.5 rounded-lg border p-3 transition-[color,background-color,border-color,box-shadow,transform,opacity] duration-150
              {form.type === 'SNAT' ? 'border-info/50 bg-info/[0.06] ring-1 ring-info/30' : 'border-border/60 bg-card hover:border-info/30'}"
          >
            <RadioGroup.Item value="SNAT" id="nat-type-snat" class="mt-0.5" />
            <div class="min-w-0">
              <div class="text-[12.5px] font-semibold text-foreground">SNAT（固定转换）</div>
              <p class="mt-0.5 text-[11.5px] leading-relaxed text-muted-foreground">
                出口 IP 固定时用：手动指定网卡上的公网 IP。云服务器多 IP、机房专线场景选这个，性能略好且地址明确。
              </p>
            </div>
          </Label>
        </RadioGroup.Root>
      </div>

      <!-- 右栏：表单 -->
      <div class="flex flex-col gap-4 p-6">
        <!-- 顶部：规则名称 + 备注 -->
        <div class="grid grid-cols-[1fr_1.2fr] gap-3">
          <div class="grid gap-2">
            <Label for="nat-name">规则名称</Label>
            <Input id="nat-name" placeholder="如：内网上网" bind:value={form.name} />
          </div>
          <div class="grid gap-2">
            <Label for="nat-remark">备注 <span class="text-muted-foreground/70">（可选）</span></Label>
            <Input id="nat-remark" bind:value={form.remark} />
          </div>
        </div>

      <!-- 第 1 步：来源网段 -->
      <div class="grid gap-1.5">
        <div class="flex items-center gap-1.5">
          <Label for="nat-src">第 1 步 · 哪些来源地址要出网</Label>
          <Tooltip.Root>
            <Tooltip.Trigger class="text-muted-foreground/60 hover:text-foreground">
              <Info size={12} />
            </Tooltip.Trigger>
            <Tooltip.Content class="text-xs">即源 IP 范围（CIDR 写法）。只有这个范围内的设备出网时会被改写地址，其余不受影响。</Tooltip.Content>
          </Tooltip.Root>
        </div>
        <Input id="nat-src" placeholder="192.168.1.0/24" bind:value={form.src_cidr} class="font-mono" />
        <p class="text-[11px] text-muted-foreground">
          写法：网段 + 掩码位数。<span class="font-mono">192.168.1.0/24</span> 表示 192.168.1.1 ~ 192.168.1.254 整个 C 段
        </p>
      </div>

      <!-- 第 2 步：出口网卡（下拉，遍历系统网卡） -->
      <div class="grid gap-1.5">
        <Label for="nat-iface">第 2 步 · 从哪块网卡出去</Label>
        <Select.Root
          type="single"
          value={form.out_iface}
          onValueChange={(v) => (form.out_iface = v)}
        >
          <Select.Trigger class="w-full font-mono">
            {form.out_iface || '选择网卡…'}
            {#if form.out_iface && ifaceAddr(form.out_iface)}
              <span class="ml-1 text-[11px] text-muted-foreground">({ifaceAddr(form.out_iface)})</span>
            {/if}
          </Select.Trigger>
          <Select.Content>
            {#each ifaces as i (i.name)}
              <Select.Item value={i.name}>
                {i.name}
                {#if (i.addrs || []).length > 0}
                  <span class="ml-1 text-muted-foreground">{(i.addrs || []).join(', ')}</span>
                {:else}
                  <span class="ml-1 text-muted-foreground/60">（无 IP）</span>
                {/if}
              </Select.Item>
            {/each}
          </Select.Content>
        </Select.Root>
        <p class="text-[11px] text-muted-foreground">连接外网的那块网卡（通常是有公网/上层网关地址的）</p>
      </div>

      <!-- 第 3 步：SNAT 出口 IP -->
      {#if form.type === 'SNAT'}
        <div class="grid gap-1.5 rounded-lg border border-info/30 bg-info/[0.04] p-3">
          <Label for="nat-to">第 3 步 · 出网时用什么地址（网卡上的固定 IP）</Label>
          <Input id="nat-to" placeholder="203.0.113.5" bind:value={form.to_addr} class="font-mono" />
          <p class="text-[11px] text-muted-foreground">
            必须是出口网卡上真实配置的地址。外网设备看到的来源就是这个 IP。
            {#if form.out_iface && ifaceAddr(form.out_iface)}
              <button type="button" class="ml-1 text-info hover:underline" onclick={() => (form.to_addr = ifaceAddr(form.out_iface))}>
                填入 {form.out_iface} 当前地址 {ifaceAddr(form.out_iface)}
              </button>
            {/if}
          </p>
        </div>
      {/if}

        <!-- 底部右侧：操作按钮 -->
        <div class="mt-auto flex justify-end gap-2 border-t border-border/60 pt-3">
          <Button variant="outline" onclick={() => (createOpen = false)}>取消</Button>
          <Button disabled={!valid || submitting} onclick={submit}>{submitting ? '应用中…' : editing ? '保存并应用' : '创建并应用'}</Button>
        </div>
      </div>
    </div>
  </Dialog.Content>
</Dialog.Root>

{#if danger}
  <DangerDialog sync={danger.sync} onClose={() => { danger = null; load(); }} />
{/if}
