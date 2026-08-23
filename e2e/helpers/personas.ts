import { join } from 'node:path';
import { TMP } from './env';

export interface Persona {
	key: string;
	name: string;
	email: string;
	password: string;
}

// Four accounts, each reached by a different route into the group. Passwords
// are >= 8 characters (the server's minimum).
export const personas = {
	// First account created — becomes the admin. Notifications stay OFF, so
	// the poll fan-out must not reach her.
	alice: { key: 'alice', name: 'Alice Admin', email: 'alice@e2e.test', password: 'padel-alice-1' },
	// Joins via a one-time invite. Both notifications ON.
	bob: { key: 'bob', name: 'Bob Single', email: 'bob@e2e.test', password: 'padel-bob-123' },
	// Joins via a reusable group invite. Both notifications ON.
	carol: { key: 'carol', name: 'Carol Group', email: 'carol@e2e.test', password: 'padel-carol-1' },
	// Joins via an emailed invite. Notifications stay OFF — the control group
	// that proves a voter with notifications off is not mailed.
	dave: { key: 'dave', name: 'Dave Email', email: 'dave@e2e.test', password: 'padel-dave-12' }
} as const satisfies Record<string, Persona>;

export type PersonaKey = keyof typeof personas;

/** Where a logged-in persona's cookies are parked between spec files. */
export const stateFile = (key: PersonaKey): string => join(TMP, `state-${key}.json`);
