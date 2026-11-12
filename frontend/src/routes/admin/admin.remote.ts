import * as v from 'valibot';
import { error, invalid, redirect } from '@sveltejs/kit';
import { command, form, getRequestEvent, query } from '$app/server';
import { API_URL } from '$app/env/private';
import { SESSION_COOKIE } from '#lib/server/auth.js';
import { db } from '#lib/server/db.js';
import { LOG_TYPES } from '#lib/types.js';
import type {
	Log,
	LogSummary,
	Post,
	PostSummary,
	Source,
	SourceCheck,
	Tag,
	TagAlias,
	TagProposal
} from '#lib/types.js';

const PAGE_SIZE = 50;

// The Go app's admin routes check the same session, so the admin's token goes along.
function goAuth() {
	return { authorization: `Bearer ${getRequestEvent().cookies.get(SESSION_COOKIE) ?? ''}` };
}

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
			searchesToday: number;
		}[]
	>`
		SELECT
			(SELECT count(*)::int FROM sources WHERE deleted_at IS NULL) AS sources,
			(SELECT count(*)::int FROM sources WHERE deleted_at IS NULL AND enabled) AS enabled_sources,
			(SELECT count(*)::int FROM sources WHERE deleted_at IS NULL AND status = 'flagged') AS flagged_sources,
			(SELECT count(*)::int FROM posts) AS posts,
			(SELECT count(*)::int FROM posts WHERE created_at > now() - interval '24 hours') AS posts_today,
			(SELECT count(*)::int FROM discovery_search_queries WHERE searched_at > now() - interval '24 hours' AND result IS NOT NULL) AS searches_today
	`;

	return row;
});

