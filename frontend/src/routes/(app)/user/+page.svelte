<script lang="ts">
    import { Input } from "$lib/components/ui/input";
		import { api, type EmailChangeStatus, type Settings } from "$lib/api";
    import { onMount } from "svelte";
    import { toast } from "svelte-sonner";
    import { auth } from "$lib/auth.svelte";
		import { sync } from "$lib/sync.svelte";
		import Button from "$lib/components/ui/button/button.svelte";
    import Checkbox from "$lib/components/ui/checkbox/checkbox.svelte";

		let userEmail = $state<string>(auth.me?.user?.email ?? "");
		let userSettings = $state<Settings | null>();
		let saving = $state<boolean>(false);
		let changingEmail = $state<boolean>(false);
		let cancellingEmailChange = $state<boolean>(false);
		let pendingEmailChange = $state<EmailChangeStatus | null>(null);
		let slot_booked = $state<boolean>(false);
		let poll_created = $state<boolean>(false);

    $effect(() => {
		if (auth.me?.user) userEmail = auth.me.user.email;
		});

		async function saveSettings() {
        if (!userSettings) return;
        saving = true;
        try {
						userSettings.notifications["slot_booked"] = slot_booked;
						userSettings.notifications["poll_created"] = poll_created;
            userSettings = await api.put<Settings>("/api/settings", userSettings);
            // Apply to the store right away instead of waiting for the SSE
            // delta — the delta then confirms with identical content.
            sync.settings = $state.snapshot(userSettings) as Settings;
            toast.success("Settings saved");
        } catch (err) {
            toast.error(err instanceof Error ? err.message : "Could not save");
        } finally {
            saving = false;
        }
	    }

	async function requestEmailChange() {
		changingEmail = true;
		try {
			pendingEmailChange = await api.post<EmailChangeStatus>("/api/auth/email-change", {
				new_email: userEmail,
				origin: location.origin
			});
			toast.success("Confirmation email sent", {
				description: `Open the message sent to ${pendingEmailChange.pending_email} to finish the change.`
			});
		} catch (err) {
			toast.error(err instanceof Error ? err.message : "Could not request email change");
		} finally {
			changingEmail = false;
		}
	}

	async function cancelEmailChange() {
		cancellingEmailChange = true;
		try {
			await api.del<{ ok: boolean }>("/api/auth/email-change");
			pendingEmailChange = null;
			toast.success("Pending email change cancelled");
		} catch (err) {
			toast.error(err instanceof Error ? err.message : "Could not cancel email change");
		} finally {
			cancellingEmailChange = false;
		}
	}

	onMount(async () => {
		[userSettings, pendingEmailChange] = await Promise.all([
			api.get<Settings>("/api/settings"),
			api.get<EmailChangeStatus>("/api/auth/email-change")
		]);
		slot_booked = userSettings?.notifications["slot_booked"];
		poll_created = userSettings?.notifications["poll_created"];
	});
</script>

<div class="flex flex-col gap-6">
	<div class="flex flex-wrap items-center justify-between gap-3">
		<div>
			<h1 class="text-2xl font-semibold tracking-tight">User Settings</h1>
			<p class="text-sm text-muted-foreground">
				Here you can change your contact details and notification settings.
			</p>
		</div>

		<div class="flex w-full items-center gap-3">
			<label for="email" class="whitespace-nowrap">E-Mail:</label>
			<Input
				id="email"
				class="flex-grow"
				placeholder="Loading..."
				type="email"
				autocomplete="email"
				bind:value={userEmail}
			/>
			<Button
				onclick={requestEmailChange}
				disabled={
					changingEmail ||
					!auth.me?.emailer_enabled ||
					!userEmail.trim() ||
					userEmail.trim().toLowerCase() === auth.me?.user.email
				}
			>
				{changingEmail ? "Sending…" : "Change"}
			</Button>
		</div>
		{#if pendingEmailChange?.pending_email}
			<div class="flex w-full flex-wrap items-center justify-between gap-3 rounded-md border bg-muted/40 p-3 text-sm">
				<p>
					Waiting for confirmation from
					<span class="font-medium">{pendingEmailChange.pending_email}</span>.
					Your current email remains unchanged.
				</p>
				<Button
					variant="outline"
					size="sm"
					onclick={cancelEmailChange}
					disabled={cancellingEmailChange}
				>
					{cancellingEmailChange ? "Cancelling…" : "Cancel"}
				</Button>
			</div>
		{:else if auth.me && !auth.me.emailer_enabled}
			<p class="w-full text-sm text-muted-foreground">
				Email changes are unavailable because email delivery is disabled.
			</p>
		{/if}

		<div class="flex w-full flex-col gap-2">
			<label class="flex items-center gap-2">
				<Checkbox bind:checked={poll_created} />
				Notify on new poll
			</label>
			<label class="flex items-center gap-2">
				<Checkbox bind:checked={slot_booked} />
				Notify on booked slot
			</label>
		</div>

		<Button onclick={saveSettings}>Save</Button>
	</div>
</div>
