import {
  api,
  type Invite,
  type Settings,
  type SlotGroup,
  type User,
} from "./api";

// Client side of the sync engine (see backend/sync.go). The UI renders
// exclusively from this normalized store: it is hydrated once via
// /api/sync/bootstrap and kept current by the SSE delta stream, so pages
// never poll. Mutations go through the helpers at the bottom, which apply
// optimistically and let the authoritative delta confirm.

// --- Entities mirrored from the server's sync log ---

export interface SyncPollSlot {
  id: number;
  date: string;
  time: string;
  duration_minutes: number;
  location: string;
  court: string;
  price: number;
  currency: string;
}

export interface SyncPoll {
  id: number;
  title: string;
  creator_id: number;
  creator_name: string;
  status: "active" | "closed";
  winning_slot_id: number | null;
  created_at: string;
  closed_at: string | null;
  slots: SyncPollSlot[];
}

export interface SyncVote {
  poll_slot_id: number;
  user_id: number;
  name: string;
  vote: boolean;
}

export interface SyncMember {
  id: number;
  name: string;
  is_admin: boolean;
}

interface Bootstrap {
  sync_id: number;
  users: SyncMember[];
  settings: Settings;
  polls: SyncPoll[];
  votes: SyncVote[];
  invites: Invite[] | null; // null for non-admins
  slot_keys: string[];
  last_fetched_at: string;
  scraping: boolean;
}

interface Delta {
  id: number;
  entity: string;
  entity_id: string;
  action: "upsert" | "delete";
  payload?: unknown;
}

export const sync = $state({
  ready: false, // bootstrap done
  live: false, // SSE stream connected
  polls: {} as Record<number, SyncPoll>,
  votes: {} as Record<string, SyncVote>, // key: `${poll_slot_id}|${user_id}`
  members: {} as Record<number, SyncMember>,
  invites: {} as Record<string, Invite>, // key: token; admins only
  settings: null as Settings | null,
  // `${date}|${time}|${duration}|${location}` keys of the latest scrape,
  // used to derive whether a poll slot is still bookable.
  slotKeys: {} as Record<string, true>,
  lastFetchedAt: "",
  scraping: false,
  slotsVersion: 0, // bumped per scrape snapshot — invalidates the slotGroups cache
  // Cached /api/slots view (filtered by my settings). Lives here, not in the
  // page, so navigating back to /slots doesn't refetch anything.
  slotGroups: null as SlotGroup[] | null,
});

let syncId = 0; // resume cursor — last applied sync_log id
let es: EventSource | null = null;
let started = false;

// The server sends a `ping` event every 5 s. EventSource detects clean breaks
// by itself, but a silently dead connection (sleep/wake, Wi-Fi switch, quiet
// proxy) can linger — so we track when we last heard *anything* and force a
// reconnect after three missed pings.
const STALE_AFTER_MS = 15_000;
let lastHeard = 0;
let watchdog: ReturnType<typeof setInterval> | null = null;

// Set `localStorage.sync_debug = 1` in the console to log every sync message;
// `localStorage.removeItem('sync_debug')` turns it off. Checked per call, so
// it takes effect without a reload.
function debug(...args: unknown[]) {
  if (localStorage.getItem("sync_debug")) console.log("[sync]", ...args);
}

export function voteKey(slotId: number, userId: number): string {
  return `${slotId}|${userId}`;
}

export async function startSync() {
  if (started) return;
  started = true;
  while (started) {
    try {
      await bootstrap();
      break;
    } catch {
      await new Promise((r) => setTimeout(r, 5000));
    }
  }
  if (started) {
    connect();
    watchdog = setInterval(() => {
      if (!es) return;
      if (Date.now() - lastHeard > STALE_AFTER_MS) {
        debug("connection stale — forcing reconnect");
        sync.live = false;
        connect();
      }
    }, 5_000);
  }
}

export function stopSync() {
  started = false;
  if (watchdog) clearInterval(watchdog);
  watchdog = null;
  es?.close();
  es = null;
  sync.ready = false;
  sync.live = false;
}

async function bootstrap() {
  const b = await api.get<Bootstrap>("/api/sync/bootstrap");
  syncId = b.sync_id;
  sync.polls = Object.fromEntries(b.polls.map((p) => [p.id, p]));
  sync.votes = Object.fromEntries(
    b.votes.map((v) => [voteKey(v.poll_slot_id, v.user_id), v]),
  );
  sync.members = Object.fromEntries(b.users.map((m) => [m.id, m]));
  sync.invites = Object.fromEntries((b.invites ?? []).map((i) => [i.token, i]));
  sync.settings = b.settings;
  sync.slotKeys = Object.fromEntries(
    b.slot_keys.map((k) => [k, true as const]),
  );
  sync.lastFetchedAt = b.last_fetched_at;
  sync.scraping = b.scraping;
  sync.slotsVersion++;
  sync.ready = true;
  debug("bootstrap", {
    sync_id: b.sync_id,
    polls: b.polls.length,
    votes: b.votes.length,
    users: b.users.length,
    invites: b.invites?.length ?? 0,
    slot_keys: b.slot_keys.length,
  });

  lastHeard = Date.now();
  sync.live = true;
}

