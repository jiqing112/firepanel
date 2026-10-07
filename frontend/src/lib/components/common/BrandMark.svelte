<script lang="ts">
  import { Flame } from '@lucide/svelte';
  import { t } from '$lib/i18n';

  let { size = 28 }: { size?: number } = $props();
</script>

<span class="inline-flex items-center justify-center rounded-lg bg-gradient-to-br from-sky-400 via-blue-500 to-blue-600 shadow-soft" style="width:{size}px;height:{size}px">
  <Flame size={size * 0.58} strokeWidth={2.2} class="text-white" />
</span>
{#if size >= 24}
  <span class="sr-only">{t('app.name')}</span>
{/if}
