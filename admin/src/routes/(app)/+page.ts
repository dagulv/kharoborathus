import { api, guard } from '$lib/api.js';
import type { Stats } from '$lib/types.js';
import type { PageLoad } from './$types';

export const load: PageLoad = async ({ fetch }) => ({
	stats: await guard(api<Stats>('/admin/stats', { fetch }))
});
