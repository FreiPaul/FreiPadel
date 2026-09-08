# Deployment

The deploy targets in the `Makefile` push the application to a single server
that runs it with Docker Compose. The server's `./data` directory, which holds
the SQLite database and the scraper configuration, is never touched.

## Credentials

Put the SMTP and Telegram credentials into `data/production.env`, which is
git-ignored. The deploy uploads that file separately to
`/opt/freipadel/data/production.env`, sets its mode to `0600` and references it
from the production Compose file.

Both ship targets stop before changing anything on the server if `SMTP_HOST`,
`SMTP_USER`, `SMTP_PASS`, `TELEGRAM_BOT_TOKEN` or `TELEGRAM_ADMIN_CHAT_ID` is
missing.

Do not put credentials into `docker-compose-prod.yml`. `.env` files are
excluded from both the source archive and the Docker build context.

## Shipping

```sh
make ship               # copy the source over and build on the server
make ship-local-build   # build the image locally and push the image
```

Use `ship-local-build` when the server is slow or of a different architecture
than the build machine. Both back up the SQLite database before starting the
new container, if one exists.

```sh
make logs      # follow the container logs
make status    # docker compose ps on the server
```

The target host and directory come from `SHIP_HOST` and `SHIP_DIR` in the
`Makefile` and can be overridden per invocation.
