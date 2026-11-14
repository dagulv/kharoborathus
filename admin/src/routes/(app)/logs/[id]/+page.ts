import { api, guard } from '$lib/api.js';
import type { Log } from '$lib/types.js';
import type { PageLoad } from './$types';

export const load: PageLoad = async ({ fetch, params }) => ({
	log: await guard(api<Log>(`/admin/logs/${encodeURIComponent(params.id)}`, { fetch }))
});
