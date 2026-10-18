import tailwindcss from '@tailwindcss/vite';
import adapter from '@sveltejs/adapter-node';
import { sveltekit } from '@sveltejs/kit/vite';
import { defineConfig, loadEnv } from 'vite';

export default defineConfig(({ mode }) => {
	// kit reads one env dir, so infra/web/.env, the deploy's safe values, goes in through the process env.
	// Only keys the root .env and the shell lack, so dev overrides like API_URL win. Prod reads only the process env.
	const root = loadEnv(mode, '..', '');
	for (const [key, value] of Object.entries(loadEnv(mode, '../infra/web', ''))) {
		if (!(key in root)) process.env[key] = value;
	}

	return {
		plugins: [
			tailwindcss(),
			sveltekit({
				compilerOptions: {
					// Force runes mode for the project, except for libraries. Can be removed in svelte 6.
					runes: ({ filename }) =>
						filename.split(/[/\\]/).includes('node_modules') ? undefined : true,
					experimental: { async: true }
				},
				adapter: adapter(),
				// The root .env, shared with the Go app and filled by infra/setup.sh.
				env: { dir: '..' },
				experimental: { remoteFunctions: true, forkPreloads: true }
			})
		]
	};
});
