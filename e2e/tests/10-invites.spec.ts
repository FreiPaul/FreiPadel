import { expect, test } from '@playwright/test';
import { count, one, rows } from '../helpers/db';
import { clearMail, linkTo, waitForMail } from '../helpers/mail';
import { personas, stateFile } from '../helpers/personas';
import { need, writeScratch } from '../helpers/scratch';
import {
	copyToClipboard,
	inviteRow,
	newPersonaContext,
	registerViaUI,
	saveState,
	tokenFromURL,
	waitForSync
} from '../helpers/app';

// Phase 10 — the three invite kinds, all the ways they can be refused, and the
// three remaining accounts joining the group.
test.describe.serial('invites', () => {
	test.describe('as the admin', () => {
		test.use({ storageState: stateFile('alice') });

		test('creates a one-time, a group and an emailed invite', async ({ page }) => {
			await clearMail();
			await page.goto('/admin');
			await waitForSync(page);
			await expect(page.getByText('No invites yet')).toBeVisible();

			const singleURL = await copyToClipboard(page, () =>
				page.getByRole('button', { name: '+ One-time link' }).click()
			);
			const groupURL = await copyToClipboard(page, () =>
				page.getByRole('button', { name: '+ Group link' }).click()
			);

			// The emailed invite is the only kind that carries an address.
			await page.getByPlaceholder('E-Mail').fill(personas.dave.email);
			await page.getByRole('button', { name: 'Invite member' }).click();
			await expect(page.getByText('Email invite sent')).toBeVisible();

			const single = tokenFromURL(singleURL);
			const group = tokenFromURL(groupURL);
			expect(singleURL).toBe(`http://localhost:8099/register?token=${single}`);

			const invites = rows<{ token: string; kind: string; email: string | null; disabled: number; uses: number }>(
				'SELECT token, kind, email, disabled, uses FROM invites ORDER BY rowid'
			);
			expect(invites).toHaveLength(3);
			expect(invites.map((i) => i.kind)).toEqual(['single', 'group', 'email']);
			expect(invites.map((i) => i.email)).toEqual([null, null, personas.dave.email]);
			expect(invites.every((i) => i.disabled === 0 && i.uses === 0)).toBe(true);
			expect(invites[0].token).toBe(single);
			expect(invites[1].token).toBe(group);

			// The emailed link is built from PUBLIC_ORIGIN.
			const mail = await waitForMail({
				to: personas.dave.email,
				subject: "You've been invited to FreiPadel"
			});
			const link = linkTo(mail.HTML, '/register');
			expect(link).toBe(`http://localhost:8099/register?token=${invites[2].token}`);

			writeScratch({ singleInviteToken: single, groupInviteToken: group, emailInviteToken: invites[2].token });

			// All three are listed, in the states the admin page distinguishes.
			await expect(inviteRow(page, group).getByText('👥 group')).toBeVisible();
			await expect(inviteRow(page, invites[2].token).getByText(personas.dave.email)).toBeVisible();
			await expect(inviteRow(page, single).getByText('open', { exact: true })).toBeVisible();
		});

		test('ignores a forged origin when building the emailed link', async ({ page }) => {
			// PUBLIC_ORIGIN is set, so the origin in the request body — which any
			// logged-in user controls — must not reach the mail.
			const res = await page.request.post('/api/invites', {
				data: { kind: 'email', email: 'forged@e2e.test', origin: 'http://evil.test' }
			});
			expect(res.status()).toBe(201);
			const { token } = (await res.json()) as { token: string };

			const mail = await waitForMail({ to: 'forged@e2e.test' });
			const link = linkTo(mail.HTML, '/register');
			expect(link).toBe(`http://localhost:8099/register?token=${token}`);
			expect(mail.HTML).not.toContain('evil.test');

			writeScratch({ forgedOriginInviteToken: token });
		});
	});

	test('the public check endpoint reports each invite state', async ({ request, page }) => {
		const single = need('singleInviteToken');

		const ok = await request.get(`/api/invites/${single}/check`);
		expect(await ok.json()).toEqual({ valid: true, email: '' });

		const emailInvite = await request.get(`/api/invites/${need('emailInviteToken')}/check`);
		expect(await emailInvite.json()).toEqual({ valid: true, email: personas.dave.email });

		const unknown = await request.get('/api/invites/not-a-real-token/check');
		expect(await unknown.json()).toEqual({ valid: false, reason: 'unknown' });

		await page.goto('/register?token=not-a-real-token');
		await expect(page.getByText('This invite link is not valid')).toBeVisible();

		await page.goto('/register');
		await expect(page.getByText('You need an invite link to register')).toBeVisible();
	});

	test('a one-time invite is redeemed, and the admin sees it flip live', async ({ browser }) => {
		const token = need('singleInviteToken');

		// The admin sits on the invite list the whole time — the change has to
		// arrive over the SSE delta stream, without a reload.
		const adminContext = await newPersonaContext(browser, 'alice');
		const admin = await adminContext.newPage();
		await admin.goto('/admin');
		await waitForSync(admin);
		await expect(inviteRow(admin, token).getByText('open', { exact: true })).toBeVisible();

		const bobContext = await newPersonaContext(browser);
		const bob = await bobContext.newPage();
		await registerViaUI(bob, personas.bob, token);
		await saveState(bob, 'bob');

		await expect(admin.getByText(`used by ${personas.bob.name}`)).toBeVisible();

		const invite = one<{ used_by: number | null; used_at: string | null; uses: number }>(
			'SELECT used_by, used_at, uses FROM invites WHERE token = ?',
			token
		);
		expect(invite).toMatchObject({ used_by: 2, uses: 1 });
		expect(invite!.used_at).toBeTruthy();

		expect(
			rows<{ email: string; is_admin: number }>('SELECT email, is_admin FROM users ORDER BY id')
		).toEqual([
			{ email: personas.alice.email, is_admin: 1 },
			{ email: personas.bob.email, is_admin: 0 }
		]);

		await bobContext.close();
		await adminContext.close();
	});

	test('a group invite counts uses instead of being consumed', async ({ browser }) => {
		const token = need('groupInviteToken');
		const context = await newPersonaContext(browser);
		const page = await context.newPage();

		await registerViaUI(page, personas.carol, token);
		await saveState(page, 'carol');

		expect(one('SELECT used_by, uses FROM invites WHERE token = ?', token)).toEqual({
			used_by: null,
			uses: 1
		});
		// Still valid for the next person.
		const res = await page.request.get(`/api/invites/${token}/check`);
		expect(await res.json()).toEqual({ valid: true, email: '' });

		await context.close();
	});

	test('an emailed invite is bound to its address', async ({ browser, request }) => {
		const token = need('emailInviteToken');

		// Registering under a different address is refused, even with a valid token.
		const wrong = await request.post('/api/auth/register', {
			data: {
				invite_token: token,
				email: 'someone-else@e2e.test',
				name: 'Impostor',
				password: 'impostor-123'
			}
		});
		expect(wrong.status()).toBe(403);
		expect(await wrong.json()).toEqual({ error: 'this invite belongs to another email' });
		expect(count('users')).toBe(3);

		const context = await newPersonaContext(browser);
		const page = await context.newPage();
		await page.goto(`/register?token=${token}`);

		// The address is prefilled from the invite and cannot be edited.
		const email = page.getByLabel('Email');
		await expect(email).toHaveValue(personas.dave.email);
		await expect(email).toBeDisabled();

		await registerViaUI(page, personas.dave, token);
		await saveState(page, 'dave');

		expect(count('users')).toBe(4);
		expect(one('SELECT used_by, uses FROM invites WHERE token = ?', token)).toMatchObject({ uses: 1 });
		await context.close();
	});

	test('a spent one-time invite cannot be reused', async ({ request, page }) => {
		const token = need('singleInviteToken');

		const check = await request.get(`/api/invites/${token}/check`);
		expect(await check.json()).toEqual({ valid: false, reason: 'used' });

		await page.goto(`/register?token=${token}`);
		await expect(page.getByText('This invite link has already been used')).toBeVisible();

		const res = await request.post('/api/auth/register', {
			data: { invite_token: token, email: 'late@e2e.test', name: 'Late', password: 'late-1234' }
		});
		expect(res.status()).toBe(403);
		expect(await res.json()).toEqual({ error: 'this invite link has already been used' });
		expect(count('users')).toBe(4);
	});

	test.describe('back on the admin page', () => {
		test.use({ storageState: stateFile('alice') });

		test('disabling a group invite stops new registrations', async ({ page, request }) => {
			const token = need('groupInviteToken');
			await page.goto('/admin');
			await waitForSync(page);

			await inviteRow(page, token).getByRole('button', { name: 'Disable' }).click();
			await expect(page.getByText('Invite link disabled')).toBeVisible();
			expect(one('SELECT disabled FROM invites WHERE token = ?', token)).toEqual({ disabled: 1 });

			const check = await request.get(`/api/invites/${token}/check`);
			expect(await check.json()).toEqual({ valid: false, reason: 'disabled' });

			const res = await request.post('/api/auth/register', {
				data: { invite_token: token, email: 'nope@e2e.test', name: 'Nope', password: 'nope-1234' }
			});
			expect(res.status()).toBe(403);
			expect(await res.json()).toEqual({ error: 'this invite link has been disabled' });
		});

		test('revoking an unused invite removes it', async ({ page }) => {
			const token = need('forgedOriginInviteToken');
			await page.goto('/admin');
			await waitForSync(page);

			await inviteRow(page, token).getByRole('button', { name: 'Revoke' }).click();
			await expect(page.getByText(`…${token.slice(-8)}`)).toHaveCount(0);
			expect(count('invites', 'token = ?', token)).toBe(0);
			expect(count('invites')).toBe(3);
		});
	});
});
