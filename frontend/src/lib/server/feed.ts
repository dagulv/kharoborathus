import { db } from '#lib/server/db.js';
import type { FeedPost } from '#lib/types.js';

export const FEED_PAGE_SIZE = 30;

// Pages by the last post seen, not an offset, so posts arriving while scrolling cause no repeats.
export async function listFeed(after?: string) {
	const sql = db();
	const cursor = after
		? sql`AND (p.posted_at, p.id) < (SELECT posted_at, id FROM posts WHERE id = ${after})`
		: sql``;

	const posts = await sql<(Omit<FeedPost, 'postedAt'> & { postedAt: Date })[]>`
		SELECT p.id, d.base_url AS domain_url, p.url, p.title, p.short_content, p.posted_at
		FROM posts p
		JOIN domains d ON d.id = p.domain_id
		WHERE p.status = 'active' ${cursor}
		ORDER BY p.posted_at DESC, p.id DESC
		LIMIT ${FEED_PAGE_SIZE}
	`;

	// The same shape from the page load and the JSON endpoint.
	return posts.map((p): FeedPost => ({ ...p, postedAt: p.postedAt.toISOString() }));
}
