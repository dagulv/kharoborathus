import { api, guard } from '$lib/api.js';
import type { AdminEntity, TagOverview } from '$lib/types.js';
import type { PageLoad } from './$types';

export const load: PageLoad = async ({ fetch, depends, params }) => {
	depends('app:entity');

	const [entity, overview] = await Promise.all([
		guard(api<AdminEntity>(`/admin/entities/${encodeURIComponent(params.key)}`, { fetch })),
		guard(api<TagOverview>('/admin/tags', { fetch }))
	]);

	return { entity, tagKeys: overview.tags.map((tag) => tag.key) };
};
