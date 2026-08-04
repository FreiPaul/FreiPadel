# 🎾 FreiPadel

Find padel slots where enough people from your group have time.

FreiPadel scrapes free court slots from pluggable booking providers (see
`backend/scraper/README.md`), shows each member the slots matching their
personal availability window, and lets
anyone start a **slot poll**: pick a few candidate slots, everyone votes
"I have time" / "no time" per slot, and slots where 4+ people can play get
highlighted. The poll creator then closes the poll, picks the winning slot
and books the court.

## Running in Docker

```sh
docker compose up -d --build
```

The app listens on **http://localhost:8080**. SQLite database and the scraper
config live in `./data/` (created on first start).

- **First user**: open the app, register without an invite — this account
  becomes the **admin**.
- **Inviting friends**: as admin, go to *Invites* → *New invite link* → send
  the copied link. Each link works exactly once.
- **Scraper config**: edit `data/config.json` (sources, days ahead, scrape
  window, timezone) and restart the container.
- Serving over HTTPS behind a reverse proxy? Set `COOKIE_SECURE: "1"` in
  `docker-compose.yml`.

## Production deployment

Copy the SMTP credentials into the Git-ignored `production.env` file at the
repository root, then run `make ship` (or `make ship-local-build`). The deploy
uploads that file separately to `/opt/freipadel/data/production.env`, sets its
permissions to `0600`, and references it from the production Compose file.

The ship targets stop before changing the server if `SMTP_HOST`, `SMTP_USER`,
or `SMTP_PASS` is missing. Do not add credentials to
`docker-compose-prod.yml`; `.env` files are excluded from both the source
archive and Docker build context.

## Environment variables

| Variable                  | Default    | Meaning                              |
| ------------------------- | ---------- | ------------------------------------ |
| `PORT`                    | `8080`     | HTTP port                            |
| `DATA_DIR`                | `./data`   | SQLite db + `config.json` location   |
| `STATIC_DIR`              | `./static` | Built frontend to serve              |
| `SCRAPE_INTERVAL_MINUTES` | `30`       | Court availability refresh interval  |
| `COOKIE_SECURE`           | `0`        | Set `1` when serving over HTTPS      |
| `EMAILER_ENABLED`         | —          | Wether the emailer is enabled        |
| `SMTP_HOST`               | —          | SMTP server hostname                 |
| `SMTP_PORT`               | `587`      | SMTP submission port                 |
| `SMTP_USER`               | —          | SMTP authentication username         |
| `SMTP_PASS`               | —          | SMTP authentication password         |
| `MAIL_FROM`               | SMTP user  | Sender email address                 |

## How slot polls work

1. Set your availability under **My availability** (e.g. weekdays 19:00–21:00).
2. **Available slots** shows free courts matching *your* window, grouped by
   date — courts at the same date/time/location are collapsed into one row.
3. Hit **Start slot poll**, tick candidate slots, name the poll.
4. Everyone sees it under **Active slot polls** and votes 👍/👎 per slot.
   Slots with **4+ yes votes** turn green.
5. The poll creator closes the poll and picks the slot to book — booking
   itself happens on the court provider's own site as usual.
