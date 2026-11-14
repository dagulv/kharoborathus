import { api, guard } from '$lib/api.js';
import type { TagOverview } from '$lib/types.js';
import type { PageLoad } from './$types';

export const load: PageLoad = async ({ fetch, depends }) => {
	depends('app:tags');

	return { overview: await guard(api<TagOverview>('/admin/tags', { fetch })) };
};
