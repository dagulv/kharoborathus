import { listFeed } from '#lib/server/feed.js';
import type { PageServerLoad } from './$types';

export const load: PageServerLoad = async () => ({ posts: await listFeed() });
