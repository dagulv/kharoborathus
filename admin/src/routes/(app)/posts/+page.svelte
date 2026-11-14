<script lang="ts">
	import { page } from '$app/state';
	import XIcon from '@lucide/svelte/icons/x';
	import { Badge } from '$lib/components/ui/badge/index.js';
	import { Button } from '$lib/components/ui/button/index.js';
	import { Input } from '$lib/components/ui/input/index.js';
	import { fmtDateTime, withParams } from '$lib/format.js';
	import type { PageProps } from './$types';

	let { data }: PageProps = $props();

	const filter = $derived(data.filter);
	const result = $derived(data.result);
	const pageCount = $derived(Math.max(1, Math.ceil(result.total / result.pageSize)));

	const link = (changes: Record<string, string | undefined>) =>
		withParams(page.url.searchParams, changes);
</script>

<svelte:head><title>Posts · Admin</title></svelte:head>

<div class="mb-4 flex flex-wrap items-center justify-between gap-3">
	<h1 class="text-lg font-semibold">
		Posts <span class="text-muted-foreground">({result.total})</span>
	</h1>

	<form method="GET" class="flex gap-2">
		{#if filter.source}<input type="hidden" name="source" value={filter.source} />{/if}
		{#if filter.tag}<input type="hidden" name="tag" value={filter.tag} />{/if}
		<Input name="search" value={filter.search ?? ''} placeholder="Search posts…" class="w-64" />
		<Button type="submit" size="sm">Search</Button>
	</form>
</div>

{#if filter.source || filter.tag || filter.search}
	<div class="mb-4 flex flex-wrap gap-2">
		{#if filter.source}
			<Badge variant="secondary" href={link({ source: undefined, page: undefined })}>
				source: {filter.source.slice(0, 8)}
				<XIcon />
			</Badge>
		{/if}
		{#if filter.tag}
			<Badge variant="secondary" href={link({ tag: undefined, page: undefined })}>
				tag: {filter.tag}
				<XIcon />
			</Badge>
		{/if}
		{#if filter.search}
			<Badge variant="secondary" href={link({ search: undefined, page: undefined })}>
				search: {filter.search}
				<XIcon />
			</Badge>
		{/if}
	</div>
{/if}

<ul class="divide-y rounded-lg border">
	{#each result.posts as post (post.id)}
		<li class="px-4 py-3">
			<div class="flex items-baseline justify-between gap-4">
				<a class="font-medium hover:underline" href="/posts/{post.id}">{post.title}</a>
				<span class="shrink-0 text-xs whitespace-nowrap text-muted-foreground">
					{post.postedAt ? fmtDateTime(post.postedAt) : '—'}
				</span>
			</div>
			<div class="mt-0.5 flex flex-wrap gap-x-3 text-xs text-muted-foreground">
				<a class="hover:underline" href={link({ source: post.sourceId, page: undefined })}>
					{post.domainUrl}
				</a>
				<span>{post.lang}</span>
				{#if post.authors.length}<span>{post.authors.join(', ')}</span>{/if}
			</div>
			{#if post.shortContent}
				<p class="mt-1 line-clamp-2 text-foreground/80">{post.shortContent}</p>
			{/if}
			{#if post.tags.length}
				<div class="mt-1.5 flex flex-wrap gap-1">
					{#each post.tags as tag (tag)}
						<Badge variant="outline" href={link({ tag, page: undefined })}>{tag}</Badge>
					{/each}
				</div>
			{/if}
		</li>
	{:else}
		<li class="px-4 py-6 text-center text-muted-foreground">No posts</li>
	{/each}
</ul>

{#if pageCount > 1}
	<nav class="mt-4 flex items-center justify-center gap-4 text-sm">
		{#if result.page > 1}
			<Button variant="ghost" size="sm" href={link({ page: String(result.page - 1) })}
				>← Prev</Button
			>
		{/if}
		<span class="text-muted-foreground">Page {result.page} of {pageCount}</span>
		{#if result.page < pageCount}
			<Button variant="ghost" size="sm" href={link({ page: String(result.page + 1) })}
				>Next →</Button
			>
		{/if}
	</nav>
{/if}
