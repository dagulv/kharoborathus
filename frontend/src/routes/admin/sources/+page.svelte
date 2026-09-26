<script lang="ts">
	import { page } from '$app/state';
	import { listSources } from '../admin.remote';

	const includeDeleted = $derived(page.url.searchParams.has('deleted'));
	const sources = $derived(await listSources({ includeDeleted }));

	const fmt = new Intl.DateTimeFormat('sv-SE', { dateStyle: 'short', timeStyle: 'short' });
</script>

<svelte:head><title>Sources · Admin</title></svelte:head>

<div class="mb-4 flex items-center justify-between">
	<h1 class="text-xl font-semibold">
		Sources <span class="text-zinc-400">({sources.length})</span>
	</h1>
	<div class="flex items-center gap-4">
		<a class="text-sm text-blue-600 hover:underline" href={includeDeleted ? '?' : '?deleted'}>
			{includeDeleted ? 'Hide deleted' : 'Show deleted'}
		</a>
		<a class="rounded bg-zinc-900 px-3 py-1 text-sm text-white" href="/admin/sources/new">
			Add source
		</a>
	</div>
</div>

<div class="overflow-x-auto rounded border border-zinc-200 bg-white">
	<table class="w-full text-left text-sm">
		<thead class="border-b border-zinc-200 bg-zinc-50 text-zinc-500">
			<tr>
				<th class="px-3 py-2 font-medium">URL</th>
				<th class="px-3 py-2 font-medium">Adapter</th>
				<th class="px-3 py-2 font-medium">Status</th>
				<th class="px-3 py-2 text-right font-medium">Rate</th>
				<th class="px-3 py-2 font-medium">Target tags</th>
				<th class="px-3 py-2 text-right font-medium">Posts</th>
				<th class="px-3 py-2 font-medium">Last post</th>
				<th class="px-3 py-2 font-medium">Created</th>
			</tr>
		</thead>
		<tbody>
			{#each sources as source (source.id)}
				<tr class={['border-b border-zinc-100 last:border-0', source.deletedAt && 'opacity-50']}>
					<td class="max-w-sm px-3 py-2">
						<div class="text-xs text-zinc-500">{source.domainUrl}</div>
						<a
							class="block truncate hover:underline"
							href={source.url}
							target="_blank"
							rel="noreferrer"
							title={source.url}
						>
							{source.url}
						</a>
					</td>
					<td class="px-3 py-2">{source.adapter}</td>
					<td class="px-3 py-2">
						{#if source.deletedAt}
							<span class="rounded bg-red-100 px-1.5 py-0.5 text-xs text-red-700">deleted</span>
						{:else if source.active}
							<span class="rounded bg-green-100 px-1.5 py-0.5 text-xs text-green-700">active</span>
						{:else}
							<span class="rounded bg-zinc-100 px-1.5 py-0.5 text-xs text-zinc-600">inactive</span>
						{/if}
					</td>
					<td class="px-3 py-2 text-right tabular-nums">{source.rate}m</td>
					<td class="px-3 py-2">
						<div class="flex flex-wrap gap-1">
							{#each source.targetTags as tag (tag)}
								<span class="rounded bg-zinc-100 px-1.5 py-0.5 text-xs">{tag}</span>
							{/each}
						</div>
					</td>
					<td class="px-3 py-2 text-right tabular-nums">
						<a class="text-blue-600 hover:underline" href="/admin/posts?source={source.id}"
							>{source.postCount}</a
						>
					</td>
					<td class="px-3 py-2 whitespace-nowrap text-zinc-500">
						{source.lastPostedAt ? fmt.format(source.lastPostedAt) : '—'}
					</td>
					<td class="px-3 py-2 whitespace-nowrap text-zinc-500">{fmt.format(source.createdAt)}</td>
				</tr>
			{:else}
				<tr><td colspan="8" class="px-3 py-6 text-center text-zinc-500">No sources</td></tr>
			{/each}
		</tbody>
	</table>
</div>
