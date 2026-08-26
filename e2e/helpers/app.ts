import { expect, type Browser, type BrowserContext, type Locator, type Page } from '@playwright/test';
import { APP_URL } from './env';
import { stateFile, type Persona, type PersonaKey } from './personas';

// UI-level actions shared across phases. Anything that is itself under test
// (creating invites, voting, closing polls) lives in the spec that tests it —
// only the plumbing is here.

/** Register through the form. Pass an invite token for anyone but the first user. */
export async function registerViaUI(page: Page, persona: Persona, token?: string): Promise<void> {
	await page.goto(token ? `/register?token=${token}` : '/register');
	await expect(page.getByLabel('Name')).toBeVisible();
	await page.getByLabel('Name').fill(persona.name);
	const email = page.getByLabel('Email');
	// Email invites prefill the address and disable the field.
	if (await email.isEditable()) await email.fill(persona.email);
	await page.getByLabel('Password').fill(persona.password);
	await page.getByRole('button', { name: 'Create account' }).click();
	await expect(page).toHaveURL(/\/slots$/);
	await expect(page.getByRole('heading', { name: 'Available slots' })).toBeVisible();
}

export async function loginViaUI(page: Page, persona: Persona): Promise<void> {
	await page.goto('/login');
	await page.getByLabel('Email').fill(persona.email);
	await page.getByLabel('Password').fill(persona.password);
	await page.getByRole('button', { name: 'Log in' }).click();
	await expect(page).toHaveURL(/\/slots$/);
}

/** Park the session cookie so later spec files can reuse it via `test.use`. */
export async function saveState(page: Page, key: PersonaKey): Promise<void> {
	await page.context().storageState({ path: stateFile(key) });
}

export async function logoutViaUI(page: Page): Promise<void> {
	await page.getByRole('button', { name: /^Log out/ }).first().click();
	await expect(page).toHaveURL(/\/login/);
}

/**
 * Wait for the sync store to finish its bootstrap. The sidebar shows a green
 * dot titled "Live …" once the SSE stream is connected, which is the cheapest
 * honest signal that the page is ready to assert against.
 */
export async function waitForSync(page: Page): Promise<void> {
	await expect(page.locator('[title^="Live"]')).toBeVisible({ timeout: 20_000 });
}

export interface Settings {
	weekdays: number[];
	time_start: string;
	time_end: string;
	days_ahead: number;
	min_duration: number;
	locations: string[];
	notifications: Record<string, boolean>;
}

/** Read the logged-in user's settings through the API (uses the page's cookies). */
export async function getSettings(page: Page): Promise<Settings> {
	const res = await page.request.get('/api/settings');
	expect(res.ok()).toBeTruthy();
	return (await res.json()) as Settings;
}

/**
 * The distinct dates the current user can currently see slots on, ascending.
 *
 * Always derive dates from the server rather than from the runner's clock: the
 * container runs in Europe/Berlin while CI runs in UTC, and slots on today's
 * date disappear once their start time passes.
 */
export async function availableDates(page: Page): Promise<string[]> {
	const res = await page.request.get('/api/slots');
	expect(res.ok()).toBeTruthy();
	const body = (await res.json()) as { slots: { date: string }[] };
	return [...new Set(body.slots.map((s) => s.date))].sort();
}

/**
 * A context wired up like the config's default one — `browser.newContext()`
 * does not inherit `use` options. Pass a persona key to restore its session.
 */
export async function newPersonaContext(browser: Browser, key?: PersonaKey): Promise<BrowserContext> {
	return browser.newContext({
		baseURL: APP_URL,
		permissions: ['clipboard-read', 'clipboard-write'],
		...(key ? { storageState: stateFile(key) } : {})
	});
}

/**
 * Switch the active club through the sidebar switcher, and wait for the app to
 * settle on it. The switcher is a dropdown labelled with the current club.
 */
export async function switchClub(page: Page, to: string): Promise<void> {
	const trigger = page.getByTestId('club-switcher');
	await trigger.click();
	await page.getByRole('menuitem', { name: to, exact: false }).click();
	await expect(trigger).toContainText(to);
}

/** The club the switcher is currently showing. */
export function clubSwitcher(page: Page): Locator {
	return page.getByTestId('club-switcher');
}

/** The clubs the logged-in user belongs to, and which one is active. */
export async function getClubs(
	page: Page
): Promise<{ clubs: { id: number; name: string; locations: string[] }[]; active_club_id: number | null }> {
	const res = await page.request.get('/api/clubs');
	expect(res.ok()).toBeTruthy();
	return await res.json();
}

/** The admin page's row for one invite, located by the token suffix it renders. */
export function inviteRow(page: Page, token: string): Locator {
	const code = page.getByText(`…${token.slice(-8)}`, { exact: true });
	return page.locator('div').filter({ has: code }).last();
}

/** Click a "Copy link" style button and return what it put on the clipboard. */
export async function copyToClipboard(page: Page, click: () => Promise<void>): Promise<string> {
	await click();
	await expect(page.getByText('Invite link copied to clipboard')).toBeVisible();
	const text = await page.evaluate(() => navigator.clipboard.readText());
	// Toasts stack up and would match the next assertion; wait it out.
	await page.getByText('Invite link copied to clipboard').first().waitFor({ state: 'hidden', timeout: 20_000 });
	return text;
}

/** The token out of a `/register?token=…` URL. */
export function tokenFromURL(url: string): string {
	const token = new URL(url).searchParams.get('token');
	if (!token) throw new Error(`no token in ${url}`);
	return token;
}
