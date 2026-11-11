import { existsSync, readFileSync } from 'node:fs';
import { parseEnv } from 'node:util';
import { loadEnv } from 'vite';

// The deploy's API_URL names the api container, which dev never runs, so dev keeps the localhost default.
const deployOnly = new Set(['API_URL']);

// Shared by vite.config.ts and scripts/, which run outside kit's env handling.
export function loadDevEnv(mode: string) {
	// kit reads one env dir, so the deploy's safe values go in through the process env, web's over db's.
	// Only keys the root .env and the shell lack, so dev overrides like API_URL win. Prod reads only the process env.
	const root = loadEnv(mode, '..', '');
	// loadEnv only reads files named .env, so the deploy's <service>.env files are parsed here.
	// The Docker build has neither file and reads its env from the process.
	for (const file of ['../infra/web/web.env', '../infra/db/db.env']) {
		if (!existsSync(file)) continue;

		for (const [key, value] of Object.entries(parseEnv(readFileSync(file, 'utf8')))) {
			if (!(key in root) && process.env[key] === undefined && !deployOnly.has(key)) {
				process.env[key] = value;
			}
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

	return root;
}
