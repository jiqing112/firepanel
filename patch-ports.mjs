import fs from 'node:fs';
const p = 'Settings.svelte';
let s = fs.readFileSync(p, 'utf8');
const anchor = '  <!-- 访问防护（Basic Auth） -->';
if (!s.includes(anchor)) { console.error('missing anchor'); process.exit(1); }
const card = `  <!-- 面板端口 -->
  <Card class="p-5 shadow-soft xl:col-span-3">
    <div class="flex flex-wrap items-center gap-2.5">
      <h2 class="flex items-center gap-2 text-sm font-semibold">
        <Network size={15} class="text-primary" />
        面板端口
      </h2>
      <Badge variant="outline" class="text-[10px] bg-muted text-muted-foreground ring-border">
        当前 HTTP {portsInfo?.listen ?? '—'} · HTTPS {portsInfo?.https_port || '未启用'}
      </Badge>
      <span class="ml-auto text-[11px] text-muted-foreground">保存后需重启面板生效</span>
    </div>
    <div class="mt-3 flex flex-wrap items-end gap-3">
      <div class="grid gap-1.5">
        <Label class="text-[11px] text-muted-foreground">HTTP 端口</Label>
        <Input class="h-9 w-36 font-mono" inputmode="numeric" placeholder="18088" bind:value={portsForm.listen} />
      </div>
      <div class="grid gap-1.5">
        <Label class="text-[11px] text-muted-foreground">HTTPS 端口 <span class="text-[10px]">（需已启用 -panel-https）</span></Label>
        <Input class="h-9 w-36 font-mono" inputmode="numeric" placeholder="18443" bind:value={portsForm.https} />
      </div>
      <Button class="h-9" disabled={portsSaving || !portsDirty} onclick={savePorts}>
        {portsSaving ? '保存中…' : '保存'}
      </Button>
      {#if restartPending}
        <Button class="h-9" variant="destructive" disabled={restarting} onclick={restartPanel}>
          {restarting ? '重启中…' : '重启面板生效'}
        </Button>
      {/if}
    </div>
    <p class="mt-2.5 text-[11px] leading-relaxed text-muted-foreground">
      端口保存时自动试绑校验（被占用会拒绝）；HTTPS 端口仅在已配置面板 HTTPS（-panel-https 参数）时生效。
      重启期间面板不可访问约 3-5 秒，之后请用新地址访问。
    </p>
  </Card>

` + anchor;
s = s.replace(anchor, card);
fs.writeFileSync(p, s);
console.log('card inserted');
