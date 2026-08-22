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
- **Email links**: set `PUBLIC_ORIGIN` to the deployment's canonical base URL
  (scheme + host, no trailing path). Links in poll notifications, email invites
  and email-change confirmations are then always built from it, ignoring the
  `origin` the browser sends — otherwise any logged-in user could have mail with
  a link of their choosing sent to everyone. Left unset, the client-supplied
  origin is used, which is fine for local development. A malformed value stops
  the server at startup.

## Production deployment

Copy the SMTP and Telegram credentials into the Git-ignored
`data/production.env` file, then run `make ship` (or
`make ship-local-build`). The deploy uploads that file separately to
`/opt/freipadel/data/production.env`, sets its permissions to `0600`, and
references it from the production Compose file.

The ship targets stop before changing the server if `SMTP_HOST`, `SMTP_USER`,
`SMTP_PASS`, `TELEGRAM_BOT_TOKEN`, or `TELEGRAM_ADMIN_CHAT_ID` is missing. Do
not add credentials to `docker-compose-prod.yml`; `.env` files are excluded
from both the source archive and Docker build context.

## Environment variables

| Variable                  | Default    | Meaning                              |
| ------------------------- | ---------- | ------------------------------------ |
| `PORT`                    | `8080`     | HTTP port                            |
| `DATA_DIR`                | `./data`   | SQLite db + `config.json` location   |
| `STATIC_DIR`              | `./static` | Built frontend to serve              |
| `SCRAPE_INTERVAL_MINUTES` | `30`       | Court availability refresh interval  |
| `COOKIE_SECURE`           | `0`        | Set `1` when serving over HTTPS      |
| `PUBLIC_ORIGIN`           | —          | Canonical base URL (e.g. `https://freipadel.example.com`); when set, all links in outgoing email are built from it |
| `EMAILER_ENABLED`         | —          | Wether the emailer is enabled        |
| `SMTP_HOST`               | —          | SMTP server hostname                 |
| `SMTP_PORT`               | `587`      | SMTP submission port                 |
| `SMTP_USER`               | —          | SMTP authentication username         |
| `SMTP_PASS`               | —          | SMTP authentication password         |
| `MAIL_FROM`               | SMTP user  | Sender email address; required (falls back to `SMTP_USER`) or the emailer stays off |
| `SMTP_INSECURE`           | `0`        | Set `1` to skip STARTTLS (local only) |
| `TELEGRAM_BOT_TOKEN`      | —          | Telegram bot API token               |
| `TELEGRAM_ADMIN_CHAT_ID`  | —          | Telegram admin chat ID               |

## How slot polls work

1. Set your availability under **My availability** (e.g. weekdays 19:00–21:00).
2. **Available slots** shows free courts matching *your* window, grouped by
   date — courts at the same date/time/location are collapsed into one row.
3. Hit **Start slot poll**, tick candidate slots, name the poll.
4. Everyone sees it under **Active slot polls** and votes 👍/👎 per slot.
   Slots with **4+ yes votes** turn green.
5. The poll creator closes the poll and picks the slot to book — booking
   itself happens on the court provider's own site as usual.
