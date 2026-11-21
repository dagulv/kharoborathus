<script lang="ts">
	import { page } from '$app/state';
	import XIcon from '@lucide/svelte/icons/x';
	import { Badge } from '$lib/components/ui/badge/index.js';
	import { Button } from '$lib/components/ui/button/index.js';
	import { Input } from '$lib/components/ui/input/index.js';
	import { fmtDateTime, withParams } from '$lib/format.js';
	import { statusVariant } from '$lib/trace.js';
	import type { PostStatus } from '$lib/types.js';
	import type { LayoutProps } from './$types';

	let { data, children }: LayoutProps = $props();

	const filter = $derived(data.filter);
	const result = $derived(data.result);
	const pageCount = $derived(Math.max(1, Math.ceil(result.total / result.pageSize)));

	const chips: { label: string; status?: PostStatus }[] = [
		{ label: 'All' },
		{ label: 'Published', status: 'published' },
		{ label: 'Rejected', status: 'rejected' },
		{ label: 'Duplicate', status: 'duplicate' },
		{ label: 'Missing', status: 'missing' },
		{ label: 'Failed', status: 'failed' },
		{ label: 'Pending', status: 'pending' }
	];

	// List links stay on the list, so they drop the selected post's tab.
	const link = (changes: Record<string, string | undefined>) =>
		'/posts' + withParams(page.url.searchParams, { tab: undefined, ...changes });

	const postLink = (id: string) => `/posts/${id}` + withParams(page.url.searchParams, {});
</script>

<svelte:head><title>Posts · Admin</title></svelte:head>

<div class="mb-4 flex flex-wrap items-center justify-between gap-3">
	<h1 class="text-lg font-semibold">
		Posts <span class="text-muted-foreground">({result.total})</span>
	</h1>

	<form method="GET" action="/posts" class="flex gap-2">
		{#if filter.source}<input type="hidden" name="source" value={filter.source} />{/if}
		{#if filter.tag}<input type="hidden" name="tag" value={filter.tag} />{/if}
		{#if filter.category}<input type="hidden" name="category" value={filter.category} />{/if}
		{#if filter.entity}<input type="hidden" name="entity" value={filter.entity} />{/if}
		{#if filter.status}<input type="hidden" name="status" value={filter.status} />{/if}
		<Input name="search" value={filter.search ?? ''} placeholder="Search posts…" class="w-64" />
		<Button type="submit" size="sm">Search</Button>
	</form>
</div>

<div class="mb-3 flex flex-wrap gap-1.5">
	{#each chips as chip (chip.label)}
		<Badge
			variant={filter.status === chip.status ? 'default' : 'outline'}
			href={link({ status: chip.status, page: undefined })}
		>
			{chip.label}
		</Badge>
	{/each}
</div>

{#if filter.source || filter.tag || filter.category || filter.entity || filter.search}
	<div class="mb-3 flex flex-wrap gap-2">
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
		{#if filter.category}
			<Badge variant="secondary" href={link({ category: undefined, page: undefined })}>
				category: {filter.category}
				<XIcon />
			</Badge>
		{/if}
		{#if filter.entity}
			<Badge variant="secondary" href={link({ entity: undefined, page: undefined })}>
				entity: {filter.entity}
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

<div class="gap-4 lg:grid lg:grid-cols-[340px_minmax(0,1fr)] lg:items-start">
	<div>
		<ul class="divide-y rounded-lg border">
			{#each result.posts as post (post.id)}
				<li>
					<a
						class={[
							'block px-3 py-2.5 hover:bg-muted/50',
							page.params.id === post.id && 'bg-muted shadow-[inset_3px_0_0_var(--primary)]'
						]}
						href={postLink(post.id)}
					>
						<div class="line-clamp-2 font-medium break-words">{post.title || post.url}</div>
						<div class="mt-1 flex flex-wrap items-center gap-x-2 text-xs text-muted-foreground">
							<Badge variant={statusVariant(post.status)}>{post.status}</Badge>
							<span>{post.domainUrl.replace(/^https?:\/\//, '')}</span>
							{#if post.lang}<span>{post.lang}</span>{/if}
							<span>{fmtDateTime(post.createdAt)}</span>
						</div>
					</a>
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
	</div>

	<div class="mt-4 min-w-0 lg:mt-0">
		{@render children()}
	</div>
</div>
