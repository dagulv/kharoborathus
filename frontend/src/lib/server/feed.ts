import { db } from '#lib/server/db.js';
import type { FeedPost } from '#lib/types.js';

export const FEED_PAGE_SIZE = 30;

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
			-- A copy stays visible while its original is pending or rejected.
			AND NOT EXISTS (
				SELECT 1
				FROM post_links l
				JOIN posts o ON o.id = l.to_post_id
				WHERE l.from_post_id = p.id AND l.relation = 'duplicate_of' AND o.status = 'active'
			)
		ORDER BY p.posted_at DESC, p.id DESC
		LIMIT ${FEED_PAGE_SIZE}
	`;

	return posts.map((p): FeedPost => ({ ...p, postedAt: p.postedAt.toISOString() }));
}
