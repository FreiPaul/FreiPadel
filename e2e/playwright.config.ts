import { defineConfig, devices } from '@playwright/test';
import { APP_URL, TMP } from './helpers/env';

// One stateful journey against one container, so: a single worker, no
// parallelism and no retries. Retrying a test that already registered a user
// or closed a poll cannot succeed, and would only hide the real failure.
export default defineConfig({
	testDir: './tests',
	fullyParallel: false,
	workers: 1,
	retries: 0,
	forbidOnly: !!process.env.CI,
	timeout: 60_000,
	expect: { timeout: 15_000 },
	globalSetup: './global-setup.ts',
	globalTeardown: './global-teardown.ts',
	outputDir: `${TMP}/test-results`,
	reporter: process.env.CI
		? [['github'], ['list'], ['html', { outputFolder: `${TMP}/report`, open: 'never' }]]
		: [['list'], ['html', { outputFolder: `${TMP}/report`, open: 'never' }]],
	use: {
		baseURL: APP_URL,
		trace: 'retain-on-failure',
		video: 'retain-on-failure',
		screenshot: 'only-on-failure',
		// The admin page copies invite links to the clipboard.
		permissions: ['clipboard-read', 'clipboard-write']
	},
	// Chromium only: clipboard-read is not grantable in Firefox or WebKit, and
	// this suite tests the server stack rather than browser compatibility.
	projects: [{ name: 'chromium', use: { ...devices['Desktop Chrome'] } }]
});
