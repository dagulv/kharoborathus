import { defineEnvVars } from '@sveltejs/kit/env';
import * as v from 'valibot';

export const variables = defineEnvVars({
	POSTGRES_CONNECTION_STRING: {
		schema: v.pipe(v.string(), v.nonEmpty()),
		description: 'Postgres connection string, shared with the Go app'
	}
});
