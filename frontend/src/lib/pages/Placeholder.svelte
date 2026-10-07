<script lang="ts">
  import { Hammer } from '@lucide/svelte';
  import EmptyState from '$lib/components/common/EmptyState.svelte';
  import { Badge } from '$lib/components/ui/badge';
  import { t } from '$lib/i18n';

  let { title }: { title: string } = $props();
</script>

<div class="animate-fade-up">
  <EmptyState title={t('placeholder.title')} desc={t('placeholder.desc')}>
    {#snippet icon()}<Hammer size={22} />{/snippet}
    {#snippet action()}
      <Badge variant="outline" class="bg-warning/10 text-warning ring-warning/25">{title} · 规划中</Badge>
    {/snippet}
  </EmptyState>
</div>
