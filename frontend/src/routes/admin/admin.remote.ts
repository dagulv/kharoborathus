import * as v from 'valibot';
import { error, invalid, redirect } from '@sveltejs/kit';
import { command, form, query } from '$app/server';
import { API_URL } from '$app/env/private';
import { db } from '#lib/server/db.js';
import type { Post, PostSummary, Source } from '#lib/types.js';

const PAGE_SIZE = 50;

const uuid = v.pipe(v.string(), v.uuid());

export const getStats = query(async () => {
	const sql = db();
	const [row] = await sql<
		{ sources: number; activeSources: number; posts: number; postsToday: number }[]
	>`
		SELECT
			(SELECT count(*)::int FROM sources WHERE deleted_at IS NULL) AS sources,
			(SELECT count(*)::int FROM sources WHERE deleted_at IS NULL AND active) AS active_sources,
			(SELECT count(*)::int FROM posts) AS posts,
			(SELECT count(*)::int FROM posts WHERE created_at > now() - interval '24 hours') AS posts_today
	`;

	return row;
});

export const listSources = query(
	v.object({ includeDeleted: v.optional(v.boolean(), false) }),
	async ({ includeDeleted }) => {
		const sql = db();

		return sql<Source[]>`
			SELECT
				s.id,
				s.domain_id,
				d.base_url AS domain_url,
				s.adapter,
				s.url,
				s.active,
				s.rate,
				s.target_tags,
				s.metadata,
				s.created_at,
				s.deleted_at,
				coalesce(p.post_count, 0)::int AS post_count,
				p.last_posted_at
			FROM sources s
			JOIN domains d ON d.id = s.domain_id
			LEFT JOIN (
				SELECT source_id, count(*) AS post_count, max(posted_at) AS last_posted_at
				FROM posts
				GROUP BY source_id
			) p ON p.source_id = s.id
			${includeDeleted ? sql`` : sql`WHERE s.deleted_at IS NULL`}
			ORDER BY s.created_at DESC
		`;
	}
);

export const listPosts = query(
	v.object({
		sourceId: v.optional(uuid),
		tag: v.optional(v.pipe(v.string(), v.maxLength(200))),
		search: v.optional(v.pipe(v.string(), v.trim(), v.maxLength(500))),
		page: v.optional(v.pipe(v.number(), v.integer(), v.minValue(1)), 1)
	}),
	async ({ sourceId, tag, search, page }) => {
		const sql = db();
		const conds = [sql`TRUE`];

		if (sourceId) conds.push(sql`p.source_id = ${sourceId}`);
		if (tag) conds.push(sql`p.tags @> ARRAY[${tag}]::text[]`);

		// Mirrors feed.Store.ListPosts search in the Go app
		if (search) {
			conds.push(sql`(
				(p.lang = 'sv' AND p.ts @@ websearch_to_tsquery('ts_swedish', ${search}))
				OR (p.lang = 'en' AND p.ts @@ websearch_to_tsquery('english', ${search}))
				OR (p.lang NOT IN ('sv', 'en') AND p.ts @@ websearch_to_tsquery('simple', ${search}))
				OR p.tags && ARRAY(
					SELECT t.key
					FROM tag_terms t
					WHERE to_tsvector(
						CASE t.lang
							WHEN 'sv' THEN 'ts_swedish'::regconfig
							WHEN 'en' THEN 'english'::regconfig
							ELSE 'simple'::regconfig
						END,
						${search}
					) @@ t.matcher
				)
			)`);
		}

		const where = conds.reduce((acc, c) => sql`${acc} AND ${c}`);

		const [posts, [{ total }]] = await Promise.all([
			sql<PostSummary[]>`
				SELECT
					p.id, p.source_id, d.base_url AS domain_url, p.url, p.lang, p.title,
					p.short_content, p.tags, p.authors, p.posted_at, p.created_at
				FROM posts p
				JOIN domains d ON d.id = p.domain_id
				WHERE ${where}
				ORDER BY p.posted_at DESC
				LIMIT ${PAGE_SIZE} OFFSET ${(page - 1) * PAGE_SIZE}
			`,
			sql<{ total: number }[]>`SELECT count(*)::int AS total FROM posts p WHERE ${where}`
		]);

		return { posts, total, page, pageSize: PAGE_SIZE };
	}
);

export const getPost = query(uuid, async (id) => {
	const sql = db();
	const [post] = await sql<Post[]>`
		SELECT
			p.id, p.source_id, s.url AS source_url, d.base_url AS domain_url, p.url, p.lang, p.title,
			p.short_content, p.content, p.tags, p.authors, p.posted_at, p.created_at
		FROM posts p
		JOIN sources s ON s.id = p.source_id
		JOIN domains d ON d.id = p.domain_id
		WHERE p.id = ${id}
	`;

	if (!post) error(404, 'Post not found');

	return post;
});

const ADAPTERS = ['rss'] as const;

// Mirrors domain.BaseURL in the Go app
function baseURL(url: URL) {
	return `${url.protocol}//${url.host.toLowerCase().replace(/^www\./, '')}`;
}

export const createSource = form(
	v.object({
		url: v.pipe(v.string(), v.trim(), v.nonEmpty('URL is required'), v.url('Must be a valid URL')),
		adapter: v.picklist(ADAPTERS, 'Unknown adapter'),
		rate: v.pipe(
			v.number('Rate must be a number'),
			v.integer(),
			v.minValue(1, 'Rate must be at least 1')
		),
		active: v.optional(v.boolean(), false),
		targetTags: v.optional(v.pipe(v.string(), v.maxLength(1000)), '')
	}),
	async ({ url, adapter, rate, active, targetTags }, issue) => {
		const parsed = new URL(url);
		if (parsed.protocol !== 'http:' && parsed.protocol !== 'https:') {
			invalid(issue.url('Only http(s) URLs are supported'));
		}

		const tags = [
			...new Set(
				targetTags
					.split(',')
					.map((t) => t.trim())
					.filter(Boolean)
			)
		];

		const sql = db();
		const [existing] = await sql`SELECT 1 FROM sources WHERE url = ${url} AND deleted_at IS NULL`;
		if (existing) invalid(issue.url('A source with this URL already exists'));

		await sql.begin(async (tx) => {
			const [domain] = await tx<{ id: string }[]>`
				INSERT INTO domains (base_url)
				VALUES (${baseURL(parsed)})
				ON CONFLICT (base_url) DO UPDATE SET base_url = EXCLUDED.base_url
				RETURNING id
			`;

			await tx`
				INSERT INTO sources (domain_id, adapter, url, active, rate, target_tags)
				VALUES (${domain.id}, ${adapter}, ${url}, ${active}, ${rate}, ${tags}::text[])
			`;
		});

		redirect(303, '/admin/sources');
	}
);

export const syncSource = command(uuid, async (id) => {
	const res = await fetch(`${API_URL}/sources/${id}/sync`, { method: 'POST' }).catch(() => {
		error(502, 'Go app unreachable');
	});

	if (res.status === 404) error(404, 'Source not found');
	if (!res.ok) error(502, `Go app responded ${res.status}`);
});
