import { api, guard } from '$lib/api.js';
import type { PostPage } from '$lib/types.js';
import type { PageLoad } from './$types';

export const load: PageLoad = async ({ fetch, url }) => {
	const params = url.searchParams;
	const filter = {
		source: params.get('source') || undefined,
		tag: params.get('tag') || undefined,
		search: params.get('search') || undefined,
		page: Math.max(1, Number(params.get('page')) || 1)
	};

	return { filter, result: await guard(api<PostPage>('/admin/posts', { fetch, query: filter })) };
};
