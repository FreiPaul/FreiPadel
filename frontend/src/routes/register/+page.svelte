<script lang="ts">
	// The invite redemption page. An invite always joins you to one club, and
	// there are three ways to arrive here: as the very first account of a fresh
	// deployment, as a stranger who needs an account, or already signed in.
	import { onMount } from 'svelte';
	import { goto } from '$app/navigation';
	import { page } from '$app/state';
	import { api, ApiError, type Me } from '$lib/api';
	import { auth, loadUser } from '$lib/auth.svelte';
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
	// Strangers can flip between creating an account and signing in to one.
	let mode = $state<'register' | 'login'>('register');

	// null = still checking
	let needsSetup = $state<boolean | null>(null);
	let inviteValid = $state<boolean | null>(null);
	let inviteReason = $state('');
	let emailInvite = $state<boolean>(false);
	let clubName = $state('');
	let clubId = $state<number | null>(null);
	let alreadyMember = $state(false);

	// Whoever is signed in already; they only need to accept the invite.
	const signedIn = $derived(auth.loaded ? auth.me?.user : undefined);

	onMount(async () => {
		try {
			const setup = await api.get<{ needs_setup: boolean }>('/api/auth/setup');
			needsSetup = setup.needs_setup;
		} catch {
			needsSetup = false;
		}
		if (!needsSetup && token) {
			try {
				const res = await api.get<{
					valid: boolean;
					reason?: string;
					email?: string;
					club_id?: number;
					club_name?: string;
				}>(`/api/invites/${token}/check`);
				inviteValid = res.valid;
				inviteReason = res.reason ?? '';
				email = res.email ?? '';
				clubId = res.club_id ?? null;
				clubName = res.club_name ?? '';
				if (res.email && res.email.length > 0) emailInvite = true;
			} catch {
				inviteValid = false;
			}
		}
		// Knowing who is signed in decides which of the three forms to show.
		if (!auth.loaded) await loadUser();
		await checkMembership();
	});

	// Membership, not the active club: you can already be in a club without
	// currently looking at it.
	async function checkMembership() {
		if (!auth.me?.user || clubId === null) {
			alreadyMember = false;
			return;
		}
		try {
			const mine = await api.get<{ clubs: { id: number }[] }>('/api/clubs');
			alreadyMember = mine.clubs.some((c) => c.id === clubId);
		} catch {
			alreadyMember = false;
		}
	}

	const blocked = $derived(needsSetup === false && (!token || inviteValid === false));

	async function submit(e: SubmitEvent) {
		e.preventDefault();
		error = '';
		loading = true;
		try {
			auth.me = await api.post<Me>('/api/auth/register', {
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

	// Signing in from here joins the invite's club straight afterwards, so an
	// existing account never has to be registered a second time.
	async function loginAndJoin(e: SubmitEvent) {
		e.preventDefault();
		error = '';
		loading = true;
		try {
			auth.me = await api.post<Me>('/api/auth/login', { email, password });
			auth.loaded = true;
			await checkMembership();
			await join();
		} catch (err) {
			error = err instanceof ApiError ? err.message : 'Could not sign in';
			loading = false;
		}
	}

	async function join() {
		error = '';
		loading = true;
		try {
			await api.post(`/api/invites/${token}/accept`);
			await loadUser();
			await goto('/slots');
		} catch (err) {
			error = err instanceof ApiError ? err.message : 'Could not join the club';
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
				{:else if clubName}
					You have been invited to <strong>{clubName}</strong>.
				{:else}
					Create your account to join the padel group.
				{/if}
			</Card.Description>
		</Card.Header>
		<Card.Content>
			{#if alreadyMember && signedIn}
				<!-- Redeeming a link you already used is not an error: it just
				     means you are in. This comes before `blocked` so a spent
				     one-time link reads as "you are already in" for the person
				     who spent it. -->
				<div class="grid gap-4">
					<p class="text-sm text-muted-foreground">
						You are already in {clubName}, signed in as {signedIn.name}.
					</p>
					<Button href="/polls" class="w-full">Go to {clubName}</Button>
				</div>
			{:else if blocked}
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
			{:else if signedIn && !needsSetup}
				<!-- Already signed in: accepting is all that is left to do. -->
				<div class="grid gap-4">
					<p class="text-sm text-muted-foreground">
						Signed in as <strong>{signedIn.name}</strong> ({signedIn.email}).
					</p>
					{#if error}
						<p class="text-sm text-destructive">{error}</p>
					{/if}
					<Button onclick={join} disabled={loading} class="w-full">
						{loading ? 'Joining…' : `Join ${clubName}`}
					</Button>
					<a href="/login?redirect_to={encodeURIComponent(page.url.pathname + page.url.search)}"
						class="text-center text-sm text-muted-foreground underline">
						Use a different account
					</a>
				</div>
			{:else if mode === 'login' && !needsSetup}
				<form onsubmit={loginAndJoin} class="grid gap-4">
					<div class="grid gap-2">
						<Label for="login-email">Email</Label>
						<Input id="login-email" type="email" bind:value={email} required autocomplete="email" />
					</div>
					<div class="grid gap-2">
						<Label for="login-password">Password</Label>
						<Input
							id="login-password"
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
						{loading ? 'Joining…' : `Log in and join ${clubName}`}
					</Button>
				</form>
				<p class="mt-4 text-center text-sm text-muted-foreground">
					No account yet?
					<button type="button" class="underline" onclick={() => ((mode = 'register'), (error = ''))}>
						Create one
					</button>
				</p>
			{:else}
				<form onsubmit={submit} class="grid gap-4">
					<div class="grid gap-2">
						<Label for="name">Name</Label>
						<Input id="name" bind:value={name} required placeholder="How the group knows you" />
					</div>
					<div class="grid gap-2">
						<Label for="email">Email</Label>
						<Input id="email" type="email" bind:value={email} disabled={emailInvite} required autocomplete="email" />
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
				{#if !needsSetup}
					<p class="mt-4 text-center text-sm text-muted-foreground">
						Already registered?
						<button type="button" class="underline" onclick={() => ((mode = 'login'), (error = ''))}>
							Log in to join
						</button>
					</p>
				{/if}
			{/if}
		</Card.Content>
	</Card.Root>
</div>
