import { dirname, join } from 'node:path';
import { fileURLToPath } from 'node:url';

export const E2E_DIR = join(dirname(fileURLToPath(import.meta.url)), '..');
export const COMPOSE_FILE = join(E2E_DIR, 'docker-compose.e2e.yml');

/** Reports, traces, saved sessions. The database itself lives in a Docker volume. */
export const TMP = join(E2E_DIR, '.tmp');

/** Path of the SQLite database *inside* the app and sqlite containers. */
export const DB_PATH = '/data/freipadel.db';

// Deliberately not 8080/8025: a dev stack may be running on the usual ports.
// APP_URL must match PUBLIC_ORIGIN in docker-compose.e2e.yml — the tests
// follow links out of real emails.
export const APP_URL = 'http://localhost:8099';
export const MAILPIT_URL = 'http://localhost:8125';
