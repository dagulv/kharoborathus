<script lang="ts">
	import ChevronRightIcon from '@lucide/svelte/icons/chevron-right';
	import { Badge } from '$lib/components/ui/badge/index.js';
	import { STEP_LABELS, isEmpty, show, type GraphRow } from '$lib/trace.js';
	import type { LogSummary } from '$lib/types.js';
	import CallIo from './call-io.svelte';

	let { row }: { row: GraphRow } = $props();

	const step = $derived(row.kind === 'step' ? row.step : undefined);
	const name = $derived(row.kind === 'step' ? row.step.name : row.name);
	const stopped = $derived(step?.outcome === 'stopped' || step?.outcome === 'error');

	// svelte-ignore state_referenced_locally
	let open = $state(stopped);
	// Bodies load once, on the first open, and stay loaded.
	// svelte-ignore state_referenced_locally
	let opened = $state(stopped);

	const types = $derived([...new Set(step?.calls.map((c) => c.type) ?? [])]);

	const dot = $derived.by(() => {
		if (!step) return 'border border-dashed border-muted-foreground bg-background';
		if (stopped) return 'bg-destructive';
		if (step.outcome === 'pending') return 'bg-amber-500';
		return step.calls.length ? 'bg-emerald-500' : 'bg-muted-foreground/60';
	});

	function summary(c: LogSummary) {
		return [c.method, c.status, `${c.durationMs}ms`].filter(Boolean).join(' · ');
	}
</script>

{#snippet io(label: string, value: unknown)}
	<div class="min-w-0">
		<div class="mb-1 text-[10px] font-semibold tracking-wide text-muted-foreground uppercase">
			{label}
		</div>
		<pre
			class="max-h-80 overflow-auto rounded-md bg-muted p-2 text-xs break-words whitespace-pre-wrap">{show(
				value
			)}</pre>
	</div>
{/snippet}

{#snippet call(c: LogSummary, label?: string)}
	<div class="mt-3">
		<div class="mb-1.5 flex min-w-0 items-center gap-2 text-xs">
			{#if label}<span class="font-medium">{label}</span>{/if}
			<Badge variant="outline">{c.type}</Badge>
			<span class="truncate font-mono text-muted-foreground">{c.method ?? ''} {c.url ?? ''}</span>
			<a class="ml-auto shrink-0 text-primary hover:underline" href="/logs/{c.id}">log #{c.id}</a>
		</div>
		<CallIo id={c.id} />
	</div>
{/snippet}

<li class="relative pl-6">
	<span class={['absolute top-3 left-0 size-3 rounded-full ring-4 ring-background', dot]}></span>

	{#if step}
		<details
			class={['rounded-md border', stopped && 'border-destructive/50']}
			bind:open
			ontoggle={() => {
				if (open) opened = true;
			}}
		>
			<summary
				class={[
					'flex cursor-pointer list-none flex-wrap items-center gap-x-2 gap-y-0.5 px-3 py-2 text-sm [&::-webkit-details-marker]:hidden',
					stopped && 'bg-destructive/5'
				]}
			>
				<ChevronRightIcon class={['size-3.5 shrink-0 transition-transform', open && 'rotate-90']} />
				<span class="font-medium">{STEP_LABELS[name] ?? name}</span>
				{#each types as type (type)}
					<Badge variant="outline">{type}</Badge>
				{/each}
				<span class="ml-auto shrink-0 font-mono text-xs text-muted-foreground">
					{#if step.outcome === 'pending'}
						running
					{:else if step.calls.length === 1}
						{summary(step.calls[0])}
					{:else if step.calls.length > 1}
						{step.calls.length} calls
					{/if}
				</span>
				{#if step.reason}
					<span
						class={[
							'basis-full pl-5.5 text-xs',
							stopped ? 'font-medium text-destructive' : 'text-muted-foreground'
						]}
					>
						{step.reason}
					</span>
				{/if}
			</summary>

			{#if opened}
				<div class="border-t px-3 pt-1 pb-3">
					{#if !isEmpty(step.input) || !isEmpty(step.output)}
						<div class="mt-2 grid gap-2 xl:grid-cols-2">
							{@render io('→ Input', step.input)}
							{@render io('← Output', step.output)}
						</div>
					{/if}

					{#each step.calls as c (c.id)}
						{@render call(c)}
					{/each}

					{#if step.ruleCall}
						{@render call(step.ruleCall, 'Rule generation (domain)')}
					{/if}

					{#if isEmpty(step.input) && isEmpty(step.output) && !step.calls.length && !step.ruleCall}
						<p class="mt-2 text-xs text-muted-foreground">Nothing recorded for this step.</p>
					{/if}
				</div>
			{/if}
		</details>
	{:else}
		<div
			class="flex items-center gap-2 rounded-md border border-dashed px-3 py-2 text-sm text-muted-foreground"
		>
			<span class="size-3.5"></span>
			<span>{STEP_LABELS[name] ?? name}</span>
			<span class="ml-auto text-xs">{row.kind === 'waiting' ? 'waiting' : 'not reached'}</span>
		</div>
	{/if}
</li>
