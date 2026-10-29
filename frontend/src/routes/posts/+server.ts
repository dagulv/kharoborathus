import * as v from 'valibot';
import { error } from '@sveltejs/kit';
import { listFeed } from '#lib/server/feed.js';
import type { RequestHandler } from './$types';

export const GET: RequestHandler = async ({ url }) => {
	const after = v.safeParse(v.pipe(v.string(), v.uuid()), url.searchParams.get('after'));
	if (!after.success) error(400, 'after must be a post id');

	return Response.json(await listFeed(after.output));
};
