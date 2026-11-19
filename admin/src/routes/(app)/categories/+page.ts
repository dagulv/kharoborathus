import { api, guard } from '$lib/api.js';
import type { Category } from '$lib/types.js';
import type { PageLoad } from './$types';

export const load: PageLoad = async ({ fetch, depends }) => {
	depends('app:categories');

	return { categories: await guard(api<Category[]>('/admin/categories', { fetch })) };
};
