<script lang="ts">
	import { listSwitches, setSwitch } from '../admin.remote';

	const switches = $derived(await listSwitches());

	const fmt = new Intl.DateTimeFormat('sv-SE', { dateStyle: 'short', timeStyle: 'short' });

	const descriptions: Record<string, string> = {
		shadow_gate:
			'Stores the shadow gate result per post and flags drifting sources. Turn on when several gate tags exist.',
		discovered_ingest:
			'Fetches queued links, gates them and adds accepted pages as posts with proposed sources. Link extraction fills the queue either way.',
		search_discovery:
			'Searches Brave for events from trusted posts whose action is a gate tag and queues relevant results for ingest. Needs discovered_ingest on. Event extraction runs either way. At most 330 searches a day.',
		source_promotion:
			'Promotes proposed sources with at least 5 accepted posts on 3 days and a 60% acceptance rate, at most 5 a week. Dormancy and probe run either way.'
	};

	let failed = $state('');

	async function toggle(key: string, enabled: boolean) {
		failed = '';
		try {
			await setSwitch({ key, enabled });
		} catch {
			failed = key;
			await listSwitches().refresh();
		}
	}
</script>

<svelte:head><title>Switches · Admin</title></svelte:head>

<h1 class="mb-1 text-xl font-semibold">Switches</h1>
<p class="mb-4 text-sm text-zinc-500">
	Features that need data before they run. Readiness is a hint, never enforced.
</p>

<div class="overflow-x-auto rounded border border-zinc-200 bg-white">
	<table class="w-full text-left text-sm">
		<thead class="border-b border-zinc-200 bg-zinc-50 text-zinc-500">
			<tr>
				<th class="px-3 py-2 font-medium">Switch</th>
				<th class="px-3 py-2 font-medium">Readiness</th>
				<th class="px-3 py-2 font-medium">Changed</th>
				<th class="px-3 py-2 font-medium">On</th>
			</tr>
		</thead>
		<tbody>
			{#each switches as sw (sw.key)}
				<tr class="border-b border-zinc-100 last:border-0">
					<td class="px-3 py-2">
						<div class="font-mono">{sw.key}</div>
						{#if descriptions[sw.key]}
							<div class="text-xs text-zinc-500">{descriptions[sw.key]}</div>
						{/if}
					</td>
					<td class="px-3 py-2 text-zinc-600">{sw.readiness ?? '—'}</td>
					<td class="px-3 py-2 whitespace-nowrap text-zinc-500">{fmt.format(sw.updatedAt)}</td>
					<td class="px-3 py-2">
						<input
							type="checkbox"
							checked={sw.enabled}
							onchange={(e) => toggle(sw.key, e.currentTarget.checked)}
						/>
						{#if failed === sw.key}<span class="ml-2 text-xs text-red-600">Failed</span>{/if}
					</td>
				</tr>
			{:else}
				<tr><td colspan="4" class="px-3 py-6 text-center text-zinc-500">No switches</td></tr>
			{/each}
		</tbody>
	</table>
</div>
