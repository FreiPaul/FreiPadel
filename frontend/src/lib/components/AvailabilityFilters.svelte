<script lang="ts">
    import { api, type Settings } from "$lib/api";
    import { sync } from "$lib/sync.svelte";
    import { Button } from "$lib/components/ui/button";
    import * as Card from "$lib/components/ui/card";
    import { Input } from "$lib/components/ui/input";
    import { Label } from "$lib/components/ui/label";
    import { Skeleton } from "$lib/components/ui/skeleton";
    import { toast } from "svelte-sonner";

    let { onSaved }: { onSaved?: () => void } = $props();

    const WEEKDAYS = ["Mo", "Tu", "We", "Th", "Fr", "Sa", "Su"];

    let settings = $state<Settings | null>(null);
    let saving = $state(false);

    // Editable copy, seeded once the sync store is hydrated.
    $effect(() => {
        if (!settings && sync.settings) {
            settings = $state.snapshot(sync.settings) as Settings;
        }
    });

    // Locations known from the latest scrape (the location is the last part
    // of each slot key), plus previously selected ones so they stay visible
    // even when fully booked right now.
    const allLocations = $derived.by(() => {
        const locs = new Set<string>();
        for (const k of Object.keys(sync.slotKeys)) {
            locs.add(k.split("|").slice(3).join("|"));
        }
        for (const l of sync.settings?.locations ?? []) locs.add(l);
        return [...locs].sort();
    });

    function toggleDay(day: number) {
        if (!settings) return;
        settings.weekdays = settings.weekdays.includes(day)
            ? settings.weekdays.filter((d) => d !== day)
            : [...settings.weekdays, day].sort((a, b) => a - b);
    }

    function toggleLocation(loc: string) {
        if (!settings) return;
        settings.locations = settings.locations.includes(loc)
            ? settings.locations.filter((l) => l !== loc)
            : [...settings.locations, loc];
    }

    async function save(e: SubmitEvent) {
        e.preventDefault();
        if (!settings) return;
        saving = true;
        try {
            settings = await api.put<Settings>("/api/settings", settings);
            // Apply to the store right away instead of waiting for the SSE
            // delta — the delta then confirms with identical content.
            sync.settings = $state.snapshot(settings) as Settings;
            toast.success("Availability saved");
            onSaved?.();
        } catch (err) {
            toast.error(err instanceof Error ? err.message : "Could not save");
        } finally {
            saving = false;
        }
    }
</script>

{#if !settings}
    <Skeleton class="h-64 w-full" />
{:else}
    <form onsubmit={save}>
        <Card.Root>
            <Card.Header>
                <Card.Title class="text-base">My availability</Card.Title>
                <Card.Description>
                    When and where you can usually play — the overview only
                    shows matching courts.
                </Card.Description>
            </Card.Header>
            <Card.Content class="flex flex-col gap-5">
                <div class="flex flex-col gap-2">
                    <Label>Weekdays</Label>
                    <div class="flex flex-wrap gap-1.5">
                        {#each WEEKDAYS as day, i (day)}
                            {@const on = settings.weekdays.includes(i)}
                            <button
                                type="button"
                                class="rounded-full border px-3.5 py-1.5 text-sm font-medium transition-colors
									{on
                                    ? 'border-primary bg-primary text-primary-foreground'
                                    : 'bg-background text-muted-foreground hover:bg-accent'}"
                                onclick={() => toggleDay(i)}
                            >
                                {day}
                            </button>
                        {/each}
                    </div>
                </div>

                <div class="flex flex-col gap-2">
                    <Label>
                        Locations
                        <span class="font-normal text-muted-foreground"
                            >(none selected = all)</span
                        >
                    </Label>
                    <div class="flex flex-wrap gap-1.5">
                        {#each allLocations as loc (loc)}
                            {@const on = settings.locations.includes(loc)}
                            <button
                                type="button"
                                class="rounded-full border px-3.5 py-1.5 text-sm font-medium transition-colors
									{on
                                    ? 'border-primary bg-primary text-primary-foreground'
                                    : 'bg-background text-muted-foreground hover:bg-accent'}"
                                onclick={() => toggleLocation(loc)}
                            >
                                {loc}
                            </button>
                        {:else}
                            <p class="text-sm text-muted-foreground">
                                No locations known yet — refresh the slots
                                first.
                            </p>
                        {/each}
                    </div>
                </div>

                <div class="grid grid-cols-2 gap-4 md:grid-cols-4">
                    <div class="flex flex-col gap-2">
                        <Label for="start">Earliest start</Label>
                        <Input
                            id="start"
                            type="time"
                            bind:value={settings.time_start}
                            required
                        />
                    </div>
                    <div class="flex flex-col gap-2">
                        <Label for="end">Latest start</Label>
                        <Input
                            id="end"
                            type="time"
                            bind:value={settings.time_end}
                            required
                        />
                    </div>
                    <div class="flex flex-col gap-2">
                        <Label for="days">Days ahead</Label>
                        <Input
                            id="days"
                            type="number"
                            min="1"
                            max="21"
                            bind:value={settings.days_ahead}
                            required
                        />
                    </div>
                    <div class="flex flex-col gap-2">
                        <Label for="duration">Min. duration</Label>
                        <Input
                            id="duration"
                            type="number"
                            min="30"
                            max="240"
                            step="30"
                            bind:value={settings.min_duration}
                            required
                        />
                    </div>
                </div>
            </Card.Content>
            <Card.Footer>
                <Button
                    type="submit"
                    disabled={saving || settings.weekdays.length === 0}
                >
                    {saving ? "Saving…" : "Save availability"}
                </Button>
            </Card.Footer>
        </Card.Root>
    </form>
{/if}
