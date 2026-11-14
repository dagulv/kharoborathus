import { api, guard } from '$lib/api.js';
import type { Source } from '$lib/types.js';
import type { PageLoad } from './$types';

export const load: PageLoad = async ({ fetch, url, depends }) => {
	depends('app:sources');

	const includeDeleted = url.searchParams.has('deleted');
	const sources = await guard(
		api<Source[]>('/admin/sources', {
			fetch,
			query: { deleted: includeDeleted ? 'true' : undefined }
		})
	);

	return { sources, includeDeleted };
};
