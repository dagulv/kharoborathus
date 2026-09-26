export type Source = {
	id: string;
	domainId: string;
	domainUrl: string;
	adapter: string;
	url: string;
	active: boolean;
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
