<script lang="ts">
  /**
   * 高危变更确认流程：
   *  阶段A（未生效）：倒计时自动生效 / 立即生效 / 撤销
   *  阶段B（已生效）：确认窗口倒计时内「确认保留」，超时自动回滚
   */
  import { AlertTriangle } from '@lucide/svelte';
  import * as Dialog from '$lib/components/ui/dialog';
  import { Button } from '$lib/components/ui/button';
  import { backend, onWSMessage, type SyncResult } from '$lib/api';
  import { toast } from 'svelte-sonner';

  let { sync, onClose }: { sync: SyncResult; onClose: (resolved: 'confirmed' | 'cancelled') => void } = $props();

  // svelte-ignore state_referenced_locally -- 组件实例随弹窗挂载/销毁，props 恒为打开时传入值，有意捕获初始值
  let phase = $state<'pending' | 'applied'>(sync.applied ? 'applied' : 'pending');
  // svelte-ignore state_referenced_locally -- 同上：倒计时初值取自打开时的 sync
  let countdown = $state(phase === 'pending' ? (sync.delay_sec ?? 60) : (sync.confirm_sec ?? 90));
  let busy = $state(false);
  let done = $state(false);

  // 本地倒计时（仅展示；真实状态以服务端为准）
  $effect(() => {
    const t = setInterval(() => {
      countdown = Math.max(0, countdown - 1);
    }, 1000);
    return () => clearInterval(t);
  });

  // 服务端事件驱动弹窗收尾：超时回滚 / 其他会话确认或撤销时，本地不再停留
  $effect(() => {
    const off = onWSMessage((topic, data) => {
      if (topic !== 'events' || done) return;
      const e = data as { type?: string; token?: string; message?: string };
      if (!e.type || (e.token && sync.token && e.token !== sync.token)) return;
      if (e.type === 'rollback') {
        done = true;
        toast.warning('确认窗口已超时，规则已自动回滚');
        onClose('cancelled');
      } else if (e.type === 'danger_applied' && phase === 'pending') {
        // 未点「立即生效」、由服务端延时看门狗自动生效
        phase = 'applied';
        countdown = sync.confirm_sec ?? 90;
        toast.info('高危规则已生效', { description: `${countdown} 秒内请确认保留，否则自动回滚` });
      } else if (e.type === 'apply_ok') {
        if ((e.message ?? '').includes('已确认') && phase !== 'pending') {
          done = true;
          toast.success('已确认保留新规则');
          onClose('confirmed');
        } else if ((e.message ?? '').includes('已撤销')) {
          done = true;
          toast.info('已撤销，相关数据已恢复');
          onClose('cancelled');
        }
      }
    });
    return off;
  });

  async function applyNow() {
    busy = true;
    try {
      await backend.confirmDanger(sync.token!, 1);
      phase = 'applied';
      countdown = sync.confirm_sec ?? 90;
      toast.info('高危规则已生效', { description: `${countdown} 秒内请确认保留，否则自动回滚` });
    } catch (e) {
      toast.error(e instanceof Error ? e.message : String(e));
    } finally {
      busy = false;
    }
  }

  async function confirmKeep() {
    busy = true;
    try {
      await backend.confirmDanger(sync.token!, 2);
      done = true;
      toast.success('已确认保留新规则');
      onClose('confirmed');
    } catch (e) {
      toast.error(e instanceof Error ? e.message : String(e));
    } finally {
      busy = false;
    }
  }

  async function cancel() {
    busy = true;
    try {
      await backend.cancelDanger(sync.token!);
      done = true;
      toast.info('已撤销，相关数据已恢复');
      onClose('cancelled');
    } catch (e) {
      toast.error(e instanceof Error ? e.message : String(e));
    } finally {
      busy = false;
    }
  }
</script>

<Dialog.Root open={true}>
  <Dialog.Content class="sm:max-w-md" onInteractOutside={(e) => e.preventDefault()}>
    <Dialog.Header>
      <Dialog.Title class="flex items-center gap-2 text-warning">
        <span class="flex h-8 w-8 items-center justify-center rounded-lg bg-warning/15">
          <AlertTriangle size={17} class="text-warning" />
        </span>
        {phase === 'pending' ? '高危规则变更确认' : '请确认保留新规则'}
      </Dialog.Title>
      <Dialog.Description class="text-left">{sync.reason}</Dialog.Description>
    </Dialog.Header>

    <div class="rounded-lg border border-warning/30 bg-warning/5 px-4 py-3 text-[12.5px] leading-relaxed">
      {#if phase === 'pending'}
        规则将在 <span class="font-num font-semibold text-warning">{countdown}</span> 秒后自动生效；
        生效后有 <span class="font-num font-semibold">{sync.confirm_sec ?? 90}</span> 秒确认窗口，
        未确认将自动回滚到当前规则。
      {:else}
        新规则已生效。请在
        <span class="font-num font-semibold text-warning">{countdown}</span>
        秒内点击「确认保留」，否则系统将自动回滚，防止误操作导致失联。
      {/if}
    </div>

    <Dialog.Footer class="gap-2 sm:justify-end">
      {#if phase === 'pending'}
        <Button variant="outline" disabled={busy} onclick={cancel}>撤销变更</Button>
        <Button disabled={busy} onclick={applyNow}>立即生效</Button>
      {:else}
        <Button variant="outline" disabled={busy} onclick={cancel}>回滚规则</Button>
        <Button disabled={busy} onclick={confirmKeep}>确认保留</Button>
      {/if}
    </Dialog.Footer>
  </Dialog.Content>
</Dialog.Root>
