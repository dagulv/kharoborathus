import type { Handle } from '@sveltejs/kit/hooks';

export const handle: Handle = async ({ event, resolve }) => {
	// Haloy's health check sends no credentials.
	if (event.url.pathname === '/healthz') return new Response('ok');

	return resolve(event);
};
