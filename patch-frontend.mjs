import fs from 'node:fs';

// ── Setup.svelte：字段级校验特效 + 成功后跳登录 ──
{
  const p = 'lib/pages/Setup.svelte';
  let s = fs.readFileSync(p, 'utf8');
  const ok = (n, c) => { if (!c) { console.error('FAIL ' + n); process.exit(1); } console.log('ok ' + n); };

  // 1) attempted 状态 + touched 驱动的错误显示
  ok('submit fn', s.includes('async function submit(e: SubmitEvent) {'));
  s = s.replace(
    `  let error = $state('');
  let loading = $state(false);

  const strong = $derived(password.length >= 6);
  const match = $derived(password === password2);
  const valid = $derived(username.trim().length >= 2 && strong && match);

  async function submit(e: SubmitEvent) {
    e.preventDefault();
    if (!valid || loading) return;`,
    `  let error = $state('');
  let loading = $state(false);
  let attempted = $state(false);

  const strong = $derived(password.length >= 6);
  const match = $derived(password === password2);
  const valid = $derived(username.trim().length >= 2 && strong && match);
  // 各字段是否需要提醒（提交过一次后逐字段显形）
  const userInvalid = $derived(attempted && username.trim().length < 2);
  const passInvalid = $derived(attempted && !strong);
  const matchInvalid = $derived(attempted && (password2 === '' || !match));
  const anyInvalid = $derived(userInvalid || passInvalid || matchInvalid);

  async function submit(e: SubmitEvent) {
    e.preventDefault();
    attempted = true;
    if (!valid || loading) return;`,
  );

  // 2) 成功后直接跳登录页
  ok('setup redirect anchor', s.includes(`      const { token, user } = await backend.setup(username.trim(), password);
      localStorage.setItem('firepanel-token', token);
      session.user = user;
      navigate('/dashboard');`));
  s = s.replace(
    `      const { token, user } = await backend.setup(username.trim(), password);
      localStorage.setItem('firepanel-token', token);
      session.user = user;
      navigate('/dashboard');`,
    `      await backend.setup(username.trim(), password);
      toast.success('管理员创建成功，请登录');
      navigate('/login');`,
  );
  // toast import
  ok('toast import', s.includes("import { fade, fly } from 'svelte/transition';"));
  s = s.replace(
    "import { fade, fly } from 'svelte/transition';",
    "import { fade, fly } from 'svelte/transition';\n  import { toast } from 'svelte-sonner';",
  );
  // session import 可能不再使用
  if (!s.includes('session.')) {
    s = s.replace("  import { session } from '$lib/stores/session.svelte';\n", '');
  }

  // 3) 字段红边 + shake + 错误提示
  ok('username input', s.includes('<Input id="username" autocomplete="username" bind:value={username} class="h-10" placeholder="admin" />'));
  s = s.replace(
    `<Input id="username" autocomplete="username" bind:value={username} class="h-10" placeholder="admin" />`,
    `<Input id="username" autocomplete="username" bind:value={username} class="h-10 {userInvalid ? 'border-destructive ring-2 ring-destructive/30 animate-shake' : ''}" />`,
  );
  s = s.replace(
    `<Input id="password" type="password" autocomplete="new-password" bind:value={password} class="h-10" />`,
    `<Input id="password" type="password" autocomplete="new-password" bind:value={password} class="h-10 {passInvalid ? 'border-destructive ring-2 ring-destructive/30 animate-shake' : ''}" />`,
  );
  s = s.replace(
    `<Input id="password2" type="password" autocomplete="new-password" bind:value={password2} class="h-10" />`,
    `<Input id="password2" type="password" autocomplete="new-password" bind:value={password2} class="h-10 {matchInvalid ? 'border-destructive ring-2 ring-destructive/30 animate-shake' : ''}" />`,
  );

  // 4) 每字段的明确错误文案
  s = s.replace(
    `          <Input id="username" autocomplete="username" bind:value={username} class="h-10 {userInvalid ? 'border-destructive ring-2 ring-destructive/30 animate-shake' : ''}" />`,
    `<Input id="username" autocomplete="username" bind:value={username} class="h-10 {userInvalid ? 'border-destructive ring-2 ring-destructive/30 animate-shake' : ''}" />
          {#if userInvalid}
            <p class="text-[11px] text-destructive animate-fade-in">请填写用户名（至少 2 个字符）</p>
          {/if}`,
  );
  s = s.replace(
    `<Input id="password" type="password" autocomplete="new-password" bind:value={password} class="h-10 {passInvalid ? 'border-destructive ring-2 ring-destructive/30 animate-shake' : ''}" />`,
    `<Input id="password" type="password" autocomplete="new-password" bind:value={password} class="h-10 {passInvalid ? 'border-destructive ring-2 ring-destructive/30 animate-shake' : ''}" />
          {#if passInvalid}
            <p class="text-[11px] text-destructive animate-fade-in">请设置密码（至少 6 位字符）</p>
          {/if}`,
  );
  s = s.replace(
    `<Input id="password2" type="password" autocomplete="new-password" bind:value={password2} class="h-10 {matchInvalid ? 'border-destructive ring-2 ring-destructive/30 animate-shake' : ''}" />`,
    `<Input id="password2" type="password" autocomplete="new-password" bind:value={password2} class="h-10 {matchInvalid ? 'border-destructive ring-2 ring-destructive/30 animate-shake' : ''}" />
          {#if matchInvalid}
            <p class="text-[11px] text-destructive animate-fade-in">{password2 ? '两次输入不一致' : '请再次输入密码'}</p>
          {/if}`,
  );

  // 5) 全局错误改为「先补全标红字段」提示
  ok('error block', s.includes(`        {#if error}`));
  // 保持原样（服务器返回错误仍显示）

  fs.writeFileSync(p, s);
}

