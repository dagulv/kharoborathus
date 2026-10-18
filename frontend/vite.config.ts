import tailwindcss from '@tailwindcss/vite';
import adapter from '@sveltejs/adapter-node';
import { sveltekit } from '@sveltejs/kit/vite';
import { defineConfig, loadEnv } from 'vite';

export default defineConfig(({ mode }) => {
	// kit reads one env dir, so the deploy's safe values go in through the process env, web's over db's.
	// Only keys the root .env and the shell lack, so dev overrides like API_URL win. Prod reads only the process env.
	const root = loadEnv(mode, '..', '');
	for (const dir of ['../infra/web', '../infra/db']) {
		for (const [key, value] of Object.entries(loadEnv(mode, dir, ''))) {
			if (!(key in root) && process.env[key] === undefined) process.env[key] = value;
		}
	}

	// Like the Go app, build the connection string from its parts when nothing sets it.
	const get = (key: string) => root[key] ?? process.env[key] ?? 'postgres';
	if (
		root.POSTGRES_CONNECTION_STRING === undefined &&
		process.env.POSTGRES_CONNECTION_STRING === undefined
	) {
		process.env.POSTGRES_CONNECTION_STRING = `postgresql://${get('POSTGRES_USER')}:${get('POSTGRES_PASSWORD')}@localhost:5432/${get('POSTGRES_DB')}?sslmode=disable`;
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
