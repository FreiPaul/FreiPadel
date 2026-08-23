import { expect, test, type Page } from '@playwright/test';
import { count, one, rows } from '../helpers/db';
import { clearMail, linkTo, mailCount, waitForMail, waitForMailCount } from '../helpers/mail';
import { personas, stateFile } from '../helpers/personas';
import { need, writeScratch } from '../helpers/scratch';
import { availableDates, newPersonaContext, waitForSync } from '../helpers/app';

// Phase 30 — the core loop: propose slots, everyone votes, the creator closes
// on a winner. Both notification fan-outs run here, and both are goroutines,
// so every mail assertion waits.

const POLL_TITLE = 'E2E Padel Poll';

/** Mirrors the backend's formatSlotWhen time range, e.g. "18:00–19:30 (90 min)". */
function timeRange(start: string, durationMinutes: number): string {
	const [h, m] = start.split(':').map(Number);
	const end = new Date(Date.UTC(2000, 0, 1, h, m + durationMinutes));
	const hh = String(end.getUTCHours()).padStart(2, '0');
	const mm = String(end.getUTCMinutes()).padStart(2, '0');
	return `${start}\u2013${hh}:${mm} (${durationMinutes} min)`;
}

/** The slot row a vote button belongs to. Rows render in poll-slot order. */
function slotRow(page: Page, index: number) {
	return page
		.getByRole('button', { name: '👍 I have time' })
		.nth(index)
		.locator('xpath=ancestor::div[contains(@class,"rounded-md")][1]');
}

async function vote(page: Page, slotIndex: number, yes: boolean): Promise<void> {
	await page.goto('/polls');
	await waitForSync(page);
	const row = slotRow(page, slotIndex);
	await row.getByRole('button', { name: yes ? '👍 I have time' : '👎 No time' }).click();
}

