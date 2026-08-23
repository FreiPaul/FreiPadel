<script lang="ts">
	import { page } from '$app/state';
	import { api, ApiError } from '$lib/api';
	import { auth, loadUser } from '$lib/auth.svelte';
	import { Button } from '$lib/components/ui/button';
	import * as Card from '$lib/components/ui/card';

	const token = $derived(page.url.searchParams.get('token') ?? '');
	let confirming = $state(false);
	let confirmedEmail = $state('');
	let error = $state('');
	const visibleError = $derived(error || (!token ? 'This confirmation link is incomplete.' : ''));

	async function confirmEmailChange() {
		if (!token) return;
		confirming = true;
		error = '';
		try {
			const result = await api.post<{ ok: boolean; email: string }>(
				'/api/auth/email-change/confirm',
				{ token }
			);
			confirmedEmail = result.email;
			history.replaceState(history.state, '', '/confirm-email');
			await loadUser();
		} catch (err) {
			error = err instanceof ApiError ? err.message : 'Could not confirm the email change';
		} finally {
			confirming = false;
		}
	}
</script>

<svelte:head>
	<title>Confirm email change · FreiPadel</title>
	<meta name="referrer" content="no-referrer" />
</svelte:head>

<div class="flex min-h-svh items-center justify-center bg-muted/40 p-4">
	<Card.Root class="w-full max-w-md">
		<Card.Header>
			<Card.Title class="text-2xl">Confirm email change</Card.Title>
			<Card.Description>
				Your FreiPadel login email will only change after you confirm here.
			</Card.Description>
		</Card.Header>
		<Card.Content class="grid gap-4">
			{#if confirmedEmail}
				<p class="text-sm">
					Your email address has been changed to
					<span class="font-medium">{confirmedEmail}</span>.
				</p>
				<Button href={auth.me?.user ? '/user' : '/login'} class="w-full">
					{auth.me?.user ? 'Back to settings' : 'Log in'}
				</Button>
			{:else}
				{#if visibleError}
					<p class="text-sm text-destructive">{visibleError}</p>
				{/if}
				<Button onclick={confirmEmailChange} disabled={!token || confirming} class="w-full">
					{confirming ? 'Confirming…' : 'Confirm email change'}
				</Button>
				<Button href="/login" variant="outline" class="w-full">Cancel</Button>
			{/if}
		</Card.Content>
	</Card.Root>
</div>
