export interface User {
	id: number;
	email: string;
	name: string;
	is_admin: boolean;
}

export interface Settings {
	weekdays: number[]; // 0=Monday … 6=Sunday
	time_start: string;
	time_end: string;
	days_ahead: number;
	min_duration: number;
	locations: string[]; // empty = all locations
}

export interface SlotGroup {
	date: string;
	weekday: number; // 0=Monday … 6=Sunday
	time: string;
	duration_minutes: number;
	location: string;
	source: string;
	courts: string[];
	min_price: number;
	currency: string;
}

export interface SlotsResponse {
	slots: SlotGroup[];
	last_fetched_at: string;
	scraping: boolean;
}

export interface Voter {
	user_id: number;
	name: string;
	vote: boolean;
}

export interface PollSlot {
	id: number;
	date: string;
	time: string;
	duration_minutes: number;
	location: string;
	court: string;
	price: number;
	currency: string;
	votes: Voter[];
	yes_count: number;
	no_count: number;
	my_vote: boolean | null;
	available: boolean; // still bookable according to the latest scrape
	expired: boolean; // start time has passed
}

export interface Poll {
	id: number;
	title: string;
	creator_id: number;
	creator_name: string;
	status: 'active' | 'closed';
	winning_slot_id: number | null;
	created_at: string;
	closed_at: string | null;
	slots: PollSlot[];
}

export interface Invite {
	token: string;
	kind: 'single' | 'group';
	created_at: string;
	used_by: string | null;
	used_at: string | null;
	disabled: boolean;
	uses: number;
}

export interface Member {
	id: number;
	name: string;
	is_admin: boolean;
}

export class ApiError extends Error {
	status: number;

	constructor(status: number, message: string) {
		super(message);
		this.status = status;
	}
}

async function request<T>(method: string, path: string, body?: unknown): Promise<T> {
	const res = await fetch(path, {
		method,
		headers: body !== undefined ? { 'Content-Type': 'application/json' } : undefined,
		body: body !== undefined ? JSON.stringify(body) : undefined
	});
	if (!res.ok) {
		let message = res.statusText;
		try {
			const data = await res.json();
			if (data?.error) message = data.error;
		} catch {
			// non-JSON error body
		}
		throw new ApiError(res.status, message);
	}
	return res.json() as Promise<T>;
}

export const api = {
	get: <T>(path: string) => request<T>('GET', path),
	post: <T>(path: string, body?: unknown) => request<T>('POST', path, body),
	put: <T>(path: string, body?: unknown) => request<T>('PUT', path, body),
	del: <T>(path: string) => request<T>('DELETE', path)
};

// Number of yes votes needed for a playable padel slot.
export const PLAYERS_NEEDED = 4;
