<script lang="ts">
	import { graphRows } from '$lib/trace.js';
	import type { PostStatus, Trace } from '$lib/types.js';
	import StepRow from './step-row.svelte';

	let { trace, status }: { trace: Trace; status: PostStatus } = $props();

	const rows = $derived(graphRows(trace, status));
</script>

{#if rows.length}
	<ol
		class="relative flex flex-col gap-2 before:absolute before:top-4 before:bottom-4 before:left-[5px] before:w-0.5 before:bg-border"
	>
		{#each rows as row (row.kind === 'step' ? `s${row.step.id}` : `x${row.name}`)}
			<StepRow {row} />
		{/each}
	</ol>
{:else}
	<p class="text-sm text-muted-foreground">No steps recorded for this post.</p>
{/if}
