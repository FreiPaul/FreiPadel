<script lang="ts">
    import { Input } from "$lib/components/ui/input";
    import { api, type User, type Settings } from "$lib/api";
    import { onMount } from "svelte";
    import { toast } from "svelte-sonner";
    import { auth } from "$lib/auth.svelte";
	import { sync } from "$lib/sync.svelte";
  	import Button from "$lib/components/ui/button/button.svelte";
    import Checkbox from "$lib/components/ui/checkbox/checkbox.svelte";


		let userEmail = $state<string>(auth.me?.user?.email ?? "");
		let userSettings = $state<Settings|null>();
		let saving = $state<boolean>(false);
		let slot_booked = $state<boolean>(false);
		let poll_created = $state<boolean>(false);

    $effect(() => {
		if (auth.me?.user) userEmail = auth.me?.user.email;
	});

	async function saveSettings() {
        if (!userSettings) return;
        saving = true;
        try {
			userSettings.notifications['slot_booked'] = slot_booked;
			userSettings.notifications['poll_created'] = poll_created;
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

	onMount(async () => {
		userSettings = await api.get<Settings>("/api/settings");
		slot_booked = userSettings?.notifications['slot_booked'];
		poll_created = userSettings?.notifications['poll_created'];
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

		<div class="flex gap-3 items-center w-full">

		<label for="email" class="whitespace-nowrap">E-Mail:</label>
		 <Input
			    id="email"
                class="flex-grow"
                placeholder="Loading..."
                bind:value={userEmail}
            />
		<Button>Change</Button>

		</div>

		<div class="flex flex-col gap-2 w-full">
    <label class="flex items-center gap-2">
        <Checkbox bind:checked={poll_created}></Checkbox>

        Notify on new poll
    </label>
    <label class="flex items-center gap-2">
        <Checkbox bind:checked={slot_booked}></Checkbox>
        Notify on booked slot
    </label>
</div>

		<Button onclick={saveSettings}>Save</Button>
	</div>
</div>
