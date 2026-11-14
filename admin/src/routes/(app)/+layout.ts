import { api, guard } from '$lib/api.js';
import type { LayoutLoad } from './$types';

export const load: LayoutLoad = async ({ fetch }) => {
	await guard(api('/admin/session', { fetch }));
};
