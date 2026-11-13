export type Source = {
	id: string;
	domainId: string;
	domainUrl: string;
	adapter: string;
	url: string;
	enabled: boolean;
	status: 'proposed' | 'active' | 'flagged';
	rate: number;
	metadata: Record<string, string>;
	createdAt: Date;
	deletedAt: Date | null;
	postCount: number;
	lastPostedAt: Date | null;
};

export type PostSummary = {
	id: string;
	sourceId: string;
	domainUrl: string;
	url: string;
	lang: string;
	title: string;
	shortContent: string;
	tags: string[];
	authors: string[];
	postedAt: Date;
	createdAt: Date;
};

export type FeedPost = {
	id: string;
	domainUrl: string;
	url: string;
	title: string;
	shortContent: string;
	postedAt: string;
};

export type Post = PostSummary & {
	content: string;
	sourceUrl: string;
};

export type Tag = {
	key: string;
	enabled: boolean;
	status: 'proposed' | 'active';
	type: string;
	createdAt: Date;
	terms: string[];
	postCount: number;
};

export type TagProposal = {
	key: string;
	terms: string[];
	postCount: number;
	domainCount: number;
	lastProposedAt: Date;
};

export type TagAlias = {
	alias: string;
	key: string;
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
	archive?: { url: string; total: number };
};

export type LogSummary = {
	id: string;
	type: string;
	level: string;
	method: string | null;
	url: string | null;
	status: number | null;
	durationMs: number;
	error: string | null;
	metadata: Record<string, unknown>;
	createdAt: Date;
};

export type Log = LogSummary & {
	request: unknown;
	response: unknown;
};

// Mirrors the types in docs/kb/request-logs.md
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
