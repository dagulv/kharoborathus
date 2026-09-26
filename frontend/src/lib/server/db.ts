import postgres from 'postgres';
import { POSTGRES_CONNECTION_STRING } from '$app/env/private';

let client: postgres.Sql | undefined;

// Lazily connect so builds and prerendering don't require a database
export function db(): postgres.Sql {
	if (!client) {
		client = postgres(POSTGRES_CONNECTION_STRING, {
			max: 10,
			idle_timeout: 15 * 60,
			connect_timeout: 5,
			transform: postgres.camel
		});
	}

	return client;
}
