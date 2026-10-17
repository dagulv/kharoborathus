import { createHash, timingSafeEqual } from 'node:crypto';
import type { Handle } from '@sveltejs/kit/hooks';
import { ADMIN_PASSWORD, ADMIN_USER } from '$app/env/private';

// Hashing gives equal lengths, which timingSafeEqual requires.
const digest = (value: string) => createHash('sha256').update(value).digest();

const expected = digest(`${ADMIN_USER}:${ADMIN_PASSWORD}`);

// The admin has no login and its remote functions live outside /admin, so every route needs auth.
// adapter-node serves the static build before this hook, so bundles stay public. They hold code, never data.
export const handle: Handle = ({ event, resolve }) => {
	// Haloy's health check sends no credentials.
	if (event.url.pathname === '/healthz') return new Response('ok');

	const header = event.request.headers.get('authorization') ?? '';
	const given = header.startsWith('Basic ')
		? Buffer.from(header.slice('Basic '.length), 'base64').toString()
		: '';

	if (!timingSafeEqual(digest(given), expected)) {
		return new Response('Unauthorized', {
			status: 401,
			headers: { 'www-authenticate': 'Basic realm="admin", charset="UTF-8"' }
		});
	}

	return resolve(event);
};
