# 🎾 FreiPadel

[![CI](https://github.com/FreiPaul/FreiPadel/actions/workflows/ci.yml/badge.svg?branch=main)](https://github.com/FreiPaul/FreiPadel/actions/workflows/ci.yml)

Find padel slots where enough people from your group have time.

FreiPadel scrapes free court slots from your club's booking site, shows every
member only the slots inside their own availability window, and lets anyone
start a poll on a handful of candidate slots. Slots that enough people vote
for are highlighted, and the poll creator books the winner.

## Features

- **Slot scraping from pluggable sources.** A source is a single file that
  registers itself and is selected at runtime from `config.json`. Adding one
  touches no shared code. See [backend/scraper](backend/scraper/README.md).
- **Personal availability.** Each member picks weekdays and a time window and
  only sees slots that match. Courts at the same date, time and location are
  collapsed into one row.
- **Slot polls.** Pick candidate slots, everyone votes per slot, slots with
  enough yes votes are highlighted. The creator closes the poll and books the
  winner.
- **Clubs.** Members belong to a club and switch between the clubs they are
  in. Each club has its own locations, members and invites.
- **Invite-only registration.** One-time links, group links that count uses,
  and links bound to one email address. The first account registers without an
  invite and becomes the admin.
- **Notifications** by email and Telegram when a poll is created or a slot is
  booked, per member and per kind.
- **One container.** Go backend, SvelteKit frontend, SQLite. Nothing else is
  required, and mail is optional.

## What it does not do

FreiPadel does not book courts. It finds the slots and organises the decision;
the booking itself happens on the provider's own site. It is built for a group
of people who already play together, not as a public court directory.

## Run it

```sh
docker compose up -d --build
```

The app listens on http://localhost:8080. The SQLite database and the scraper
configuration are created under `./data` on first start. Register the first
account without an invite to become the admin, then invite the others.

## Documentation

- [Usage](docs/usage.md): invites, availability, polls, clubs and notifications
- [Configuration](docs/configuration.md): environment variables and `config.json`
- [Deployment](docs/deployment.md): running it on a server
- [Development](docs/development.md): building, tests and the end-to-end suite
- [Scraper sources](backend/scraper/README.md): writing a source for your club
