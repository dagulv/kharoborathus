import { redirect } from '@sveltejs/kit';
import { PUBLIC_API_URL } from '$app/env/public';

export class ApiError extends Error {
	constructor(
		public status: number,
		message: string
	) {
		super(message);
	}
}

type Options = {
	method?: 'GET' | 'POST' | 'PATCH';
	body?: unknown;
	query?: Record<string, string | number | undefined>;
	fetch?: typeof fetch;
};

// The session is an HttpOnly cookie on the API host, so every call sends credentials.
// Writes always carry a JSON body, since the API rejects other content types.
export async function api<T>(path: string, opts: Options = {}): Promise<T> {
	const { method = 'GET', body, query, fetch: f = fetch } = opts;

	const url = new URL(PUBLIC_API_URL + path);
	for (const [key, value] of Object.entries(query ?? {})) {
		if (value !== undefined && value !== '') url.searchParams.set(key, String(value));
	}

	const write = method !== 'GET';

	const res = await f(url, {
		method,
		credentials: 'include',
		headers: write ? { 'Content-Type': 'application/json' } : undefined,
		body: write ? JSON.stringify(body ?? {}) : undefined
	});

	const text = await res.text();
	if (!res.ok) throw new ApiError(res.status, text.trim() || res.statusText);

	// Writes answer 201, 202 or 204 with no body.
	return (text ? JSON.parse(text) : undefined) as T;
}

// For load functions: a lapsed session sends the admin to the login page.
export async function guard<T>(p: Promise<T>): Promise<T> {
	try {
		return await p;
	} catch (err) {
		if (err instanceof ApiError && err.status === 401) redirect(307, '/login');
		throw err;
	}
}

export function errorMessage(err: unknown, fallback: string) {
	return err instanceof ApiError ? err.message : fallback;
}
