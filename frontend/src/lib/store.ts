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
  let settings: Settings;
  let allLocations: string[];

  if (!force && currentDataState.user_settings != null) {
    return;
  }

  appDataStore.update((s) => ({ ...s, loading: true }));

  // fill settings and locations
  await Promise.all([
    api.get<Settings>("/api/settings"),
    api.get<string[]>("/api/locations"),
  ])
    .then(([s, locs]) => {
      settings = s;
      // Keep previously selected locations visible even if they are
      // not in the current scrape (e.g. fully booked right now).
      allLocations = [...new Set([...locs, ...s.locations])].sort();

      appDataStore.set({
        user_settings: settings,
        loading: false,
        locations: allLocations,
      });
    })
    .catch(() => {
      console.error("Could not load your settings and locations");
      appDataStore.update((s) => ({ ...s, loading: false }));
    });
}
