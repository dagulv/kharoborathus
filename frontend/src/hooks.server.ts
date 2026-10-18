import type { Handle } from '@sveltejs/kit/hooks';
import { SESSION_COOKIE, validSession } from '#lib/server/auth.js';

// Remote functions live under /_app/remote, not /admin, and every one of them is admin.
// adapter-node serves the static build before this hook, so bundles stay public. They hold code, never data.
function isProtected(method: string, pathname: string) {
	if (pathname === '/login') return false;
	if (method !== 'GET' && method !== 'HEAD') return true;

	return pathname.startsWith('/admin') || pathname.startsWith('/_app/remote/');
}

export const handle: Handle = async ({ event, resolve }) => {
	// Haloy's health check sends no credentials.
	if (event.url.pathname === '/healthz') return new Response('ok');

	if (!isProtected(event.request.method, event.url.pathname)) return resolve(event);
	if (await validSession(event.cookies.get(SESSION_COOKIE) ?? '')) return resolve(event);

	if (event.request.method === 'GET' && event.url.pathname.startsWith('/admin')) {
		return new Response(null, { status: 303, headers: { location: '/login' } });
	}

	return new Response('Unauthorized', { status: 401 });
};
