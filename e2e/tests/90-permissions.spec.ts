import { expect, test } from '@playwright/test';
import { count } from '../helpers/db';
import { stateFile } from '../helpers/personas';
import { need } from '../helpers/scratch';
import { waitForSync } from '../helpers/app';

// Phase 90 — who is allowed to do what, and logging out. Runs last: it ends
// the admin's session.

test.describe.serial('permissions', () => {
	test('an anonymous visitor is bounced to the login page', async ({ page, request }) => {
		const res = await request.get('/api/slots');
		expect(res.status()).toBe(401);
		expect(await res.json()).toEqual({ error: 'not logged in' });

		await page.goto('/polls');
		await expect(page).toHaveURL(/\/login\?redirect_to=%2Fpolls/);
	});

	test.describe('as a non-admin member', () => {
		test.use({ storageState: stateFile('carol') });

		test('has no invites tab and cannot reach the invite API', async ({ page }) => {
			await page.goto('/slots');
			await waitForSync(page);
			await expect(page.getByRole('link', { name: 'Available slots' })).toBeVisible();
			await expect(page.getByRole('link', { name: 'Administration' })).toHaveCount(0);

			for (const res of [
				await page.request.get('/api/invites'),
				await page.request.post('/api/invites', { data: { kind: 'single' } })
			]) {
				expect(res.status()).toBe(403);
				expect(await res.json()).toEqual({ error: 'admin only' });
			}
			// Three into "All" from phase 10, plus the Köln Crew one from phase 50.
			expect(count('invites')).toBe(4);
		});

		test('cannot manage a poll somebody else created', async ({ page }) => {
			await page.goto('/polls');
			await waitForSync(page);
			// The manage buttons only render for the creator or an admin.
			await expect(page.getByRole('button', { name: '🗑️' })).toHaveCount(0);

			const res = await page.request.delete(`/api/polls/${need('pollId')}`);
			expect(res.status()).toBe(403);
			expect(await res.json()).toEqual({ error: 'only the poll creator can delete it' });
			// Hers from phase 30 and the Köln Crew one from phase 50.
			expect(count('polls')).toBe(2);
		});
	});

	test.describe('as the admin', () => {
		test.use({ storageState: stateFile('alice') });

		test('logging out drops the session', async ({ page }) => {
			await page.goto('/slots');
			await waitForSync(page);
			const before = count('sessions');

			await page.getByRole('button', { name: 'Log out', exact: true }).click();
			await expect(page).toHaveURL(/\/login/);
			expect(count('sessions')).toBe(before - 1);

			// The cookie is gone, so protected routes bounce again.
			await page.goto('/polls');
			await expect(page).toHaveURL(/\/login\?redirect_to=%2Fpolls/);
		});
	});
});
