import { fail, redirect } from '@sveltejs/kit';
import { TOKEN_COOKIE, validToken } from '#lib/server/auth.js';
import type { Actions } from './$types';

export const actions: Actions = {
	default: async ({ request, cookies }) => {
		const token = (await request.formData()).get('token');

		if (typeof token !== 'string' || !validToken(token)) {
			return fail(401, { invalid: true });
		}

		cookies.set(TOKEN_COOKIE, token, {
			path: '/',
			httpOnly: true,
			sameSite: 'strict',
			maxAge: 60 * 60 * 24 * 30
		});

		redirect(303, '/admin');
	}
};
