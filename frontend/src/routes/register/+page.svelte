<script lang="ts">
	import { onMount } from 'svelte';
	import { goto } from '$app/navigation';
	import { page } from '$app/state';
	import { api, ApiError, type User } from '$lib/api';
	import { auth } from '$lib/auth.svelte';
	import { Button } from '$lib/components/ui/button';
	import * as Card from '$lib/components/ui/card';
	import { Input } from '$lib/components/ui/input';
	import { Label } from '$lib/components/ui/label';

	const token = $derived(page.url.searchParams.get('token') ?? '');

	let name = $state('');
	let email = $state('');
	let password = $state('');
	let error = $state('');
	let loading = $state(false);

	// null = still checking
	let needsSetup = $state<boolean | null>(null);
	let inviteValid = $state<boolean | null>(null);
	let inviteReason = $state('');

	onMount(() => {
		api.get<{ needs_setup: boolean }>('/api/auth/setup').then((r) => {
			needsSetup = r.needs_setup;
			if (!r.needs_setup && token) {
				api
					.get<{ valid: boolean; reason?: string }>(`/api/invites/${token}/check`)
					.then((res) => {
						inviteValid = res.valid;
						inviteReason = res.reason ?? '';
					})
					.catch(() => (inviteValid = false));
			}
		});
	});

	const blocked = $derived(needsSetup === false && (!token || inviteValid === false));

	async function submit(e: SubmitEvent) {
		e.preventDefault();
		error = '';
		loading = true;
		try {
			auth.user = await api.post<User>('/api/auth/register', {
				invite_token: token,
				name,
				email,
				password
			});
			auth.loaded = true;
			await goto('/slots');
		} catch (err) {
			error = err instanceof ApiError ? err.message : 'Registration failed';
		} finally {
			loading = false;
		}
	}
</script>

<div class="flex min-h-svh items-center justify-center bg-muted/40 p-4">
	<Card.Root class="w-full max-w-sm">
		<Card.Header>
			<Card.Title class="text-2xl">🎾 Join FreiPadel</Card.Title>
			<Card.Description>
				{#if needsSetup}
					Set up the first account — it becomes the admin account.
				{:else}
					Create your account to join the padel group.
				{/if}
			</Card.Description>
		</Card.Header>
		<Card.Content>
			{#if blocked}
				<p class="text-sm text-destructive">
					{#if inviteReason === 'used'}
						This invite link has already been used. Ask for a new one.
					{:else if inviteReason === 'disabled'}
						This invite link has been disabled. Ask for a new one.
					{:else if token}
						This invite link is not valid. Ask for a new one.
					{:else}
						You need an invite link to register. Ask a group member for one.
					{/if}
				</p>
				<Button href="/login" variant="outline" class="mt-4 w-full">Back to login</Button>
			{:else}
				<form onsubmit={submit} class="grid gap-4">
					<div class="grid gap-2">
						<Label for="name">Name</Label>
						<Input id="name" bind:value={name} required placeholder="How the group knows you" />
					</div>
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
							minlength={8}
							autocomplete="new-password"
							placeholder="At least 8 characters"
						/>
					</div>
					{#if error}
						<p class="text-sm text-destructive">{error}</p>
					{/if}
					<Button type="submit" disabled={loading} class="w-full">
						{loading ? 'Creating account…' : 'Create account'}
					</Button>
				</form>
				<p class="mt-4 text-center text-sm text-muted-foreground">
					Already registered? <a href="/login" class="underline">Log in</a>
				</p>
			{/if}
		</Card.Content>
	</Card.Root>
</div>
