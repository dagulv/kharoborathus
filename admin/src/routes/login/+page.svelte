<script lang="ts">
	import { goto } from '$app/navigation';
	import { api, ApiError } from '$lib/api.js';
	import { Button } from '$lib/components/ui/button/index.js';
	import * as Card from '$lib/components/ui/card/index.js';
	import { Input } from '$lib/components/ui/input/index.js';
	import { Label } from '$lib/components/ui/label/index.js';

	let email = $state('');
	let password = $state('');
	let error = $state('');
	let pending = $state(false);

	async function submit(e: SubmitEvent) {
		e.preventDefault();
		pending = true;
		error = '';

		try {
			await api('/admin/login', { method: 'POST', body: { email: email.trim(), password } });
			await goto('/');
		} catch (err) {
			error =
				err instanceof ApiError && err.status === 401 ? 'Wrong email or password' : 'Login failed';
		} finally {
			pending = false;
		}
	}
</script>

<svelte:head><title>Log in · Admin</title></svelte:head>

<div class="flex min-h-svh items-center justify-center bg-muted/40 p-4">
	<Card.Root class="w-full max-w-sm">
		<Card.Header>
			<Card.Title>kharoborathus admin</Card.Title>
		</Card.Header>
		<Card.Content>
			<form class="space-y-4" onsubmit={submit}>
				<div class="space-y-1.5">
					<Label for="email">Email</Label>
					<Input id="email" type="email" required autocomplete="username" bind:value={email} />
				</div>
				<div class="space-y-1.5">
					<Label for="password">Password</Label>
					<Input
						id="password"
						type="password"
						required
						autocomplete="current-password"
						bind:value={password}
					/>
				</div>
				{#if error}<p class="text-sm text-destructive">{error}</p>{/if}
				<Button type="submit" class="w-full" disabled={pending}>
					{pending ? 'Logging in…' : 'Log in'}
				</Button>
			</form>
		</Card.Content>
	</Card.Root>
</div>
