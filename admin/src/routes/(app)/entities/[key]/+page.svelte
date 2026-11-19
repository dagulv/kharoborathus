<script lang="ts">
	import { goto, invalidate } from '$app/navigation';
	import { toast } from 'svelte-sonner';
	import { api, actionError } from '$lib/api.js';
	import { Badge } from '$lib/components/ui/badge/index.js';
	import { Button } from '$lib/components/ui/button/index.js';
	import { Input } from '$lib/components/ui/input/index.js';
	import * as Select from '$lib/components/ui/select/index.js';
	import * as Table from '$lib/components/ui/table/index.js';
	import { fmtDateTime } from '$lib/format.js';
	import { ENTITY_KINDS, type Entity, type EntityPage } from '$lib/types.js';
	import type { PageProps } from './$types';

	let { data }: PageProps = $props();

	const entity = $derived(data.entity);
	const tagKeys = $derived(data.tagKeys);
	const key = $derived(encodeURIComponent(entity.key));

	let name = $derived(entity.name);
	let kind = $derived<string>(entity.kind);
	let saving = $state(false);
	let tagsPending = $state(false);

	const dirty = $derived(name.trim() !== entity.name || kind !== entity.kind);

	async function save(e: SubmitEvent) {
		e.preventDefault();
		saving = true;
		try {
			await api(`/admin/entities/${key}`, { method: 'PATCH', body: { name: name.trim(), kind } });
			await invalidate('app:entity');
		} catch (err) {
			toast.error(actionError(err, 'Update failed'));
		} finally {
			saving = false;
		}
	}

	async function setTags(tags: string[]) {
		tagsPending = true;
		try {
			await api(`/admin/entities/${key}/tags`, { method: 'PUT', body: { tags } });
		} catch (err) {
			toast.error(actionError(err, 'Update failed'));
		}
		try {
			await invalidate('app:entity');
		} finally {
			tagsPending = false;
		}
	}

	let targetSearch = $state('');
	let candidates = $state<Entity[]>([]);
	let target = $state<Entity | undefined>();
	let merging = $state(false);
	let mergeError = $state('');
	let searchSeq = 0;
	let searchTimer: ReturnType<typeof setTimeout> | undefined;

	function searchTargets() {
		clearTimeout(searchTimer);
		const search = targetSearch.trim();
		if (!search) {
			searchSeq++;
			candidates = [];
			return;
		}
		searchTimer = setTimeout(async () => {
			const seq = ++searchSeq;
			try {
				const res = await api<EntityPage>('/admin/entities', { query: { search } });
				if (seq === searchSeq) candidates = res.entities.filter((c) => c.key !== entity.key);
			} catch (err) {
				if (seq === searchSeq) mergeError = actionError(err, 'Search failed');
			}
		}, 250);
	}

	async function merge(e: SubmitEvent) {
		e.preventDefault();
		if (!target) return;
		merging = true;
		mergeError = '';
		try {
			await api('/admin/entities/merge', {
				method: 'POST',
				body: { from: entity.key, to: target.key }
			});
			const to = target.key;
			target = undefined;
			targetSearch = '';
			candidates = [];
			await goto(`/entities/${encodeURIComponent(to)}`);
		} catch (err) {
			mergeError = actionError(err, 'Merge failed');
		} finally {
			merging = false;
		}
	}
</script>

<svelte:head><title>{entity.name} · Entities · Admin</title></svelte:head>

