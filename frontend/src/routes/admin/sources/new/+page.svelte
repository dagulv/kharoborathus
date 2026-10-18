<script lang="ts">
	import { checkSource, createSource } from '../../admin.remote';
	import type { ListingCandidate, SourceCheck } from '#lib/types.js';

	const { url, listing, rate, targetTags } = createSource.fields;

	let candidates = $state<ListingCandidate[] | null>(null);
	let wordpress = $state<SourceCheck['wordpress']>();
	let checkedURL = $state('');
	let checking = $state(false);
	let checkError = $state('');

	// Candidates belong to the url they were checked for.
	const checked = $derived(candidates !== null && url.value()?.trim() === checkedURL);

	const fmt = new Intl.DateTimeFormat('sv-SE', { dateStyle: 'short' });

	const key = (c: ListingCandidate) => `${c.adapter} ${c.url}`;

	async function check() {
		const value = url.value()?.trim() ?? '';

		checking = true;
		checkError = '';
		candidates = null;

		try {
			({ candidates, wordpress } = await checkSource(value));
			checkedURL = value;
			listing.set(key(candidates[0]));
		} catch (err) {
			checkError = (err as { body?: { message?: string } }).body?.message ?? 'Check failed';
		} finally {
			checking = false;
		}
	}
</script>

<svelte:head><title>New source · Admin</title></svelte:head>

<a class="text-sm text-blue-600 hover:underline" href="/admin/sources">← Sources</a>

<h1 class="mt-3 mb-4 text-xl font-semibold">New source</h1>

<form {...createSource} class="max-w-xl space-y-4 rounded border border-zinc-200 bg-white p-6">
	<label class="block">
		<span class="text-sm font-medium">URL</span>
		<div class="mt-1 flex gap-2">
			<input
				{...url.as('url')}
				required
				placeholder="https://example.com/"
				class="w-full rounded border border-zinc-300 px-2 py-1.5 text-sm aria-invalid:border-red-500"
			/>
			<button
				type="button"
				onclick={check}
				disabled={checking || !url.value()}
				class="rounded border border-zinc-300 px-3 py-1.5 text-sm hover:bg-zinc-100 disabled:opacity-50"
			>
				{checking ? 'Checking…' : 'Check'}
			</button>
		</div>
		<span class="mt-1 block text-xs text-zinc-500"
			>A site, a section or a feed. Check finds what it can be read as.</span
		>
		{#each url.issues() ?? [] as issue (issue.message)}
			<span class="mt-1 block text-sm text-red-600">{issue.message}</span>
		{/each}
		{#if checkError}<span class="mt-1 block text-sm text-red-600">{checkError}</span>{/if}
	</label>

	{#if checked && candidates}
		<fieldset class="space-y-2">
			<legend class="mb-1 text-sm font-medium">Read as</legend>
			{#each candidates as candidate (key(candidate))}
				<label class="flex items-start gap-2 text-sm">
					<input {...listing.as('radio', key(candidate))} class="mt-1" />
					<span class="min-w-0">
						{#if candidate.adapter === 'rss'}
							<span class="font-medium">Feed</span>
							{candidate.title}
							<span class="block truncate text-xs text-zinc-500">{candidate.url}</span>
							<span class="block text-xs text-zinc-500">
								{candidate.items ?? 0} items{candidate.latest
									? `, newest ${fmt.format(new Date(candidate.latest))}`
									: ''}
							</span>
						{:else}
							<span class="font-medium">Page</span>
							<span class="block truncate text-xs text-zinc-500">{candidate.url}</span>
							<span class="block text-xs text-zinc-500"
								>Article links found on the page by an LLM-written rule</span
							>
						{/if}
					</span>
				</label>
			{/each}
			{#each listing.issues() ?? [] as issue (issue.message)}
				<span class="block text-sm text-red-600">{issue.message}</span>
			{/each}
		</fieldset>
		{#if wordpress}
			<p class="text-sm text-zinc-600">
				WordPress site. All {wordpress.total} posts are backfilled through its REST API once the source
				is scraped.
			</p>
		{/if}
	{/if}

	<label class="block">
		<span class="text-sm font-medium">Rate (minutes)</span>
		<input
			{...rate.as('number', 15)}
			min="1"
			class="mt-1 w-32 rounded border border-zinc-300 px-2 py-1.5 text-sm aria-invalid:border-red-500"
		/>
		{#each rate.issues() ?? [] as issue (issue.message)}
			<span class="mt-1 block text-sm text-red-600">{issue.message}</span>
		{/each}
	</label>

	<label class="block">
		<span class="text-sm font-medium">Target tags</span>
		<input
			{...targetTags.as('text')}
			placeholder="finance, banking"
			class="mt-1 w-full rounded border border-zinc-300 px-2 py-1.5 text-sm"
		/>
		<span class="mt-1 block text-xs text-zinc-500">Comma separated</span>
	</label>

	{#each createSource.fields.issues() ?? [] as issue (issue.message)}
		<p class="text-sm text-red-600">{issue.message}</p>
	{/each}

	<button
		disabled={!checked || createSource.pending > 0}
		class="rounded bg-zinc-900 px-4 py-1.5 text-sm text-white disabled:opacity-50"
	>
		{createSource.pending > 0 ? 'Adding…' : 'Add source'}
	</button>
</form>
