<script lang="ts">
	import { invalidate } from '$app/navigation';
	import { toast } from 'svelte-sonner';
	import { api, actionError } from '$lib/api.js';
	import { Button } from '$lib/components/ui/button/index.js';
	import * as Select from '$lib/components/ui/select/index.js';
	import { Switch } from '$lib/components/ui/switch/index.js';
	import * as Table from '$lib/components/ui/table/index.js';
	import { fmtDate } from '$lib/format.js';
	import type { PageProps } from './$types';

	let { data }: PageProps = $props();

	const tags = $derived(data.overview.tags);
	const proposals = $derived(data.overview.proposals);
	const aliases = $derived(data.overview.aliases);

	let from = $state('');
	let to = $state('');
	let mergeError = $state('');

	async function merge(e: SubmitEvent) {
		e.preventDefault();
		mergeError = '';
		try {
			await api('/admin/tags/merge', { method: 'POST', body: { from, to } });
			from = '';
			to = '';
			await invalidate('app:tags');
		} catch (err) {
			mergeError = actionError(err, 'Merge failed');
		}
	}

	async function setEnabled(key: string, enabled: boolean) {
		try {
			await api(`/admin/tags/${encodeURIComponent(key)}`, { method: 'PATCH', body: { enabled } });
		} catch (err) {
			toast.error(actionError(err, 'Update failed'));
		}
		await invalidate('app:tags');
	}
</script>

<svelte:head><title>Tags · Admin</title></svelte:head>

<h1 class="mb-4 text-lg font-semibold">
	Tags <span class="text-muted-foreground">({tags.length})</span>
</h1>

<form class="mb-6 flex flex-wrap items-end gap-2 text-sm" onsubmit={merge}>
	<div>
		<span class="mb-1 block text-xs text-muted-foreground">Merge</span>
		<Select.Root type="single" bind:value={from}>
			<Select.Trigger size="sm" class="w-44">{from || 'tag'}</Select.Trigger>
			<Select.Content>
				{#each tags as tag (tag.key)}
					<Select.Item value={tag.key} label={tag.key} />
				{/each}
			</Select.Content>
		</Select.Root>
	</div>
	<div>
		<span class="mb-1 block text-xs text-muted-foreground">into</span>
		<Select.Root type="single" bind:value={to}>
			<Select.Trigger size="sm" class="w-44">{to || 'tag'}</Select.Trigger>
			<Select.Content>
				{#each tags as tag (tag.key)}
					<Select.Item value={tag.key} label={tag.key} />
				{/each}
			</Select.Content>
		</Select.Root>
	</div>
	<Button type="submit" size="sm" disabled={!from || !to || from === to}>Merge</Button>
	{#if mergeError}<span class="text-destructive">{mergeError}</span>{/if}
</form>

<div class="rounded-lg border">
	<Table.Root>
		<Table.Header>
			<Table.Row>
				<Table.Head>Key</Table.Head>
				<Table.Head>Terms</Table.Head>
				<Table.Head>Status</Table.Head>
				<Table.Head>Type</Table.Head>
				<Table.Head class="text-right">Posts</Table.Head>
				<Table.Head>Enabled</Table.Head>
			</Table.Row>
		</Table.Header>
		<Table.Body>
			{#each tags as tag (tag.key)}
				<Table.Row class={tag.enabled ? '' : 'opacity-50'}>
					<Table.Cell>
						<a class="hover:underline" href="/posts?tag={encodeURIComponent(tag.key)}">{tag.key}</a>
					</Table.Cell>
					<Table.Cell class="text-xs whitespace-normal text-muted-foreground">
						{tag.terms.join(', ')}
					</Table.Cell>
					<Table.Cell>{tag.status}</Table.Cell>
					<Table.Cell>{tag.type}</Table.Cell>
					<Table.Cell class="text-right tabular-nums">{tag.postCount}</Table.Cell>
					<Table.Cell>
						<Switch
							checked={tag.enabled}
							onCheckedChange={(enabled) => setEnabled(tag.key, enabled)}
							aria-label="Enable {tag.key}"
						/>
					</Table.Cell>
				</Table.Row>
			{/each}
		</Table.Body>
	</Table.Root>
</div>

<h2 class="mt-8 mb-2 text-sm font-semibold">
	Proposals <span class="text-muted-foreground">({proposals.length})</span>
</h2>

<div class="rounded-lg border">
	<Table.Root>
		<Table.Header>
			<Table.Row>
				<Table.Head>Key</Table.Head>
				<Table.Head>Terms</Table.Head>
				<Table.Head class="text-right">Posts</Table.Head>
				<Table.Head class="text-right">Domains</Table.Head>
				<Table.Head>Last proposed</Table.Head>
			</Table.Row>
		</Table.Header>
		<Table.Body>
			{#each proposals as proposal (proposal.key)}
				<Table.Row>
					<Table.Cell>{proposal.key}</Table.Cell>
					<Table.Cell class="text-xs whitespace-normal text-muted-foreground">
						{proposal.terms.join(', ')}
					</Table.Cell>
					<Table.Cell class="text-right tabular-nums">{proposal.postCount}</Table.Cell>
					<Table.Cell class="text-right tabular-nums">{proposal.domainCount}</Table.Cell>
					<Table.Cell>{fmtDate(proposal.lastProposedAt)}</Table.Cell>
				</Table.Row>
			{/each}
		</Table.Body>
	</Table.Root>
</div>

<h2 class="mt-8 mb-2 text-sm font-semibold">
	Aliases <span class="text-muted-foreground">({aliases.length})</span>
</h2>

<ul class="text-sm">
	{#each aliases as alias (alias.alias)}
		<li><span class="text-muted-foreground">{alias.alias}</span> → {alias.key}</li>
	{/each}
</ul>
