<script lang="ts">
    // Club administration. Deliberately fetched over REST rather than fed by
    // the sync store: clubs change rarely, and nobody needs to watch the
    // administration page update live.
    import { api, type AdminClub, type Member } from "$lib/api";
    import { Badge } from "$lib/components/ui/badge";
    import { Button } from "$lib/components/ui/button";
    import * as Card from "$lib/components/ui/card";
    import { Input } from "$lib/components/ui/input";
    import { Label } from "$lib/components/ui/label";
    import { Separator } from "$lib/components/ui/separator";
    import { Skeleton } from "$lib/components/ui/skeleton";
    import { toast } from "svelte-sonner";
    import { onMount } from "svelte";

    let clubs = $state<AdminClub[] | null>(null);
    let everyone = $state<Member[]>([]);
    let venues = $state<string[]>([]);
    let membersOf = $state<Record<number, Member[]>>({});
    let expanded = $state<number | null>(null);
    let newClubName = $state("");
    let busy = $state(false);

    async function reload() {
        clubs = await api.get<AdminClub[]>("/api/admin/clubs");
    }

    onMount(async () => {
        try {
            await reload();
            // ?all=1 is the whole directory, which is what staffing a club needs.
            everyone = await api.get<Member[]>("/api/users?all=1");
            venues = await api.get<string[]>("/api/locations?all=1");
        } catch {
            toast.error("Could not load clubs");
        }
    });

    async function toggle(club: AdminClub) {
        if (expanded === club.id) {
            expanded = null;
            return;
        }
        expanded = club.id;
        if (membersOf[club.id]) return;
        try {
            membersOf[club.id] = await api.get<Member[]>(
                `/api/clubs/${club.id}/members`,
            );
        } catch {
            toast.error("Could not load members");
        }
    }

    async function run(action: () => Promise<unknown>, failure: string) {
        busy = true;
        try {
            await action();
        } catch (e) {
            toast.error(failure, {
                description: e instanceof Error ? e.message : undefined,
            });
        } finally {
            busy = false;
        }
    }

    async function create() {
        const name = newClubName.trim();
        if (!name) {
            toast.error("Enter a club name");
            return;
        }
        await run(async () => {
            await api.post("/api/admin/clubs", { name });
            newClubName = "";
            await reload();
            toast.success(`Created ${name}`);
        }, "Could not create the club");
    }

    async function rename(club: AdminClub, name: string) {
        if (name.trim() === club.name) return;
        await run(async () => {
            await api.patch(`/api/clubs/${club.id}`, { name: name.trim() });
            await reload();
        }, "Could not rename the club");
    }

    async function setOwner(club: AdminClub, ownerId: number) {
        await run(async () => {
            await api.patch(`/api/clubs/${club.id}`, { owner_id: ownerId });
            delete membersOf[club.id];
            await reload();
            if (expanded === club.id) {
                membersOf[club.id] = await api.get<Member[]>(
                    `/api/clubs/${club.id}/members`,
                );
            }
        }, "Could not change the owner");
    }

    // An empty venue list means the club plays everywhere, so clicking the last
    // selected venue off puts it back to "all venues".
    async function toggleVenue(club: AdminClub, venue: string) {
        const next = club.locations.includes(venue)
            ? club.locations.filter((v) => v !== venue)
            : [...club.locations, venue];
        await run(async () => {
            await api.patch(`/api/clubs/${club.id}`, { locations: next });
            await reload();
        }, "Could not change the venues");
    }

    async function addMember(club: AdminClub, userId: number) {
        await run(async () => {
            await api.post(`/api/clubs/${club.id}/members`, { user_id: userId });
            membersOf[club.id] = await api.get<Member[]>(
                `/api/clubs/${club.id}/members`,
            );
            await reload();
        }, "Could not add the member");
    }

    async function removeMember(club: AdminClub, userId: number) {
        await run(async () => {
            await api.del(`/api/clubs/${club.id}/members/${userId}`);
            membersOf[club.id] = await api.get<Member[]>(
                `/api/clubs/${club.id}/members`,
            );
            await reload();
        }, "Could not remove the member");
    }

    async function remove(club: AdminClub) {
        await run(async () => {
            await api.del(`/api/clubs/${club.id}`);
            expanded = null;
            await reload();
            toast.success(`Deleted ${club.name}`);
        }, "Could not delete the club");
    }

    function notIn(club: AdminClub): Member[] {
        const inside = new Set((membersOf[club.id] ?? []).map((m) => m.id));
        return everyone.filter((m) => !inside.has(m.id));
    }
</script>