test.describe.serial('polls', () => {
	test.describe('as the admin', () => {
		test.use({ storageState: stateFile('alice') });

		test('starts a poll from two slots', async ({ page }) => {
			await clearMail();
			await page.goto('/slots');
			await waitForSync(page);

			// Pick dates at least two days out: a slot on today's date vanishes
			// from the list once its start time passes, and one on tomorrow's
			// would expire if the suite ever ran near midnight.
			const dates = await availableDates(page);
			const [dateA, dateB] = [dates[2], dates[3]];

			const res = await page.request.get('/api/slots');
			const { slots } = (await res.json()) as {
				slots: { date: string; time: string; location: string; courts: string[]; min_price: number }[];
			};
			// The list renders in exactly the order the API returns, so the
			// checkbox index is the slot index.
			const indexA = slots.findIndex((s) => s.date === dateA && s.location === 'Mock Padel Club');
			const indexB = slots.findIndex((s) => s.date === dateB && s.location === 'Mock Padel Club');
			expect(indexA).toBeGreaterThanOrEqual(0);
			expect(indexB).toBeGreaterThan(indexA);

			await page.getByRole('button', { name: '+ Start slot poll' }).click();
			await page.getByRole('checkbox').nth(indexA).click();
			await page.getByRole('checkbox').nth(indexB).click();
			await page.getByPlaceholder('Poll name').fill(POLL_TITLE);
			await page.getByRole('button', { name: 'Start poll with 2 slots' }).click();

			await expect(page).toHaveURL(/\/polls$/);
			await expect(page.getByText(POLL_TITLE)).toBeVisible();
			await expect(page.getByText(`started by ${personas.alice.name}`)).toBeVisible();

			const poll = one<{ id: number; title: string; creator_id: number; status: string; winning_slot_id: null }>(
				'SELECT id, title, creator_id, status, winning_slot_id FROM polls'
			);
			expect(poll).toEqual({ id: 1, title: POLL_TITLE, creator_id: 1, status: 'active', winning_slot_id: null });

			const pollSlots = rows<{
				id: number;
				date: string;
				time: string;
				duration_minutes: number;
				location: string;
				court: string;
				price: number;
				currency: string;
			}>('SELECT id, date, time, duration_minutes, location, court, price, currency FROM poll_slots ORDER BY id');
			expect(pollSlots).toHaveLength(2);
			expect(pollSlots[0]).toMatchObject({
				date: dateA,
				location: 'Mock Padel Club',
				// The selected group's courts are collapsed into one field.
				court: slots[indexA].courts.join(', '),
				price: slots[indexA].min_price,
				currency: 'EUR'
			});
			expect(pollSlots[1]).toMatchObject({ date: dateB, location: 'Mock Padel Club' });

			writeScratch({
				pollId: poll!.id,
				slotAId: pollSlots[0].id,
				slotBId: pollSlots[1].id,
				dateA,
				dateB
			});
		});
	});

	test('only the users who opted in are told about the new poll', async () => {
		// Bob and Carol enabled poll_created in phase 20; Dave and the admin
		// (who created it) did not.
		const messages = await waitForMailCount({ subject: 'New Padel Poll' }, 2);
		expect(messages.map((m) => m.To[0].Address).sort()).toEqual([personas.bob.email, personas.carol.email]);
		expect(await mailCount()).toBe(2);

		const mail = await waitForMail({ to: personas.bob.email, subject: 'New Padel Poll' });
		// Built from PUBLIC_ORIGIN, not from the origin the browser sent.
		expect(linkTo(mail.HTML, '/polls')).toBe('http://localhost:8099/polls');
		expect(mail.From?.Address).toBe('freipadel@e2e.test');
	});

	test('votes from three members reach the admin live', async ({ browser }) => {
		const [slotA, slotB] = [need('slotAId'), need('slotBId')];

		// The admin watches the poll without ever reloading — every count below
		// has to arrive over the SSE delta stream.
		const adminContext = await newPersonaContext(browser, 'alice');
		const admin = await adminContext.newPage();
		await admin.goto('/polls');
		await waitForSync(admin);
		await expect(slotRow(admin, 0).getByText('0 yes')).toBeVisible();

		const bobContext = await newPersonaContext(browser, 'bob');
		await vote(await bobContext.newPage(), 0, true);
		await expect(admin.getByText(`👍 ${personas.bob.name}`)).toBeVisible();

		// Carol cannot make slot A but can make slot B.
		const carolContext = await newPersonaContext(browser, 'carol');
		const carol = await carolContext.newPage();
		await vote(carol, 0, false);
		await vote(carol, 1, true);

		const daveContext = await newPersonaContext(browser, 'dave');
		await vote(await daveContext.newPage(), 0, true);

		await expect(slotRow(admin, 0).getByText('2 yes')).toBeVisible();
		await expect(slotRow(admin, 0).getByText('1 no')).toBeVisible();
		await expect(slotRow(admin, 1).getByText('1 yes')).toBeVisible();
		await expect(admin.getByText(`👎 ${personas.carol.name}`)).toBeVisible();
		await expect(admin.getByText(`👍 ${personas.dave.name}`)).toBeVisible();

		expect(
			rows<{ poll_slot_id: number; user_id: number; vote: number }>(
				'SELECT poll_slot_id, user_id, vote FROM votes ORDER BY poll_slot_id, user_id'
			)
		).toEqual([
			{ poll_slot_id: slotA, user_id: 2, vote: 1 }, // Bob, yes
			{ poll_slot_id: slotA, user_id: 3, vote: 0 }, // Carol, no
			{ poll_slot_id: slotA, user_id: 4, vote: 1 }, // Dave, yes
			{ poll_slot_id: slotB, user_id: 3, vote: 1 } // Carol, yes
		]);

		for (const context of [bobContext, carolContext, daveContext, adminContext]) await context.close();
	});

	test.describe('closing the poll', () => {
		test.use({ storageState: stateFile('alice') });

		test('picks a winning slot and mails the voters', async ({ page }) => {
			await clearMail();
			await page.goto('/polls');
			await waitForSync(page);

			await page.getByRole('button', { name: 'Close poll' }).click();
			const dialog = page.getByRole('dialog');
			await expect(dialog.getByText(`Close “${POLL_TITLE}”`)).toBeVisible();

			// Radio 0 is "no winning slot"; the poll's slots follow in order.
			// Nothing is preselected here — that needs 4 yes votes.
			await expect(dialog.locator('input[name="winner"]').first()).toBeChecked();
			await dialog.locator('input[name="winner"]').nth(1).check();
			await dialog.getByRole('button', { name: 'Close poll' }).click();
			await expect(page.getByText('Poll closed')).toBeVisible();

			const poll = one<{ status: string; winning_slot_id: number; closed_at: string | null }>(
				'SELECT status, winning_slot_id, closed_at FROM polls WHERE id = ?',
				need('pollId')
			);
			expect(poll).toMatchObject({ status: 'closed', winning_slot_id: need('slotAId') });
			expect(poll!.closed_at).toBeTruthy();

			await expect(page.getByText('🏆 Booked slot')).toBeVisible();
			await expect(page.getByRole('button', { name: '👍 I have time' })).toHaveCount(0);
		});

		test('tells the winners and the also-rans apart, and skips everyone else', async () => {
			// Bob voted yes on the slot that got booked.
			const bob = await waitForMail({ to: personas.bob.email, subject: 'Padel Slot Booked' });
			expect(bob.HTML).toContain('A padel slot has been booked');
			expect(bob.HTML).toContain(POLL_TITLE);
			// The "When"/"Where" lines describe the slot that was actually booked.
			const winner = one<{ time: string; duration_minutes: number; location: string }>(
				'SELECT time, duration_minutes, location FROM poll_slots WHERE id = ?',
				need('slotAId')
			);
			expect(bob.HTML).toContain(winner!.location);
			expect(bob.HTML).toContain(timeRange(winner!.time, winner!.duration_minutes));
			expect(linkTo(bob.HTML, '/polls')).toBe('http://localhost:8099/polls');

			// Carol voted, but not for the winning slot.
			const carol = await waitForMail({ to: personas.carol.email, subject: 'Another Padel Slot Was Booked' });
			expect(carol.HTML).toContain('Another padel slot was booked');
			expect(carol.HTML).toContain('you are not on the list for this one');

			// Dave voted yes on the winner but never opted in; the admin opted
			// out and did not vote at all. Exactly two mails, no more.
			const messages = await waitForMailCount({}, 2);
			expect(messages.map((m) => m.To[0].Address).sort()).toEqual([personas.bob.email, personas.carol.email]);
		});

		test('a closed poll rejects further votes', async ({ page }) => {
			const res = await page.request.post(`/api/polls/${need('pollId')}/vote`, {
				data: { poll_slot_id: need('slotBId'), vote: true }
			});
			expect(res.status()).toBe(409);
			expect(await res.json()).toEqual({ error: 'poll is closed' });
			expect(count('votes')).toBe(4);
		});
	});
});
