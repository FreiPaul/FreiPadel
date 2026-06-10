<script lang="ts">
    import { goto } from "$app/navigation";
    import { page } from "$app/state";
    import { auth, loadUser, logout } from "$lib/auth.svelte";
    import { startSync, stopSync, sync } from "$lib/sync.svelte";
    import { Badge } from "$lib/components/ui/badge";
    import { Button } from "$lib/components/ui/button";
    import { Separator } from "$lib/components/ui/separator";
    import ThemeToggle from "$lib/components/ThemeToggle.svelte";

    let { children } = $props();

    $effect(() => {
        if (!auth.loaded) {
            loadUser().then((u) => {
                if (!u) goto("/login");
            });
        } else if (!auth.user) {
            goto("/login");
        } else {
            // Hydrate the sync store and open the delta stream once per session.
            startSync();
        }
    });

    const activePolls = $derived(
        Object.values(sync.polls).filter((p) => p.status === "active").length,
    );

    const tabs = $derived([
        { href: "/slots", label: "Available slots", icon: "🎾" },
        {
            href: "/polls",
            label: "Active slot polls",
            icon: "🗳️",
            badge: activePolls,
        },
        ...(auth.user?.is_admin
            ? [{ href: "/admin", label: "Invites", icon: "✉️" }]
            : []),
    ]);

    async function handleLogout() {
        stopSync();
        await logout();
        goto("/login");
    }
</script>

{#if auth.user}
    <div class="flex min-h-svh flex-col md:flex-row">
        <!-- Sidebar with vertical tabs -->
        <aside
            class="flex w-full shrink-0 flex-col border-b bg-sidebar md:sticky md:top-0 md:h-svh md:w-60 md:border-r md:border-b-0"
        >
            <div class="flex items-center gap-2 px-4 py-4">
                <span class="text-xl">🎾</span>
                <span class="text-lg font-semibold tracking-tight"
                    >FreiPadel</span
                >
                <div class="ml-auto flex items-center gap-3">
                    {#if sync.ready && !sync.live}
                        <span
                            class="flex items-center gap-1.5 text-xs text-amber-500"
                            title="Connection to the server lost — data on screen may be stale"
                        >
                            <span
                                class="size-2 animate-pulse rounded-full bg-amber-500"
                            ></span>
                            reconnecting…
                        </span>
                    {:else if sync.ready}
                        <span
                            class="size-2 rounded-full bg-green-500"
                            title="Live — changes from others appear instantly"
                        ></span>
                    {/if}
                    <ThemeToggle />
                </div>
            </div>
            <Separator class="hidden md:block" />
            <nav
                class="flex gap-1 overflow-x-auto px-2 py-2 md:flex-col md:py-3"
            >
                {#each tabs as tab (tab.href)}
                    {@const active = page.url.pathname.startsWith(tab.href)}
                    <a
                        href={tab.href}
                        class="flex shrink-0 items-center gap-2 rounded-md px-3 py-2 text-sm font-medium transition-colors
							{active
                            ? 'bg-sidebar-accent text-sidebar-accent-foreground'
                            : 'text-muted-foreground hover:bg-sidebar-accent/60 hover:text-sidebar-accent-foreground'}"
                    >
                        <span>{tab.icon}</span>
                        <span>{tab.label}</span>
                        {#if tab.badge}
                            <Badge
                                variant={active ? "default" : "secondary"}
                                class="ml-auto">{tab.badge}</Badge
                            >
                        {/if}
                    </a>
                {/each}
            </nav>
            <div class="mt-auto hidden flex-col gap-2 p-4 md:flex">
                <Separator />
                <div class="text-sm">
                    <div class="font-medium">{auth.user.name}</div>
                    <div class="truncate text-xs text-muted-foreground">
                        {auth.user.email}
                    </div>
                </div>
                <Button variant="outline" size="sm" onclick={handleLogout}
                    >Log out</Button
                >
            </div>
        </aside>

        <!-- Main content -->
        <main class="min-w-0 flex-1 bg-muted/20">
            <div class="mx-auto w-full max-w-4xl p-4 md:p-8">
                {@render children()}
            </div>
            <!-- Mobile logout -->
            <div class="p-4 text-center md:hidden">
                <Button variant="ghost" size="sm" onclick={handleLogout}>
                    Log out ({auth.user.name})
                </Button>
            </div>
        </main>
    </div>
{:else}
    <div
        class="flex min-h-svh items-center justify-center text-muted-foreground"
    >
        Loading…
    </div>
{/if}
