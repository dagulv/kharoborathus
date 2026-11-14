const dateTime = new Intl.DateTimeFormat('sv-SE', { dateStyle: 'short', timeStyle: 'short' });
const date = new Intl.DateTimeFormat('sv-SE', { dateStyle: 'short' });
const dateTimeSeconds = new Intl.DateTimeFormat('sv-SE', {
	dateStyle: 'short',
	timeStyle: 'medium'
});

export const fmtDateTime = (iso: string) => dateTime.format(new Date(iso));
export const fmtDate = (iso: string) => date.format(new Date(iso));
export const fmtDateTimeSeconds = (iso: string) => dateTimeSeconds.format(new Date(iso));

// A query string for the current filters with some changed. Empty values drop the key.
export function withParams(params: URLSearchParams, changes: Record<string, string | undefined>) {
	const next = new URLSearchParams(params);
	for (const [key, value] of Object.entries(changes)) {
		if (value) next.set(key, value);
		else next.delete(key);
	}
	const qs = next.toString();
	return qs ? `?${qs}` : '?';
}
