<script lang="ts">
    import { goto } from "$app/navigation";
    import { api, type SlotGroup } from "$lib/api";
    import { ensureSlots, sync } from "$lib/sync.svelte";
    import AvailabilityFilters from "$lib/components/AvailabilityFilters.svelte";
    import { Badge } from "$lib/components/ui/badge";
    import { Button } from "$lib/components/ui/button";
    import * as Card from "$lib/components/ui/card";
    import { Checkbox } from "$lib/components/ui/checkbox";
    import { Input } from "$lib/components/ui/input";
    import { Skeleton } from "$lib/components/ui/skeleton";
    import { formatDate, formatTimeRange, formatTimestamp } from "$lib/format";
    import { toast } from "svelte-sonner";

    let showFilters = $state(false);

    const loading = $derived(sync.slotGroups === null);
    const refreshing = $derived(sync.scraping);

    let selecting = $state(false);
    let selected = $state<Record<string, SlotGroup>>({});
    let pollTitle = $state("");
    let creating = $state(false);

    const selectedCount = $derived(Object.keys(selected).length);

    const byDate = $derived.by(() => {
        const groups: { date: string; slots: SlotGroup[] }[] = [];
        for (const slot of sync.slotGroups ?? []) {
            const last = groups.at(-1);
            if (last?.date === slot.date) last.slots.push(slot);
            else groups.push({ date: slot.date, slots: [slot] });
        }
        return groups;
    });

    function slotKey(s: SlotGroup): string {
        return `${s.date}|${s.time}|${s.duration_minutes}|${s.location}`;
    }

    // The slot list is cached in the sync store; this only hits the network
    // when a new scrape snapshot landed or my filters changed since the last
    // fetch — plain navigation back to this page costs no request.
    $effect(() => {
        ensureSlots().catch(() => toast.error("Could not load slots"));
    });

    async function refresh() {
        try {
            const res = await api.post<{ started: boolean }>(
                "/api/slots/refresh",
            );
            if (!res.started)
                toast.info("Slots were already refreshed less than a minute ago");
        } catch {
            toast.error("Could not refresh");
        }
    }

    function toggle(s: SlotGroup) {
        const key = slotKey(s);
        if (selected[key]) delete selected[key];
        else selected[key] = s;
    }

    async function createPoll() {
        creating = true;
        try {
            const slots = Object.values(selected).map((s) => ({
                date: s.date,
                time: s.time,
                duration_minutes: s.duration_minutes,
                location: s.location,
                courts: s.courts,
                min_price: s.min_price,
                currency: s.currency,
            }));
            await api.post("/api/polls", { title: pollTitle, slots });
            toast.success("Slot poll started!");
            selecting = false;
            selected = {};
            pollTitle = "";
            await goto("/polls");
        } catch {
            toast.error("Could not create the poll");
        } finally {
            creating = false;
        }
    }
</script>

