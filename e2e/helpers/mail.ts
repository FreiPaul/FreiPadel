import { MAILPIT_URL } from './env';

// Mailpit's REST API (https://mailpit.axllent.org/docs/api-v1/). Summaries come
// from the list endpoint; bodies need a second call per message.

export interface MailAddress {
	Name: string;
	Address: string;
}

export interface MailSummary {
	ID: string;
	From: MailAddress | null;
	To: MailAddress[];
	Subject: string;
	Snippet: string;
	Created: string;
}

export interface MailMessage extends MailSummary {
	HTML: string;
	Text: string;
}

async function api<T>(path: string, init?: RequestInit): Promise<T> {
	const res = await fetch(`${MAILPIT_URL}${path}`, init);
	if (!res.ok) throw new Error(`mailpit ${init?.method ?? 'GET'} ${path} -> ${res.status} ${await res.text()}`);
	return (await res.json()) as T;
}

/** Empty the inbox. Call at the start of a phase so counts can be exact. */
export async function clearMail(): Promise<void> {
	const res = await fetch(`${MAILPIT_URL}/api/v1/messages`, { method: 'DELETE' });
	if (!res.ok) throw new Error(`mailpit DELETE /api/v1/messages -> ${res.status}`);
}

export async function listMail(): Promise<MailSummary[]> {
	const { messages } = await api<{ messages: MailSummary[] }>('/api/v1/messages?limit=200');
	return messages ?? [];
}

export interface MailFilter {
	to?: string;
	subject?: string | RegExp;
}

function matches(message: MailSummary, filter: MailFilter): boolean {
	if (filter.to && !message.To.some((t) => t.Address.toLowerCase() === filter.to!.toLowerCase())) return false;
	if (typeof filter.subject === 'string' && message.Subject !== filter.subject) return false;
	if (filter.subject instanceof RegExp && !filter.subject.test(message.Subject)) return false;
	return true;
}

export async function findMail(filter: MailFilter = {}): Promise<MailSummary[]> {
	return (await listMail()).filter((m) => matches(m, filter));
}

export async function mailCount(filter: MailFilter = {}): Promise<number> {
	return (await findMail(filter)).length;
}

export async function readMail(id: string): Promise<MailMessage> {
	return api<MailMessage>(`/api/v1/message/${id}`);
}

/**
 * Wait for exactly one message matching the filter and return it with its body.
 * The poll/booking notifications are sent from a goroutine after the HTTP
 * response, so every mail assertion has to wait rather than read once.
 */
export async function waitForMail(filter: MailFilter, timeout = 20_000): Promise<MailMessage> {
	const deadline = Date.now() + timeout;
	for (;;) {
		const found = await findMail(filter);
		if (found.length > 0) return readMail(found[0].ID);
		if (Date.now() > deadline) {
			const inbox = (await listMail()).map((m) => `${m.To.map((t) => t.Address).join(',')} :: ${m.Subject}`);
			throw new Error(
				`timed out after ${timeout}ms waiting for mail ${JSON.stringify({
					to: filter.to,
					subject: String(filter.subject)
				})}\ninbox:\n  ${inbox.join('\n  ') || '(empty)'}`
			);
		}
		await new Promise((r) => setTimeout(r, 250));
	}
}

/** Wait until the inbox holds exactly `n` matching messages, then hold to prove no extras arrive. */
export async function waitForMailCount(filter: MailFilter, n: number, settle = 2_000): Promise<MailSummary[]> {
	const deadline = Date.now() + 20_000;
	for (;;) {
		const found = await findMail(filter);
		if (found.length >= n) {
			// Give any straggler send a moment to land, then assert the exact count.
			await new Promise((r) => setTimeout(r, settle));
			return findMail(filter);
		}
		if (Date.now() > deadline) {
			const inbox = (await listMail()).map((m) => `${m.To.map((t) => t.Address).join(',')} :: ${m.Subject}`);
			throw new Error(`timed out waiting for ${n} message(s); inbox:\n  ${inbox.join('\n  ') || '(empty)'}`);
		}
		await new Promise((r) => setTimeout(r, 250));
	}
}

/** Every href in an HTML body, in document order. */
export function linksIn(html: string): string[] {
	return [...html.matchAll(/href="([^"]+)"/g)].map((m) => m[1]);
}

/** The first href pointing at `path` (e.g. "/register"). */
export function linkTo(html: string, path: string): string {
	const link = linksIn(html).find((href) => href.includes(path));
	if (!link) throw new Error(`no link containing ${path} in:\n${html}`);
	return link;
}
