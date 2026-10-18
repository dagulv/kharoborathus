<script lang="ts">
	import { page } from '$app/state';
	import { SvelteURLSearchParams } from 'svelte/reactivity';
	import { listLogs } from '../admin.remote';
	import { LOG_TYPES } from '#lib/types.js';

	const params = $derived(page.url.searchParams);
	const filter = $derived({
		type: (params.get('type') || undefined) as (typeof LOG_TYPES)[number] | undefined,
		level: (params.get('level') || undefined) as 'INFO' | 'WARN' | 'ERROR' | undefined,
		metaKey: params.get('key') || undefined,
		metaValue: params.get('value') || undefined,
		page: Math.max(1, Number(params.get('page')) || 1)
	});

	const result = $derived(await listLogs(filter));

	const fmt = new Intl.DateTimeFormat('sv-SE', { dateStyle: 'short', timeStyle: 'medium' });

	function withParams(changes: Record<string, string | undefined>) {
		const next = new SvelteURLSearchParams(params.toString());
		for (const [key, value] of Object.entries(changes)) {
			if (value) next.set(key, value);
			else next.delete(key);
		}
		const qs = next.toString();
		return qs ? `?${qs}` : '?';
	}

	// Grouped metadata shows as dotted keys, but only top-level keys can be filtered on.
	function flatten(meta: Record<string, unknown>, prefix = ''): [string, string, boolean][] {
		return Object.entries(meta).flatMap(([key, value]) =>
			value && typeof value === 'object'
				? flatten(value as Record<string, unknown>, `${prefix}${key}.`)
				: [[`${prefix}${key}`, String(value), prefix === '']]
		);
	}

	const levelClass: Record<string, string> = {
		INFO: 'bg-zinc-100 text-zinc-600',
		WARN: 'bg-amber-100 text-amber-800',
		ERROR: 'bg-red-100 text-red-700'
	};
</script>

<svelte:head><title>Logs · Admin</title></svelte:head>

<div class="mb-4 flex flex-wrap items-center justify-between gap-3">
	<h1 class="text-xl font-semibold">Logs</h1>

	<form method="GET" class="flex flex-wrap gap-2 text-sm">
		<select
			name="type"
			value={filter.type ?? ''}
			class="rounded border border-zinc-300 bg-white px-2 py-1"
		>
			<option value="">All types</option>
			{#each LOG_TYPES as type (type)}<option value={type}>{type}</option>{/each}
		</select>
		<select
			name="level"
			value={filter.level ?? ''}
			class="rounded border border-zinc-300 bg-white px-2 py-1"
		>
			<option value="">All levels</option>
			<option value="INFO">INFO</option>
			<option value="WARN">WARN</option>
			<option value="ERROR">ERROR</option>
		</select>
		<input
			name="key"
			value={filter.metaKey ?? ''}
			placeholder="metadata key"
			class="w-36 rounded border border-zinc-300 bg-white px-2 py-1"
		/>
		<input
			name="value"
			value={filter.metaValue ?? ''}
			placeholder="value"
			class="w-72 rounded border border-zinc-300 bg-white px-2 py-1"
		/>
		<button class="rounded bg-zinc-900 px-3 py-1 text-white">Filter</button>
		<a class="self-center text-blue-600 hover:underline" href="?">Clear</a>
	</form>
</div>

<div class="overflow-x-auto rounded border border-zinc-200 bg-white">
	<table class="w-full text-left text-sm">
		<thead class="border-b border-zinc-200 bg-zinc-50 text-zinc-500">
			<tr>
				<th class="px-3 py-2 font-medium">Time</th>
				<th class="px-3 py-2 font-medium">Type</th>
				<th class="px-3 py-2 font-medium">Status</th>
				<th class="px-3 py-2 text-right font-medium">Duration</th>
				<th class="px-3 py-2 font-medium">Request</th>
				<th class="px-3 py-2 font-medium">Metadata</th>
			</tr>
		</thead>
		<tbody>
			{#each result.logs as log (log.id)}
				<tr class="border-b border-zinc-100 align-top last:border-0">
					<td class="px-3 py-2 whitespace-nowrap">
						<a class="text-blue-600 hover:underline" href="/admin/logs/{log.id}"
							>{fmt.format(log.createdAt)}</a
						>
					</td>
					<td class="px-3 py-2">
						<a class="hover:underline" href={withParams({ type: log.type, page: undefined })}
							>{log.type}</a
						>
					</td>
					<td class="px-3 py-2 whitespace-nowrap">
						<span class={['rounded px-1.5 py-0.5 text-xs', levelClass[log.level]]}
							>{log.status ?? log.level}</span
						>
					</td>
					<td class="px-3 py-2 text-right whitespace-nowrap tabular-nums">{log.durationMs} ms</td>
					<td class="max-w-md px-3 py-2">
						<div class="truncate" title={log.url ?? ''}>
							<span class="text-xs text-zinc-500">{log.method}</span>
							{log.url}
						</div>
						{#if log.error}
							<div class="truncate text-xs text-red-600" title={log.error}>{log.error}</div>
						{/if}
					</td>
					<td class="px-3 py-2">
						<div class="flex flex-wrap gap-1">
							{#each flatten(log.metadata) as [key, value, filterable] (key)}
								{#if filterable}
									<a
										class="rounded bg-zinc-100 px-1.5 py-0.5 text-xs hover:bg-zinc-200"
										href={withParams({ key, value, page: undefined })}>{key}: {value}</a
									>
								{:else}
									<span class="rounded bg-zinc-100 px-1.5 py-0.5 text-xs">{key}: {value}</span>
								{/if}
							{/each}
						</div>
					</td>
				</tr>
			{:else}
				<tr><td colspan="6" class="px-3 py-6 text-center text-zinc-500">No logs</td></tr>
			{/each}
		</tbody>
	</table>
</div>

{#if result.page > 1 || result.hasNext}
	<nav class="mt-4 flex items-center justify-center gap-4 text-sm">
		{#if result.page > 1}
			<a class="text-blue-600 hover:underline" href={withParams({ page: String(result.page - 1) })}
				>← Prev</a
			>
		{/if}
		<span class="text-zinc-500">Page {result.page}</span>
		{#if result.hasNext}
			<a class="text-blue-600 hover:underline" href={withParams({ page: String(result.page + 1) })}
				>Next →</a
			>
		{/if}
	</nav>
{/if}
