<script lang="ts">
	import { getStats } from './admin.remote';

	const stats = $derived(await getStats());
</script>

<svelte:head><title>Admin</title></svelte:head>

<h1 class="mb-4 text-xl font-semibold">Overview</h1>

<div class="grid grid-cols-2 gap-4 sm:grid-cols-5">
	{#each [['Sources', stats.sources], ['Enabled sources', stats.enabledSources], ['Flagged sources', stats.flaggedSources], ['Posts', stats.posts], ['Posts (24h)', stats.postsToday]] as [label, value] (label)}
		<div class="rounded border border-zinc-200 bg-white p-4">
			<div class="text-sm text-zinc-500">{label}</div>
			<div class="text-2xl font-semibold tabular-nums">{value}</div>
		</div>
	{/each}
</div>

<div class="mt-6 flex gap-4 text-sm">
	<a class="text-blue-600 hover:underline" href="/admin/sources">Browse sources →</a>
	<a class="text-blue-600 hover:underline" href="/admin/posts">Browse posts →</a>
</div>
