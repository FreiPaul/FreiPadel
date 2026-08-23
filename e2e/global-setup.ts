import { execFileSync } from 'node:child_process';
import { mkdirSync, rmSync } from 'node:fs';
import { COMPOSE_FILE, E2E_DIR, TMP } from './helpers/env';
import { clearMail } from './helpers/mail';
import { one, scalar, waitFor } from './helpers/db';

function compose(...args: string[]): void {
	execFileSync('docker', ['compose', '-f', COMPOSE_FILE, ...args], {
		cwd: E2E_DIR,
		stdio: 'inherit',
		env: process.env
	});
}

export default async function globalSetup(): Promise<void> {
	// `down -v` destroys the data volume, which is what makes every run start
	// from a genuinely empty database: no users, invites or polls survive.
	console.log('\n[e2e] tearing down any previous stack');
	compose('down', '-v', '--remove-orphans');

	// Reports, traces, saved sessions and the cross-spec scratch file.
	rmSync(TMP, { recursive: true, force: true });
	mkdirSync(TMP, { recursive: true });

	// Seed the scraper config before first boot — LoadConfig only writes its
	// defaults when the file is absent, and the tests depend on these exact
	// sources (105 slot rows across two locations).
	console.log('[e2e] seeding the scraper config');
	compose('run', '--rm', 'seed');

	console.log('[e2e] building and starting the stack');
	compose('up', '-d', '--build', '--wait');

	// The server is healthy, but the first scrape runs in a background
	// goroutine. Wait for it, or the first spec races an empty slot cache.
	console.log('[e2e] waiting for the first scrape');
	await waitFor(
		() => ({
			slots: scalar<number>('SELECT COUNT(*) AS n FROM slots'),
			fetchedAt: one<{ value: string }>('SELECT value FROM meta WHERE key = ?', 'last_fetched_at')?.value
		}),
		(state) => state.slots > 0 && !!state.fetchedAt,
		{ timeout: 60_000, what: 'the first scrape to land' }
	);

	await clearMail();
	console.log('[e2e] ready\n');
}
