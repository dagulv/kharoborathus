import { fail, redirect } from '@sveltejs/kit';
import { SESSION_COOKIE, SESSION_DAYS, login } from '#lib/server/auth.js';
import type { Actions } from './$types';

export const actions: Actions = {
	default: async ({ request, cookies }) => {
		const data = await request.formData();
		const email = data.get('email');
		const password = data.get('password');

		if (typeof email !== 'string' || typeof password !== 'string') {
			return fail(400, { invalid: true });
		}

		const token = await login(email.trim(), password);
		if (!token) return fail(401, { invalid: true });

		cookies.set(SESSION_COOKIE, token, {
			path: '/',
			httpOnly: true,
			sameSite: 'strict',
			maxAge: 60 * 60 * 24 * SESSION_DAYS
		});

		redirect(303, '/admin');
	}
};
