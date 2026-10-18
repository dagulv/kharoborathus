import { createHash, randomBytes } from 'node:crypto';
import { db } from '#lib/server/db.js';
import { hashPassword, verifyPassword } from '#lib/server/password.js';

export const SESSION_COOKIE = 'session';
export const SESSION_DAYS = 30;

const digest = (token: string) => createHash('sha256').update(token).digest();

// Verified against when the email is unknown, so a miss takes as long as a wrong password.
const dummyHash = hashPassword(randomBytes(16).toString('hex'));

export async function login(email: string, password: string) {
	const sql = db();
	const [user] = await sql<{ id: string; passwordHash: string }[]>`
		SELECT id, password_hash FROM users WHERE email = ${email}
	`;

	const valid = await verifyPassword(password, user?.passwordHash ?? (await dummyHash));
	if (!user || !valid) return null;

	const token = randomBytes(32).toString('base64url');

	await sql`DELETE FROM sessions WHERE expires_at < now()`;
	await sql`
		INSERT INTO sessions (token_hash, user_id, expires_at)
		VALUES (${digest(token)}, ${user.id}, now() + make_interval(days => ${SESSION_DAYS}))
	`;

	return token;
}

export async function validSession(token: string) {
	if (!token) return false;

	const sql = db();
	const [session] = await sql`
		SELECT 1 FROM sessions WHERE token_hash = ${digest(token)} AND expires_at > now()
	`;

	return Boolean(session);
}
