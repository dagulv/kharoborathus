import * as v from 'valibot';
import { error, invalid, redirect } from '@sveltejs/kit';
import { command, form, query } from '$app/server';
import { API_URL } from '$app/env/private';
import { db } from '#lib/server/db.js';
import type { Post, PostSummary, Source, Switch, Tag, TagAlias, TagProposal } from '#lib/types.js';

const PAGE_SIZE = 50;

const uuid = v.pipe(v.string(), v.uuid());

export const getStats = query(async () => {
	const sql = db();
	const [row] = await sql<
		{
			sources: number;
			enabledSources: number;
			flaggedSources: number;
			posts: number;
			postsToday: number;
		}[]
	>`
		SELECT
			(SELECT count(*)::int FROM sources WHERE deleted_at IS NULL) AS sources,
			(SELECT count(*)::int FROM sources WHERE deleted_at IS NULL AND enabled) AS enabled_sources,
			(SELECT count(*)::int FROM sources WHERE deleted_at IS NULL AND flagged_at IS NOT NULL) AS flagged_sources,
			(SELECT count(*)::int FROM posts) AS posts,
			(SELECT count(*)::int FROM posts WHERE created_at > now() - interval '24 hours') AS posts_today
	`;

	return row;
});

export const listSources = query(
	v.object({ includeDeleted: v.optional(v.boolean(), false) }),
	async ({ includeDeleted }) => {
		const sql = db();

		// gate_rate mirrors the window in tagging.Store.FlagSources in the Go app
		return sql<Source[]>`
			SELECT
				s.id,
				s.domain_id,
				d.base_url AS domain_url,
				s.adapter,
				s.url,
				s.enabled,
				s.rate,
				s.target_tags,
				s.metadata,
				s.created_at,
				s.deleted_at,
				coalesce(p.post_count, 0)::int AS post_count,
				p.last_posted_at,
				s.flagged_at,
				g.gate_rate
			FROM sources s
			JOIN domains d ON d.id = s.domain_id
			LEFT JOIN (
				SELECT source_id, count(*) AS post_count, max(posted_at) AS last_posted_at
				FROM posts
				GROUP BY source_id
			) p ON p.source_id = s.id
			LEFT JOIN LATERAL (
				SELECT avg(r.gate_passed::int)::real AS gate_rate
				FROM (
					SELECT c.gate_passed
					FROM tag_checked c
					JOIN posts po ON po.id = c.post_id
					WHERE po.source_id = s.id
						AND c.gate_passed IS NOT NULL
						AND c.checked_at > coalesce(s.reviewed_at, '-infinity')
					ORDER BY c.checked_at DESC
					LIMIT 50
				) r
			) g ON true
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
		enabled: v.optional(v.boolean(), false),
		targetTags: v.optional(v.pipe(v.string(), v.maxLength(1000)), '')
	}),
	async ({ url, adapter, rate, enabled, targetTags }, issue) => {
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
				INSERT INTO sources (domain_id, adapter, url, enabled, rate, target_tags)
				VALUES (${domain.id}, ${adapter}, ${url}, ${enabled}, ${rate}, ${tags}::text[])
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

const tagKey = v.pipe(v.string(), v.nonEmpty(), v.maxLength(40));

export const listTags = query(async () => {
	const sql = db();

	return sql<Tag[]>`
		SELECT
			t.key,
			t.enabled,
			t.status,
			t.gate,
			t.match_rate,
			t.last_matched_at,
			t.created_at,
			coalesce(array_agg(tt.lang || ': ' || tt.term ORDER BY tt.lang, tt.term) FILTER (WHERE tt.term IS NOT NULL), '{}') AS terms,
			(SELECT count(*)::int FROM posts p WHERE p.tags @> ARRAY[t.key]) AS post_count
		FROM tags t
		LEFT JOIN tag_terms tt ON tt.key = t.key
		GROUP BY t.key
		ORDER BY t.key
	`;
});

export const listProposals = query(async () => {
	const sql = db();

	return sql<TagProposal[]>`
		SELECT
			p.key,
			array_agg(DISTINCT p.term) AS terms,
			count(DISTINCT p.post_id)::int AS post_count,
			count(DISTINCT po.domain_id)::int AS domain_count,
			max(p.created_at) AS last_proposed_at
		FROM tag_proposals p
		JOIN posts po ON po.id = p.post_id
		GROUP BY p.key
		ORDER BY post_count DESC, p.key
	`;
});

export const listAliases = query(async () => {
	const sql = db();

	return sql<TagAlias[]>`SELECT alias, key FROM tag_aliases ORDER BY alias`;
});

export const setTagEnabled = command(
	v.object({ key: tagKey, enabled: v.boolean() }),
	async ({ key, enabled }) => {
		const sql = db();
		await sql`UPDATE tags SET enabled = ${enabled} WHERE key = ${key}`;
		await listTags().refresh();
	}
);

export const mergeTag = command(v.object({ from: tagKey, to: tagKey }), async ({ from, to }) => {
	if (from === to) error(400, 'Cannot merge a tag into itself');

	const sql = db();

	await sql.begin(async (tx) => {
		const [target] = await tx`SELECT 1 FROM tags WHERE key = ${to}`;
		if (!target) error(404, `Unknown tag ${to}`);

		await tx`
			INSERT INTO tag_terms (key, lang, term)
			SELECT ${to}, lang, term FROM tag_terms WHERE key = ${from}
			ON CONFLICT DO NOTHING
		`;
		await tx`
			UPDATE posts
			SET tags = ARRAY(SELECT DISTINCT unnest(array_replace(tags, ${from}, ${to})))
			WHERE tags @> ARRAY[${from}]::text[]
		`;
		await tx`
			UPDATE sources
			SET target_tags = ARRAY(SELECT DISTINCT unnest(array_replace(target_tags, ${from}, ${to})))
			WHERE target_tags @> ARRAY[${from}]::text[]
		`;
		await tx`UPDATE tag_aliases SET key = ${to} WHERE key = ${from}`;
		await tx`
			INSERT INTO tag_aliases (alias, key) VALUES (${from}, ${to})
			ON CONFLICT (alias) DO UPDATE SET key = EXCLUDED.key
		`;
		await tx`DELETE FROM tags WHERE key = ${from}`;
	});

	await Promise.all([listTags().refresh(), listAliases().refresh()]);
});

export const reviewSource = command(
	v.object({ id: uuid, enabled: v.boolean(), targetTags: v.array(tagKey) }),
	async ({ id, enabled, targetTags }) => {
		const sql = db();
		const [source] = await sql`
			UPDATE sources
			SET enabled = ${enabled}, target_tags = ${targetTags}::text[], flagged_at = NULL, reviewed_at = now()
			WHERE id = ${id} AND deleted_at IS NULL
			RETURNING 1
		`;

		if (!source) error(404, 'Source not found');
	}
);

// Readiness per key mirrors the switch table in docs/kb/rollout.md
export const listSwitches = query(async () => {
	const sql = db();

	return sql<Switch[]>`
		SELECT
			s.key,
			s.enabled,
			s.updated_at,
			CASE s.key
				WHEN 'shadow_gate' THEN (
					SELECT count(*) FROM tags WHERE enabled AND status = 'active' AND gate
				) || ' gate tags'
			END AS readiness
		FROM switches s
		ORDER BY s.key
	`;
});

export const setSwitch = command(
	v.object({ key: v.pipe(v.string(), v.nonEmpty(), v.maxLength(40)), enabled: v.boolean() }),
	async ({ key, enabled }) => {
		const sql = db();
		const [row] = await sql`
			UPDATE switches SET enabled = ${enabled}, updated_at = now()
			WHERE key = ${key}
			RETURNING 1
		`;

		if (!row) error(404, `Unknown switch ${key}`);

		await listSwitches().refresh();
	}
);
