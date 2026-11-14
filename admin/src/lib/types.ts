// Mirror the Go admin routes' JSON. Dates are ISO strings.

export type Stats = {
	sources: number;
	enabledSources: number;
	flaggedSources: number;
	posts: number;
	postsToday: number;
	searchesToday: number;
	links: Record<string, number>;
};

export type Source = {
	id: string;
	domainId: string;
	domainUrl: string;
	adapter: string;
	url: string;
	enabled: boolean;
	status: 'proposed' | 'active' | 'flagged';
	rate: number;
	lang: string;
	metadata: Record<string, string>;
	createdAt: string;
	deletedAt: string | null;
	postCount: number;
	lastPostedAt: string | null;
};

export type ListingCandidate = {
	adapter: 'rss' | 'page';
	url: string;
	title?: string;
	items?: number;
	latest?: string;
};

export type SourceCheck = {
	candidates: ListingCandidate[];
	wordpress?: { posts: string; total: number };
};

export type PostSummary = {
	id: string;
	sourceId: string;
	domainId: string;
	domainUrl: string;
	url: string;
	lang: string;
	title: string;
	shortContent: string;
	tags: string[];
	authors: string[];
	postedAt: string | null;
	createdAt: string;
};

export type Post = PostSummary & {
	content: string;
	sourceUrl: string;
};

export type PostPage = {
	posts: PostSummary[];
	total: number;
	page: number;
	pageSize: number;
};

export type Tag = {
	key: string;
	enabled: boolean;
	status: 'proposed' | 'active';
	type: string;
	createdAt: string;
	terms: string[];
	postCount: number;
};

export type TagProposal = {
	key: string;
	terms: string[];
	postCount: number;
	domainCount: number;
	lastProposedAt: string;
};

export type TagAlias = {
	alias: string;
	key: string;
};

export type TagOverview = {
	tags: Tag[];
	proposals: TagProposal[];
	aliases: TagAlias[];
};

export type LogSummary = {
	id: number;
	type: string;
	level: string;
	method: string | null;
	url: string | null;
	status: number | null;
	durationMs: number;
	error: string | null;
	metadata: Record<string, unknown>;
	createdAt: string;
};

export type Log = LogSummary & {
	request: unknown;
	response: unknown;
};

export type LogPage = {
	logs: LogSummary[];
	hasNext: boolean;
	page: number;
};

// Mirrors domain.LogTypes in the Go app
export const LOG_TYPES = [
	'deepseek',
	'jev',
	'brave',
	'rss',
	'page',
	'trim',
	'ingest',
	'robots',
	'homepage',
	'wordpress'
] as const;
