<script lang="ts">
  import { DatabaseBackup, Download, Upload, FileJson, FileCode, ShieldCheck } from '@lucide/svelte';
  import { Card } from '$lib/components/ui/card';
  import { Button } from '$lib/components/ui/button';
  import { Badge } from '$lib/components/ui/badge';
  import { toast } from 'svelte-sonner';
  import EmptyState from '$lib/components/common/EmptyState.svelte';
  import DangerDialog from '$lib/components/common/DangerDialog.svelte';
  import { backend, type SyncResult } from '$lib/api';

  let fileInput = $state<HTMLInputElement | null>(null);
  let restoring = $state(false);
  let danger = $state<{ sync: SyncResult } | null>(null);

  function downloadBackup() {
    const a = document.createElement('a');
    a.href = `/api/v1/system/backup?format=json&t=${Date.now()}`;
    a.download = 'firepanel-backup.json';
    a.click();
    toast.success('备份已开始下载');
  }

  function exportKernel(format: 'iptables-save' | 'nft') {
    const a = document.createElement('a');
    a.href = `/api/v1/system/backup?format=${format}&t=${Date.now()}`;
    a.download = '';
    a.click();
  }

  async function onFileChosen(e: Event) {
    const input = e.target as HTMLInputElement;
    const file = input.files?.[0];
    if (!file) return;
    restoring = true;
    try {
      const text = await file.text();
      const payload = JSON.parse(text);
      const r = await backend.restore(payload);
      if (r.sync?.danger) {
        danger = { sync: r.sync };
      } else {
        toast.success(`恢复完成：${r.forwards} 条转发、${r.ip_lists} 条名单已应用`);
      }
    } catch (e) {
      toast.error(e instanceof Error ? `恢复失败: ${e.message}` : String(e));
    } finally {
      restoring = false;
      if (fileInput) fileInput.value = '';
    }
  }
</script>

<div class="animate-fade-up grid grid-cols-1 gap-4 lg:grid-cols-2">
  <!-- 备份导出 -->
  <Card class="p-5 shadow-soft">
    <div class="mb-1 flex items-center gap-2">
      <span class="flex h-9 w-9 items-center justify-center rounded-lg bg-primary/10 text-primary">
        <Download size={17} />
      </span>
      <h2 class="text-sm font-semibold">备份导出</h2>
    </div>
    <p class="mb-4 text-[12px] leading-relaxed text-muted-foreground">
      面板 JSON 备份包含全部转发规则与黑/白名单（期望状态），可在另一台机器恢复；
      内核格式导出用于与 iptables-save / nft 工作流互通。
    </p>
    <div class="grid gap-2.5">
      <Button variant="outline" class="justify-start" onclick={downloadBackup}>
        <FileJson size={15} class="text-primary" />
        面板期望状态（JSON）— 推荐
      </Button>
      <Button variant="outline" class="justify-start" onclick={() => exportKernel('iptables-save')}>
        <FileCode size={15} class="text-info" />
        iptables-save 格式（iptables 后端）
      </Button>
      <Button variant="outline" class="justify-start" onclick={() => exportKernel('nft')}>
        <FileCode size={15} class="text-info" />
        nft ruleset 格式（nftables 后端）
      </Button>
    </div>
  </Card>

  <!-- 恢复 -->
  <Card class="p-5 shadow-soft">
    <div class="mb-1 flex items-center gap-2">
      <span class="flex h-9 w-9 items-center justify-center rounded-lg bg-warning/15 text-warning">
        <Upload size={17} />
      </span>
      <h2 class="text-sm font-semibold">从备份恢复</h2>
    </div>
    <p class="mb-4 text-[12px] leading-relaxed text-muted-foreground">
      选择面板 JSON 备份文件，将<b class="text-warning">全量替换</b>当前转发规则与黑/白名单并同步内核。
      涉及 SSH 端口的规则会走高危确认流程。操作前建议先下载一份当前备份。
    </p>
    <input
      type="file"
      accept=".json,application/json"
      class="hidden"
      bind:this={fileInput}
      onchange={onFileChosen}
    />
    <button
      type="button"
      class="flex w-full flex-col items-center gap-2 rounded-xl border border-dashed border-border/80 bg-card/50 px-6 py-10 transition-colors hover:border-primary/40 hover:bg-primary/[0.03] disabled:opacity-50"
      onclick={() => fileInput?.click()}
      disabled={restoring}
    >
      <DatabaseBackup size={22} class="text-muted-foreground" />
      {#if restoring}
        <span class="text-[13px] font-medium">恢复中…</span>
      {:else}
        <span class="text-[13px] font-medium">选择备份文件</span>
        <span class="text-[11px] text-muted-foreground">JSON 格式 · 最大 8MB</span>
      {/if}
    </button>
    <div class="mt-3 flex items-center gap-1.5 text-[11px] text-muted-foreground">
      <ShieldCheck size={13} class="text-success" />
      恢复前会校验每条规则，非法条目将被拒绝
    </div>
  </Card>

  <!-- 跨机迁移说明 -->
  <Card class="p-5 shadow-soft lg:col-span-2">
    <h2 class="mb-3 text-sm font-semibold">跨机迁移</h2>
    <ol class="grid gap-2.5 text-[12.5px] leading-relaxed text-muted-foreground md:grid-cols-3">
      <li class="rounded-lg border border-border/60 bg-foreground/[0.015] px-4 py-3">
        <span class="font-num mr-1.5 font-semibold text-foreground">1</span>
        在源机下载面板 JSON 备份（本页上方按钮）。
      </li>
      <li class="rounded-lg border border-border/60 bg-foreground/[0.015] px-4 py-3">
        <span class="font-num mr-1.5 font-semibold text-foreground">2</span>
        在目标机安装 FirePanel 并完成初始化向导。
      </li>
      <li class="rounded-lg border border-border/60 bg-foreground/[0.015] px-4 py-3">
        <span class="font-num mr-1.5 font-semibold text-foreground">3</span>
        在目标机上传备份文件恢复，规则自动应用到新机内核。
      </li>
    </ol>
    <div class="mt-3">
      <Badge variant="outline" class="bg-info/5 text-[10.5px] text-info ring-info/20">
        反代域名需在新机重新指向；自动证书在新机按需重新签发
      </Badge>
    </div>
  </Card>
</div>

{#if danger}
  <DangerDialog sync={danger.sync} onClose={() => (danger = null)} />
{/if}
