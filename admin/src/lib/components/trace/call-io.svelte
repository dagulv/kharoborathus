<script lang="ts">
	import { api } from '$lib/api.js';
	import { show } from '$lib/trace.js';
	import type { Log } from '$lib/types.js';

	let { id }: { id: number } = $props();

	// Mounted on the row's first open, so a stored page is fetched only when asked for.
	// svelte-ignore state_referenced_locally
	const log = api<Log>(`/admin/logs/${id}`);
</script>

{#snippet body(label: string, value: unknown)}
	<div class="min-w-0">
		<div class="mb-1 text-[10px] font-semibold tracking-wide text-muted-foreground uppercase">
			{label}
		</div>
		{#if value === null || value === undefined}
			<p class="text-xs text-muted-foreground">No body</p>
		{:else}
			<pre
				class="max-h-80 overflow-auto rounded-md bg-muted p-2 text-xs break-words whitespace-pre-wrap">{show(
					value
				)}</pre>
		{/if}
	</div>
{/snippet}

{#await log}
	<p class="text-xs text-muted-foreground">Loading…</p>
{:then log}
	<div class="grid gap-2 xl:grid-cols-2">
		{@render body('→ Input', log.request)}
		{@render body(`← Output${log.status ? ` · ${log.status}` : ''}`, log.response)}
	</div>
	{#if log.error}
		<p class="mt-1 text-xs break-all text-destructive">{log.error}</p>
	{/if}
{:catch err}
	<p class="text-xs text-destructive">Could not load the call: {err.message}</p>
{/await}
