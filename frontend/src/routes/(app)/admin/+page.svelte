<script lang="ts">
	import { api } from '$lib/api';
	import { sync } from '$lib/sync.svelte';
	import { Badge } from '$lib/components/ui/badge';
	import { Button } from '$lib/components/ui/button';
	import * as Card from '$lib/components/ui/card';
	import { Separator } from '$lib/components/ui/separator';
	import { formatTimestamp } from '$lib/format';
	import { toast } from 'svelte-sonner';

	// Rendered straight from the sync store — no fetching on navigation, and
	// invites flip to "used" live when a friend registers.
	const invites = $derived(
		Object.values(sync.invites).sort((a, b) => b.created_at.localeCompare(a.created_at))
	);
	const members = $derived(
		Object.values(sync.members).sort((a, b) => a.name.localeCompare(b.name))
	);
	let creating = $state(false);

	function inviteURL(token: string): string {
		return `${location.origin}/register?token=${token}`;
	}

	async function createInvite(kind: 'single' | 'group') {
		creating = true;
		try {
			const { token } = await api.post<{ token: string }>('/api/invites', { kind });
			await copy(token); // the new row arrives as a sync delta
		} catch {
			toast.error('Could not create invite');
		} finally {
			creating = false;
		}
	}

	async function disable(token: string) {
		try {
			await api.post(`/api/invites/${token}/disable`);
			const inv = sync.invites[token];
			if (inv) inv.disabled = true; // optimistic; the delta confirms
			toast.success('Invite link disabled');
		} catch {
			toast.error('Could not disable invite');
		}
	}

	async function copy(token: string) {
		try {
			await navigator.clipboard.writeText(inviteURL(token));
			toast.success('Invite link copied to clipboard');
		} catch {
			toast.info(inviteURL(token));
		}
	}

	async function revoke(token: string) {
		try {
			await api.del(`/api/invites/${token}`);
			delete sync.invites[token]; // optimistic; the delta confirms
		} catch {
			toast.error('Could not revoke invite');
		}
	}
</script>

<div class="flex flex-col gap-6">
	<div class="flex flex-wrap items-center justify-between gap-3">
		<div>
			<h1 class="text-2xl font-semibold tracking-tight">Invites</h1>
			<p class="text-sm text-muted-foreground">
				One-time links work for a single registration; group links keep working until you disable
				them.
			</p>
		</div>
		<div class="flex gap-2">
			<Button variant="outline" onclick={() => createInvite('single')} disabled={creating}>
				+ One-time link
			</Button>
			<Button onclick={() => createInvite('group')} disabled={creating}>+ Group link</Button>
		</div>
	</div>

	<Card.Root>
		<Card.Content class="flex flex-col divide-y">
			{#if invites.length === 0}
				<p class="py-6 text-center text-sm text-muted-foreground">
					No invites yet. Create one and send the link to a friend.
				</p>
			{/if}
			{#each invites as invite (invite.token)}
				<div class="flex flex-wrap items-center gap-2 py-2.5 first:pt-0 last:pb-0">
					<code class="truncate text-xs text-muted-foreground">…{invite.token.slice(-8)}</code>
					{#if invite.kind === 'group'}
						<Badge variant="outline">👥 group</Badge>
						{#if invite.disabled}
							<Badge variant="secondary" class="opacity-70">disabled</Badge>
						{:else}
							<Badge>active</Badge>
						{/if}
						<span class="text-xs text-muted-foreground">
							{invite.uses}
							{invite.uses === 1 ? 'registration' : 'registrations'}
						</span>
						<div class="ml-auto flex gap-1.5">
							{#if !invite.disabled}
								<Button size="sm" variant="outline" onclick={() => copy(invite.token)}>
									Copy link
								</Button>
								<Button size="sm" variant="ghost" onclick={() => disable(invite.token)}>
									Disable
								</Button>
							{:else}
								<Button size="sm" variant="ghost" onclick={() => revoke(invite.token)}>Delete</Button>
							{/if}
						</div>
					{:else if invite.used_by}
						<Badge variant="secondary">used by {invite.used_by}</Badge>
						<span class="text-xs text-muted-foreground">{formatTimestamp(invite.used_at ?? '')}</span>
					{:else if invite.disabled}
						<Badge variant="secondary" class="opacity-70">disabled</Badge>
						<div class="ml-auto">
							<Button size="sm" variant="ghost" onclick={() => revoke(invite.token)}>Delete</Button>
						</div>
					{:else}
						<Badge>open</Badge>
						<div class="ml-auto flex gap-1.5">
							<Button size="sm" variant="outline" onclick={() => copy(invite.token)}>
								Copy link
							</Button>
							<Button size="sm" variant="ghost" onclick={() => revoke(invite.token)}>Revoke</Button>
						</div>
					{/if}
				</div>
			{/each}
		</Card.Content>
	</Card.Root>

	<Separator />

	<div>
		<h2 class="mb-3 text-lg font-medium">Members ({members.length})</h2>
		<Card.Root>
			<Card.Content class="flex flex-col divide-y">
				{#each members as member (member.id)}
					<div class="flex items-center gap-2 py-2 first:pt-0 last:pb-0 text-sm">
						<span>{member.name}</span>
						{#if member.is_admin}
							<Badge variant="outline">admin</Badge>
						{/if}
					</div>
				{/each}
			</Card.Content>
		</Card.Root>
	</div>
</div>
