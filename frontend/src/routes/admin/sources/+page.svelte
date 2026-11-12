<script lang="ts">
	import { page } from '$app/state';
	import { SvelteMap } from 'svelte/reactivity';
	import { backfillSource, listSources, reviewSource, syncSource } from '../admin.remote';
	import type { Source } from '#lib/types.js';

	const includeDeleted = $derived(page.url.searchParams.has('deleted'));
	const sources = $derived(await listSources({ includeDeleted }));

	const fmt = new Intl.DateTimeFormat('sv-SE', { dateStyle: 'short', timeStyle: 'short' });

	const syncState = new SvelteMap<string, 'pending' | 'requested' | 'failed'>();

	async function sync(id: string) {
		syncState.set(id, 'pending');
		try {
			await syncSource(id);
			syncState.set(id, 'requested');
		} catch {
			syncState.set(id, 'failed');
		}
		setTimeout(() => syncState.delete(id), 3000);
	}

	const backfillState = new SvelteMap<string, 'pending' | 'requested' | 'failed'>();

	async function backfill(id: string) {
		backfillState.set(id, 'pending');
		try {
			await backfillSource(id);
			backfillState.set(id, 'requested');
		} catch {
			backfillState.set(id, 'failed');
		}
		setTimeout(() => backfillState.delete(id), 3000);
	}

	// A disabled flagged source keeps its flag but has already been reviewed.
	const flagged = $derived(
		sources.filter((s) => s.status === 'flagged' && s.enabled && !s.deletedAt)
	);

	const reviewState = new SvelteMap<string, 'pending' | 'failed'>();

	async function review(source: Source, enabled: boolean) {
		reviewState.set(source.id, 'pending');
		try {
			await reviewSource({ id: source.id, enabled }).updates(listSources({ includeDeleted }));
			reviewState.delete(source.id);
		} catch {
			reviewState.set(source.id, 'failed');
		}
	}
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

{#if flagged.length}
	<section class="mb-6 rounded border border-amber-300 bg-amber-50 p-4">
		<h2 class="font-medium">Flagged <span class="text-zinc-500">({flagged.length})</span></h2>
		<p class="mb-3 text-sm text-zinc-600">Keep makes a source active again. Disable stops it.</p>
		<ul class="space-y-2">
			{#each flagged as source (source.id)}
				{@const status = reviewState.get(source.id)}
				<li class="flex flex-wrap items-center gap-2 text-sm">
					<a
						class="min-w-0 flex-1 truncate hover:underline"
						href="/admin/posts?source={source.id}"
						title={source.url}
					>
						{source.url}
					</a>
					<button
						type="button"
						class="rounded border border-zinc-300 bg-white px-2 py-0.5 text-xs hover:bg-zinc-100 disabled:opacity-50"
						disabled={status === 'pending'}
						onclick={() => review(source, true)}
					>
						Keep
					</button>
					<button
						type="button"
						class="rounded border border-red-300 bg-white px-2 py-0.5 text-xs text-red-700 hover:bg-red-50 disabled:opacity-50"
						disabled={status === 'pending'}
						onclick={() => review(source, false)}
					>
						Disable
					</button>
					{#if status === 'failed'}<span class="text-xs text-red-600">Failed</span>{/if}
				</li>
			{/each}
		</ul>
	</section>
{/if}

<div class="overflow-x-auto rounded border border-zinc-200 bg-white">
	<table class="w-full text-left text-sm">
		<thead class="border-b border-zinc-200 bg-zinc-50 text-zinc-500">
			<tr>
				<th class="px-3 py-2 font-medium">URL</th>
				<th class="px-3 py-2 font-medium">Adapter</th>
				<th class="px-3 py-2 font-medium">Status</th>
				<th class="px-3 py-2 text-right font-medium">Rate</th>
				<th class="px-3 py-2 text-right font-medium">Posts</th>
				<th class="px-3 py-2 font-medium">Last post</th>
				<th class="px-3 py-2 font-medium">Created</th>
				<th class="px-3 py-2"></th>
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
						{:else if source.enabled}
							<span class="rounded bg-green-100 px-1.5 py-0.5 text-xs text-green-700">enabled</span>
						{:else}
							<span class="rounded bg-zinc-100 px-1.5 py-0.5 text-xs text-zinc-600">disabled</span>
						{/if}
						{#if source.status !== 'active'}
							<span class="rounded bg-blue-100 px-1.5 py-0.5 text-xs text-blue-700"
								>{source.status}</span
							>
						{/if}
					</td>
					<td class="px-3 py-2 text-right tabular-nums">{source.rate}m</td>
					<td class="px-3 py-2 text-right tabular-nums">
						<a class="text-blue-600 hover:underline" href="/admin/posts?source={source.id}"
							>{source.postCount}</a
						>
					</td>
					<td class="px-3 py-2 whitespace-nowrap text-zinc-500">
						{source.lastPostedAt ? fmt.format(source.lastPostedAt) : '—'}
					</td>
					<td class="px-3 py-2 whitespace-nowrap text-zinc-500">{fmt.format(source.createdAt)}</td>
					<td class="px-3 py-2 text-right whitespace-nowrap">
						{#if !source.deletedAt && source.enabled && source.status === 'active'}
							{@const status = syncState.get(source.id)}
							{@const backfillStatus = backfillState.get(source.id)}
							<button
								type="button"
								onclick={() => sync(source.id)}
								disabled={status === 'pending'}
								title="Run the pipeline now and restart the {source.rate}m wait"
								class={[
									'rounded border px-2 py-0.5 text-xs disabled:opacity-50',
									status === 'failed'
										? 'border-red-300 text-red-700'
										: status === 'requested'
											? 'border-green-300 text-green-700'
											: 'border-zinc-300 hover:bg-zinc-100'
								]}
							>
								{status === 'pending'
									? 'Syncing…'
									: status === 'requested'
										? 'Requested'
										: status === 'failed'
											? 'Failed'
											: 'Sync'}
							</button>
							<button
								type="button"
								onclick={() => backfill(source.id)}
								disabled={backfillStatus === 'pending'}
								title="Read the source's history, skipping stored posts"
								class={[
									'rounded border px-2 py-0.5 text-xs disabled:opacity-50',
									backfillStatus === 'failed'
										? 'border-red-300 text-red-700'
										: backfillStatus === 'requested'
											? 'border-green-300 text-green-700'
											: 'border-zinc-300 hover:bg-zinc-100'
								]}
							>
								{backfillStatus === 'pending'
									? 'Backfilling…'
									: backfillStatus === 'requested'
										? 'Requested'
										: backfillStatus === 'failed'
											? 'Failed'
											: 'Backfill'}
							</button>
						{/if}
					</td>
				</tr>
			{:else}
				<tr><td colspan="8" class="px-3 py-6 text-center text-zinc-500">No sources</td></tr>
			{/each}
		</tbody>
	</table>
</div>
