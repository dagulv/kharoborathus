import { randomBytes, scrypt, timingSafeEqual } from 'node:crypto';
import { promisify } from 'node:util';

// No $app imports: scripts/add-user.ts runs this under plain node.

const derive = promisify(scrypt) as (
	password: string,
	salt: Buffer,
	keylen: number
) => Promise<Buffer>;

const KEY_LENGTH = 64;

export async function hashPassword(password: string) {
	const salt = randomBytes(16);
	const hash = await derive(password, salt, KEY_LENGTH);

	return `${salt.toString('hex')}:${hash.toString('hex')}`;
}

export async function verifyPassword(password: string, stored: string) {
	const [salt, hash] = stored.split(':');
	const given = await derive(password, Buffer.from(salt, 'hex'), KEY_LENGTH);

	return timingSafeEqual(given, Buffer.from(hash, 'hex'));
}
