import adapter from '@sveltejs/adapter-static';

/** @type {import('@sveltejs/kit').Config} */
const config = {
	compilerOptions: {
		// Force runes mode for the project, except for libraries. Can be removed in svelte 6.
		runes: ({ filename }) => (filename.split(/[/\\]/).includes('node_modules') ? undefined : true)
	},
	kit: {
		// Static SPA build served by the Go backend; index.html is the
		// fallback for client-side routes.
		adapter: adapter({
			fallback: 'index.html'
		})
	}
};

export default config;
