# scraper

Fetches free padel court slots from one or more **sources** and merges them into
a single list. Sources are pluggable: each one lives in its own file, registers
itself, and is selected at runtime from `config.json`. Adding a source touches
no shared code.

## How a scrape is configured

`config.json` (created with defaults on first run) lists the sources to scrape:

```json
{
  "sources": [
    { "type": "mock", "options": { "location": "My Club" } }
  ],
  "days_ahead": 21,
  "time_start": "07:00",
  "time_end": "22:00",
  "timezone": "Europe/Berlin"
}
```

Each entry has a `type` (which connector to use) and an opaque `options` object
that only that connector understands. A `type` that isn't registered is a
startup error. The window settings (`days_ahead`, `time_start`/`time_end`,
`timezone`) are shared across all sources — per-user filtering happens later, at
query time in the API.

## Writing your own connector

A connector is a single new `.go` file in this package. There are three pieces:

1. **An options struct** — your connector's slice of `config.json`, decoded from
   the raw `options` JSON.
2. **A factory** `func(json.RawMessage) (Source, error)` — decodes and validates
   the options, returns a ready `Source` (or an error that fails startup).
3. **`Register("yourtype", yourFactory)` in `init()`** — wires the `type` string
   to the factory. This is the only thing that makes the connector selectable.

The `Source` interface is just:

```go
type Source interface {
	Fetch(ctx context.Context, w Window) ([]Slot, error)
}
```

In `Fetch`, loop the dates with `w.Dates()`, build a `Slot` per opening, and
call `w.Keep(slot)` so start-time bounds and the "no singles" rule stay
consistent with every other source. Return the slots you kept. A source whose
`Fetch` returns an error is logged and skipped; the scrape only fails if *every*
source fails.

See **`mock.go`** for a complete, network-free example you can copy from.

## The `mock` connector

`mock.go` is a reference connector that generates a handful of synthetic slots
with no network calls. It lets the app run end-to-end before you've written a
real connector, and doubles as the canonical example.

It is **not active by default** — like every connector it only self-registers.
It runs solely when `config.json` contains a source of `{"type": "mock"}`. All
its options are optional:

| option             | default              |
|--------------------|----------------------|
| `location`         | `"Mock Padel Club"`  |
| `courts`           | `["Court 1", "Court 2"]` |
| `times`            | `["18:00", "19:30"]` |
| `duration_minutes` | `90`                 |
| `price`            | `24`                 |
