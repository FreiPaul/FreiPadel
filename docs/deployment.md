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

## Container hardening

In production the container runs without root, as the user that owns
`/opt/freipadel/data` on the server (normally the ssh user). Both ship targets
write that uid into `/opt/freipadel/.env` and stop if a file in `data/`
belongs to someone else. Before the first ship with this setup, hand the
directory over once on the server (the old container ran as root and created
the db as root):

```sh
sudo chown -R "$(id -u):$(id -g)" /opt/freipadel/data
```

The container also has a read-only root filesystem (only `/data` and a small
`/tmp` are writable), no Linux capabilities, `no-new-privileges` and memory
and process limits. The Go server sets read, write and idle timeouts; the SSE
stream lifts them for its own connection.

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
