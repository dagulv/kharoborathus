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
	archive?: { url: string; total: number };
};

// Mirrors domain.AdminPostStatuses in the Go app
export const POST_STATUSES = [
	'published',
	'accepted',
	'pending',
	'rejected',
	'duplicate',
	'missing',
	'failed'
] as const;

export type PostStatus = (typeof POST_STATUSES)[number];

// A raw post, with the post it became if any. The id is the raw post's.
export type PostSummary = {
	id: string;
	postId: string | null;
	sourceId: string | null;
	domainUrl: string;
	url: string;
	lang: string;
	title: string;
	shortContent: string;
	tags: string[];
	authors: string[];
	postedAt: string | null;
	status: PostStatus;
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

export type StepOutcome = 'passed' | 'stopped' | 'error' | 'pending';

export type TraceStep = {
	id: number;
	name: string;
	outcome: StepOutcome;
	reason: string;
	input: unknown;
	output: unknown;
	createdAt: string;
	calls: LogSummary[];
	ruleCall?: LogSummary;
};

export type Trace = {
	path: 'sourced' | 'discovered';
	steps: TraceStep[];
};

export type Tag = {
	key: string;
	enabled: boolean;
	status: 'proposed' | 'active';
	type: string;
	createdAt: string;
	terms: string[];
	categories: string[];
	postCount: number;
};

export type Category = {
	key: string;
	enabled: boolean;
	terms: Record<string, string>;
	tags: string[];
	createdAt: string;
	tagCount: number;
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
	'wordpress',
	'step'
] as const;