<div class="mb-4 flex flex-wrap items-center gap-3">
	<h1 class="text-lg font-semibold">{entity.name}</h1>
	<span class="text-sm text-muted-foreground">{entity.key}</span>
	<Badge variant="outline">{entity.status}</Badge>
	{#if entity.focus}<Badge>focus</Badge>{/if}
	{#if !entity.enabled}<Badge variant="secondary">disabled</Badge>{/if}
	<a class="ml-auto text-sm hover:underline" href="/posts?entity={key}">View posts →</a>
</div>

<form class="mb-6 flex flex-wrap items-end gap-2 text-sm" onsubmit={save}>
	<div>
		<label class="mb-1 block text-xs text-muted-foreground" for="entity-name">Name</label>
		<Input id="entity-name" bind:value={name} class="w-64" />
	</div>
	<div>
		<span class="mb-1 block text-xs text-muted-foreground">Kind</span>
		<Select.Root type="single" bind:value={kind}>
			<Select.Trigger size="sm" class="w-44" aria-label="Kind">{kind}</Select.Trigger>
			<Select.Content>
				{#each ENTITY_KINDS as k (k)}
					<Select.Item value={k} label={k} />
				{/each}
			</Select.Content>
		</Select.Root>
	</div>
	<Button type="submit" size="sm" disabled={!dirty || !name.trim() || saving}>Save</Button>
</form>

<dl class="mb-6 grid grid-cols-[auto_1fr] gap-x-6 gap-y-3 text-sm">
	<dt class="text-muted-foreground">Strength</dt>
	<dd class="tabular-nums">{entity.strength} posts in 30 days</dd>

	<dt class="text-muted-foreground">Support</dt>
	<dd class="tabular-nums">{entity.support} trusted posts in 30 days</dd>

	<dt class="text-muted-foreground">Tags</dt>
	<dd>
		<Select.Root type="multiple" value={entity.tags} onValueChange={setTags} disabled={tagsPending}>
			<Select.Trigger size="sm" class="w-72" aria-label="Tags of {entity.key}">
				<span class="truncate">{entity.tags.join(', ') || 'none'}</span>
			</Select.Trigger>
			<Select.Content>
				{#each tagKeys as tagKey (tagKey)}
					<Select.Item value={tagKey} label={tagKey} />
				{/each}
			</Select.Content>
		</Select.Root>
	</dd>

	<dt class="text-muted-foreground">Terms</dt>
	<dd>
		{#each Object.entries(entity.terms) as [lang, terms] (lang)}
			<div><span class="text-muted-foreground">{lang}</span> {terms.join(', ')}</div>
		{:else}
			<span class="text-muted-foreground">none</span>
		{/each}
	</dd>

	<dt class="text-muted-foreground">Aliases</dt>
	<dd>
		{#if entity.aliases.length}
			{entity.aliases.join(', ')}
		{:else}
			<span class="text-muted-foreground">none</span>
		{/if}
	</dd>
</dl>

<h2 class="mb-2 text-sm font-semibold">
	Queries <span class="text-muted-foreground">({entity.queries.length})</span>
</h2>

<div class="mb-6 rounded-lg border">
	<Table.Root>
		<Table.Header>
			<Table.Row>
				<Table.Head>Action</Table.Head>
				<Table.Head>Query</Table.Head>
				<Table.Head>Searched</Table.Head>
				<Table.Head class="text-right">Kept</Table.Head>
			</Table.Row>
		</Table.Header>
		<Table.Body>
			{#each entity.queries as q (q.id)}
				<Table.Row>
					<Table.Cell>{q.action}</Table.Cell>
					<Table.Cell class="whitespace-normal">{q.query}</Table.Cell>
					<Table.Cell>{q.searchedAt ? fmtDateTime(q.searchedAt) : 'pending'}</Table.Cell>
					<Table.Cell class="text-right tabular-nums">{q.result?.kept?.length ?? '–'}</Table.Cell>
				</Table.Row>
			{:else}
				<Table.Row>
					<Table.Cell colspan={4} class="py-6 text-center text-muted-foreground">
						No queries
					</Table.Cell>
				</Table.Row>
			{/each}
		</Table.Body>
	</Table.Root>
</div>

<h2 class="mb-2 text-sm font-semibold">Merge into another entity</h2>

<form class="flex flex-wrap items-start gap-2 text-sm" onsubmit={merge}>
	<div class="w-64">
		<Input
			bind:value={targetSearch}
			oninput={searchTargets}
			placeholder="Find target…"
			aria-label="Find merge target"
		/>
		{#if candidates.length}
			<ul class="mt-1 max-h-48 overflow-y-auto rounded-lg border">
				{#each candidates as c (c.key)}
					<li>
						<button
							type="button"
							class="block w-full px-3 py-1.5 text-left hover:bg-muted/50"
							onclick={() => (target = c)}
						>
							{c.name} <span class="text-xs text-muted-foreground">{c.key}</span>
						</button>
					</li>
				{/each}
			</ul>
		{/if}
	</div>
	{#if target}
		<Badge variant="secondary" class="mt-1.5">{target.key}</Badge>
	{/if}
	<Button type="submit" size="sm" variant="destructive" disabled={!target || merging}>
		Merge {entity.key} into {target?.key ?? '…'}
	</Button>
	{#if mergeError}<span class="mt-1.5 text-destructive">{mergeError}</span>{/if}
</form>
