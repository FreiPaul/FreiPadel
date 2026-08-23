# End-to-end tests

One journey through the real application: the production Docker image, a
[Mailpit](https://mailpit.axllent.org) inbox for the mail it sends, and a
`sqlite3` shell for checking what actually landed in the database.

```sh
make e2e-install   # once: npm ci + the Chromium build
make e2e           # or: cd e2e && npm test
```

The stack comes up and goes down around the run; nothing needs to be started
by hand. Ports are **8099** (app) and **8125** (Mailpit UI), deliberately not
the usual 8080/8025 so a dev instance can keep running alongside.

## What it covers

| Phase | File | What it proves |
| --- | --- | --- |
| 00 | `00-bootstrap.spec.ts` | Empty deployment, the startup scrape, the first account becoming admin |
| 10 | `10-invites.spec.ts` | One-time / group / emailed invites, every refusal path, three accounts joining |
| 20 | `20-settings.spec.ts` | Availability window and location filter, notification opt-in |
| 30 | `30-polls.spec.ts` | Creating a poll, voting live over SSE, closing on a winner, both mail fan-outs |
| 40 | `40-email-change.spec.ts` | Request → confirm, the notice to the old address, rate limiting, cancelling |
| 90 | `90-permissions.spec.ts` | Anonymous redirects, admin-only routes, non-owner refusals, logout |

## Things that are the way they are for a reason

**Database queries run inside a container, never on the host.** SQLite's WAL
index is shared memory backed by the `-shm` file, and that is *not* coherent
across a macOS bind mount (virtiofs / gRPC-FUSE). A reader on the host sees a
snapshot frozen at its first open, and — worse — checkpoints the WAL it cannot
see when it closes, silently discarding committed rows. So the data lives in a
named volume and `helpers/db.ts` execs `sqlite3` in the `sqlite` sidecar, which
shares that volume. Do not "simplify" this to a bind mount.

**One worker, no parallelism, no retries.** This is a single stateful journey
against one container. Retrying a test that already registered a user or closed
a poll cannot succeed; it would only bury the real failure. Files run in name
order and hand values to each other through `.tmp/scratch.json`.

**Dates come from the server, never from the runner's clock.** The container
runs in Europe/Berlin while CI runs in UTC, and slots on today's date disappear
from the list once their start time has passed. `availableDates()` reads them
from `/api/slots`, and the poll uses dates at index 2 and 3 so nothing can
expire mid-run.

**Mail assertions always wait.** Both notification fan-outs
(`notifyOnNewPoll`, `notifyOnSlotBooked`) run in a goroutine after the HTTP
response has been sent, so `waitForMail` / `waitForMailCount` poll.

**Chromium only.** The admin page copies invite links via
`navigator.clipboard`, and `clipboard-read` cannot be granted in Firefox or
WebKit. This suite tests the server stack, not browser compatibility.

**The scraper config is seeded before first boot.** `LoadConfig` only writes
its defaults when the file is absent, so `fixtures/config.json` is copied into
the volume by the one-shot `seed` service. It pins two mock sources, which is
what makes `SELECT COUNT(*) FROM slots` exactly 105.

## Debugging a failure

```sh
E2E_KEEP_STACK=1 npm test     # leave the containers up afterwards
npm run report                # open the HTML report (traces, video, screenshots)
docker compose -f docker-compose.e2e.yml logs app
open http://localhost:8125    # the Mailpit inbox, if the stack is still up
```

`.tmp/containers.log` is written on every run and uploaded as a CI artifact.
