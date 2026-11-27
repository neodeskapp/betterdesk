/**
 * L87: the side column of Settings › General says where each address
 * comes from. A value of the server's environment is not "Set on this
 * page"; it names its variable.
 */
import { ENDPOINT_VARIABLES, endpointSourceLabel } from '#lib/settings/general.ts';

const t = (key: string, params?: Record<string, string>) =>
	params ? `${key}(${Object.values(params).join(',')})` : key;

describe('endpointSourceLabel', () => {
	it('calls only a saved value set on this page', () => {
		expect(endpointSourceLabel('saved', ENDPOINT_VARIABLES.host, t)).toBe('settings.side.src_page');
		expect(endpointSourceLabel('env', ENDPOINT_VARIABLES.host, t)).not.toBe('settings.side.src_page');
	});

	it('names the variable of an environment value', () => {
		expect(endpointSourceLabel('env', ENDPOINT_VARIABLES.host, t)).toBe(
			'settings.side.src_env(PUBLIC_SERVER_ID)'
		);
		expect(endpointSourceLabel('env', ENDPOINT_VARIABLES.relay, t)).toBe(
			'settings.side.src_env(PUBLIC_RELAY_SERVER)'
		);
		expect(endpointSourceLabel('env', ENDPOINT_VARIABLES.api, t)).toBe(
			'settings.side.src_env(PUBLIC_API_URL)'
		);
		expect(endpointSourceLabel('panel_host', ENDPOINT_VARIABLES.relay, t)).toBe(
			'settings.side.src_env(PANEL_PUBLIC_HOST)'
		);
		// Setup audit DOCKER-QUICK-1: the address the Docker installer wrote
		expect(endpointSourceLabel('relay_servers', ENDPOINT_VARIABLES.host, t)).toBe(
			'settings.side.src_env(RELAY_SERVERS)'
		);
	});

	it('calls everything else found automatically', () => {
		for (const source of ['config', 'request', 'derived', 'default', 'query', undefined]) {
			expect(endpointSourceLabel(source, ENDPOINT_VARIABLES.api, t)).toBe('settings.side.src_auto');
		}
	});
});
