import { execFileSync } from 'node:child_process';
import { writeFileSync } from 'node:fs';
import { join } from 'node:path';
import { COMPOSE_FILE, E2E_DIR, TMP } from './helpers/env';

export default async function globalTeardown(): Promise<void> {
	// Container logs are the first thing you want when a run fails in CI, and
	// they are gone the moment the stack comes down.
	try {
		const logs = execFileSync('docker', ['compose', '-f', COMPOSE_FILE, 'logs', '--no-color', '--timestamps'], {
			cwd: E2E_DIR,
			encoding: 'utf8',
			maxBuffer: 32 * 1024 * 1024
		});
		writeFileSync(join(TMP, 'containers.log'), logs);
	} catch (err) {
		console.warn('[e2e] could not capture container logs:', err);
	}

	// E2E_KEEP_STACK=1 leaves the containers up for poking at the app or the
	// Mailpit UI on http://localhost:8125 after a failure.
	if (process.env.E2E_KEEP_STACK === '1') {
		console.log('[e2e] E2E_KEEP_STACK=1 — leaving the stack running');
		return;
	}
	execFileSync('docker', ['compose', '-f', COMPOSE_FILE, 'down', '-v', '--remove-orphans'], {
		cwd: E2E_DIR,
		stdio: 'inherit'
	});
}
