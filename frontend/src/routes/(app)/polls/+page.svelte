<script lang="ts">
    import { onMount } from "svelte";
    import { PLAYERS_NEEDED } from "$lib/api";
    import { auth } from "$lib/auth.svelte";
    import {
        castVote,
        closePoll,
        deletePoll,
        slotAvailable,
        slotVotes,
        sync,
        type SyncPoll,
        type SyncPollSlot,
        type SyncVote,
    } from "$lib/sync.svelte";
    import { Badge } from "$lib/components/ui/badge";
    import { Button } from "$lib/components/ui/button";
    import * as Card from "$lib/components/ui/card";
    import * as Dialog from "$lib/components/ui/dialog";
    import { Separator } from "$lib/components/ui/separator";
    import { Skeleton } from "$lib/components/ui/skeleton";
    import { formatDate, formatTimeRange } from "$lib/format";
    import { toast } from "svelte-sonner";

    // View models: sync entities enriched with the fields the markup needs,
    // all derived client-side from the store.
    type SlotView = SyncPollSlot & {
        votes: SyncVote[];
        yes_count: number;
        no_count: number;
        my_vote: boolean | null;
        available: boolean;
        expired: boolean;
    };
    type PollView = Omit<SyncPoll, "slots"> & { slots: SlotView[] };

    // Clock for "expired" — refreshed every 30 s so slots flip without a reload.
    let now = $state(new Date());
    onMount(() => {
        const id = setInterval(() => (now = new Date()), 30_000);
        return () => clearInterval(id);
    });
    const today = $derived(
        `${now.getFullYear()}-${String(now.getMonth() + 1).padStart(2, "0")}-${String(now.getDate()).padStart(2, "0")}`,
    );
    const nowTime = $derived(
        `${String(now.getHours()).padStart(2, "0")}:${String(now.getMinutes()).padStart(2, "0")}`,
    );

    function enrich(p: SyncPoll): PollView {
        const slots = p.slots.map((s) => {
            const votes = slotVotes(s.id);
            const mine = votes.find((v) => v.user_id === auth.user?.id);
            const expired =
                s.date < today || (s.date === today && s.time <= nowTime);
            return {
                ...s,
                votes,
                yes_count: votes.filter((v) => v.vote).length,
                no_count: votes.filter((v) => !v.vote).length,
                my_vote: mine ? mine.vote : null,
                expired,
                available:
                    !expired &&
                    slotAvailable(s) &&
                    !s.court.toLowerCase().includes("single"),
            };
        });
        return { ...p, slots };
    }

    const polls = $derived(
        Object.values(sync.polls)
            .map(enrich)
            .sort((a, b) =>
                a.status === b.status
                    ? b.created_at.localeCompare(a.created_at)
                    : a.status === "active"
                      ? -1
                      : 1,
            ),
    );
    const active = $derived(polls.filter((p) => p.status === "active"));
    const closed = $derived(polls.filter((p) => p.status === "closed"));
    const loading = $derived(!sync.ready);

    let closeTargetId = $state<number | null>(null);
    let winnerID = $state<number | null>(null);
    // Derived from the live store, so the dialog updates while votes come in.
    const closeTarget = $derived(
        polls.find((p) => p.id === closeTargetId) ?? null,
    );

    async function vote(poll: PollView, slot: SlotView, value: boolean) {
        if (!auth.user) return;
        // Clicking the same answer again clears the vote.
        const newVote = slot.my_vote === value ? null : value;
        try {
            await castVote(poll.id, slot.id, newVote, auth.user);
        } catch {
            toast.error("Could not save your vote");
        }
    }

    function openClose(poll: PollView) {
        closeTargetId = poll.id;
        // Preselect the best slot if one is playable.
        const best = [...poll.slots].sort(
            (a, b) => b.yes_count - a.yes_count,
        )[0];
        winnerID = best && best.yes_count >= PLAYERS_NEEDED ? best.id : null;
    }

    async function confirmClose() {
        if (closeTargetId === null) return;
        try {
            await closePoll(closeTargetId, winnerID);
            toast.success("Poll closed");
            closeTargetId = null;
        } catch {
            toast.error("Could not close the poll");
        }
    }

    async function removePoll(poll: PollView) {
        if (!confirm(`Delete poll “${poll.title}”?`)) return;
        try {
            await deletePoll(poll.id);
        } catch {
            toast.error("Could not delete the poll");
        }
    }

    function canManage(poll: PollView): boolean {
        return auth.user?.id === poll.creator_id || !!auth.user?.is_admin;
    }
