<script lang="ts">
	import { goto } from '$app/navigation';
	import { page } from '$app/state';
	import FileTextIcon from '@lucide/svelte/icons/file-text';
	import HouseIcon from '@lucide/svelte/icons/house';
	import LogOutIcon from '@lucide/svelte/icons/log-out';
	import RssIcon from '@lucide/svelte/icons/rss';
	import ScrollTextIcon from '@lucide/svelte/icons/scroll-text';
	import TagIcon from '@lucide/svelte/icons/tag';
	import { api } from '$lib/api.js';
	import * as Sidebar from '$lib/components/ui/sidebar/index.js';

	let { children } = $props();

	const links = [
		{ href: '/', label: 'Overview', icon: HouseIcon },
		{ href: '/sources', label: 'Sources', icon: RssIcon },
		{ href: '/tags', label: 'Tags', icon: TagIcon },
		{ href: '/posts', label: 'Posts', icon: FileTextIcon },
		{ href: '/logs', label: 'Logs', icon: ScrollTextIcon }
	];

	function isActive(href: string) {
		return href === '/' ? page.url.pathname === href : page.url.pathname.startsWith(href);
	}

	async function logout() {
		await api('/admin/logout', { method: 'POST' }).catch(() => {});
		await goto('/login');
	}
</script>

<Sidebar.Provider>
	<Sidebar.Root>
		<Sidebar.Header>
			<span class="px-2 py-1 text-sm font-semibold">kharoborathus</span>
		</Sidebar.Header>
		<Sidebar.Content>
			<Sidebar.Group>
				<Sidebar.Menu>
					{#each links as link (link.href)}
						<Sidebar.MenuItem>
							<Sidebar.MenuButton isActive={isActive(link.href)}>
								{#snippet child({ props })}
									<a href={link.href} {...props}>
										<link.icon />
										<span>{link.label}</span>
									</a>
								{/snippet}
							</Sidebar.MenuButton>
						</Sidebar.MenuItem>
					{/each}
				</Sidebar.Menu>
			</Sidebar.Group>
		</Sidebar.Content>
		<Sidebar.Footer>
			<Sidebar.Menu>
				<Sidebar.MenuItem>
					<Sidebar.MenuButton onclick={logout}>
						<LogOutIcon />
						<span>Log out</span>
					</Sidebar.MenuButton>
				</Sidebar.MenuItem>
			</Sidebar.Menu>
		</Sidebar.Footer>
	</Sidebar.Root>
	<Sidebar.Inset>
		<header class="flex h-12 items-center border-b px-4 md:hidden">
			<Sidebar.Trigger />
		</header>
		<main class="mx-auto w-full max-w-6xl px-4 py-6 md:px-6">
			<svelte:boundary>
				{@render children()}

				{#snippet failed(err)}
					<p class="text-sm text-destructive">
						Failed to load: {(err as Error)?.message ?? 'unknown error'}
					</p>
				{/snippet}
			</svelte:boundary>
		</main>
	</Sidebar.Inset>
</Sidebar.Provider>
