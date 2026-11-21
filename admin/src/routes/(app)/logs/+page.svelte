<script lang="ts">
	import { page } from '$app/state';
	import { Badge } from '$lib/components/ui/badge/index.js';
	import { Button } from '$lib/components/ui/button/index.js';
	import { Input } from '$lib/components/ui/input/index.js';
	import * as Table from '$lib/components/ui/table/index.js';
	import { fmtDateTimeSeconds, withParams } from '$lib/format.js';
	import { cn } from '$lib/utils.js';
	import { LOG_TYPES } from '$lib/types.js';
	import type { PageProps } from './$types';

	let { data }: PageProps = $props();

	const filter = $derived(data.filter);
	const result = $derived(data.result);

	const link = (changes: Record<string, string | undefined>) =>
		withParams(page.url.searchParams, changes);

	// Grouped metadata shows as dotted keys, but only top-level keys can be filtered on.
	function flatten(meta: Record<string, unknown>, prefix = ''): [string, string, boolean][] {
		return Object.entries(meta).flatMap(([key, value]) =>
			value && typeof value === 'object'
				? flatten(value as Record<string, unknown>, `${prefix}${key}.`)
				: [[`${prefix}${key}`, String(value), prefix === '']]
		);
	}

	const levelClass: Record<string, string> = {
		INFO: '',
		WARN: 'border-amber-500/40 text-amber-600 dark:text-amber-400',
		ERROR: 'border-destructive/40 text-destructive'
	};

	// Native selects, so the filter form submits as a plain GET.
	const selectClass =
		'h-8 rounded-lg border border-input bg-transparent px-2 text-sm dark:bg-input/30';
</script>

<svelte:head><title>Logs · Admin</title></svelte:head>

<div class="mb-4 flex flex-wrap items-center justify-between gap-3">
	<h1 class="text-lg font-semibold">Logs</h1>

	<form method="GET" class="flex flex-wrap items-center gap-2">
		<select name="type" value={filter.type ?? ''} class={selectClass}>
			<option value="">All types</option>
			{#each LOG_TYPES as type (type)}<option value={type}>{type}</option>{/each}
		</select>
		<select name="level" value={filter.level ?? ''} class={selectClass}>
			<option value="">All levels</option>
			<option value="INFO">INFO</option>
			<option value="WARN">WARN</option>
			<option value="ERROR">ERROR</option>
		</select>
		<Input name="key" value={filter.key ?? ''} placeholder="metadata key" class="w-36" />
		<Input name="value" value={filter.value ?? ''} placeholder="value" class="w-56" />
		<Button type="submit" size="sm">Filter</Button>
		<Button variant="ghost" size="sm" href="?">Clear</Button>
	</form>
</div>

<div class="rounded-lg border">
	<Table.Root>
		<Table.Header>
			<Table.Row>
				<Table.Head>Time</Table.Head>
				<Table.Head>Type</Table.Head>
				<Table.Head>Status</Table.Head>
				<Table.Head class="text-right">Duration</Table.Head>
				<Table.Head>Request</Table.Head>
				<Table.Head>Metadata</Table.Head>
			</Table.Row>
		</Table.Header>
		<Table.Body>
			{#each result.logs as log (log.id)}
				<Table.Row class="align-top">
					<Table.Cell>
						<a class="text-primary hover:underline" href="/logs/{log.id}">
							{fmtDateTimeSeconds(log.createdAt)}
						</a>
					</Table.Cell>
					<Table.Cell>
						<a class="hover:underline" href={link({ type: log.type, page: undefined })}>
							{log.type}
						</a>
					</Table.Cell>
					<Table.Cell>
						<Badge variant="outline" class={levelClass[log.level]}>
							{log.status ?? log.level}
						</Badge>
					</Table.Cell>
					<Table.Cell class="text-right tabular-nums">{log.durationMs} ms</Table.Cell>
					<Table.Cell class="max-w-md">
						<div class="truncate" title={log.url ?? ''}>
							<span class="text-xs text-muted-foreground">{log.method}</span>
							{log.url}
						</div>
						{#if log.error}
							<div class="truncate text-xs text-destructive" title={log.error}>{log.error}</div>
						{/if}
					</Table.Cell>
					<Table.Cell class="whitespace-normal">
						<div class="flex flex-wrap gap-1">
							{#each flatten(log.metadata) as [key, value, filterable] (key)}
								<Badge
									variant="secondary"
									class={cn('font-normal', !filterable && 'opacity-70')}
									href={filterable ? link({ key, value, page: undefined }) : undefined}
								>
									{key}: {value}
								</Badge>
							{/each}
						</div>
					</Table.Cell>
				</Table.Row>
			{:else}
				<Table.Row>
					<Table.Cell colspan={6} class="py-6 text-center text-muted-foreground">No logs</Table.Cell
					>
				</Table.Row>
			{/each}
		</Table.Body>
	</Table.Root>
</div>

{#if result.page > 1 || result.hasNext}
	<nav class="mt-4 flex items-center justify-center gap-4 text-sm">
		{#if result.page > 1}
			<Button variant="ghost" size="sm" href={link({ page: String(result.page - 1) })}
				>← Prev</Button
			>
		{/if}
		<span class="text-muted-foreground">Page {result.page}</span>
		{#if result.hasNext}
			<Button variant="ghost" size="sm" href={link({ page: String(result.page + 1) })}
				>Next →</Button
			>
		{/if}
	</nav>
{/if}
