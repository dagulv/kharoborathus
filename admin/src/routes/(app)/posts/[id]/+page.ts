import { api, guard } from '$lib/api.js';
import type { Post } from '$lib/types.js';
import type { PageLoad } from './$types';

export const load: PageLoad = async ({ fetch, params }) => ({
	post: await guard(api<Post>(`/admin/posts/${encodeURIComponent(params.id)}`, { fetch }))
});
