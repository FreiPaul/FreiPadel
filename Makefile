# FreiPadel — build & deployment helpers
#
# `make ship` copies the source to the server (tar over ssh — no rsync
# needed there) and rebuilds the container on the server itself (it is
# x86_64, this machine is arm64 — building there avoids cross-compilation).
#
# The server's ./data directory (SQLite db + scraper config) is never touched.

SHIP_HOST ?= tunnel
SHIP_DIR  ?= /opt/freipadel

# Back up the server's SQLite db before deploying — but only if it exists,
# so a fresh server (no db yet) doesn't fail the deploy.
TS := $(shell date +%Y%m%d-%H%M%S)
BACKUP_CMD = if [ -f $(SHIP_DIR)/data/freipadel.db ]; then sqlite3 $(SHIP_DIR)/data/freipadel.db ".backup $(SHIP_DIR)/data/freipadel-bkp-$(TS).db"; else echo "  (no db on $(SHIP_HOST) yet — fresh server, skipping backup)"; fi

TAR_EXCLUDES = \
	--exclude ./.git \
	--exclude ./.claude \
	--exclude .DS_Store \
	--exclude ./frontend/node_modules \
	--exclude ./frontend/.svelte-kit \
	--exclude ./frontend/build \
	--exclude ./backend/static \
	--exclude ./backend/freipadel \
	--exclude ./data \
	--exclude ./logic/venv \
	--exclude ./logic/__pycache__

.PHONY: ship ship-local-build logs status local

ship:
	@echo "→ syncing source to $(SHIP_HOST):$(SHIP_DIR)"
	ssh $(SHIP_HOST) 'mkdir -p $(SHIP_DIR) && find $(SHIP_DIR) -mindepth 1 -maxdepth 1 ! -name data -exec rm -rf {} +'
	tar -czf - $(TAR_EXCLUDES) . | ssh $(SHIP_HOST) 'tar -xzf - -C $(SHIP_DIR)'
	@echo "→ building & starting on $(SHIP_HOST)"
	ssh $(SHIP_HOST) '$(BACKUP_CMD)'
	ssh $(SHIP_HOST) 'cd $(SHIP_DIR) && docker compose up -d --build'
	@echo "✅ shipped — http://$(SHIP_HOST):8080"

# Build the image locally (natively cross-compiled to linux/amd64 — see the
# Dockerfile's --platform=$$BUILDPLATFORM stages) and push the finished image to
# the server, skipping the server-side build entirely. The server's ./data
# directory is never touched.
ship-local-build:
	@echo "→ building linux/amd64 image locally"
	docker buildx build --platform linux/amd64 -t freipadel:latest --load .
	@echo "→ syncing compose config to $(SHIP_HOST):$(SHIP_DIR)"
	ssh $(SHIP_HOST) 'mkdir -p $(SHIP_DIR)'
	scp docker-compose.yml $(SHIP_HOST):$(SHIP_DIR)/docker-compose.yml
	@echo "→ shipping image (docker save | ssh | docker load)"
	docker save freipadel:latest | gzip | ssh $(SHIP_HOST) 'gunzip | docker load'
	@echo "→ backing up db & starting on $(SHIP_HOST)"
	ssh $(SHIP_HOST) '$(BACKUP_CMD)'
	ssh $(SHIP_HOST) 'cd $(SHIP_DIR) && docker compose up -d'
	@echo "✅ shipped (local build) — http://$(SHIP_HOST):8080"

logs:
	ssh $(SHIP_HOST) 'cd $(SHIP_DIR) && docker compose logs -f --tail 50'

status:
	ssh $(SHIP_HOST) 'cd $(SHIP_DIR) && docker compose ps'

# Run the local docker stack (same as on the server)
local:
	docker compose up -d --build
