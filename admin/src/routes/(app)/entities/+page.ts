import { api, guard } from '$lib/api.js';
import type { EntityPage } from '$lib/types.js';
import type { PageLoad } from './$types';

export const load: PageLoad = async ({ fetch, depends, url }) => {
	depends('app:entities');

	const params = url.searchParams;
	const filter = {
		kind: params.get('kind') || undefined,
		status: params.get('status') || undefined,
		focus: params.get('focus') === 'true' ? 'true' : undefined,
		search: params.get('search') || undefined,
		page: Math.max(1, Number(params.get('page')) || 1)
	};

	return {
		filter,
		result: await guard(api<EntityPage>('/admin/entities', { fetch, query: filter }))
	};
};
