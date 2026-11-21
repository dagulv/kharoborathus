import { api, guard } from '$lib/api.js';
import type { Post, Trace } from '$lib/types.js';
import type { PageLoad } from './$types';

export const load: PageLoad = async ({ fetch, params }) => {
	const id = encodeURIComponent(params.id);
	const [post, trace] = await Promise.all([
		guard(api<Post>(`/admin/posts/${id}`, { fetch })),
		guard(api<Trace>(`/admin/posts/${id}/trace`, { fetch }))
	]);

	return { post, trace };
};
