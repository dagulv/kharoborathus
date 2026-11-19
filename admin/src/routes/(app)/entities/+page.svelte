<script lang="ts">
	import { invalidate } from '$app/navigation';
	import { page } from '$app/state';
	import { toast } from 'svelte-sonner';
	import { api, actionError } from '$lib/api.js';
	import { Badge } from '$lib/components/ui/badge/index.js';
	import { Button } from '$lib/components/ui/button/index.js';
	import { Input } from '$lib/components/ui/input/index.js';
	import { Switch } from '$lib/components/ui/switch/index.js';
	import * as Table from '$lib/components/ui/table/index.js';
	import { withParams } from '$lib/format.js';
	import { ENTITY_KINDS } from '$lib/types.js';
	import type { PageProps } from './$types';

	let { data }: PageProps = $props();

	const filter = $derived(data.filter);
	const result = $derived(data.result);
	const pageCount = $derived(Math.max(1, Math.ceil(result.total / result.pageSize)));

	const link = (changes: Record<string, string | undefined>) =>
		'/entities' + withParams(page.url.searchParams, changes);

	async function patch(key: string, body: { enabled: boolean } | { focus: boolean }) {
		try {
			await api(`/admin/entities/${encodeURIComponent(key)}`, { method: 'PATCH', body });
		} catch (err) {
			toast.error(actionError(err, 'Update failed'));
		}
		await invalidate('app:entities');
	}
</script>

<svelte:head><title>Entities · Admin</title></svelte:head>

<div class="mb-4 flex flex-wrap items-center justify-between gap-3">
	<h1 class="text-lg font-semibold">
		Entities <span class="text-muted-foreground">({result.total})</span>
	</h1>

	<form method="GET" action="/entities" class="flex gap-2">
		{#if filter.kind}<input type="hidden" name="kind" value={filter.kind} />{/if}
		{#if filter.status}<input type="hidden" name="status" value={filter.status} />{/if}
		{#if filter.focus}<input type="hidden" name="focus" value={filter.focus} />{/if}
		<Input name="search" value={filter.search ?? ''} placeholder="Search entities…" class="w-64" />
		<Button type="submit" size="sm">Search</Button>
	</form>
</div>

<div class="mb-3 flex flex-wrap gap-1.5">
	<Badge
		variant={filter.kind ? 'outline' : 'default'}
		href={link({ kind: undefined, page: undefined })}
	>
		All kinds
	</Badge>
	{#each ENTITY_KINDS as kind (kind)}
		<Badge
			variant={filter.kind === kind ? 'default' : 'outline'}
			href={link({ kind, page: undefined })}
		>
			{kind}
		</Badge>
	{/each}
</div>

<div class="mb-3 flex flex-wrap gap-1.5">
	<Badge
		variant={filter.status ? 'outline' : 'default'}
		href={link({ status: undefined, page: undefined })}
	>
		Any status
	</Badge>
	{#each ['active', 'proposed'] as status (status)}
		<Badge
			variant={filter.status === status ? 'default' : 'outline'}
			href={link({ status, page: undefined })}
		>
			{status}
		</Badge>
	{/each}
	<Badge
		variant={filter.focus ? 'default' : 'outline'}
		href={link({ focus: filter.focus ? undefined : 'true', page: undefined })}
	>
		Focus only
	</Badge>
</div>

<div class="rounded-lg border">
	<Table.Root>
		<Table.Header>
			<Table.Row>
				<Table.Head>Name</Table.Head>
				<Table.Head>Key</Table.Head>
				<Table.Head>Kind</Table.Head>
				<Table.Head>Status</Table.Head>
				<Table.Head class="text-right">Strength</Table.Head>
				<Table.Head>Focus</Table.Head>
				<Table.Head>Enabled</Table.Head>
			</Table.Row>
		</Table.Header>
		<Table.Body>
			{#each result.entities as entity (entity.key)}
				<Table.Row class={entity.enabled ? '' : 'opacity-50'}>
					<Table.Cell>
						<a class="hover:underline" href="/entities/{encodeURIComponent(entity.key)}">
							{entity.name}
						</a>
					</Table.Cell>
					<Table.Cell class="text-xs text-muted-foreground">{entity.key}</Table.Cell>
					<Table.Cell>{entity.kind}</Table.Cell>
					<Table.Cell>{entity.status}</Table.Cell>
					<Table.Cell class="text-right tabular-nums">{entity.strength}</Table.Cell>
					<Table.Cell>
						<Switch
							checked={entity.focus}
							onCheckedChange={(focus) => patch(entity.key, { focus })}
							aria-label="Focus {entity.key}"
						/>
					</Table.Cell>
					<Table.Cell>
						<Switch
							checked={entity.enabled}
							onCheckedChange={(enabled) => patch(entity.key, { enabled })}
							aria-label="Enable {entity.key}"
						/>
					</Table.Cell>
				</Table.Row>
			{:else}
				<Table.Row>
					<Table.Cell colspan={7} class="py-6 text-center text-muted-foreground">
						No entities
					</Table.Cell>
				</Table.Row>
			{/each}
		</Table.Body>
	</Table.Root>
</div>

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
