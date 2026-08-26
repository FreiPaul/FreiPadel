import { expect, test } from '@playwright/test';
import { count, one, rows, scalar, userId } from '../helpers/db';
import { clearMail, mailCount, waitForMailCount } from '../helpers/mail';
import { personas, stateFile } from '../helpers/personas';
import { need, writeScratch } from '../helpers/scratch';
import {
	availableDates,
	clubSwitcher,
	copyToClipboard,
	getClubs,
	switchClub,
	tokenFromURL,
	waitForSync
} from '../helpers/app';

// Phase 50 — a second club. Everything up to here happened inside the "All"
// club the first registration founded; this splits the deployment in two and
// proves the halves cannot see each other.
//
// Note this runs after phase 40, so Bob's address is bob.new@e2e.test by now.
// It is read from the database rather than hard-coded.
test.describe.serial('clubs', () => {
	const CREW = 'Köln Crew';

	function bobEmail(): string {
		return one<{ email: string }>('SELECT email FROM users WHERE name = ?', personas.bob.name)!.email;
	}

	test.describe('as the admin', () => {
		test.use({ storageState: stateFile('alice') });

		test('creates a second club restricted to one venue', async ({ page }) => {
			await page.goto('/admin?tab=clubs');
			await waitForSync(page);

			await page.getByPlaceholder('New club name').fill(CREW);
			await page.getByRole('button', { name: '+ Club' }).click();
			await expect(page.getByText(`Created ${CREW}`)).toBeVisible();

			const club = one<{ id: number; name: string; owner_id: number; locations: string }>(
				'SELECT id, name, owner_id, locations FROM clubs WHERE name = ?',
				CREW
			);
			expect(club).toMatchObject({ name: CREW, owner_id: userId(personas.alice.email) });
			// A new club starts unrestricted: an empty list means every venue.
			expect(club!.locations).toBe('[]');
			// Creating a club makes its owner a member, or nobody could see it.
			expect(count('club_members', 'club_id = ?', club!.id)).toBe(1);

			// Narrow it to a single venue, which is what the isolation below
			// hangs on: Köln Crew plays at Second Club only.
			await page.getByRole('button', { name: 'Edit' }).last().click();
			await page.getByTestId('club-venue').filter({ hasText: 'Second Club' }).click();
			await expect
				.poll(() => one<{ locations: string }>('SELECT locations FROM clubs WHERE id = ?', club!.id)!.locations)
				.toBe('["Second Club"]');

			writeScratch({ crewClubId: club!.id });
		});

		test('invites into the club it names, not the one the admin is in', async ({ page }) => {
			await page.goto('/admin');
			await waitForSync(page);

			await page.getByTestId('invite-club').filter({ hasText: CREW }).click();
			const url = await copyToClipboard(page, () =>
				page.getByRole('button', { name: '+ One-time link' }).click()
			);
			const token = tokenFromURL(url);

			expect(
				one<{ club_id: number }>('SELECT club_id FROM invites WHERE token = ?', token)
			).toEqual({ club_id: need('crewClubId') });
			// The admin is still looking at "All" — the invite followed the
			// selector, not the active club.
			expect(
				one<{ active_club_id: number }>(
					'SELECT active_club_id FROM users WHERE email = ?',
					personas.alice.email
				)
			).toEqual({ active_club_id: need('allClubId') });

			writeScratch({ crewInviteToken: token });
		});
	});

	test.describe('as an existing account', () => {
		test.use({ storageState: stateFile('bob') });

		test('joins the club from the invite link without registering again', async ({ page }) => {
			const before = count('users');
			await page.goto(`/register?token=${need('crewInviteToken')}`);

			// Signed in already, so the page offers to join rather than to
			// create a second account.
			await expect(page.getByText(`Signed in as`)).toBeVisible();
			await page.getByRole('button', { name: `Join ${CREW}` }).click();
			await expect(page).toHaveURL(/\/slots$/);
			await waitForSync(page);

			// No new account, and the one-time invite is spent.
			expect(count('users')).toBe(before);
			expect(
				one<{ uses: number; used_by: number | null }>(
					'SELECT uses, used_by FROM invites WHERE token = ?',
					need('crewInviteToken')
				)
			).toEqual({ uses: 1, used_by: userId(bobEmail()) });

			// He is in both clubs, and the new one is what he is looking at.
			expect(count('club_members', 'user_id = ?', userId(bobEmail()))).toBe(2);
			expect(
				one<{ active_club_id: number }>('SELECT active_club_id FROM users WHERE name = ?', personas.bob.name)
			).toEqual({ active_club_id: need('crewClubId') });
			await expect(clubSwitcher(page)).toContainText(CREW);
		});

		test('a spent invite tells him he is already in the club', async ({ page }) => {
			await page.goto(`/register?token=${need('crewInviteToken')}`);
			// Reusing a one-time link is refused for a stranger, but the person
			// who already redeemed it is simply told they are in.
			await expect(page.getByText(`You are already in ${CREW}`)).toBeVisible();
		});

		test('the club decides which venues he can see', async ({ page }) => {
			await page.goto('/slots');
			await waitForSync(page);
			await expect(clubSwitcher(page)).toContainText(CREW);

			// Köln Crew plays at Second Club only.
			const res = await page.request.get('/api/slots');
			const crewSlots = ((await res.json()) as { slots: { location: string }[] }).slots;
			expect(crewSlots.length).toBeGreaterThan(0);
			expect([...new Set(crewSlots.map((s) => s.location))]).toEqual(['Second Club']);

			// The other club is unrestricted, so it still sees both venues.
			await switchClub(page, 'All');
			await expect
				.poll(async () => {
					const all = await page.request.get('/api/slots');
					const slots = ((await all.json()) as { slots: { location: string }[] }).slots;
					return [...new Set(slots.map((s) => s.location))].sort();
				})
				.toEqual(['Mock Padel Club', 'Second Club']);
		});

		test('a poll belongs to the club it was started in', async ({ page }) => {
			await clearMail();
			await page.goto('/slots');
			await waitForSync(page);
			await switchClub(page, CREW);

			const dates = await availableDates(page);
			expect(dates.length).toBeGreaterThan(2);

			await page.getByRole('button', { name: '+ Start slot poll' }).click();
			await page.getByRole('checkbox').nth(1).click();
			await page.getByPlaceholder('Poll name').fill('Crew night');
			await page.getByRole('button', { name: /Start poll with 1 slot/ }).click();
			await expect(page).toHaveURL(/\/polls$/);

			const poll = one<{ id: number; club_id: number; title: string }>(
				'SELECT id, club_id, title FROM polls WHERE title = ?',
				'Crew night'
			);
			expect(poll!.club_id).toBe(need('crewClubId'));

			// Only the club is told about it. Carol has both notifications on
			// but is not in Köln Crew, so the fan-out must skip her; Alice is in
			// the club but opted out. That leaves Bob alone.
			await waitForMailCount({ subject: 'New Padel Poll' }, 1);
			expect(await mailCount({ to: personas.carol.email })).toBe(0);
			expect(await mailCount({ to: bobEmail(), subject: 'New Padel Poll' })).toBe(1);
		});

		test('switching back to the other club hides it again', async ({ page }) => {
			await page.goto('/polls');
			await waitForSync(page);
			await expect(clubSwitcher(page)).toContainText(CREW);
			await expect(page.getByText('Crew night')).toBeVisible();

			await switchClub(page, 'All');
			await expect(page.getByText('Crew night')).toHaveCount(0);

			// The switch is recorded on the account, not just in this tab.
			expect(
				one<{ active_club_id: number }>('SELECT active_club_id FROM users WHERE name = ?', personas.bob.name)
			).toEqual({ active_club_id: need('allClubId') });
		});
	});

	test.describe('as a member of the other club', () => {
		test.use({ storageState: stateFile('carol') });

		test('cannot see the club she was never invited to', async ({ page }) => {
			await page.goto('/polls');
			await waitForSync(page);

			// One club, and no way to switch to the other.
			const mine = await getClubs(page);
			expect(mine.clubs.map((c) => c.name)).toEqual(['All']);
			expect(mine.active_club_id).toBe(need('allClubId'));

			await clubSwitcher(page).click();
			await expect(page.getByRole('menuitem', { name: CREW })).toHaveCount(0);
			await page.keyboard.press('Escape');

			// Nor through the API: the poll list stops at the club boundary.
			const res = await page.request.get('/api/polls');
			const polls = (await res.json()) as { title: string }[];
			expect(polls.map((p) => p.title)).not.toContain('Crew night');
			expect(scalar<number>('SELECT COUNT(*) AS n FROM polls')).toBe(2);
		});

		test('cannot vote in or delete another club\'s poll', async ({ page }) => {
			const crewPoll = one<{ id: number }>('SELECT id FROM polls WHERE title = ?', 'Crew night')!.id;
			const slot = one<{ id: number }>('SELECT id FROM poll_slots WHERE poll_id = ?', crewPoll)!.id;

			const vote = await page.request.post(`/api/polls/${crewPoll}/vote`, {
				data: { poll_slot_id: slot, vote: true }
			});
			expect(vote.status()).toBe(403);
			expect(await vote.json()).toEqual({ error: 'this poll belongs to another club' });

			const remove = await page.request.delete(`/api/polls/${crewPoll}`);
			expect(remove.status()).toBe(403);

			expect(count('votes', 'poll_slot_id = ?', slot)).toBe(0);
			expect(count('polls', 'id = ?', crewPoll)).toBe(1);
		});

		test('cannot activate or manage a club she is not in', async ({ page }) => {
			const crew = need('crewClubId');

			const activate = await page.request.post(`/api/clubs/${crew}/activate`);
			expect(activate.status()).toBe(403);
			expect(await activate.json()).toEqual({ error: 'not a member of this club' });

			const rename = await page.request.patch(`/api/clubs/${crew}`, { data: { name: 'Hijacked' } });
			expect(rename.status()).toBe(403);

			const invite = await page.request.post('/api/invites', { data: { kind: 'single', club_id: crew } });
			expect(invite.status()).toBe(403);

			expect(one<{ name: string }>('SELECT name FROM clubs WHERE id = ?', crew)).toEqual({ name: CREW });
			expect(count('invites', 'club_id = ?', crew)).toBe(1);
		});
	});

	test.describe('deleting a club', () => {
		test.use({ storageState: stateFile('alice') });

		test('is refused while anyone is still in it', async ({ page }) => {
			const crew = need('crewClubId');
			const res = await page.request.delete(`/api/clubs/${crew}`);
			expect(res.status()).toBe(409);
			expect(await res.json()).toEqual({ error: 'remove every member before deleting this club' });
			expect(count('clubs')).toBe(2);
		});
	});
});
