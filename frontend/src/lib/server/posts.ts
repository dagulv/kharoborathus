import { error } from '@sveltejs/kit';
import { API_URL } from '$app/env/private';
import type { FeedPost } from '#lib/types.js';

export const FEED_PAGE_SIZE = 30;

type APIPost = Omit<FeedPost, 'postedAt'> & { postedAt: string | null; createdAt: string };

export async function listPosts(after?: string) {
	const params = new URLSearchParams({ limit: String(FEED_PAGE_SIZE) });
	if (after) params.set('after', after);

	const res = await fetch(`${API_URL}/posts?${params}`).catch(() => {
		error(502, 'Go app unreachable');
	});

	// The API answers 400 for a cursor that names no post.
	if (res.status === 400) error(400, (await res.text()).trim());
	if (!res.ok) error(502, `Posts API answered ${res.status}`);

	const posts: APIPost[] = await res.json();

	return posts.map((p): FeedPost => ({
		id: p.id,
		domainUrl: p.domainUrl,
		url: p.url,
		title: p.title,
		shortContent: p.shortContent,
		postedAt: p.postedAt ?? p.createdAt
	}));
}
