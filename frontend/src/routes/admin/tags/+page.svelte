<script lang="ts">
	import { listAliases, listProposals, listTags, mergeTag, setTagEnabled } from '../admin.remote';

	const tags = $derived(await listTags());
	const proposals = $derived(await listProposals());
	const aliases = $derived(await listAliases());

	const fmt = new Intl.DateTimeFormat('sv-SE', { dateStyle: 'short' });
	const pct = new Intl.NumberFormat('sv-SE', { style: 'percent', maximumFractionDigits: 1 });

	let from = $state('');
	let to = $state('');
	let mergeError = $state('');

	async function merge() {
		mergeError = '';
		try {
			await mergeTag({ from, to });
			from = '';
			to = '';
		} catch (err) {
			mergeError = (err as { body?: { message?: string } }).body?.message ?? 'Merge failed';
		}
	}
</script>

<svelte:head><title>Tags · Admin</title></svelte:head>

<h1 class="mb-4 text-xl font-semibold">
	Tags <span class="text-zinc-400">({tags.length})</span>
</h1>

<form
	class="mb-6 flex flex-wrap items-end gap-2 text-sm"
	onsubmit={(e) => {
		e.preventDefault();
		merge();
	}}
>
	<label>
		<span class="block text-zinc-500">Merge</span>
		<select class="rounded border border-zinc-300 px-2 py-1" bind:value={from}>
			<option value="">tag</option>
			{#each tags as tag (tag.key)}<option value={tag.key}>{tag.key}</option>{/each}
		</select>
	</label>
	<label>
		<span class="block text-zinc-500">into</span>
		<select class="rounded border border-zinc-300 px-2 py-1" bind:value={to}>
			<option value="">tag</option>
			{#each tags as tag (tag.key)}<option value={tag.key}>{tag.key}</option>{/each}
		</select>
	</label>
	<button
		class="rounded bg-zinc-900 px-3 py-1 text-white disabled:opacity-50"
		disabled={!from || !to || from === to}
	>
		Merge
	</button>
	{#if mergeError}<span class="text-red-600">{mergeError}</span>{/if}
</form>

<div class="overflow-x-auto rounded border border-zinc-200 bg-white">
	<table class="w-full text-left text-sm">
		<thead class="border-b border-zinc-200 bg-zinc-50 text-zinc-500">
			<tr>
				<th class="px-3 py-2 font-medium">Key</th>
				<th class="px-3 py-2 font-medium">Terms</th>
				<th class="px-3 py-2 font-medium">Status</th>
				<th class="px-3 py-2 font-medium">Gate</th>
				<th class="px-3 py-2 text-right font-medium">Match rate</th>
				<th class="px-3 py-2 text-right font-medium">Posts</th>
				<th class="px-3 py-2 font-medium">Last match</th>
				<th class="px-3 py-2 font-medium">Enabled</th>
			</tr>
		</thead>
		<tbody>
			{#each tags as tag (tag.key)}
				<tr class={['border-b border-zinc-100 last:border-0', !tag.enabled && 'opacity-50']}>
					<td class="px-3 py-2">
						<a class="hover:underline" href="/admin/posts?tag={encodeURIComponent(tag.key)}">{tag.key}</a>
					</td>
					<td class="px-3 py-2 text-xs text-zinc-500">{tag.terms.join(', ')}</td>
					<td class="px-3 py-2">{tag.status}</td>
					<td class="px-3 py-2">{tag.gate ? 'yes' : 'no'}</td>
					<td class="px-3 py-2 text-right tabular-nums">
						{tag.matchRate === null ? '' : pct.format(tag.matchRate)}
					</td>
					<td class="px-3 py-2 text-right tabular-nums">{tag.postCount}</td>
					<td class="px-3 py-2">{tag.lastMatchedAt ? fmt.format(tag.lastMatchedAt) : ''}</td>
					<td class="px-3 py-2">
						<input
							type="checkbox"
							checked={tag.enabled}
							onchange={(e) => setTagEnabled({ key: tag.key, enabled: e.currentTarget.checked })}
						/>
					</td>
				</tr>
			{/each}
		</tbody>
	</table>
</div>

<h2 class="mt-8 mb-2 font-semibold">Proposals <span class="text-zinc-400">({proposals.length})</span></h2>

<div class="overflow-x-auto rounded border border-zinc-200 bg-white">
	<table class="w-full text-left text-sm">
		<thead class="border-b border-zinc-200 bg-zinc-50 text-zinc-500">
			<tr>
				<th class="px-3 py-2 font-medium">Key</th>
				<th class="px-3 py-2 font-medium">Terms</th>
				<th class="px-3 py-2 text-right font-medium">Posts</th>
				<th class="px-3 py-2 text-right font-medium">Domains</th>
				<th class="px-3 py-2 font-medium">Last proposed</th>
			</tr>
		</thead>
		<tbody>
			{#each proposals as proposal (proposal.key)}
				<tr class="border-b border-zinc-100 last:border-0">
					<td class="px-3 py-2">{proposal.key}</td>
					<td class="px-3 py-2 text-xs text-zinc-500">{proposal.terms.join(', ')}</td>
					<td class="px-3 py-2 text-right tabular-nums">{proposal.postCount}</td>
					<td class="px-3 py-2 text-right tabular-nums">{proposal.domainCount}</td>
					<td class="px-3 py-2">{fmt.format(proposal.lastProposedAt)}</td>
				</tr>
			{/each}
		</tbody>
	</table>
</div>

<h2 class="mt-8 mb-2 font-semibold">Aliases <span class="text-zinc-400">({aliases.length})</span></h2>

<ul class="text-sm">
	{#each aliases as alias (alias.alias)}
		<li><span class="text-zinc-500">{alias.alias}</span> → {alias.key}</li>
	{/each}
</ul>
