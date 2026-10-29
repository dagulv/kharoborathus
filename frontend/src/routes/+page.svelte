<script lang="ts">
	import type { Attachment } from 'svelte/attachments';
	import type { FeedPost } from '#lib/types.js';
	import type { PageProps } from './$types';

	let { data }: PageProps = $props();

	let more = $state.raw<FeedPost[]>([]);
	let loading = $state(false);
	let failed = $state(false);
	let done = $state(false);

	const posts = $derived([...data.posts, ...more]);

	const fmt = new Intl.DateTimeFormat('sv-SE', {
		dateStyle: 'short',
		timeStyle: 'short',
		timeZone: 'Europe/Stockholm'
	});

	async function loadMore() {
		const last = posts.at(-1);
		if (loading || done || !last) return;

		loading = true;
		failed = false;

		try {
			const res = await fetch(`/posts?after=${last.id}`);
			if (!res.ok) throw new Error(`status ${res.status}`);

			const page: FeedPost[] = await res.json();
			if (page.length === 0) done = true;
			more = [...more, ...page];
		} catch {
			failed = true;
		} finally {
			loading = false;
		}
	}

	const nearEnd: Attachment = (node) => {
		const observer = new IntersectionObserver(
			([entry]) => {
				if (entry.isIntersecting) loadMore();
			},
			{ rootMargin: '400px' }
		);
		observer.observe(node);

		return () => observer.disconnect();
	};
</script>

<svelte:head><title>Kharoborathus</title></svelte:head>

<main class="mx-auto max-w-2xl px-4 py-6">
	<h1 class="mb-4 text-xl font-semibold">Kharoborathus</h1>

	<ul class="divide-y divide-zinc-100">
		{#each posts as post (post.id)}
			<li class="py-3">
				<a class="font-medium hover:underline" href={post.url}>{post.title}</a>
				<div class="mt-0.5 text-xs text-zinc-500">
					{post.domainUrl.replace(/^https?:\/\//, '')} · {fmt.format(new Date(post.postedAt))}
				</div>
				{#if post.shortContent}
					<p class="mt-1 text-sm text-zinc-700">{post.shortContent}</p>
				{/if}
			</li>
		{:else}
			<li class="py-6 text-center text-sm text-zinc-500">No posts</li>
		{/each}
	</ul>

	{#if failed}
		<button class="mt-4 w-full text-sm text-blue-600 hover:underline" onclick={loadMore}
			>Could not load more posts. Retry</button
		>
	{:else if !done && posts.length}
		{#key posts.length}
			<div {@attach nearEnd}></div>
		{/key}
		{#if loading}<p class="mt-4 text-center text-sm text-zinc-500">Loading…</p>{/if}
	{/if}
</main>
