<script lang="ts">
  import { Tooltip } from '$lib/components/ui/tooltip';

  let {
    state,
    label,
  }: {
    state: 'active' | 'disabled' | 'pending' | 'error';
    label?: string;
  } = $props();

  const dot: Record<string, string> = {
    active: 'bg-success',
    disabled: 'bg-muted-foreground/40',
    pending: 'bg-warning',
    error: 'bg-destructive',
  };
  const text: Record<string, string> = {
    active: '生效中',
    disabled: '已停用',
    pending: '待生效',
    error: '异常',
  };
</script>

<Tooltip.Root>
  <Tooltip.Trigger class="inline-flex cursor-default items-center gap-1.5">
    <span class="relative flex h-[7px] w-[7px]">
      {#if state === 'active'}
        <span class="absolute inline-flex h-full w-full rounded-full bg-success opacity-50 animate-ping"></span>
      {/if}
      <span class="relative inline-flex h-[7px] w-[7px] rounded-full {dot[state]}"></span>
    </span>
    <span class="text-xs {state === 'disabled' ? 'text-muted-foreground' : 'text-foreground/80'}">
      {label ?? text[state]}
    </span>
  </Tooltip.Trigger>
  <Tooltip.Content class="text-xs">{label ?? text[state]}</Tooltip.Content>
</Tooltip.Root>
