export type Source = {
	id: string;
	domainId: string;
	domainUrl: string;
	adapter: string;
	url: string;
	enabled: boolean;
	rate: number;
	targetTags: string[];
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

export type Post = PostSummary & {
	content: string;
	sourceUrl: string;
};

export type Tag = {
	key: string;
	enabled: boolean;
	status: 'active' | 'dormant';
	gate: boolean;
	matchRate: number | null;
	lastMatchedAt: Date | null;
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

export type Switch = {
	key: string;
	enabled: boolean;
	updatedAt: Date;
	readiness: string | null;
};
