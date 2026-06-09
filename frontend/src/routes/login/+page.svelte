<script lang="ts">
	import { onMount } from 'svelte';
	import { goto } from '$app/navigation';
	import { api, ApiError, type User } from '$lib/api';
	import { auth } from '$lib/auth.svelte';
	import { Button } from '$lib/components/ui/button';
	import * as Card from '$lib/components/ui/card';
	import { Input } from '$lib/components/ui/input';
	import { Label } from '$lib/components/ui/label';

	let email = $state('');
	let password = $state('');
	let error = $state('');
	let loading = $state(false);
	let needsSetup = $state(false);

	onMount(() => {
		api.get<{ needs_setup: boolean }>('/api/auth/setup').then((r) => (needsSetup = r.needs_setup));
	});

	async function submit(e: SubmitEvent) {
		e.preventDefault();
		error = '';
		loading = true;
		try {
			auth.user = await api.post<User>('/api/auth/login', { email, password });
			auth.loaded = true;
			await goto('/slots');
		} catch (err) {
			error = err instanceof ApiError ? err.message : 'Login failed';
		} finally {
			loading = false;
		}
	}
</script>

<div class="flex min-h-svh items-center justify-center bg-muted/40 p-4">
	<Card.Root class="w-full max-w-sm">
		<Card.Header>
			<Card.Title class="text-2xl">🎾 FreiPadel</Card.Title>
			<Card.Description>Find slots where your padel group has time.</Card.Description>
		</Card.Header>
		<Card.Content>
			<form onsubmit={submit} class="grid gap-4">
				<div class="grid gap-2">
					<Label for="email">Email</Label>
					<Input id="email" type="email" bind:value={email} required autocomplete="email" />
				</div>
				<div class="grid gap-2">
					<Label for="password">Password</Label>
					<Input
						id="password"
						type="password"
						bind:value={password}
						required
						autocomplete="current-password"
					/>
				</div>
				{#if error}
					<p class="text-sm text-destructive">{error}</p>
				{/if}
				<Button type="submit" disabled={loading} class="w-full">
					{loading ? 'Logging in…' : 'Log in'}
				</Button>
			</form>
			{#if needsSetup}
				<p class="mt-4 text-center text-sm text-muted-foreground">
					No account yet? <a href="/register" class="underline">Create the first (admin) account</a>
				</p>
			{:else}
				<p class="mt-4 text-center text-sm text-muted-foreground">
					Joining the group? Ask for an invite link.
				</p>
			{/if}
		</Card.Content>
	</Card.Root>
</div>