export const getLinkQueue = query(async () => {
	const sql = db();
	const rows = await sql<{ status: string; count: number }[]>`
		SELECT status, count(*)::int AS count FROM discovery_posts GROUP BY status
	`;

	return Object.fromEntries(rows.map((r) => [r.status, r.count])) as Record<string, number>;
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
				s.enabled,
				s.status,
				s.rate,
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

		// Mirrors PostStore.ListPosts search in the Go app
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

const httpURL = v.pipe(
	v.string(),
	v.trim(),
	v.nonEmpty('URL is required'),
	v.url('Must be a valid URL'),
	v.regex(/^https?:\/\//, 'Only http(s) URLs are supported')
);

// Lists what the Go app can read a url as, best first. See discover in the Go app.
export const checkSource = command(httpURL, async (url) => {
	const res = await fetch(`${API_URL}/admin/listing?url=${encodeURIComponent(url)}`, {
		headers: goAuth()
	}).catch(() => {
		error(502, 'Go app unreachable');
	});

	if (res.status === 401) error(401, 'Go app rejected the session');
	if (!res.ok) error(422, `Could not fetch ${url}: ${(await res.text()).trim()}`);

	return (await res.json()) as SourceCheck;
});

// Mirrors domain.BaseURL in the Go app
function baseURL(url: URL) {
	return `${url.protocol}//${url.host.toLowerCase().replace(/^www\./, '')}`;
}

export const createSource = form(
	v.object({
		url: httpURL,
		// One of the checked candidates, as "adapter url".
		listing: v.pipe(
			v.string('Check the URL first'),
			v.regex(/^(rss|page) https?:\/\/\S+$/, 'Check the URL first')
		),
		rate: v.pipe(
			v.number('Rate must be a number'),
			v.integer(),
			v.minValue(1, 'Rate must be at least 1')
		),
		lang: v.picklist(['', 'sv', 'en'])
	}),
	async ({ url, listing, rate, lang }, issue) => {
		const [adapter, listingURL] = listing.split(' ');

		const sql = db();
		const [existing] =
			await sql`SELECT 1 FROM sources WHERE url = ${listingURL} AND deleted_at IS NULL`;
		if (existing) invalid(issue.listing('A source with this feed or page already exists'));

		const id = await sql.begin(async (tx) => {
			const [domain] = await tx<{ id: string }[]>`
				INSERT INTO domains (base_url)
				VALUES (${baseURL(new URL(url))})
				ON CONFLICT (base_url) DO UPDATE SET base_url = EXCLUDED.base_url
				RETURNING id
			`;

			const [source] = await tx<{ id: string }[]>`
				INSERT INTO sources (domain_id, adapter, url, enabled, rate, lang)
				VALUES (${domain.id}, ${adapter}, ${listingURL}, true, ${rate}, ${lang})
				RETURNING id
			`;

			return source.id;
		});

		// The source is saved either way. The Backfill button covers a failed request.
		await queueSource(id, 'backfill').catch((err) => console.error('backfill', id, err));

		redirect(303, '/admin/sources');
	}
);

async function queueSource(id: string, action: 'sync' | 'backfill') {
	const res = await fetch(`${API_URL}/admin/sources/${id}/${action}`, {
		method: 'POST',
		headers: goAuth()
	}).catch(() => {
		error(502, 'Go app unreachable');
	});

	if (res.status === 401) error(401, 'Go app rejected the session');
	if (res.status === 404) error(404, 'Source not found');
	if (res.status === 409) error(409, 'Source is not active or is disabled');
	if (!res.ok) error(502, `Go app responded ${res.status}`);
}

export const syncSource = command(uuid, (id) => queueSource(id, 'sync'));

export const backfillSource = command(uuid, (id) => queueSource(id, 'backfill'));

const tagKey = v.pipe(v.string(), v.nonEmpty(), v.maxLength(40));

export const listTags = query(async () => {
	const sql = db();

	return sql<Tag[]>`
		SELECT
			t.key,
			t.enabled,
			t.status,
			t.type,
			t.created_at,
			coalesce(array_agg(tt.lang || ': ' || tt.term ORDER BY tt.lang, tt.term) FILTER (WHERE tt.term IS NOT NULL), '{}') AS terms,
			(SELECT count(*)::int FROM posts p WHERE p.tags @> ARRAY[t.key]) AS post_count
		FROM tags t
		LEFT JOIN tag_terms tt ON tt.key = t.key
		GROUP BY t.key
		ORDER BY t.key
	`;
});

// Proposed tags live in tags, and their support in posts.tags, which is what promotion counts.
export const listProposals = query(async () => {
	const sql = db();

	return sql<TagProposal[]>`
		SELECT
			t.key,
			coalesce(array_agg(DISTINCT tt.lang || ': ' || tt.term) FILTER (WHERE tt.term IS NOT NULL), '{}') AS terms,
			count(DISTINCT p.id)::int AS post_count,
			count(DISTINCT p.domain_id)::int AS domain_count,
			coalesce(max(p.created_at), t.created_at) AS last_proposed_at
		FROM tags t
		LEFT JOIN tag_terms tt ON tt.key = t.key
		LEFT JOIN posts p ON p.tags @> ARRAY[t.key]
		WHERE t.status = 'proposed' AND t.enabled
		GROUP BY t.key
		ORDER BY post_count DESC, t.key
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
		await tx`UPDATE tag_aliases SET key = ${to} WHERE key = ${from}`;
		await tx`
			INSERT INTO tag_aliases (alias, key) VALUES (${from}, ${to})
			ON CONFLICT (alias) DO UPDATE SET key = EXCLUDED.key
		`;
		await tx`DELETE FROM tags WHERE key = ${from}`;
	});

	await Promise.all([listTags().refresh(), listAliases().refresh()]);
});

// Keeping a flagged source makes it active again. Disabling it keeps the flag, so it gets reviewed before it runs again.
// Approving a proposed source is its first scrape, so it backfills.
export const reviewSource = command(
	v.object({ id: uuid, enabled: v.boolean() }),
	async ({ id, enabled }) => {
		const sql = db();
		const [source] = await sql<{ was: string }[]>`
			UPDATE sources s
			SET enabled = ${enabled}, status = CASE WHEN ${enabled} THEN 'active' ELSE s.status END
			FROM (SELECT id, status FROM sources WHERE id = ${id}) old
			WHERE s.id = old.id AND s.deleted_at IS NULL
			RETURNING old.status AS was
		`;

		if (!source) error(404, 'Source not found');

		if (enabled && source.was === 'proposed') {
			await queueSource(id, 'backfill').catch((err) => console.error('backfill', id, err));
		}
	}
);

// The camel transform would rename keys inside jsonb too, so it is read as text.
function parseJson<T extends Record<string, unknown>>(row: T, keys: (keyof T)[]): T {
	for (const key of keys) row[key] = JSON.parse(row[key] as string);
	return row;
}

// Bodies are left out, since a stored page can be megabytes.
// No total count: count(*) over the whole table gets slow as it grows.
export const listLogs = query(
	v.object({
		type: v.optional(v.picklist(LOG_TYPES)),
		level: v.optional(v.picklist(['INFO', 'WARN', 'ERROR'])),
		metaKey: v.optional(v.pipe(v.string(), v.trim(), v.nonEmpty(), v.maxLength(100))),
		metaValue: v.optional(v.pipe(v.string(), v.trim(), v.maxLength(500)), ''),
		page: v.optional(v.pipe(v.number(), v.integer(), v.minValue(1)), 1)
	}),
	async ({ type, level, metaKey, metaValue, page }) => {
		const sql = db();
		const conds = [sql`TRUE`];

		if (type) conds.push(sql`type = ${type}`);
		if (level) conds.push(sql`level = ${level}`);
		if (metaKey && metaValue) conds.push(sql`metadata @> ${sql.json({ [metaKey]: metaValue })}`);
		if (metaKey && !metaValue) conds.push(sql`metadata ? ${metaKey}`);

		const where = conds.reduce((acc, c) => sql`${acc} AND ${c}`);

		const rows = await sql<LogSummary[]>`
			SELECT
				id, type, level, request->>'method' AS method, request->>'url' AS url,
				status, duration_ms::int, error, metadata::text, created_at
			FROM logs
			WHERE ${where}
			ORDER BY id DESC
			LIMIT ${PAGE_SIZE + 1} OFFSET ${(page - 1) * PAGE_SIZE}
		`;

		return {
			logs: rows.slice(0, PAGE_SIZE).map((r) => parseJson(r, ['metadata'])),
			hasNext: rows.length > PAGE_SIZE,
			page
		};
	}
);

export const getLog = query(v.pipe(v.string(), v.regex(/^\d+$/)), async (id) => {
	const sql = db();
	const [log] = await sql<Log[]>`
		SELECT
			id, type, level, request->>'method' AS method, request->>'url' AS url,
			status, duration_ms::int, error, metadata::text, created_at, request::text, response::text
		FROM logs
		WHERE id = ${id}
	`;

	if (!log) error(404, 'Log not found');

	return parseJson(log, ['metadata', 'request', 'response']);
});
