import { expect, test } from '@playwright/test';
import { count, one, userId } from '../helpers/db';
import { clearMail, linkTo, waitForMail, waitForMailCount } from '../helpers/mail';
import { personas, stateFile } from '../helpers/personas';

// Phase 40 — changing the login address. The account keeps its old address
// until a token sent to the *new* one is confirmed, and both addresses are
// told about the request.

const NEW_EMAIL = 'bob.new@e2e.test';

test.describe.serial('email change', () => {
	test.use({ storageState: stateFile('bob') });

	test('requesting a change mails both addresses and leaves the account alone', async ({ page }) => {
		await clearMail();
		await page.goto('/user');
		await page.getByLabel('E-Mail:').fill(NEW_EMAIL);
		await page.getByRole('button', { name: 'Change' }).click();
		await expect(page.getByText('Confirmation email sent')).toBeVisible();

		const bobId = userId(personas.bob.email);
		expect(one('SELECT user_id, new_email FROM pending_email_changes')).toEqual({
			user_id: bobId,
			new_email: NEW_EMAIL
		});
		// Not applied yet.
		expect(one('SELECT email FROM users WHERE id = ?', bobId)).toEqual({ email: personas.bob.email });

		await waitForMailCount({}, 2);
		const confirmation = await waitForMail({
			to: NEW_EMAIL,
			subject: 'Confirm your new FreiPadel email address'
		});
		expect(linkTo(confirmation.HTML, '/confirm-email')).toMatch(
			/^http:\/\/localhost:8099\/confirm-email\?token=[0-9a-f]{64}$/
		);

		const notice = await waitForMail({ to: personas.bob.email, subject: 'FreiPadel email change requested' });
		expect(notice.HTML).toContain(NEW_EMAIL);

		// The pending request is shown back on the settings page.
		await page.reload();
		await expect(page.getByText(`Waiting for confirmation from`)).toBeVisible();
		await expect(page.getByText(NEW_EMAIL, { exact: true })).toBeVisible();
	});

	test('a second request while one is pending is rate limited', async ({ page }) => {
		const res = await page.request.post('/api/auth/email-change', {
			data: { new_email: 'bob.other@e2e.test', origin: 'http://localhost:8099' }
		});
		expect(res.status()).toBe(429);
		expect(res.headers()['retry-after']).toBe('60');
		expect(count('pending_email_changes')).toBe(1);
	});

	test('cancelling drops the pending request', async ({ page }) => {
		await page.goto('/user');
		await page.getByRole('button', { name: 'Cancel' }).click();
		await expect(page.getByText('Pending email change cancelled')).toBeVisible();
		expect(count('pending_email_changes')).toBe(0);
	});

	test('confirming the emailed link changes the login address', async ({ page }) => {
		await clearMail();

		// Cancelling cleared the cooldown along with the row, so this is allowed.
		await page.goto('/user');
		await page.getByLabel('E-Mail:').fill(NEW_EMAIL);
		await page.getByRole('button', { name: 'Change' }).click();
		await expect(page.getByText('Confirmation email sent')).toBeVisible();

		const mail = await waitForMail({ to: NEW_EMAIL, subject: 'Confirm your new FreiPadel email address' });
		await page.goto(linkTo(mail.HTML, '/confirm-email'));
		await page.getByRole('button', { name: 'Confirm email change' }).click();
		await expect(page.getByText(`Your email address has been changed to`)).toBeVisible();

		const bobId = userId(NEW_EMAIL);
		expect(one('SELECT email FROM users WHERE id = ?', bobId)).toEqual({ email: NEW_EMAIL });
		expect(count('pending_email_changes')).toBe(0);
		expect(count('users', 'email = ?', personas.bob.email)).toBe(0);
	});

	test('the confirmation token is single use', async ({ page }) => {
		const res = await page.request.post('/api/auth/email-change/confirm', {
			data: { token: 'deadbeef'.repeat(8) }
		});
		expect(res.status()).toBe(400);
		expect(await res.json()).toEqual({ error: 'invalid or expired confirmation link' });
	});
});
