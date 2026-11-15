<script lang="ts">
	import { goto, invalidateAll } from '$app/navigation';
	import { page } from '$app/state';
	import RefreshCwIcon from '@lucide/svelte/icons/refresh-cw';
	import { toast } from 'svelte-sonner';
	import { api, actionError } from '$lib/api.js';
	import { Badge } from '$lib/components/ui/badge/index.js';
	import { Button } from '$lib/components/ui/button/index.js';
	import * as Card from '$lib/components/ui/card/index.js';
	import * as Tabs from '$lib/components/ui/tabs/index.js';
	import StepGraph from '$lib/components/trace/step-graph.svelte';
	import { fmtDateTime, withParams } from '$lib/format.js';
	import { statusVariant } from '$lib/trace.js';
	import type { PageProps } from './$types';

	let { data }: PageProps = $props();

	const post = $derived(data.post);
	const tab = $derived(page.url.searchParams.get('tab') === 'logs' ? 'logs' : 'content');

	function selectTab(value: string) {
		const changes = { tab: value === 'logs' ? 'logs' : undefined };
		goto(withParams(page.url.searchParams, changes), { replace: true, reset: false });
	}

	// Set while a queued resync has not reached the worker, which turns the post pending.
	let queued = $state(false);

	async function resync(override: boolean) {
		try {
			await api(`/admin/posts/${encodeURIComponent(post.id)}/resync`, {
				method: 'POST',
				body: { override }
			});
		} catch (err) {
			toast.error(actionError(err, 'Resync failed'));
			return;
		}

		toast.success(override ? 'Resync queued, skipping checks' : 'Resync queued');
		queued = true;
		setTimeout(() => (queued = false), 10_000);
		await invalidateAll();
	}

	// A post in the pipeline refreshes until it settles, so its logs fill in.
	$effect(() => {
		if (!queued && post.status !== 'pending') return;

		const timer = setInterval(invalidateAll, 2000);
		return () => clearInterval(timer);
	});
</script>

<svelte:head><title>{post.title || post.url} · Admin</title></svelte:head>

<Card.Root>
	<Card.Content>
		<div class="flex flex-wrap items-start gap-2">
			<Badge variant={statusVariant(post.status)} class="mt-1">{post.status}</Badge>
			<h1 class="min-w-0 flex-1 text-lg font-semibold break-words">{post.title || post.url}</h1>
			<div class="flex gap-2">
				{#if post.status === 'rejected'}
					<Button
						variant="outline"
						size="sm"
						title="Fetch again, past the date window and quality check"
						onclick={() => resync(true)}
					>
						Resync, skip checks
					</Button>
				{/if}
				<Button
					variant="outline"
					size="sm"
					disabled={post.status === 'pending'}
					onclick={() => resync(false)}
				>
					<RefreshCwIcon class={[post.status === 'pending' && 'animate-spin']} />
					Resync
				</Button>
			</div>
		</div>
		<div class="mt-1 flex flex-wrap gap-x-3 text-xs text-muted-foreground">
			<a class="break-all hover:underline" href={post.url} target="_blank" rel="noreferrer">
				{post.url}
			</a>
			{#if post.postedAt}<span>posted {fmtDateTime(post.postedAt)}</span>{/if}
		</div>

		<Tabs.Root value={tab} onValueChange={selectTab} class="mt-4">
			<Tabs.List>
				<Tabs.Trigger value="content">Content</Tabs.Trigger>
				<Tabs.Trigger value="logs">Logs</Tabs.Trigger>
			</Tabs.List>

			<Tabs.Content value="content" class="pt-2">
				<dl class="grid grid-cols-[max-content_1fr] gap-x-4 gap-y-1">
					<dt class="text-muted-foreground">Source</dt>
					<dd>
						{#if post.sourceId}
							<a class="break-all hover:underline" href="/posts?source={post.sourceId}"
								>{post.sourceUrl}</a
							>
						{:else}
							<span class="text-muted-foreground">—</span>
						{/if}
					</dd>
					<dt class="text-muted-foreground">Language</dt>
					<dd>{post.lang || '—'}</dd>
					{#if post.authors.length}
						<dt class="text-muted-foreground">Authors</dt>
						<dd>{post.authors.join(', ')}</dd>
					{/if}
					<dt class="text-muted-foreground">Ingested</dt>
					<dd>{fmtDateTime(post.createdAt)}</dd>
					{#if post.tags.length}
						<dt class="text-muted-foreground">Tags</dt>
						<dd class="flex flex-wrap gap-1">
							{#each post.tags as tag (tag)}
								<Badge variant="outline" href="/posts?tag={encodeURIComponent(tag)}">{tag}</Badge>
							{/each}
						</dd>
					{/if}
				</dl>

				{#if post.shortContent}
					<section class="mt-6">
						<h2 class="mb-1 text-xs font-medium text-muted-foreground">Summary</h2>
						<p>{post.shortContent}</p>
					</section>
				{/if}

				<section class="mt-6">
					<h2 class="mb-1 text-xs font-medium text-muted-foreground">Content (markdown)</h2>
					{#if post.content}
						<pre
							class="overflow-x-auto rounded-md bg-muted p-4 text-sm whitespace-pre-wrap">{post.content}</pre>
					{:else}
						<p class="text-sm text-muted-foreground">No content was extracted.</p>
					{/if}
				</section>
			</Tabs.Content>

			<Tabs.Content value="logs" class="pt-2">
				<StepGraph trace={data.trace} status={post.status} />
			</Tabs.Content>
		</Tabs.Root>
	</Card.Content>
</Card.Root>
