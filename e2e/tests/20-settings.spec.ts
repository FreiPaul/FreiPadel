import { expect, test, type Page } from '@playwright/test';
import { one, userId } from '../helpers/db';
import { personas, stateFile, type PersonaKey } from '../helpers/personas';
import { availableDates, getSettings, waitForSync } from '../helpers/app';

// Phase 20 — the two settings surfaces: the availability filter on /slots
// (which decides what the slot list shows) and the notification checkboxes on
// /user (which decide who gets mailed in phase 30).

const WEEKDAYS = ['Mo', 'Tu', 'We', 'Th', 'Fr', 'Sa', 'Su'];

async function openFilters(page: Page): Promise<void> {
	await page.goto('/slots');
	await waitForSync(page);
	await page.getByRole('button', { name: '⚙ Filters' }).click();
	// Card titles render as plain divs, not headings.
	await expect(page.getByText('My availability')).toBeVisible();
}

/** Turn every weekday on — the pills carry no ARIA state, only a class. */
async function selectAllWeekdays(page: Page): Promise<void> {
	for (const day of WEEKDAYS) {
		const pill = page.getByRole('button', { name: day, exact: true });
		const className = (await pill.getAttribute('class')) ?? '';
		if (!className.includes('bg-primary')) await pill.click();
	}
}

async function setNotifications(page: Page, { poll, booked }: { poll: boolean; booked: boolean }): Promise<void> {
	await page.goto('/user');
	const pollCreated = page.getByRole('checkbox', { name: 'Notify on new poll' });
	const slotBooked = page.getByRole('checkbox', { name: 'Notify on booked slot' });
	await expect(pollCreated).toBeVisible();
	if ((await pollCreated.isChecked()) !== poll) await pollCreated.click();
	if ((await slotBooked.isChecked()) !== booked) await slotBooked.click();
	await page.getByRole('button', { name: 'Save', exact: true }).click();
	await expect(page.getByText('Settings saved')).toBeVisible();
}

function storedNotifications(key: PersonaKey): Record<string, boolean> {
	const row = one<{ notifications: string }>(
		'SELECT notifications FROM user_settings WHERE user_id = ?',
		userId(personas[key].email)
	);
	return JSON.parse(row!.notifications) as Record<string, boolean>;
}

test.describe.serial('settings', () => {
	test.describe('availability, as the admin', () => {
		test.use({ storageState: stateFile('alice') });

		test('the default window hides the shorter, earlier slots', async ({ page }) => {
			// Defaults are Mon–Fri, 19:00–21:00, min 60 min: Second Club only
			// offers 60 minutes at 18:00, so it is filtered out entirely.
			const res = await page.request.get('/api/slots');
			const { slots } = (await res.json()) as { slots: { time: string; location: string }[] };
			expect([...new Set(slots.map((s) => s.location))]).toEqual(['Mock Padel Club']);
			expect([...new Set(slots.map((s) => s.time))]).toEqual(['19:30']);
		});

		test('widening the window brings both locations into view', async ({ page }) => {
			await openFilters(page);
			await selectAllWeekdays(page);
			await page.getByLabel('Earliest start').fill('07:00');
			await page.getByLabel('Latest start').fill('22:00');
			await page.getByLabel('Days ahead').fill('21');
			await page.getByLabel('Min. duration').fill('60');
			await page.getByRole('button', { name: 'Save availability' }).click();
			await expect(page.getByText('Availability saved')).toBeVisible();

			expect(
				one('SELECT weekdays, time_start, time_end, days_ahead, min_duration FROM user_settings WHERE user_id = ?', 1)
			).toEqual({
				weekdays: '[0,1,2,3,4,5,6]',
				time_start: '07:00',
				time_end: '22:00',
				days_ahead: 21,
				min_duration: 60
			});

			const settings = await getSettings(page);
			expect(settings.weekdays).toEqual([0, 1, 2, 3, 4, 5, 6]);
			expect(settings.locations).toEqual([]);

			const res = await page.request.get('/api/slots');
			const { slots } = (await res.json()) as { slots: { time: string; location: string }[] };
			expect([...new Set(slots.map((s) => s.location))].sort()).toEqual(['Mock Padel Club', 'Second Club']);
			expect([...new Set(slots.map((s) => s.time))].sort()).toEqual(['18:00', '19:30']);

			// Today drops out of the list once its last start time has passed.
			const dates = await availableDates(page);
			expect(dates.length).toBeGreaterThanOrEqual(20);
			expect(dates.length).toBeLessThanOrEqual(21);
		});

		test('a location filter narrows the list, and clearing it restores everything', async ({ page }) => {
			await openFilters(page);
			await page.getByRole('button', { name: 'Second Club', exact: true }).click();
			await page.getByRole('button', { name: 'Save availability' }).click();
			await expect(page.getByText('Availability saved')).toBeVisible();

			expect(one('SELECT locations FROM user_settings WHERE user_id = ?', 1)).toEqual({
				locations: '["Second Club"]'
			});
			let res = await page.request.get('/api/slots');
			let body = (await res.json()) as { slots: { location: string }[] };
			expect([...new Set(body.slots.map((s) => s.location))]).toEqual(['Second Club']);

			// Deselect it again — phase 30 needs the full list.
			await page.getByRole('button', { name: 'Second Club', exact: true }).click();
			await page.getByRole('button', { name: 'Save availability' }).click();
			await expect(page.getByText('Availability saved').last()).toBeVisible();

			expect(one('SELECT locations FROM user_settings WHERE user_id = ?', 1)).toEqual({ locations: '[]' });
			res = await page.request.get('/api/slots');
			body = (await res.json()) as { slots: { location: string }[] };
			expect([...new Set(body.slots.map((s) => s.location))].sort()).toEqual(['Mock Padel Club', 'Second Club']);
		});
	});

	// Who hears about what in phase 30. Bob and Carol opt in; Dave votes but
	// stays opted out, and the admin never opts in at all.
	test.describe('notifications, as Bob', () => {
		test.use({ storageState: stateFile('bob') });
		test('opts into both notifications', async ({ page }) => {
			await setNotifications(page, { poll: true, booked: true });
			expect(storedNotifications('bob')).toEqual({ poll_created: true, slot_booked: true });
		});
	});

	test.describe('notifications, as Carol', () => {
		test.use({ storageState: stateFile('carol') });
		test('opts into both notifications', async ({ page }) => {
			await setNotifications(page, { poll: true, booked: true });
			expect(storedNotifications('carol')).toEqual({ poll_created: true, slot_booked: true });
		});
	});

	test.describe('notifications, as Dave', () => {
		test.use({ storageState: stateFile('dave') });
		test('saves his settings without opting in', async ({ page }) => {
			// Saving with both boxes clear must persist "off", not "unset".
			await setNotifications(page, { poll: false, booked: false });
			expect(storedNotifications('dave')).toEqual({ poll_created: false, slot_booked: false });
		});
	});

	test('the admin never opted in', () => {
		expect(storedNotifications('alice')).toEqual({ poll_created: false, slot_booked: false });
	});
});
