import '@testing-library/jest-dom/vitest';
import { afterAll } from 'vitest';

// The remote viewer's drop bridge (desktop-dnd.ts) looks for the desktop shell
// every 250 ms for 10 seconds after it loads. A test file that ends sooner
// stops it, so it does not run after jsdom is gone.
afterAll(() => {
	if (typeof window === 'undefined') return;
	const w: Window & { __rdDeskNativeDropRetryTimer?: ReturnType<typeof setInterval> | null } = window;
	if (w.__rdDeskNativeDropRetryTimer) clearInterval(w.__rdDeskNativeDropRetryTimer);
	w.__rdDeskNativeDropRetryTimer = null;
});
