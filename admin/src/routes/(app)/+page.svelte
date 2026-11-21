<script lang="ts">
	import * as Card from '$lib/components/ui/card/index.js';
	import type { PageProps } from './$types';

	let { data }: PageProps = $props();

	const stats = $derived(data.stats);

	const LINK_STATUSES = ['pending', 'accepted', 'rejected', 'duplicate', 'failed'];

	// Mirrors dailySearchBudget in internal/adapter/search/search.go
	const SEARCH_BUDGET = 322;
</script>

<svelte:head><title>Admin</title></svelte:head>

{#snippet stat(label: string, value: string | number)}
	<Card.Root class="gap-1 py-3">
		<Card.Content class="px-4">
			<div class="text-xs text-muted-foreground">{label}</div>
			<div class="text-xl font-semibold tabular-nums">{value}</div>
		</Card.Content>
	</Card.Root>
{/snippet}

<h1 class="mb-4 text-lg font-semibold">Overview</h1>

<div class="grid grid-cols-2 gap-3 sm:grid-cols-5">
	{@render stat('Sources', stats.sources)}
	{@render stat('Enabled sources', stats.enabledSources)}
	{@render stat('Flagged sources', stats.flaggedSources)}
	{@render stat('Posts', stats.posts)}
	{@render stat('Posts (24h)', stats.postsToday)}
</div>

<h2 class="mt-8 mb-2 text-sm font-medium">Discovered links</h2>

<div class="grid grid-cols-2 gap-3 sm:grid-cols-5">
	{#each LINK_STATUSES as status (status)}
		{@render stat(status[0].toUpperCase() + status.slice(1), stats.links[status] ?? 0)}
	{/each}
</div>

<h2 class="mt-8 mb-2 text-sm font-medium">Search discovery</h2>

<div class="grid grid-cols-2 gap-3 sm:grid-cols-5">
	{@render stat('Brave searches (24h)', `${stats.searchesToday} / ${SEARCH_BUDGET}`)}
</div>

<div class="mt-6 flex gap-4 text-sm">
	<a class="text-primary hover:underline" href="/sources">Browse sources →</a>
	<a class="text-primary hover:underline" href="/posts">Browse posts →</a>
</div>
