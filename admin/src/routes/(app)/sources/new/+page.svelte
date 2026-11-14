<script lang="ts">
	import { goto } from '$app/navigation';
	import { api, errorMessage } from '$lib/api.js';
	import { Button } from '$lib/components/ui/button/index.js';
	import * as Card from '$lib/components/ui/card/index.js';
	import { Input } from '$lib/components/ui/input/index.js';
	import { Label } from '$lib/components/ui/label/index.js';
	import * as RadioGroup from '$lib/components/ui/radio-group/index.js';
	import * as Select from '$lib/components/ui/select/index.js';
	import { fmtDate } from '$lib/format.js';
	import type { ListingCandidate, SourceCheck } from '$lib/types.js';

	let url = $state('');
	let rate = $state(15);
	// bits-ui treats an empty value as no selection, so Unknown is stored as a sentinel.
	let lang = $state('unknown');
	let listing = $state('');

	let candidates = $state<ListingCandidate[] | null>(null);
	let wordpress = $state<SourceCheck['wordpress']>();
	let checkedURL = $state('');
	let checking = $state(false);
	let checkError = $state('');
	let submitError = $state('');
	let submitting = $state(false);

	// Candidates belong to the url they were checked for.
	const checked = $derived(candidates !== null && url.trim() === checkedURL);

	const key = (c: ListingCandidate) => `${c.adapter} ${c.url}`;

	const LANGS: Record<string, string> = { unknown: 'Unknown', sv: 'Swedish', en: 'English' };

	async function check() {
		const value = url.trim();

		checking = true;
		checkError = '';
		candidates = null;

		try {
			({ candidates, wordpress } = await api<SourceCheck>('/admin/listing', {
				query: { url: value }
			}));
			checkedURL = value;
			listing = key(candidates[0]);
		} catch (err) {
			checkError = errorMessage(err, 'Check failed');
		} finally {
			checking = false;
		}
	}

	async function submit(e: SubmitEvent) {
		e.preventDefault();

		const chosen = candidates?.find((c) => key(c) === listing);

		submitting = true;
		submitError = '';

		try {
			await api('/admin/sources', {
				method: 'POST',
				body: {
					url: url.trim(),
					adapter: chosen?.adapter ?? '',
					listingUrl: chosen?.url ?? '',
					rate,
					lang: lang === 'unknown' ? '' : lang
				}
			});
			await goto('/sources');
		} catch (err) {
			submitError = errorMessage(err, 'Adding the source failed');
		} finally {
			submitting = false;
		}
	}
</script>

<svelte:head><title>New source · Admin</title></svelte:head>

<a class="text-sm text-muted-foreground hover:text-foreground" href="/sources">← Sources</a>

<h1 class="mt-3 mb-4 text-lg font-semibold">New source</h1>

<Card.Root class="max-w-xl">
	<Card.Content>
		<form class="space-y-5" onsubmit={submit}>
			<div class="space-y-1.5">
				<Label for="url">URL</Label>
				<div class="flex gap-2">
					<Input id="url" type="url" required placeholder="https://example.com/" bind:value={url} />
					<Button type="button" variant="outline" onclick={check} disabled={checking || !url}>
						{checking ? 'Checking…' : 'Check'}
					</Button>
				</div>
				<p class="text-xs text-muted-foreground">
					A site, a section or a feed. Check finds what it can be read as.
				</p>
				{#if checkError}<p class="text-sm text-destructive">{checkError}</p>{/if}
			</div>

			{#if checked && candidates}
				<fieldset class="space-y-2">
					<legend class="mb-1.5 text-sm font-medium">Read as</legend>
					<RadioGroup.Root bind:value={listing} class="gap-2.5">
						{#each candidates as candidate (key(candidate))}
							<div class="flex items-start gap-2 text-sm">
								<RadioGroup.Item value={key(candidate)} id={key(candidate)} class="mt-0.5" />
								<Label for={key(candidate)} class="block min-w-0 font-normal">
									{#if candidate.adapter === 'rss'}
										<span class="font-medium">Feed</span>
										{candidate.title}
										<span class="block truncate text-xs text-muted-foreground">{candidate.url}</span
										>
										<span class="block text-xs text-muted-foreground">
											{candidate.items ?? 0} items{candidate.latest
												? `, newest ${fmtDate(candidate.latest)}`
												: ''}
										</span>
									{:else}
										<span class="font-medium">Page</span>
										<span class="block truncate text-xs text-muted-foreground">{candidate.url}</span
										>
										<span class="block text-xs text-muted-foreground">
											Article links found on the page by an LLM-written rule
										</span>
									{/if}
								</Label>
							</div>
						{/each}
					</RadioGroup.Root>
				</fieldset>
				{#if wordpress}
					<p class="text-sm text-muted-foreground">
						WordPress site. All {wordpress.total} posts are backfilled through its REST API once the source
						is scraped.
					</p>
				{/if}
			{/if}

			<div class="space-y-1.5">
				<Label for="rate">Rate (minutes)</Label>
				<Input id="rate" type="number" min="1" class="w-32" bind:value={rate} />
			</div>

			<div class="space-y-1.5">
				<Label>Language</Label>
				<Select.Root type="single" bind:value={lang}>
					<Select.Trigger class="w-40">{LANGS[lang]}</Select.Trigger>
					<Select.Content>
						{#each Object.entries(LANGS) as [value, label] (value)}
							<Select.Item {value} {label} />
						{/each}
					</Select.Content>
				</Select.Root>
				<p class="text-xs text-muted-foreground">Used when a post declares no language.</p>
			</div>

			{#if submitError}<p class="text-sm text-destructive">{submitError}</p>{/if}

			<Button type="submit" disabled={!checked || submitting}>
				{submitting ? 'Adding…' : 'Add source'}
			</Button>
		</form>
	</Card.Content>
</Card.Root>