<div class="flex flex-col gap-6">
    <div class="flex flex-wrap items-end justify-between gap-3">
        <div>
            <h2 class="text-lg font-medium">Clubs</h2>
            <p class="text-sm text-muted-foreground">
                A club groups its members, its polls and the venues it plays at.
                Leave the venues empty to let a club see every venue.
            </p>
        </div>
        <div class="flex gap-2">
            <Input
                placeholder="New club name"
                bind:value={newClubName}
                onkeydown={(e) => {
                    if (e.key === "Enter") {
                        e.preventDefault();
                        create();
                    }
                }}
            />
            <Button onclick={create} disabled={busy}>+ Club</Button>
        </div>
    </div>

    {#if clubs === null}
        <Skeleton class="h-32 w-full" />
    {:else}
        <Card.Root>
            <Card.Content class="flex flex-col divide-y">
                {#if clubs.length === 0}
                    <p class="py-6 text-center text-sm text-muted-foreground">
                        No clubs yet.
                    </p>
                {/if}
                {#each clubs as club (club.id)}
                    <div class="flex flex-col gap-2 py-3 first:pt-0 last:pb-0">
                        <div class="flex flex-wrap items-center gap-2">
                            <button
                                type="button"
                                class="font-medium hover:underline"
                                onclick={() => toggle(club)}
                            >
                                {club.name}
                            </button>
                            <Badge variant="outline">
                                {club.member_count}
                                {club.member_count === 1 ? "member" : "members"}
                            </Badge>
                            <span class="text-xs text-muted-foreground">
                                owned by {club.owner_name}
                            </span>
                            <span class="text-xs text-muted-foreground">
                                {club.locations.length === 0
                                    ? "all venues"
                                    : club.locations.join(", ")}
                            </span>
                            <div class="ml-auto flex gap-1.5">
                                <Button
                                    size="sm"
                                    variant="outline"
                                    onclick={() => toggle(club)}
                                >
                                    {expanded === club.id ? "Close" : "Edit"}
                                </Button>
                                <Button
                                    size="sm"
                                    variant="ghost"
                                    disabled={busy || club.member_count > 0}
                                    title={club.member_count > 0
                                        ? "Remove every member first"
                                        : "Delete this club"}
                                    onclick={() => remove(club)}
                                >
                                    Delete
                                </Button>
                            </div>
                        </div>

                        {#if expanded === club.id}
                            <div
                                class="flex flex-col gap-4 rounded-md bg-muted/40 p-3"
                            >
                                <div class="grid gap-2">
                                    <Label for={`name-${club.id}`}>Name</Label>
                                    <Input
                                        id={`name-${club.id}`}
                                        value={club.name}
                                        onblur={(e) =>
                                            rename(
                                                club,
                                                e.currentTarget.value,
                                            )}
                                    />
                                </div>

                                <div class="grid gap-2">
                                    <Label>Owner</Label>
                                    <div class="flex flex-wrap gap-1.5">
                                        {#each membersOf[club.id] ?? [] as member (member.id)}
                                            <button
                                                type="button"
                                                disabled={busy}
                                                class="rounded-full border px-3 py-1 text-xs transition-colors
													{member.id === club.owner_id
                                                    ? 'bg-primary text-primary-foreground'
                                                    : 'hover:bg-accent'}"
                                                onclick={() =>
                                                    setOwner(club, member.id)}
                                            >
                                                {member.name}
                                            </button>
                                        {/each}
                                    </div>
                                </div>

                                <div class="grid gap-2">
                                    <Label>Venues</Label>
                                    <div class="flex flex-wrap gap-1.5">
                                        {#each venues as venue (venue)}
                                            <button
                                                type="button"
                                                disabled={busy}
                                                class="rounded-full border px-3 py-1 text-xs transition-colors
													{club.locations.includes(venue)
                                                    ? 'bg-primary text-primary-foreground'
                                                    : 'hover:bg-accent'}"
                                                onclick={() =>
                                                    toggleVenue(club, venue)}
                                            >
                                                {venue}
                                            </button>
                                        {/each}
                                    </div>
                                    <p class="text-xs text-muted-foreground">
                                        {club.locations.length === 0
                                            ? "Nothing selected — this club sees every venue."
                                            : "Only the selected venues are shown to this club."}
                                    </p>
                                </div>

                                <Separator />

                                <div class="grid gap-2">
                                    <Label>Members</Label>
                                    <div class="flex flex-col divide-y">
                                        {#each membersOf[club.id] ?? [] as member (member.id)}
                                            <div
                                                class="flex items-center gap-2 py-1.5 text-sm"
                                            >
                                                <span>{member.name}</span>
                                                {#if member.id === club.owner_id}
                                                    <Badge variant="outline"
                                                        >owner</Badge
                                                    >
                                                {/if}
                                                <Button
                                                    size="sm"
                                                    variant="ghost"
                                                    class="ml-auto"
                                                    disabled={busy ||
                                                        member.id ===
                                                            club.owner_id}
                                                    onclick={() =>
                                                        removeMember(
                                                            club,
                                                            member.id,
                                                        )}
                                                >
                                                    Remove
                                                </Button>
                                            </div>
                                        {/each}
                                    </div>
                                </div>

                                {#if notIn(club).length > 0}
                                    <div class="grid gap-2">
                                        <Label>Add a member</Label>
                                        <div class="flex flex-wrap gap-1.5">
                                            {#each notIn(club) as member (member.id)}
                                                <button
                                                    type="button"
                                                    disabled={busy}
                                                    class="rounded-full border px-3 py-1 text-xs transition-colors hover:bg-accent"
                                                    onclick={() =>
                                                        addMember(
                                                            club,
                                                            member.id,
                                                        )}
                                                >
                                                    + {member.name}
                                                </button>
                                            {/each}
                                        </div>
                                    </div>
                                {/if}
                            </div>
                        {/if}
                    </div>
                {/each}
            </Card.Content>
        </Card.Root>
    {/if}
</div>