// ── app.css：shake 动画 ──
{
  const p = 'app.css';
  let s = fs.readFileSync(p, 'utf8');
  if (!s.includes('@keyframes shake')) {
    s += `
/* 表单校验失败时的抖动提醒 */
@keyframes shake {
  10%, 90% { transform: translateX(-1px); }
  20%, 80% { transform: translateX(2px); }
  30%, 50%, 70% { transform: translateX(-3px); }
  40%, 60% { transform: translateX(3px); }
}

@utility animate-shake {
  animation: shake 0.4s cubic-bezier(0.36, 0.07, 0.19, 0.97) both;
}
`;
    fs.writeFileSync(p, s);
    console.log('ok shake utility');
  }
}

// ── attribute_quoted 修复（3 处）──
{
  const fixes = [
    ['lib/components/common/EmptyState.svelte',
      `<div class="{compact ? 'text-sm' : 'text-[15px]'} font-medium">{title}</div>`,
      `<div class={compact ? 'text-sm' : 'text-[15px]'} class:font-medium={false} class:text-[15px]={true} >{title}</div>`],
  ];
  // EmptyState 的正确写法直接三元
  let s = fs.readFileSync('lib/components/common/EmptyState.svelte', 'utf8');
  s = s.replace(
    `<div class="{compact ? 'text-sm' : 'text-[15px]'} font-medium">{title}</div>`,
    `<div class={compact ? 'text-sm font-medium' : 'text-[15px] font-medium'}>{title}</div>`,
  );
  fs.writeFileSync('lib/components/common/EmptyState.svelte', s);
  console.log('ok EmptyState');

  let t = fs.readFileSync('lib/components/layout/Topbar.svelte', 'utf8');
  t = t.replace(
    `<RefreshCw size={16} class="{refreshing ? 'animate-spin' : ''}" />`,
    `<RefreshCw size={16} class={refreshing ? 'animate-spin' : ''} />`,
  );
  fs.writeFileSync('lib/components/layout/Topbar.svelte', t);
  console.log('ok Topbar');

  let n = fs.readFileSync('lib/pages/NAT.svelte', 'utf8');
  n = n.replace(
    `<Badge variant="outline" class="{r.type === 'MASQ' ? 'bg-primary/10 text-primary ring-primary/20' : 'bg-info/10 text-info ring-info/20'}">`,
    `<Badge variant="outline" class={r.type === 'MASQ' ? 'bg-primary/10 text-primary ring-primary/20' : 'bg-info/10 text-info ring-info/20'}>`,
  );
  fs.writeFileSync('lib/pages/NAT.svelte', n);
  console.log('ok NAT');
}

// ── 动态导入混用消除 ──
{
  let app = fs.readFileSync('App.svelte', 'utf8');
  const dyn = "    const { initRouter } = await import('$lib/router.svelte');\n    initRouter();";
  if (app.includes(dyn)) {
    app = app.replace(dyn, '    initRouter();');
    fs.writeFileSync('App.svelte', app);
    console.log('ok App.svelte static initRouter');
  } else {
    console.log('skip App.svelte (pattern differs)');
  }

  let ip = fs.readFileSync('lib/pages/IPLists.svelte', 'utf8');
  const m = ip.match(/const \{ api \} = await import\('\$lib\/api'\);/);
  if (m) {
    // 移除动态导入行，改用顶部静态 api
    ip = ip.replace("      const { api } = await import('$lib/api');\n", '');
    fs.writeFileSync('lib/pages/IPLists.svelte', ip);
    console.log('ok IPLists dynamic api removed');
  } else {
    console.log('skip IPLists');
  }
}

// ── DangerDialog：意图性初始捕获加 ignore 注释 ──
{
  const p = 'lib/components/common/DangerDialog.svelte';
  let s = fs.readFileSync(p, 'utf8');
  const anchor = `  let phase = $state<'pending' | 'applied'>(sync.applied ? 'applied' : 'pending');`;
  if (!s.includes(anchor)) { console.error('DangerDialog anchor missing'); process.exit(1); }
  // 组件实例随 {#if danger} 挂载/销毁，props 不会中途变化——初始捕获是有意为之
  s = s.replace(
    anchor,
    `  // svelte-ignore state_referenced_locally -- 组件实例随弹窗挂载/销毁，props 恒为打开时传入值，有意捕获初始值\n` + anchor,
  );
  fs.writeFileSync(p, s);
  console.log('ok DangerDialog ignore');
}
