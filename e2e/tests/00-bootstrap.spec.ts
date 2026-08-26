import { expect, test } from '@playwright/test';
import { count, one, rows, scalar, userId } from '../helpers/db';
import { listMail } from '../helpers/mail';
import { personas, stateFile } from '../helpers/personas';
import { registerViaUI, saveState, waitForSync } from '../helpers/app';
import { writeScratch } from '../helpers/scratch';

// Phase 00 — an empty deployment: setup state, the startup scrape, and the
// first account becoming the admin.
test.describe.serial('bootstrap', () => {
	test('a fresh deployment asks to be set up', async ({ page, request }) => {
		expect(count('users')).toBe(0);

		const res = await request.get('/api/auth/setup');
		expect(res.status()).toBe(200);
		expect(await res.json()).toEqual({ needs_setup: true });

		await page.goto('/register');
		await expect(page.getByText('Set up the first account')).toBeVisible();
	});

	test('the startup scrape filled the slot cache from both mock sources', () => {
		// fixtures/config.json: 21 days x (2 times x 2 courts) for Mock Padel
		// Club, plus 21 days x (1 time x 1 court) for Second Club.
		expect(scalar<number>('SELECT COUNT(*) AS n FROM slots')).toBe(105);

		const locations = rows<{ location: string }>('SELECT DISTINCT location FROM slots ORDER BY location');
		expect(locations.map((l) => l.location)).toEqual(['Mock Padel Club', 'Second Club']);

		expect(scalar<number>("SELECT COUNT(*) AS n FROM slots WHERE source <> 'mock'")).toBe(0);
		expect(scalar<number>('SELECT COUNT(DISTINCT date) AS n FROM slots')).toBe(21);
		expect(one('SELECT value FROM meta WHERE key = ?', 'last_fetched_at')).toBeTruthy();
	});

	test('the first account is created as admin, with default settings', async ({ page }) => {
		await registerViaUI(page, personas.alice);
		await waitForSync(page);

		// Only admins get the administration tab.
		await expect(page.getByRole('link', { name: 'Administration' })).toBeVisible();

		expect(
			rows<{ email: string; name: string; is_admin: number }>(
				'SELECT email, name, is_admin FROM users ORDER BY id'
			)
		).toEqual([{ email: personas.alice.email, name: personas.alice.name, is_admin: 1 }]);

		// CreateDefaultSettings ran inside the registration transaction.
		const settings = one<{
			weekdays: string;
			time_start: string;
			time_end: string;
			days_ahead: number;
			min_duration: number;
			locations: string;
			notifications: string;
		}>('SELECT * FROM user_settings');
		expect(settings).toMatchObject({
			weekdays: '[0,1,2,3,4]',
			time_start: '19:00',
			time_end: '21:00',
			days_ahead: 10,
			min_duration: 60,
			locations: '[]'
		});
		// Both notification types default to off — nothing opts a new user in.
		expect(JSON.parse(settings!.notifications)).toEqual({});

		await saveState(page, 'alice');
	});

	// A fresh deployment has no admin to own a club when the migration runs, so
	// the first registration founds "All" instead.
	test('the first account founds the All club and lands in it', async ({ page }) => {
		const club = one<{ id: number; name: string; owner_id: number; locations: string }>(
			'SELECT id, name, owner_id, locations FROM clubs'
		);
		expect(club).toMatchObject({ name: 'All', owner_id: userId(personas.alice.email) });
		// An empty venue list means every venue.
		expect(club!.locations).toBe('[]');

		expect(count('club_members', 'club_id = ?', club!.id)).toBe(1);
		expect(
			one<{ active_club_id: number }>('SELECT active_club_id FROM users WHERE email = ?', personas.alice.email)
		).toEqual({ active_club_id: club!.id });

		// The switcher names the club she is in.
		await page.goto('/slots');
		await waitForSync(page);
		await expect(page.getByRole('button', { name: 'All' })).toBeVisible();

		writeScratch({ allClubId: club!.id });
	});

	test('registering does not send any email', async () => {
		expect(await listMail()).toHaveLength(0);
	});

	// A saved session, reloaded in a fresh browser context: the cookie is what
	// carries the login, not anything in the page.
	test.describe('with the admin session restored', () => {
		test.use({ storageState: stateFile('alice') });

		test('the session survives a fresh context and unknown API routes 404', async ({ page, request }) => {
			await page.goto('/slots');
			await expect(page.getByRole('heading', { name: 'Available slots' })).toBeVisible();
			expect(count('sessions')).toBe(1);

			// Unknown /api/ paths must not fall through to the SPA's index.html.
			const res = await request.get('/api/does-not-exist');
			expect(res.status()).toBe(404);
			expect(await res.json()).toEqual({ error: 'not found' });
		});
	});
});
