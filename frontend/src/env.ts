import { defineEnvVars } from '@sveltejs/kit/env';

export const variables = defineEnvVars({
	API_URL: {
		schema: (value) => (value || 'http://localhost:3001').replace(/\/+$/, ''),
		description: 'Base URL of the Go app HTTP server, which serves the public posts'
	}
});
