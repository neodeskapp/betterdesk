/** /api/accessibility: the signed-in account's own accessibility settings (console/routes/accessibility) */

import { request } from './client';

/** Store the settings for the account; the server answers them as it keeps them */
export function saveAccessibility(settings: object): Promise<unknown> {
	return request('/api/accessibility', { method: 'PUT', body: settings });
}
