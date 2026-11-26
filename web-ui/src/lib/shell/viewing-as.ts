/** Sign in as without a DOM: the time left as the banner writes it, and the tab title. */

/** Milliseconds as "14:05", "0:09"; nothing left is "0:00" */
export function timeLeft(ms: number): string {
	const total = Math.max(0, Math.ceil(ms / 1000));
	const minutes = Math.floor(total / 60);
	const seconds = total % 60;
	return `${minutes}:${String(seconds).padStart(2, '0')}`;
}

/** The tab title while viewing as someone: the prefix first, so it shows in a narrow tab */
export function viewingTitle(prefix: string, title: string): string {
	return `${prefix} - ${title}`;
}
