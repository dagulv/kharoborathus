import { api, guard } from '$lib/api.js';
import type { Category, TagOverview } from '$lib/types.js';
import type { PageLoad } from './$types';

export const load: PageLoad = async ({ fetch, depends, url }) => {
	depends('app:tags');

	const [overview, categories] = await Promise.all([
		guard(api<TagOverview>('/admin/tags', { fetch })),
		guard(api<Category[]>('/admin/categories', { fetch }))
	]);

	return { overview, categories, category: url.searchParams.get('category') || undefined };
};
