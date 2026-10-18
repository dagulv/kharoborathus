<script lang="ts">
	import { page } from '$app/state';
	import { getLog } from '../../admin.remote';

	const log = $derived(await getLog(page.params.id!));

	const fmt = new Intl.DateTimeFormat('sv-SE', { dateStyle: 'short', timeStyle: 'medium' });

	// Non-JSON bodies, like pages, are stored as strings and read better unquoted.
	function show(value: unknown) {
		return typeof value === 'string' ? value : JSON.stringify(value, null, 2);
	}
</script>

<svelte:head><title>Log {log.id} · Admin</title></svelte:head>

<a class="text-sm text-blue-600 hover:underline" href="/admin/logs">← Logs</a>

<article class="mt-3 rounded border border-zinc-200 bg-white p-6">
	<h1 class="text-xl font-semibold">{log.type} <span class="text-zinc-400">#{log.id}</span></h1>

	<dl class="mt-3 grid grid-cols-[max-content_1fr] gap-x-4 gap-y-1 text-sm">
		<dt class="text-zinc-500">Time</dt>
		<dd>{fmt.format(log.createdAt)}</dd>
		<dt class="text-zinc-500">Level</dt>
		<dd>{log.level}</dd>
		<dt class="text-zinc-500">Status</dt>
		<dd>{log.status ?? '—'}</dd>
		<dt class="text-zinc-500">Duration</dt>
		<dd>{log.durationMs} ms</dd>
		<dt class="text-zinc-500">Request</dt>
		<dd class="break-all">{log.method} {log.url}</dd>
		{#if log.error}
			<dt class="text-zinc-500">Error</dt>
			<dd class="break-all text-red-600">{log.error}</dd>
		{/if}
	</dl>

	<section class="mt-6">
		<h2 class="mb-1 text-sm font-medium text-zinc-500">Metadata</h2>
		<pre class="overflow-x-auto rounded bg-zinc-50 p-4 text-sm">{show(log.metadata)}</pre>
	</section>

	<section class="mt-6">
		<h2 class="mb-1 text-sm font-medium text-zinc-500">Request</h2>
		<pre
			class="max-h-[40rem] overflow-auto rounded bg-zinc-50 p-4 text-sm whitespace-pre-wrap">{show(
				log.request
			)}</pre>
	</section>

	<section class="mt-6">
		<h2 class="mb-1 text-sm font-medium text-zinc-500">Response</h2>
		{#if log.response === null}
			<p class="text-sm text-zinc-400">No response body</p>
		{:else}
			<pre
				class="max-h-[40rem] overflow-auto rounded bg-zinc-50 p-4 text-sm whitespace-pre-wrap">{show(
					log.response
				)}</pre>
		{/if}
	</section>
</article>
