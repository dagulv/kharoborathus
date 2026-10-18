import { createHash, timingSafeEqual } from 'node:crypto';
import { ADMIN_TOKEN } from '$app/env/private';

export const TOKEN_COOKIE = 'admin_token';

// Hashing gives equal lengths, which timingSafeEqual requires.
const digest = (value: string) => createHash('sha256').update(value).digest();

const expected = digest(ADMIN_TOKEN);

export function validToken(token: string) {
	return timingSafeEqual(digest(token), expected);
}
