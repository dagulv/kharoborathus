import type { BadgeVariant } from '$lib/components/ui/badge/index.js';
import type { PostStatus, Trace, TraceStep } from '$lib/types.js';

export const STEP_LABELS: Record<string, string> = {
	found_by: 'Found by',
	domain_check: 'Domain check',
	fetch: 'Fetch',
	extract: 'Extract',
	date_window: 'Date window',
	source_match: 'Source match',
	duplicate_check: 'Duplicate check',
	quality_check: 'Quality check',
	enrich: 'Enrich',
	discover: 'Discover',
	resync: 'Resync'
};

// Mirrors the step order of each path in the Go pipeline.
export const STEP_ORDER: Record<Trace['path'], string[]> = {
	sourced: ['fetch', 'extract', 'duplicate_check', 'quality_check', 'enrich', 'discover'],
	discovered: [
		'found_by',
		'domain_check',
		'fetch',
		'extract',
		'date_window',
		'source_match',
		'duplicate_check',
		'quality_check',
		'enrich',
		'discover'
	]
};

export type GraphRow =
	{ kind: 'step'; step: TraceStep } | { kind: 'not-reached' | 'waiting'; name: string };

const STOPPED: PostStatus[] = ['rejected', 'duplicate', 'missing', 'failed'];

// The recorded steps, then the steps after the last one of the latest run: not reached when the
// post stopped, waiting while it is still in the pipeline. A resync starts a new run.
export function graphRows(trace: Trace, status: PostStatus): GraphRow[] {
	const rows: GraphRow[] = trace.steps.map((step) => ({ kind: 'step', step }));

	if (status === 'published') return rows;

	const order = STEP_ORDER[trace.path];
	const run = trace.steps.slice(trace.steps.findLastIndex((s) => s.name === 'resync') + 1);
	const last = Math.max(-1, ...run.map((s) => order.indexOf(s.name)));
	const kind = STOPPED.includes(status) ? 'not-reached' : 'waiting';

	for (const name of order.slice(last + 1)) rows.push({ kind, name });

	return rows;
}

export function statusVariant(status: PostStatus): BadgeVariant {
	switch (status) {
		case 'published':
			return 'default';
		case 'rejected':
		case 'missing':
		case 'failed':
			return 'destructive';
		case 'duplicate':
			return 'secondary';
		default:
			return 'outline';
	}
}

// Non-JSON bodies, like pages, are stored as strings and read better unquoted.
export function show(value: unknown) {
	return typeof value === 'string' ? value : JSON.stringify(value, null, 2);
}

export function isEmpty(value: unknown) {
	return (
		value === null ||
		value === undefined ||
		(typeof value === 'object' && Object.keys(value as object).length === 0)
	);
}
