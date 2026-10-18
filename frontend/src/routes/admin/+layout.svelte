<script lang="ts">
	import { page } from '$app/state';

	let { children } = $props();

	const links = [
		{ href: '/admin', label: 'Overview' },
		{ href: '/admin/sources', label: 'Sources' },
		{ href: '/admin/tags', label: 'Tags' },
		{ href: '/admin/posts', label: 'Posts' },
		{ href: '/admin/switches', label: 'Switches' },
		{ href: '/admin/logs', label: 'Logs' }
	];

	function isActive(href: string) {
		return href === '/admin' ? page.url.pathname === href : page.url.pathname.startsWith(href);
	}
</script>

<div class="min-h-screen bg-zinc-50 text-zinc-900">
	<header class="border-b border-zinc-200 bg-white">
		<nav class="mx-auto flex max-w-6xl items-center gap-6 px-4 py-3">
			<span class="font-semibold">kharoborathus admin</span>
			{#each links as link (link.href)}
				<a
					href={link.href}
					class={[
						'text-sm',
						isActive(link.href) ? 'font-medium text-zinc-900' : 'text-zinc-500 hover:text-zinc-900'
					]}
				>
					{link.label}
				</a>
			{/each}
		</nav>
	</header>

	<main class="mx-auto max-w-6xl px-4 py-6">
		<svelte:boundary>
			{@render children()}

			{#snippet failed(err)}
				<p class="text-sm text-red-600">
					Failed to load: {(err as Error)?.message ?? 'unknown error'}
				</p>
			{/snippet}
		</svelte:boundary>
	</main>
</div>
