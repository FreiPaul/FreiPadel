<script lang="ts">
    // The club switcher. Every club the user belongs to, each with the number
    // of polls still open in it, so the badge answers "where is something
    // waiting for me?" without opening each club in turn.
    import { goto } from "$app/navigation";
    import { auth } from "$lib/auth.svelte";
    import { activePollCount, myClubs, switchClub, sync } from "$lib/sync.svelte";
    import { Badge } from "$lib/components/ui/badge";
    import { Button } from "$lib/components/ui/button";
    import * as DropdownMenu from "$lib/components/ui/dropdown-menu";
    import { toast } from "svelte-sonner";

    const clubs = $derived(myClubs());
    const active = $derived(
        sync.activeClubId === null ? null : (sync.clubs[sync.activeClubId] ?? null),
    );

    async function select(clubId: number) {
        try {
            await switchClub(clubId);
        } catch {
            toast.error("Could not switch club");
        }
    }
</script>

{#if sync.ready}
    <DropdownMenu.Root>
        <DropdownMenu.Trigger>
            {#snippet child({ props })}
                <Button
                    {...props}
                    data-testid="club-switcher"
                    variant="outline"
                    size="sm"
                    class="w-full justify-between gap-2"
                >
                    <span class="truncate">
                        {active ? active.name : "No club yet"}
                    </span>
                    <span aria-hidden="true" class="text-muted-foreground">▾</span>
                </Button>
            {/snippet}
        </DropdownMenu.Trigger>
        <DropdownMenu.Content class="w-56" align="start">
            <DropdownMenu.Label>Your clubs</DropdownMenu.Label>
            {#if clubs.length === 0}
                <DropdownMenu.Item disabled>
                    You are not in a club yet
                </DropdownMenu.Item>
            {/if}
            {#each clubs as club (club.id)}
                {@const open = activePollCount(club.id)}
                <DropdownMenu.Item onSelect={() => select(club.id)}>
                    <span class="w-4 shrink-0" aria-hidden="true">
                        {club.id === sync.activeClubId ? "✓" : ""}
                    </span>
                    <span class="truncate">{club.name}</span>
                    {#if open}
                        <Badge variant="secondary" class="ml-auto">{open}</Badge>
                    {/if}
                </DropdownMenu.Item>
            {/each}
            {#if auth.me?.user?.is_admin}
                <DropdownMenu.Separator />
                <DropdownMenu.Item onSelect={() => goto("/admin?tab=clubs")}>
                    <span class="w-4 shrink-0" aria-hidden="true">⚙</span>
                    <span>Manage clubs</span>
                </DropdownMenu.Item>
            {/if}
        </DropdownMenu.Content>
    </DropdownMenu.Root>
{/if}
