import { api, guard } from '$lib/api.js';
import type { LogPage } from '$lib/types.js';
import type { PageLoad } from './$types';

export const load: PageLoad = async ({ fetch, url }) => {
	const params = url.searchParams;
	const filter = {
		type: params.get('type') || undefined,
		level: params.get('level') || undefined,
		key: params.get('key') || undefined,
		value: params.get('value') || undefined,
		page: Math.max(1, Number(params.get('page')) || 1)
	};

	return { filter, result: await guard(api<LogPage>('/admin/logs', { fetch, query: filter })) };
};
