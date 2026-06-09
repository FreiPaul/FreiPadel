import { writable, get } from "svelte/store";
import { api } from "./api";
import type { Invite, Poll, PollSlot, SlotGroup, Settings } from "./api";

interface DataState {
  user_settings: Settings | null;
  locations: string[] | null;
  loading: boolean;
}

export const appDataStore = writable<DataState>({
  user_settings: null,
  locations: null,
  loading: false,
});

export async function fetchData(force: boolean) {
  const currentDataState = get(appDataStore);

  if (
    (!force && currentDataState.user_settings != null) ||
    currentDataState.loading
  ) {
    return;
  }

  appDataStore.update((s) => ({ ...s, loading: true }));

  // fill settings and locations
  try {
    const [settings, locs] = await Promise.all([
      api.get<Settings>("/api/settings"),
      api.get<string[]>("/api/locations"),
    ]);
    // Keep previously selected locations visible even if they are
    // not in the current scrape (e.g. fully booked right now).
    const allLocations = [...new Set([...locs, ...settings.locations])].sort();

    appDataStore.set({
      user_settings: settings,
      loading: false,
      locations: allLocations,
    });
  } catch {
    console.error("Could not load your settings and locations");
    appDataStore.update((s) => ({ ...s, loading: false }));
  }
}
