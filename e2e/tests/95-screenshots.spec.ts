import { expect, test, type Browser, type Page } from '@playwright/test';
import { mkdirSync } from 'node:fs';
import { join } from 'node:path';
import { availableDates, loginViaUI, newPersonaContext, waitForSync } from '../helpers/app';
import { E2E_DIR } from '../helpers/env';
import { personas, type PersonaKey } from '../helpers/personas';

// Regenerates the images the documentation links to, driving the same stack
// the rest of the suite drives, so a screenshot cannot quietly drift away from
// the application. Off unless SCREENSHOTS is set: it writes into the working
// tree, which a CI run must not do.

const IMAGES = join(E2E_DIR, '..', 'docs', 'img');
const POLL_TITLE = 'Thursday evening';

/** The slot row a vote button belongs to. Only an active poll renders these. */
function slotRow(page: Page, index: number) {
	return page
		.getByRole('button', { name: '👍 I have time' })
		.nth(index)
		.locator('xpath=ancestor::div[contains(@class,"rounded-md")][1]');
}

async function voteAs(browser: Browser, key: PersonaKey, votes: [number, boolean][]): Promise<void> {
	const context = await newPersonaContext(browser, key);
	const page = await context.newPage();
	await page.goto('/polls');
	await waitForSync(page);
	for (const [index, yes] of votes) {
		await slotRow(page, index)
			.getByRole('button', { name: yes ? '👍 I have time' : '👎 No time' })
			.click();
	}
	await context.close();
}

test.describe.serial('screenshots', () => {
	test.skip(!process.env.SCREENSHOTS, 'set SCREENSHOTS=1 to regenerate the images in docs/img');
	test.use({ viewport: { width: 1280, height: 900 } });

	// This phase runs last, after the permissions phase has logged the admin
	// out on purpose, so it logs in again instead of restoring a session that
	// no longer exists.
	test.beforeEach(async ({ page }) => {
		await loginViaUI(page, personas.alice);
	});

	test('the slot list', async ({ page }) => {
		mkdirSync(IMAGES, { recursive: true });
		await waitForSync(page);
		await expect(page.getByRole('heading', { name: 'Available slots' })).toBeVisible();
		await page.screenshot({ path: join(IMAGES, 'slots.png') });
	});

	test('a poll with a slot everyone can play', async ({ page, browser }) => {
		await waitForSync(page);

		// The polls the earlier phases left behind carry their test names into
		// the picture, so clear them before building the one to show.
		const existing = (await (await page.request.get('/api/polls')).json()) as { id: number }[];
		for (const poll of existing) {
			expect((await page.request.delete(`/api/polls/${poll.id}`)).ok()).toBeTruthy();
		}

		const dates = await availableDates(page);
		expect(dates.length).toBeGreaterThan(4);

		const res = await page.request.get('/api/slots');
		const { slots } = (await res.json()) as { slots: { date: string; location: string }[] };
		const indices = [dates[2], dates[3], dates[4]].map((date) =>
			slots.findIndex((s) => s.date === date && s.location === 'Mock Padel Club')
		);
		expect(Math.min(...indices)).toBeGreaterThanOrEqual(0);

		await page.getByRole('button', { name: '+ Start slot poll' }).click();
		for (const index of indices) await page.getByRole('checkbox').nth(index).click();
		await page.getByPlaceholder('Poll name').fill(POLL_TITLE);
		await page.getByRole('button', { name: `Start poll with ${indices.length} slots` }).click();
		await expect(page).toHaveURL(/\/polls$/);

		// Four yes votes on the first slot make it a match, which is the state
		// worth showing. The second slot gets a split to show a contested one.
		await voteAs(browser, 'bob', [
			[0, true],
			[1, false]
		]);
		await voteAs(browser, 'carol', [
			[0, true],
			[1, true]
		]);
		await voteAs(browser, 'dave', [[0, true]]);

		await page.reload();
		await waitForSync(page);
		await slotRow(page, 0).getByRole('button', { name: '👍 I have time' }).click();
		await expect(slotRow(page, 0).getByText('4 yes')).toBeVisible();

		// Crop to where the poll actually ends, so the image does not carry a
		// few hundred pixels of empty page under it.
		const card = await page.getByText(POLL_TITLE).locator('xpath=ancestor::div[4]').boundingBox();
		expect(card).not.toBeNull();
		await page.screenshot({
			path: join(IMAGES, 'poll.png'),
			clip: { x: 0, y: 0, width: 1280, height: Math.ceil(card!.y + card!.height + 24) }
		});
	});
});
