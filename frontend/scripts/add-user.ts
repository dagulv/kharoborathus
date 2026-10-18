import { randomBytes } from 'node:crypto';
import postgres from 'postgres';
import { hashPassword } from '../src/lib/server/password.ts';
import { loadDevEnv } from '../load-env.ts';

const email = process.argv[2]?.trim();
const root = loadDevEnv('development');
const conn = process.env.POSTGRES_CONNECTION_STRING ?? root.POSTGRES_CONNECTION_STRING;

if (!email) {
	console.error('usage: pnpm user:add <email>');
	process.exit(1);
}

// Login has no rate limit, so the password is generated rather than chosen.
const password = randomBytes(24).toString('base64url');

const sql = postgres(conn);

try {
	await sql`INSERT INTO users (email, password_hash) VALUES (${email}, ${await hashPassword(password)})`;
	console.log(`Added ${email}, password: ${password}`);
} catch (err) {
	console.error(`err add user: ${(err as Error).message}`);
	process.exitCode = 1;
} finally {
	await sql.end();
}
