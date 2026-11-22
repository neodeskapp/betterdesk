/**
 * Which onboarding dialog is open, for the places that open it: the top
 * bar and the Help requests page open Quick support (the top bar draws
 * it), the Devices page draws Add a computer itself.
 */

import type { SupportSession } from '#lib/api/onboard.ts';

class QuickSupportState {
	open = $state(false);
	/** A session made before, to follow in the dialog; null for a new one */
	session = $state.raw<SupportSession | null>(null);
}

export const quickSupport = new QuickSupportState();

export function openQuickSupport(session: SupportSession | null = null): void {
	quickSupport.session = session;
	quickSupport.open = true;
}

export function closeQuickSupport(): void {
	quickSupport.open = false;
	quickSupport.session = null;
}
