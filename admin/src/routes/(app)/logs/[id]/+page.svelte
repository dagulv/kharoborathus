<script lang="ts">
	import * as Card from '$lib/components/ui/card/index.js';
	import { fmtDateTimeSeconds } from '$lib/format.js';
	import type { PageProps } from './$types';

	let { data }: PageProps = $props();

	const log = $derived(data.log);

	// Non-JSON bodies, like pages, are stored as strings and read better unquoted.
	function show(value: unknown) {
		return typeof value === 'string' ? value : JSON.stringify(value, null, 2);
	}
</script>

<svelte:head><title>Log {log.id} · Admin</title></svelte:head>

<a class="text-sm text-muted-foreground hover:text-foreground" href="/logs">← Logs</a>

<Card.Root class="mt-3">
	<Card.Content>
		<h1 class="text-lg font-semibold">
			{log.type} <span class="text-muted-foreground">#{log.id}</span>
		</h1>

		<dl class="mt-3 grid grid-cols-[max-content_1fr] gap-x-4 gap-y-1">
			<dt class="text-muted-foreground">Time</dt>
			<dd>{fmtDateTimeSeconds(log.createdAt)}</dd>
			<dt class="text-muted-foreground">Level</dt>
			<dd>{log.level}</dd>
			<dt class="text-muted-foreground">Status</dt>
			<dd>{log.status ?? '—'}</dd>
			<dt class="text-muted-foreground">Duration</dt>
			<dd>{log.durationMs} ms</dd>
			<dt class="text-muted-foreground">Request</dt>
			<dd class="break-all">{log.method} {log.url}</dd>
			{#if log.error}
				<dt class="text-muted-foreground">Error</dt>
				<dd class="break-all text-destructive">{log.error}</dd>
			{/if}
		</dl>

		<section class="mt-6">
			<h2 class="mb-1 text-xs font-medium text-muted-foreground">Metadata</h2>
			<pre class="overflow-x-auto rounded-md bg-muted p-4 text-sm">{show(log.metadata)}</pre>
		</section>

		<section class="mt-6">
			<h2 class="mb-1 text-xs font-medium text-muted-foreground">Request</h2>
			<pre
				class="max-h-[40rem] overflow-auto rounded-md bg-muted p-4 text-sm whitespace-pre-wrap">{show(
					log.request
				)}</pre>
		</section>

		<section class="mt-6">
			<h2 class="mb-1 text-xs font-medium text-muted-foreground">Response</h2>
			{#if log.response === null}
				<p class="text-muted-foreground">No response body</p>
			{:else}
				<pre
					class="max-h-[40rem] overflow-auto rounded-md bg-muted p-4 text-sm whitespace-pre-wrap">{show(
						log.response
					)}</pre>
			{/if}
		</section>
	</Card.Content>
</Card.Root>