<div class="flex flex-col gap-6 pb-24">
    <div class="flex flex-wrap items-center justify-between gap-3">
        <div>
            <h1 class="text-2xl font-semibold tracking-tight">
                Available slots
            </h1>
            <p class="text-sm text-muted-foreground">
                Free courts matching your filters.
                {#if sync.lastFetchedAt}
                    Updated {formatTimestamp(sync.lastFetchedAt)}.
                {/if}
            </p>
        </div>
        <div class="flex gap-2">
            <Button
                variant={showFilters ? "secondary" : "outline"}
                size="sm"
                onclick={() => (showFilters = !showFilters)}
            >
                ⚙ Filters
            </Button>
            <Button
                variant="outline"
                size="sm"
                onclick={refresh}
                disabled={refreshing}
            >
                {refreshing ? "Refreshing…" : "↻ Refresh"}
            </Button>
            {#if !selecting}
                <Button size="sm" onclick={() => (selecting = true)}
                    >+ Start slot poll</Button
                >
            {:else}
                <Button
                    variant="ghost"
                    size="sm"
                    onclick={() => {
                        selecting = false;
                        selected = {};
                    }}
                >
                    Cancel
                </Button>
            {/if}
        </div>
    </div>

    {#if showFilters}
        <!-- Saving updates sync.settings, which invalidates the slot cache. -->
        <AvailabilityFilters />
    {/if}

    {#if selecting}
        <Card.Root class="border-primary/50 bg-primary/5">
            <Card.Content class="text-sm">
                Select the slots you want to propose to the group, then give the
                poll a name below.
            </Card.Content>
        </Card.Root>
    {/if}

    {#if loading}
        <div class="flex flex-col gap-4">
            <Skeleton class="h-32 w-full" />
            <Skeleton class="h-32 w-full" />
        </div>
    {:else if byDate.length === 0}
        <Card.Root>
            <Card.Content class="py-10 text-center text-muted-foreground">
                {#if sync.scraping}
                    Fetching court availability…
                {:else}
                    No free slots match your availability window. Try widening
                    it under
                    <button
                        class="underline"
                        onclick={() => (showFilters = true)}>⚙ Filters</button
                    > or refresh.
                {/if}
            </Card.Content>
        </Card.Root>
    {:else}
        {#each byDate as day (day.date)}
            <Card.Root>
                <Card.Header>
                    <Card.Title class="text-base"
                        >📅 {formatDate(day.date)}</Card.Title
                    >
                </Card.Header>
                <Card.Content class="flex flex-col divide-y">
                    {#each day.slots as slot (slotKey(slot))}
                        {@const key = slotKey(slot)}
                        <label
                            class="flex cursor-pointer items-center gap-3 py-2.5 first:pt-0 last:pb-0
								{selecting ? 'hover:bg-accent/40' : 'cursor-default'} {selected[key]
                                ? 'bg-accent/60'
                                : ''} rounded-sm px-1"
                        >
                            {#if selecting}
                                <Checkbox
                                    checked={!!selected[key]}
                                    onCheckedChange={() => toggle(slot)}
                                />
                            {/if}
                            <div
                                class="flex min-w-0 flex-1 flex-wrap items-center gap-x-3 gap-y-1"
                            >
                                <span class="font-medium tabular-nums">
                                    {formatTimeRange(
                                        slot.time,
                                        slot.duration_minutes,
                                    )}
                                </span>
                                <Badge variant="outline"
                                    >{slot.duration_minutes} min</Badge
                                >
                                <span
                                    class="truncate text-sm text-muted-foreground"
                                    >{slot.location}</span
                                >
                            </div>
                            <div
                                class="flex shrink-0 items-center gap-2 text-sm text-muted-foreground"
                            >
                                <span title={slot.courts.join(", ")}>
                                    {slot.courts.length}
                                    {slot.courts.length === 1
                                        ? "court"
                                        : "courts"}
                                </span>
                                <span class="font-medium text-foreground">
                                    from {slot.min_price.toFixed(0)} €
                                </span>
                            </div>
                        </label>
                    {/each}
                </Card.Content>
            </Card.Root>
        {/each}
    {/if}
</div>

{#if selecting}
    <!-- Sticky poll creation bar -->
    <div
        class="fixed inset-x-0 bottom-0 border-t bg-background/95 p-3 backdrop-blur md:left-60"
    >
        <div
            class="mx-auto flex w-full max-w-4xl flex-wrap items-center gap-2 px-1 md:px-4"
        >
            <Input
                class="min-w-40 flex-1"
                placeholder="Poll name, e.g. “Padel next week?”"
                bind:value={pollTitle}
            />
            <Button
                onclick={createPoll}
                disabled={selectedCount === 0 || creating}
            >
                {creating
                    ? "Starting…"
                    : `Start poll with ${selectedCount} ${selectedCount === 1 ? "slot" : "slots"}`}
            </Button>
        </div>
    </div>
{/if}
