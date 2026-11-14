import { defineEnvVars } from '@sveltejs/kit/env';

export const variables = defineEnvVars({
	PUBLIC_API_URL: {
		public: true,
		static: true,
		schema: (value) => (value || 'http://localhost:3001').replace(/\/+$/, ''),
		description: 'Base URL of the Go app, which serves every admin route'
	}
});
