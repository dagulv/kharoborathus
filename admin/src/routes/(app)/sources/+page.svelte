<script lang="ts">
	import { invalidate } from '$app/navigation';
	import { SvelteMap } from 'svelte/reactivity';
	import { toast } from 'svelte-sonner';
	import { api, actionError } from '$lib/api.js';
	import { Badge } from '$lib/components/ui/badge/index.js';
	import { Button } from '$lib/components/ui/button/index.js';
	import * as Card from '$lib/components/ui/card/index.js';
	import * as Table from '$lib/components/ui/table/index.js';
	import { fmtDateTime } from '$lib/format.js';
	import type { Source } from '$lib/types.js';
	import type { PageProps } from './$types';

	let { data }: PageProps = $props();

	const sources = $derived(data.sources);

	const syncState = new SvelteMap<string, 'pending' | 'requested' | 'failed'>();

	async function sync(id: string) {
		syncState.set(id, 'pending');
		try {
			await api(`/admin/sources/${id}/sync`, { method: 'POST' });
			syncState.set(id, 'requested');
		} catch (err) {
			syncState.set(id, 'failed');
			toast.error(actionError(err, 'Sync failed'));
		}
		setTimeout(() => syncState.delete(id), 3000);
	}

	// A disabled flagged source keeps its flag but has already been reviewed.
	const flagged = $derived(
		sources.filter((s) => s.status === 'flagged' && s.enabled && !s.deletedAt)
	);

	const reviewing = new SvelteMap<string, boolean>();

	async function review(source: Source, enabled: boolean) {
		reviewing.set(source.id, true);
		try {
			await api(`/admin/sources/${source.id}/review`, { method: 'POST', body: { enabled } });
			await invalidate('app:sources');
		} catch (err) {
			toast.error(actionError(err, 'Review failed'));
		} finally {
			reviewing.delete(source.id);
		}
	}

	const syncLabel = { pending: 'Syncing…', requested: 'Requested', failed: 'Failed' };
</script>

<svelte:head><title>Sources · Admin</title></svelte:head>

<div class="mb-4 flex items-center justify-between">
	<h1 class="text-lg font-semibold">
		Sources <span class="text-muted-foreground">({sources.length})</span>
	</h1>
	<div class="flex items-center gap-3">
		<Button variant="ghost" size="sm" href={data.includeDeleted ? '?' : '?deleted'}>
			{data.includeDeleted ? 'Hide deleted' : 'Show deleted'}
		</Button>
		<Button size="sm" href="/sources/new">Add source</Button>
	</div>
</div>

{#if flagged.length}
	<Card.Root class="mb-6 gap-2 border-amber-500/40 bg-amber-500/5 py-4">
		<Card.Header class="px-4">
			<Card.Title class="text-sm">
				Flagged <span class="text-muted-foreground">({flagged.length})</span>
			</Card.Title>
			<Card.Description>Keep makes a source active again. Disable stops it.</Card.Description>
		</Card.Header>
		<Card.Content class="space-y-1.5 px-4">
			{#each flagged as source (source.id)}
				<div class="flex flex-wrap items-center gap-2 text-sm">
					<a
						class="min-w-0 flex-1 truncate hover:underline"
						href="/posts?source={source.id}"
						title={source.url}
					>
						{source.url}
					</a>
					<Button
						variant="outline"
						size="xs"
						disabled={reviewing.get(source.id)}
						onclick={() => review(source, true)}
					>
						Keep
					</Button>
					<Button
						variant="destructive"
						size="xs"
						disabled={reviewing.get(source.id)}
						onclick={() => review(source, false)}
					>
						Disable
					</Button>
				</div>
			{/each}
		</Card.Content>
	</Card.Root>
{/if}

<div class="rounded-lg border">
	<Table.Root>
		<Table.Header>
			<Table.Row>
				<Table.Head>URL</Table.Head>
				<Table.Head>Adapter</Table.Head>
				<Table.Head>Status</Table.Head>
				<Table.Head class="text-right">Rate</Table.Head>
				<Table.Head class="text-right">Posts</Table.Head>
				<Table.Head>Last post</Table.Head>
				<Table.Head>Created</Table.Head>
				<Table.Head></Table.Head>
			</Table.Row>
		</Table.Header>
		<Table.Body>
			{#each sources as source (source.id)}
				<Table.Row class={source.deletedAt ? 'opacity-50' : ''}>
					<Table.Cell class="max-w-sm">
						<div class="text-xs text-muted-foreground">{source.domainUrl}</div>
						<a
							class="block truncate hover:underline"
							href={source.url}
							target="_blank"
							rel="noreferrer"
							title={source.url}
						>
							{source.url}
						</a>
					</Table.Cell>
					<Table.Cell>{source.adapter}</Table.Cell>
					<Table.Cell>
						<div class="flex gap-1">
							{#if source.deletedAt}
								<Badge variant="destructive">deleted</Badge>
							{:else if source.enabled}
								<Badge variant="secondary" class="text-emerald-600 dark:text-emerald-400">
									enabled
								</Badge>
							{:else}
								<Badge variant="outline">disabled</Badge>
							{/if}
							{#if source.status !== 'active'}
								<Badge variant="outline" class="text-primary">{source.status}</Badge>
							{/if}
						</div>
					</Table.Cell>
					<Table.Cell class="text-right tabular-nums">{source.rate}m</Table.Cell>
					<Table.Cell class="text-right tabular-nums">
						<a class="text-primary hover:underline" href="/posts?source={source.id}">
							{source.postCount}
						</a>
					</Table.Cell>
					<Table.Cell class="whitespace-nowrap text-muted-foreground">
						{source.lastPostedAt ? fmtDateTime(source.lastPostedAt) : '—'}
					</Table.Cell>
					<Table.Cell class="whitespace-nowrap text-muted-foreground">
						{fmtDateTime(source.createdAt)}
					</Table.Cell>
					<Table.Cell class="text-right">
						{#if !source.deletedAt && source.enabled && source.status === 'active'}
							{@const status = syncState.get(source.id)}
							<Button
								variant={status === 'failed' ? 'destructive' : 'outline'}
								size="xs"
								onclick={() => sync(source.id)}
								disabled={status === 'pending'}
								title="Run the pipeline now and restart the {source.rate}m wait"
							>
								{status ? syncLabel[status] : 'Sync'}
							</Button>
						{/if}
					</Table.Cell>
				</Table.Row>
			{:else}
				<Table.Row>
					<Table.Cell colspan={8} class="py-6 text-center text-muted-foreground">
						No sources
					</Table.Cell>
				</Table.Row>
			{/each}
		</Table.Body>
	</Table.Root>
</div>
