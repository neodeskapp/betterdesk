// The old Terminal tab (?tab=terminal) is gone: its address answers the
// console's not found page instead of showing another tab.
import { error } from '@sveltejs/kit';
import { tabGone } from '#lib/serverarea/management.ts';
import type { PageLoad } from './$types';

export const load: PageLoad = ({ url }) => {
	if (tabGone(url.searchParams.get('tab'))) error(404, 'Not Found');
};
