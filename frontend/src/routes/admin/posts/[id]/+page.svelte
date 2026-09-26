<script lang="ts">
	import { page } from '$app/state';
	import { getPost } from '../../admin.remote';

	const post = $derived(await getPost(page.params.id!));

	const fmt = new Intl.DateTimeFormat('sv-SE', { dateStyle: 'short', timeStyle: 'short' });
</script>

<svelte:head><title>{post.title} · Admin</title></svelte:head>

<a class="text-sm text-blue-600 hover:underline" href="/admin/posts">← Posts</a>

<article class="mt-3 rounded border border-zinc-200 bg-white p-6">
	<h1 class="text-2xl font-semibold">{post.title}</h1>

	<dl class="mt-3 grid grid-cols-[max-content_1fr] gap-x-4 gap-y-1 text-sm">
		<dt class="text-zinc-500">URL</dt>
		<dd>
			<a
				class="break-all text-blue-600 hover:underline"
				href={post.url}
				target="_blank"
				rel="noreferrer">{post.url}</a
			>
		</dd>
		<dt class="text-zinc-500">Source</dt>
		<dd>
			<a class="break-all hover:underline" href="/admin/posts?source={post.sourceId}"
				>{post.sourceUrl}</a
			>
		</dd>
		<dt class="text-zinc-500">Language</dt>
		<dd>{post.lang}</dd>
		{#if post.authors.length}
			<dt class="text-zinc-500">Authors</dt>
			<dd>{post.authors.join(', ')}</dd>
		{/if}
		<dt class="text-zinc-500">Posted</dt>
		<dd>{fmt.format(post.postedAt)}</dd>
		<dt class="text-zinc-500">Ingested</dt>
		<dd>{fmt.format(post.createdAt)}</dd>
		<dt class="text-zinc-500">Tags</dt>
		<dd class="flex flex-wrap gap-1">
			{#each post.tags as tag (tag)}
				<a
					class="rounded bg-zinc-100 px-1.5 py-0.5 text-xs hover:bg-zinc-200"
					href="/admin/posts?tag={encodeURIComponent(tag)}">{tag}</a
				>
			{:else}
				<span class="text-zinc-400">—</span>
			{/each}
		</dd>
	</dl>

	{#if post.shortContent}
		<section class="mt-6">
			<h2 class="mb-1 text-sm font-medium text-zinc-500">Summary</h2>
			<p class="text-zinc-800">{post.shortContent}</p>
		</section>
	{/if}

	<section class="mt-6">
		<h2 class="mb-1 text-sm font-medium text-zinc-500">Content (markdown)</h2>
		<pre
			class="overflow-x-auto rounded bg-zinc-50 p-4 text-sm whitespace-pre-wrap">{post.content}</pre>
	</section>
</article>
