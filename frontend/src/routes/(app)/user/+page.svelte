<script lang="ts">
    import { Input } from "$lib/components/ui/input";
    import { api, type User } from "$lib/api";
    import { onMount } from "svelte";
    import { toast } from "svelte-sonner";
    import { auth } from "$lib/auth.svelte";

    let userEmail = $state<string>(auth.me?.user?.email ?? "");
    $effect(() => {if (auth.me?.user) userEmail = auth.me?.user.email});

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

		</div>
	</div>
</div>
