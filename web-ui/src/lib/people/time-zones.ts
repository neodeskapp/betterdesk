/**
 * The time zone choices of the user's page and Your account: the IANA
 * names the browser knows, with the one an account has kept in the list
 * even when the browser doesn't know it.
 */

/** Every IANA time zone name the browser knows, sorted; a short list where it knows none */
export function timeZoneNames(): string[] {
	const intl = Intl as unknown as { supportedValuesOf?: (key: string) => string[] };
	let names: string[];
	try {
		names = intl.supportedValuesOf?.('timeZone') ?? [];
	} catch {
		names = [];
	}
	if (!names.length) names = ['UTC', 'Europe/Berlin', 'Europe/London', 'America/New_York', 'Asia/Tokyo'];
	if (!names.includes('UTC')) names = ['UTC', ...names];
	return [...names].sort((a, b) => a.localeCompare(b));
}

/** The browser's own time zone, '' when it doesn't say */
export function browserTimeZone(): string {
	try {
		return Intl.DateTimeFormat().resolvedOptions().timeZone ?? '';
	} catch {
		return '';
	}
}

/** The choices for a Select: none first, then the names, with `current` among them */
export function timeZoneOptions(noneLabel: string, current = ''): { value: string; label: string }[] {
	const names = timeZoneNames();
	if (current && !names.includes(current)) names.unshift(current);
	return [{ value: '', label: noneLabel }, ...names.map((n) => ({ value: n, label: n.replace(/_/g, ' ') }))];
}
