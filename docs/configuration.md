# Configuration

FreiPadel is configured through environment variables and, for the scraper,
through `data/config.json`.

## Environment variables

| Variable                  | Default    | Meaning                             |
| ------------------------- | ---------- | ----------------------------------- |
| `PORT`                    | `8080`     | HTTP port                           |
| `DATA_DIR`                | `./data`   | SQLite database and `config.json`   |
| `STATIC_DIR`              | `./static` | Built frontend to serve             |
| `SCRAPE_INTERVAL_MINUTES` | `30`       | Court availability refresh interval |
| `COOKIE_SECURE`           | `0`        | Set to `1` when serving over HTTPS  |
| `PUBLIC_ORIGIN`           | unset      | Canonical base URL, see below       |
| `EMAILER_ENABLED`         | unset      | Whether the emailer is enabled      |
| `SMTP_HOST`               | unset      | SMTP server hostname                |
| `SMTP_PORT`               | `587`      | SMTP submission port                |
| `SMTP_USER`               | unset      | SMTP authentication username        |
| `SMTP_PASS`               | unset      | SMTP authentication password        |
| `MAIL_FROM`               | SMTP user  | Sender address; without it the emailer stays off |
| `SMTP_INSECURE`           | `0`        | Set to `1` to skip STARTTLS, local only |
| `TELEGRAM_BOT_TOKEN`      | unset      | Telegram bot API token              |
| `TELEGRAM_ADMIN_CHAT_ID`  | unset      | Telegram admin chat ID              |

### PUBLIC_ORIGIN

Set this to the deployment's canonical base URL, scheme and host, no trailing
path. Every link in outgoing mail is then built from it: poll notifications,
email invites and email-change confirmations.

Leaving it unset makes the server fall back to the `origin` the browser sends.
That is fine for local development and unsafe in production: any logged-in
user could have mail sent to everyone with a link of their choosing.

A malformed value stops the server at startup. Setting it also marks the
deployment as non-local, which serves the `/dev` scratch page as 404.

### Serving over HTTPS

Behind a reverse proxy, set `COOKIE_SECURE=1` so session cookies are only sent
over HTTPS.

## Scraper configuration

`data/config.json` is created with defaults on first start and lists the
sources to scrape, how many days ahead to look, the daily time window and the
timezone. Restart the container after changing it.

The available source types and how to write a new one are documented in
[backend/scraper](../backend/scraper/README.md).