</script>

{#snippet slotRow(poll: PollView, slot: SlotView)}
    {@const playable = slot.yes_count >= PLAYERS_NEEDED}
    {@const isWinner = poll.winning_slot_id === slot.id}
    {@const gone = poll.status === "active" && !slot.expired && !slot.available}
    <div
        class="flex flex-col gap-2 rounded-md border p-3
			{isWinner
            ? 'border-green-600 bg-green-50 dark:bg-green-950/40'
            : gone || (poll.status === 'active' && slot.expired)
              ? 'border-dashed opacity-75'
              : playable && poll.status === 'active'
                ? 'border-green-500/60 bg-green-50/60 dark:bg-green-950/20'
                : ''}"
    >
        <div class="flex flex-wrap items-center gap-x-3 gap-y-1">
            <span class="font-medium" class:line-through={gone}
                >{formatDate(slot.date)}</span
            >
            <span class="font-medium tabular-nums" class:line-through={gone}>
                {formatTimeRange(slot.time, slot.duration_minutes)}
            </span>
            <span class="text-sm text-muted-foreground">{slot.location}</span>
            {#if slot.price > 0}
                <span class="text-sm text-muted-foreground"
                    >from {slot.price.toFixed(0)} €</span
                >
            {/if}
            {#if isWinner}
                <Badge class="bg-green-600 text-white">🏆 Booked slot</Badge>
            {:else if poll.status === "active" && slot.expired}
                <Badge variant="secondary">⌛ in the past</Badge>
            {:else if gone}
                <Badge variant="destructive">⚠️ no longer bookable</Badge>
            {:else if playable}
                <Badge class="bg-green-600/90 text-white"
                    >✓ {PLAYERS_NEEDED}+ can play</Badge
                >
            {/if}
        </div>
        <div class="flex flex-wrap items-center justify-between gap-2">
            <div class="flex flex-wrap items-center gap-1.5 text-sm">
                <span class="font-medium text-green-700 dark:text-green-400"
                    >{slot.yes_count} yes</span
                >
                <span class="text-muted-foreground">·</span>
                <span class="text-muted-foreground">{slot.no_count} no</span>
                {#each slot.votes as v (v.user_id)}
                    <Badge
                        variant={v.vote ? "secondary" : "outline"}
                        class={v.vote ? "" : "opacity-60"}
                    >
                        {v.vote ? "👍" : "👎"}
                        {v.name}
                    </Badge>
                {/each}
            </div>
            {#if poll.status === "active"}
                <div class="flex gap-1.5">
                    <Button
                        size="sm"
                        variant={slot.my_vote === true ? "default" : "outline"}
                        class={slot.my_vote === true
                            ? "bg-green-600 hover:bg-green-700"
                            : ""}
                        disabled={slot.expired}
                        onclick={() => vote(poll, slot, true)}
                    >
                        👍 I have time
                    </Button>
                    <Button
                        size="sm"
                        variant={slot.my_vote === false
                            ? "destructive"
                            : "outline"}
                        disabled={slot.expired}
                        onclick={() => vote(poll, slot, false)}
                    >
                        👎 No time
                    </Button>
                </div>
            {/if}
        </div>
    </div>
{/snippet}

{#snippet pollCard(poll: PollView)}
    <Card.Root class={poll.status === "closed" ? "opacity-80" : ""}>
        <Card.Header>
            <div class="flex flex-wrap items-start justify-between gap-2">
                <div>
                    <Card.Title class="text-base">{poll.title}</Card.Title>
                    <Card.Description>
                        started by {poll.creator_name}
                        {#if poll.status === "closed"}
                            · closed
                        {/if}
                    </Card.Description>
                </div>
                {#if canManage(poll)}
                    <div class="flex gap-1.5">
                        {#if poll.status === "active"}
                            <Button
                                size="sm"
                                variant="outline"
                                onclick={() => openClose(poll)}
                                >Close poll</Button
                            >
                        {/if}
                        <Button
                            size="sm"
                            variant="ghost"
                            onclick={() => removePoll(poll)}>🗑️</Button
                        >
                    </div>
                {/if}
            </div>
        </Card.Header>
        <Card.Content class="flex flex-col gap-2.5">
            {#each poll.slots as slot (slot.id)}
                {@render slotRow(poll, slot)}
            {/each}
        </Card.Content>
    </Card.Root>
{/snippet}

<div class="flex flex-col gap-6">
    <div>
        <h1 class="text-2xl font-semibold tracking-tight">Active slot polls</h1>
        <p class="text-sm text-muted-foreground">
            Vote yes or no on each proposed slot — {PLAYERS_NEEDED} yes votes make
            a match.
        </p>
    </div>

    {#if loading}
        <Skeleton class="h-40 w-full" />
    {:else if active.length === 0}
        <Card.Root>
            <Card.Content class="py-10 text-center text-muted-foreground">
                No active slot polls. Start one from
                <a href="/slots" class="underline">Available slots</a>.
            </Card.Content>
        </Card.Root>
    {:else}
        {#each active as poll (poll.id)}
            {@render pollCard(poll)}
        {/each}
    {/if}

    {#if closed.length > 0}
        <Separator />
        <h2 class="text-lg font-medium text-muted-foreground">Closed polls</h2>
        {#each closed as poll (poll.id)}
            {@render pollCard(poll)}
        {/each}
    {/if}
</div>

<Dialog.Root
    open={closeTarget !== null}
    onOpenChange={(open) => {
        if (!open) closeTargetId = null;
    }}
>
    <Dialog.Content>
        <Dialog.Header>
            <Dialog.Title>Close “{closeTarget?.title}”</Dialog.Title>
            <Dialog.Description>
                Optionally pick the slot you are going to book. Don't forget to
                actually book the court!
            </Dialog.Description>
        </Dialog.Header>
        <div class="flex flex-col gap-2">
            <label
                class="flex items-center gap-2 rounded-md border p-2.5 text-sm"
            >
                <input
                    type="radio"
                    name="winner"
                    checked={winnerID === null}
                    onchange={() => (winnerID = null)}
                />
                No winning slot — just close
            </label>
            {#each closeTarget?.slots ?? [] as slot (slot.id)}
                <label
                    class="flex items-center gap-2 rounded-md border p-2.5 text-sm
						{slot.yes_count >= PLAYERS_NEEDED ? 'border-green-500/60' : ''}"
                >
                    <input
                        type="radio"
                        name="winner"
                        checked={winnerID === slot.id}
                        onchange={() => (winnerID = slot.id)}
                    />
                    <span class="font-medium"
                        >{formatDate(slot.date)}
                        {formatTimeRange(
                            slot.time,
                            slot.duration_minutes,
                        )}</span
                    >
                    <span class="text-muted-foreground">{slot.location}</span>
                    {#if slot.expired}
                        <Badge variant="secondary">⌛ past</Badge>
                    {:else if !slot.available}
                        <Badge variant="destructive">⚠️ gone</Badge>
                    {/if}
                    <Badge variant="secondary" class="ml-auto"
                        >{slot.yes_count} yes</Badge
                    >
                </label>
            {/each}
        </div>
        <Dialog.Footer>
            <Button variant="outline" onclick={() => (closeTargetId = null)}
                >Cancel</Button
            >
            <Button onclick={confirmClose}>Close poll</Button>
        </Dialog.Footer>
    </Dialog.Content>
</Dialog.Root>