function connect() {
  es?.close();
  // The browser resends `Last-Event-ID` on automatic reconnects and the
  // server replays what we missed; last_id covers the first connect.
  lastHeard = Date.now(); // grace period while connecting
  es = new EventSource(`/api/sync/events?last_id=${syncId}`);
  es.addEventListener("delta", (e) => {
    lastHeard = Date.now();
    try {
      const d = JSON.parse(e.data) as Delta;
      debug("delta", d);
      applyDelta(d);
    } catch {
      // ignore malformed events
    }
  });
  es.addEventListener("ping", () => {
    lastHeard = Date.now();
    sync.live = true;
  });
  // The server compacted its log past our cursor — start over.
  es.addEventListener("reset", () => {
    debug("reset — log compacted past our cursor, re-bootstrapping");
    void bootstrap().catch(() => {});
  });
  es.onopen = () => {
    lastHeard = Date.now();
    debug("stream connected", { last_id: syncId });
    sync.live = true;
  };
  es.onerror = () => {
    debug("stream disconnected");
    sync.live = false;
    // EventSource gives up permanently on HTTP errors; retry ourselves.
    if (started && es?.readyState === EventSource.CLOSED) {
      setTimeout(() => {
        if (started) connect();
      }, 5000);
    }
  };
}

function applyDelta(d: Delta) {
  if (d.id > 0) syncId = d.id;
  switch (d.entity) {
    case "poll": {
      if (d.action === "delete") {
        const poll = sync.polls[Number(d.entity_id)];
        if (!poll) return;
        const slotIds = new Set(poll.slots.map((s) => s.id));
        for (const [key, v] of Object.entries(sync.votes)) {
          if (slotIds.has(v.poll_slot_id)) delete sync.votes[key];
        }
        delete sync.polls[poll.id];
      } else {
        const p = d.payload as SyncPoll;
        sync.polls[p.id] = p;
      }
      break;
    }
    case "vote": {
      if (d.action === "delete") delete sync.votes[d.entity_id];
      else {
        const v = d.payload as SyncVote;
        sync.votes[voteKey(v.poll_slot_id, v.user_id)] = v;
      }
      break;
    }
    case "user": {
      if (d.action === "delete") delete sync.members[Number(d.entity_id)];
      else {
        const m = d.payload as SyncMember;
        sync.members[m.id] = m;
      }
      break;
    }
    case "invite": {
      if (d.action === "delete") delete sync.invites[d.entity_id];
      else {
        const i = d.payload as Invite;
        sync.invites[i.token] = i;
      }
      break;
    }
    case "settings":
      sync.settings = d.payload as Settings;
      break;
    case "slots": {
      const p = d.payload as { keys: string[] | null; last_fetched_at: string };
      sync.slotKeys = Object.fromEntries(
        (p.keys ?? []).map((k) => [k, true as const]),
      );
      sync.lastFetchedAt = p.last_fetched_at;
      sync.scraping = false;
      sync.slotsVersion++;
      break;
    }
    case "scrape":
      sync.scraping = (d.payload as { scraping: boolean }).scraping;
      break;
  }
}

// --- Derived helpers ---

export function slotVotes(slotId: number): SyncVote[] {
  return Object.values(sync.votes)
    .filter((v) => v.poll_slot_id === slotId)
    .sort((a, b) => a.name.localeCompare(b.name));
}

// Whether a poll slot is still bookable according to the latest scrape.
export function slotAvailable(s: SyncPollSlot): boolean {
  return (
    `${s.date}|${s.time}|${s.duration_minutes}|${s.location}` in sync.slotKeys
  );
}

let slotsFetchedFor = ""; // cache key of the last /api/slots fetch

// ensureSlots refreshes sync.slotGroups, but only when the underlying data
// changed since the last fetch: a new scrape snapshot (slotsVersion) or my
// filter settings. Call it from a component $effect — the reads below happen
// synchronously, so the effect reruns when either input changes.
export async function ensureSlots(): Promise<void> {
  if (!sync.ready) return; // bootstrap bumps slotsVersion and re-triggers
  const key = `${sync.slotsVersion}|${JSON.stringify(sync.settings)}`;
  if (key === slotsFetchedFor) return;
  slotsFetchedFor = key; // set before awaiting so concurrent calls dedupe
  try {
    const res = await api.get<{ slots: SlotGroup[] }>("/api/slots");
    sync.slotGroups = res.slots;
  } catch (err) {
    slotsFetchedFor = ""; // retry on the next trigger
    throw err;
  }
}

// --- Mutations (optimistic; the authoritative delta confirms shortly after) ---

export async function castVote(
  pollId: number,
  slotId: number,
  value: boolean | null,
  me: User,
): Promise<void> {
  const key = voteKey(slotId, me.id);
  const prev = sync.votes[key];
  debug("optimistic vote", { poll_slot_id: slotId, vote: value });
  if (value === null) delete sync.votes[key];
  else
    sync.votes[key] = {
      poll_slot_id: slotId,
      user_id: me.id,
      name: me.name,
      vote: value,
    };
  try {
    await api.post(`/api/polls/${pollId}/vote`, {
      poll_slot_id: slotId,
      vote: value,
    });
  } catch (err) {
    // Server rejected it (e.g. poll just closed) — roll back.
    debug("vote rejected — rolling back", { poll_slot_id: slotId });
    if (prev) sync.votes[key] = prev;
    else delete sync.votes[key];
    throw err;
  }
}

export async function closePoll(
  pollId: number,
  winningSlotId: number | null,
): Promise<void> {
  await api.post(`/api/polls/${pollId}/close`, {
    winning_slot_id: winningSlotId,
  });
  const p = sync.polls[pollId];
  if (p) {
    p.status = "closed";
    p.winning_slot_id = winningSlotId;
  }
}

export async function deletePoll(pollId: number): Promise<void> {
  await api.del(`/api/polls/${pollId}`);
  applyDelta({
    id: 0,
    entity: "poll",
    entity_id: String(pollId),
    action: "delete",
  });
}
