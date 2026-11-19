<script lang="ts">
	import { invalidate } from '$app/navigation';
	import { toast } from 'svelte-sonner';
	import { api, actionError } from '$lib/api.js';
	import { Button } from '$lib/components/ui/button/index.js';
	import { Input } from '$lib/components/ui/input/index.js';
	import { Switch } from '$lib/components/ui/switch/index.js';
	import * as Table from '$lib/components/ui/table/index.js';
	import type { PageProps } from './$types';

	let { data }: PageProps = $props();

	const categories = $derived(data.categories);

	let key = $state('');
	let sv = $state('');
	let en = $state('');
	let createError = $state('');

	async function create(e: SubmitEvent) {
		e.preventDefault();
		createError = '';
		try {
			await api('/admin/categories', { method: 'POST', body: { key, sv, en } });
			key = '';
			sv = '';
			en = '';
			await invalidate('app:categories');
		} catch (err) {
			createError = actionError(err, 'Create failed');
		}
	}

	async function setEnabled(categoryKey: string, enabled: boolean) {
		try {
			await api(`/admin/categories/${encodeURIComponent(categoryKey)}`, {
				method: 'PATCH',
				body: { enabled }
			});
		} catch (err) {
			toast.error(actionError(err, 'Update failed'));
		}
		await invalidate('app:categories');
	}
</script>

<svelte:head><title>Categories · Admin</title></svelte:head>

<h1 class="mb-4 text-lg font-semibold">
	Categories <span class="text-muted-foreground">({categories.length})</span>
</h1>

<form class="mb-6 flex flex-wrap items-end gap-2 text-sm" onsubmit={create}>
	<div>
		<label class="mb-1 block text-xs text-muted-foreground" for="category-key">Key</label>
		<Input id="category-key" bind:value={key} class="w-44" />
	</div>
	<div>
		<label class="mb-1 block text-xs text-muted-foreground" for="category-sv">Swedish</label>
		<Input id="category-sv" bind:value={sv} class="w-44" />
	</div>
	<div>
		<label class="mb-1 block text-xs text-muted-foreground" for="category-en">English</label>
		<Input id="category-en" bind:value={en} class="w-44" />
	</div>
	<Button type="submit" size="sm" disabled={!key.trim() || !sv.trim() || !en.trim()}>Create</Button>
	{#if createError}<span class="text-destructive">{createError}</span>{/if}
</form>

<div class="rounded-lg border">
	<Table.Root>
		<Table.Header>
			<Table.Row>
				<Table.Head>Key</Table.Head>
				<Table.Head>Swedish</Table.Head>
				<Table.Head>English</Table.Head>
				<Table.Head class="text-right">Tags</Table.Head>
				<Table.Head class="text-right">Posts</Table.Head>
				<Table.Head>Enabled</Table.Head>
			</Table.Row>
		</Table.Header>
		<Table.Body>
			{#each categories as category (category.key)}
				<Table.Row class={category.enabled ? '' : 'opacity-50'}>
					<Table.Cell>
						<a class="hover:underline" href="/posts?category={encodeURIComponent(category.key)}">
							{category.key}
						</a>
					</Table.Cell>
					<Table.Cell>{category.terms.sv}</Table.Cell>
					<Table.Cell>{category.terms.en}</Table.Cell>
					<Table.Cell class="text-right tabular-nums">
						<a class="hover:underline" href="/tags?category={encodeURIComponent(category.key)}">
							{category.tagCount}
						</a>
					</Table.Cell>
					<Table.Cell class="text-right tabular-nums">{category.postCount}</Table.Cell>
					<Table.Cell>
						<Switch
							checked={category.enabled}
							onCheckedChange={(enabled) => setEnabled(category.key, enabled)}
							aria-label="Enable {category.key}"
						/>
					</Table.Cell>
				</Table.Row>
			{:else}
				<Table.Row>
					<Table.Cell colspan={6} class="py-6 text-center text-muted-foreground">
						No categories
					</Table.Cell>
				</Table.Row>
			{/each}
		</Table.Body>
	</Table.Root>
</div>
