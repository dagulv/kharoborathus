<script lang="ts">
	import { page } from '$app/state';
	import { SvelteURLSearchParams } from 'svelte/reactivity';
	import { listPosts } from '../admin.remote';

	const params = $derived(page.url.searchParams);
	const filter = $derived({
		sourceId: params.get('source') || undefined,
		tag: params.get('tag') || undefined,
		search: params.get('search') || undefined,
		page: Math.max(1, Number(params.get('page')) || 1)
	});

	const result = $derived(await listPosts(filter));
	const pageCount = $derived(Math.max(1, Math.ceil(result.total / result.pageSize)));

	const fmt = new Intl.DateTimeFormat('sv-SE', { dateStyle: 'short', timeStyle: 'short' });

	function withParams(changes: Record<string, string | undefined>) {
		const next = new SvelteURLSearchParams(params.toString());
		for (const [key, value] of Object.entries(changes)) {
			if (value) next.set(key, value);
			else next.delete(key);
		}
		const qs = next.toString();
		return qs ? `?${qs}` : '?';
	}
</script>

<svelte:head><title>Posts · Admin</title></svelte:head>

<div class="mb-4 flex flex-wrap items-center justify-between gap-3">
	<h1 class="text-xl font-semibold">Posts <span class="text-zinc-400">({result.total})</span></h1>

	<form method="GET" class="flex gap-2">
		{#if filter.sourceId}<input type="hidden" name="source" value={filter.sourceId} />{/if}
		{#if filter.tag}<input type="hidden" name="tag" value={filter.tag} />{/if}
		<input
			name="search"
			value={filter.search ?? ''}
			placeholder="Search posts…"
			class="w-64 rounded border border-zinc-300 bg-white px-2 py-1 text-sm"
		/>
		<button class="rounded bg-zinc-900 px-3 py-1 text-sm text-white">Search</button>
	</form>
</div>

{#if filter.sourceId || filter.tag || filter.search}
	<div class="mb-4 flex flex-wrap gap-2 text-sm">
		{#if filter.sourceId}
			<a
				class="rounded bg-zinc-200 px-2 py-0.5"
				href={withParams({ source: undefined, page: undefined })}
				>source: {filter.sourceId.slice(0, 8)} ×</a
			>
		{/if}
		{#if filter.tag}
			<a
				class="rounded bg-zinc-200 px-2 py-0.5"
				href={withParams({ tag: undefined, page: undefined })}>tag: {filter.tag} ×</a
			>
		{/if}
		{#if filter.search}
			<a
				class="rounded bg-zinc-200 px-2 py-0.5"
				href={withParams({ search: undefined, page: undefined })}>search: {filter.search} ×</a
			>
		{/if}
	</div>
{/if}

<ul class="divide-y divide-zinc-100 rounded border border-zinc-200 bg-white">
	{#each result.posts as post (post.id)}
		<li class="px-4 py-3">
			<div class="flex items-baseline justify-between gap-4">
				<a class="font-medium hover:underline" href="/admin/posts/{post.id}">{post.title}</a>
				<span class="shrink-0 text-xs whitespace-nowrap text-zinc-500"
					>{fmt.format(post.postedAt)}</span
				>
			</div>
			<div class="mt-0.5 flex flex-wrap gap-x-3 text-xs text-zinc-500">
				<a class="hover:underline" href={withParams({ source: post.sourceId, page: undefined })}
					>{post.domainUrl}</a
				>
				<span>{post.lang}</span>
				{#if post.authors.length}<span>{post.authors.join(', ')}</span>{/if}
			</div>
			{#if post.shortContent}
				<p class="mt-1 line-clamp-2 text-sm text-zinc-700">{post.shortContent}</p>
			{/if}
			{#if post.tags.length}
				<div class="mt-1.5 flex flex-wrap gap-1">
					{#each post.tags as tag (tag)}
						<a
							class="rounded bg-zinc-100 px-1.5 py-0.5 text-xs hover:bg-zinc-200"
							href={withParams({ tag, page: undefined })}>{tag}</a
						>
					{/each}
				</div>
			{/if}
		</li>
	{:else}
		<li class="px-4 py-6 text-center text-sm text-zinc-500">No posts</li>
	{/each}
</ul>

{#if pageCount > 1}
	<nav class="mt-4 flex items-center justify-center gap-4 text-sm">
		{#if result.page > 1}
			<a class="text-blue-600 hover:underline" href={withParams({ page: String(result.page - 1) })}
				>← Prev</a
			>
		{/if}
		<span class="text-zinc-500">Page {result.page} of {pageCount}</span>
		{#if result.page < pageCount}
			<a class="text-blue-600 hover:underline" href={withParams({ page: String(result.page + 1) })}
				>Next →</a
			>
		{/if}
	</nav>
{/if}
