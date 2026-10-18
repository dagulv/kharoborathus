<script lang="ts">
	import type { PageProps } from './$types';

	let { form }: PageProps = $props();
</script>

<svelte:head><title>Log in · Admin</title></svelte:head>

<div class="flex min-h-screen items-center justify-center bg-zinc-50 text-zinc-900">
	<form method="POST" class="w-80 space-y-4 rounded border border-zinc-200 bg-white p-6">
		<label class="block">
			<span class="text-sm font-medium">Token</span>
			<input
				name="token"
				type="password"
				required
				autocomplete="current-password"
				class="mt-1 w-full rounded border border-zinc-300 px-2 py-1.5 text-sm"
			/>
		</label>

		{#if form?.invalid}<p class="text-sm text-red-600">Invalid token</p>{/if}

		<button class="rounded bg-zinc-900 px-4 py-1.5 text-sm text-white">Log in</button>
	</form>
</div>
