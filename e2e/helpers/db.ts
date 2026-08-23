import { execFileSync } from 'node:child_process';
import { COMPOSE_FILE, DB_PATH, E2E_DIR } from './env';

/**
 * Read-only access to the app's SQLite database.
 *
 * Queries run inside the `sqlite` sidecar, which shares the app's named
 * volume. They deliberately do NOT run on the host: SQLite's WAL index is
 * shared memory backed by the -shm file, and that is not coherent across a
 * macOS bind mount. A host reader sees a snapshot frozen at its first open
 * and, worse, checkpoints the WAL it cannot see when it closes — silently
 * discarding committed rows. Inside the volume this is ordinary
 * multi-process SQLite.
 *
 * Calls are synchronous (~100ms each) so assertions stay readable.
 */
function query(sql: string): string {
	return execFileSync(
		'docker',
		[
			'compose',
			'-f',
			COMPOSE_FILE,
			'exec',
			'-T',
			'sqlite',
			'sqlite3',
			'-json',
			// Wait rather than fail if the app happens to hold the write lock.
			'-cmd',
			'.timeout 5000',
			DB_PATH
		],
		{ cwd: E2E_DIR, input: sql, encoding: 'utf8', maxBuffer: 16 * 1024 * 1024 }
	).trim();
}

/** Inline a value into SQL. Test data only — the sqlite3 CLI has no bind parameters. */
function literal(value: unknown): string {
	if (value === null || value === undefined) return 'NULL';
	if (typeof value === 'number') return String(value);
	if (typeof value === 'boolean') return value ? '1' : '0';
	return `'${String(value).replace(/'/g, "''")}'`;
}

function render(sql: string, params: unknown[]): string {
	let i = 0;
	const rendered = sql.replace(/\?/g, () => {
		if (i >= params.length) throw new Error(`not enough parameters for: ${sql}`);
		return literal(params[i++]);
	});
	if (i !== params.length) throw new Error(`${params.length} parameters given but ${i} placeholders in: ${sql}`);
	return rendered.endsWith(';') ? rendered : `${rendered};`;
}

export function rows<T = Record<string, unknown>>(sql: string, ...params: unknown[]): T[] {
	const out = query(render(sql, params));
	return out === '' ? [] : (JSON.parse(out) as T[]);
}

export function one<T = Record<string, unknown>>(sql: string, ...params: unknown[]): T | undefined {
	return rows<T>(sql, ...params)[0];
}

/** First column of the first row — for `SELECT COUNT(*) ...` and friends. */
export function scalar<T = number>(sql: string, ...params: unknown[]): T {
	const row = one<Record<string, T>>(sql, ...params);
	if (!row) throw new Error(`no row returned for: ${sql}`);
	return Object.values(row)[0];
}

export function count(table: string, where = '1=1', ...params: unknown[]): number {
	return scalar<number>(`SELECT COUNT(*) AS n FROM ${table} WHERE ${where}`, ...params);
}

export function userId(email: string): number {
	const row = one<{ id: number }>('SELECT id FROM users WHERE email = ?', email);
	if (!row) throw new Error(`no user with email ${email}`);
	return row.id;
}

/** Poll a query until it satisfies `predicate` — for state written by a goroutine. */
export async function waitFor<T>(
	read: () => T,
	predicate: (value: T) => boolean,
	{ timeout = 20_000, interval = 250, what = 'database state' } = {}
): Promise<T> {
	const deadline = Date.now() + timeout;
	for (;;) {
		const value = read();
		if (predicate(value)) return value;
		if (Date.now() > deadline) {
			throw new Error(`timed out after ${timeout}ms waiting for ${what}; last value: ${JSON.stringify(value)}`);
		}
		await new Promise((r) => setTimeout(r, interval));
	}
}
