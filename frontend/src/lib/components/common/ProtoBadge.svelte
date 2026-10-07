<script lang="ts">
  import type { Protocol } from '$lib/types';

  let { protocol }: { protocol: Protocol } = $props();

  const styles: Record<Protocol, string> = {
    tcp: 'bg-info/10 text-info ring-info/20',
    udp: 'bg-warning/15 text-warning ring-warning/25',
    both: 'bg-primary/10 text-primary ring-primary/20',
  };
  const labels: Record<Protocol, string> = { tcp: 'TCP', udp: 'UDP', both: 'TCP+UDP' };
</script>

<span class="inline-flex items-center rounded-md px-1.5 py-0.5 font-mono text-[10.5px] font-medium ring-1 ring-inset {styles[protocol]}">
  {labels[protocol]}
</span>
