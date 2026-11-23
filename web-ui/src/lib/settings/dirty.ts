/**
 * Unsaved changes on the settings page. Each form on a tab registers a
 * check; the page asks before it is left (another tab, another page, the
 * browser closing) while any of them is dirty.
 *
 *   $effect(() => trackDirty(() => form.dirty, () => form.busy));
 */

interface Check {
	dirty: () => boolean;
	saving: () => boolean;
}

const checks = new Set<Check>();

/**
 * Register a form's checks: whether it has unsaved changes and whether a
 * save of it runs now. Returns the function that removes them (fits an
 * $effect).
 */
export function trackDirty(isDirty: () => boolean, isSaving: () => boolean = () => false): () => void {
	const check: Check = { dirty: isDirty, saving: isSaving };
	checks.add(check);
	return () => {
		checks.delete(check);
	};
}

export function anyDirty(): boolean {
	for (const check of checks) if (check.dirty()) return true;
	return false;
}

function anySaving(): boolean {
	for (const check of checks) if (check.saving()) return true;
	return false;
}

/** How long leaving waits for a running save before it asks anyway */
const SAVE_WAIT_MS = 15_000;

/**
 * Whether the page may be left. A save that runs now is waited for first:
 * what it saved is no longer unsaved. Asks while anything is still unsaved.
 */
export async function mayLeave(ask: () => Promise<boolean>): Promise<boolean> {
	const until = Date.now() + SAVE_WAIT_MS;
	while (anySaving() && Date.now() < until) await new Promise((r) => setTimeout(r, 50));
	return anyDirty() ? ask() : true;
}
