<script lang="ts">
	import { createSource } from '../../admin.remote';

	const { url, adapter, rate, enabled, targetTags } = createSource.fields;
</script>

<svelte:head><title>New source · Admin</title></svelte:head>

<a class="text-sm text-blue-600 hover:underline" href="/admin/sources">← Sources</a>

<h1 class="mt-3 mb-4 text-xl font-semibold">New source</h1>

<form {...createSource} class="max-w-xl space-y-4 rounded border border-zinc-200 bg-white p-6">
	<label class="block">
		<span class="text-sm font-medium">URL</span>
		<input
			{...url.as('url')}
			required
			placeholder="https://example.com/feed.xml"
			class="mt-1 w-full rounded border border-zinc-300 px-2 py-1.5 text-sm aria-invalid:border-red-500"
		/>
		{#each url.issues() ?? [] as issue (issue.message)}
			<span class="mt-1 block text-sm text-red-600">{issue.message}</span>
		{/each}
	</label>

	<div class="grid grid-cols-2 gap-4">
		<label class="block">
			<span class="text-sm font-medium">Adapter</span>
			<select
				{...adapter.as('select')}
				class="mt-1 w-full rounded border border-zinc-300 px-2 py-1.5 text-sm"
			>
				<option value="rss">rss</option>
			</select>
			{#each adapter.issues() ?? [] as issue (issue.message)}
				<span class="mt-1 block text-sm text-red-600">{issue.message}</span>
			{/each}
		</label>

		<label class="block">
			<span class="text-sm font-medium">Rate (minutes)</span>
			<input
				{...rate.as('number', 15)}
				min="1"
				class="mt-1 w-full rounded border border-zinc-300 px-2 py-1.5 text-sm aria-invalid:border-red-500"
			/>
			{#each rate.issues() ?? [] as issue (issue.message)}
				<span class="mt-1 block text-sm text-red-600">{issue.message}</span>
			{/each}
		</label>
	</div>

	<label class="block">
		<span class="text-sm font-medium">Target tags</span>
		<input
			{...targetTags.as('text')}
			placeholder="finance, banking"
			class="mt-1 w-full rounded border border-zinc-300 px-2 py-1.5 text-sm"
		/>
		<span class="mt-1 block text-xs text-zinc-500">Comma separated</span>
	</label>

	<label class="flex items-center gap-2">
		<input {...enabled.as('checkbox')} />
		<span class="text-sm">Enabled</span>
	</label>

	{#each createSource.fields.issues() ?? [] as issue (issue.message)}
		<p class="text-sm text-red-600">{issue.message}</p>
	{/each}

	<button
		disabled={createSource.pending > 0}
		class="rounded bg-zinc-900 px-4 py-1.5 text-sm text-white disabled:opacity-50"
	>
		{createSource.pending > 0 ? 'Adding…' : 'Add source'}
	</button>
</form>
