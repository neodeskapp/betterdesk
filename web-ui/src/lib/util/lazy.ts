/**
 * Code a page loads when it needs it: a dynamic import() behind lazy(),
 * fetched once for the page.
 *
 *   const dialog = lazy(() => import('./Dialog.svelte'));
 *   dialog.preload();                          // at idle time, ahead of the first use
 *   const { default: Dialog } = await dialog.load();
 *   {#await dialog.open(() => (open = false)) then { default: Dialog }}...{/await}
 *   {#await chart.optional() then { default: Chart }}<Chart />{/await}
 *
 * A load can fail with nothing wrong: leaving the page cuts its fetch off,
 * and Firefox then rejects the import ("error loading dynamically imported
 * module", "Unable to preload CSS for ..."). A rejection nobody handles is
 * an error on the page, and a failed load that is kept fails every later
 * use. So a failed load is forgotten, and the next call loads again.
 */
import { clientMessages } from '#lib/api/client.ts';
import { toast } from '#lib/ui/toasts.svelte.ts';

export interface Lazy<T> {
	/** The code, for a use. Rejects when it could not be loaded, for the caller to handle */
	load(): Promise<T>;
	/** Fetch the code ahead of the first use. Never rejects: a failed preload is no error */
	preload(): void;
	/**
	 * load() for what the person opened (a dialog, a sheet, the device
	 * panel), as a {#await} block shows it. When the code could not be
	 * loaded, a toast says so and `close` runs (it closes what asked for the
	 * code, so opening it again loads again); the promise then stays
	 * pending, so the block shows nothing and raises no error.
	 */
	open(close: () => void): Promise<T>;
	/**
	 * load() for a part the page can do without, as a {#await} block shows
	 * it: when the code could not be loaded, the promise stays pending, so
	 * the block shows nothing and raises no error. The next call loads again.
	 */
	optional(): Promise<T>;
}

export function lazy<T>(loader: () => Promise<T>): Lazy<T> {
	let pending: Promise<T> | null = null;
	const load = (): Promise<T> => {
		if (pending) return pending;
		const p = loader();
		pending = p;
		p.catch(() => {
			if (pending === p) pending = null;
		});
		return p;
	};
	return {
		load,
		preload() {
			if (typeof document !== 'undefined') load().catch(() => {});
		},
		open(close) {
			return load().catch(() => {
				toast.error(clientMessages.network);
				close();
				return new Promise<T>(() => {});
			});
		},
		optional() {
			return load().catch(() => new Promise<T>(() => {}));
		}
	};
}
