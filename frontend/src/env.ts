import { defineEnvVars } from '@sveltejs/kit/env';
import * as v from 'valibot';

export const variables = defineEnvVars({
	POSTGRES_CONNECTION_STRING: {
		schema: v.pipe(v.string(), v.nonEmpty()),
		description: 'Postgres connection string, shared with the Go app'
	},
	API_URL: {
		schema: (value) => (value || 'http://localhost:3002').replace(/\/+$/, ''),
		description: 'Base URL of the Go app internal HTTP server'
	},
	ADMIN_TOKEN: {
		schema: v.pipe(v.string(), v.nonEmpty()),
		description: 'Token for the admin pages and remote functions'
	}
});
