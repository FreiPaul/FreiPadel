import { existsSync, readFileSync, writeFileSync } from 'node:fs';
import { join } from 'node:path';
import { TMP } from './env';

// The journey spans several spec files, so values discovered in one phase
// (invite tokens, poll ids) are handed to the next through a small JSON file
// rather than module state, which does not survive across workers/files.

const FILE = join(TMP, 'scratch.json');

export interface Scratch {
	singleInviteToken?: string;
	groupInviteToken?: string;
	emailInviteToken?: string;
	forgedOriginInviteToken?: string;
	pollId?: number;
	slotAId?: number;
	slotBId?: number;
	dateA?: string;
	dateB?: string;
}

export function readScratch(): Scratch {
	return existsSync(FILE) ? (JSON.parse(readFileSync(FILE, 'utf8')) as Scratch) : {};
}

export function writeScratch(patch: Scratch): Scratch {
	const next = { ...readScratch(), ...patch };
	writeFileSync(FILE, JSON.stringify(next, null, 2));
	return next;
}

/** Read a value that an earlier phase must have written. */
export function need<K extends keyof Scratch>(key: K): NonNullable<Scratch[K]> {
	const value = readScratch()[key];
	if (value === undefined) throw new Error(`scratch value "${key}" is missing — did an earlier spec fail?`);
	return value as NonNullable<Scratch[K]>;
}
