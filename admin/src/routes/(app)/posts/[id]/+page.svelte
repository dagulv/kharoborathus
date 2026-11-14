<script lang="ts">
	import { Badge } from '$lib/components/ui/badge/index.js';
	import * as Card from '$lib/components/ui/card/index.js';
	import { fmtDateTime } from '$lib/format.js';
	import type { PageProps } from './$types';

	let { data }: PageProps = $props();

	const post = $derived(data.post);
</script>

<svelte:head><title>{post.title} · Admin</title></svelte:head>

<a class="text-sm text-muted-foreground hover:text-foreground" href="/posts">← Posts</a>

<Card.Root class="mt-3">
	<Card.Content>
		<h1 class="text-xl font-semibold">{post.title}</h1>

		<dl class="mt-3 grid grid-cols-[max-content_1fr] gap-x-4 gap-y-1">
			<dt class="text-muted-foreground">URL</dt>
			<dd>
				<a
					class="break-all text-primary hover:underline"
					href={post.url}
					target="_blank"
					rel="noreferrer"
				>
					{post.url}
				</a>
			</dd>
			<dt class="text-muted-foreground">Source</dt>
			<dd>
				<a class="break-all hover:underline" href="/posts?source={post.sourceId}"
					>{post.sourceUrl}</a
				>
			</dd>
			<dt class="text-muted-foreground">Language</dt>
			<dd>{post.lang}</dd>
			{#if post.authors.length}
				<dt class="text-muted-foreground">Authors</dt>
				<dd>{post.authors.join(', ')}</dd>
			{/if}
			<dt class="text-muted-foreground">Posted</dt>
			<dd>{post.postedAt ? fmtDateTime(post.postedAt) : '—'}</dd>
			<dt class="text-muted-foreground">Ingested</dt>
			<dd>{fmtDateTime(post.createdAt)}</dd>
			<dt class="text-muted-foreground">Tags</dt>
			<dd class="flex flex-wrap gap-1">
				{#each post.tags as tag (tag)}
					<Badge variant="outline" href="/posts?tag={encodeURIComponent(tag)}">{tag}</Badge>
				{:else}
					<span class="text-muted-foreground">—</span>
				{/each}
			</dd>
		</dl>

		{#if post.shortContent}
			<section class="mt-6">
				<h2 class="mb-1 text-xs font-medium text-muted-foreground">Summary</h2>
				<p>{post.shortContent}</p>
			</section>
		{/if}

		<section class="mt-6">
			<h2 class="mb-1 text-xs font-medium text-muted-foreground">Content (markdown)</h2>
			<pre
				class="overflow-x-auto rounded-md bg-muted p-4 text-sm whitespace-pre-wrap">{post.content}</pre>
		</section>
	</Card.Content>
</Card.Root>
