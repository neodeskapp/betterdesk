/**
 * The RDP/VNC target sheet warns while this server cannot run what the
 * form sets up (BUGS L83): guacd does not answer, so no session can
 * start, or the credential vault has no key, so a password cannot be
 * saved. The vault warning shows while "Save on this server" is chosen.
 */
import { cleanup, render, screen } from '@testing-library/svelte';
import userEvent from '@testing-library/user-event';
import { afterEach, describe, expect, test, vi } from 'vitest';
import RemoteTargetSheet from '../../src/lib/devices/dialogs/RemoteTargetSheet.svelte';
import type { RemoteTargetReadiness } from '../../src/lib/api/remoteTargets.ts';

afterEach(() => cleanup());

function open(ready: RemoteTargetReadiness | Error) {
	const readiness = vi.fn(async () => {
		if (ready instanceof Error) throw ready;
		return ready;
	});
	render(RemoteTargetSheet, {
		device: null,
		save: async () => {},
		test: async () => '',
		canTest: false,
		readiness,
		settle: () => {}
	});
	return readiness;
}

const guacdWarning = /guacd/;
const vaultWarning = /REMOTE_TARGET_VAULT_KEY/;

describe('remote target sheet', () => {
	test('warns that sessions cannot start while guacd does not answer', async () => {
		const readiness = open({ vault: true, guacd: false });
		expect(readiness).toHaveBeenCalledOnce();
		expect(await screen.findByText(guacdWarning)).toBeTruthy();
	});

	test('warns that a password cannot be saved while the vault has no key and Save on this server is chosen', async () => {
		open({ vault: false, guacd: true });
		await vi.waitFor(() => expect(screen.getAllByRole('combobox').length).toBeGreaterThan(1));
		expect(screen.queryByText(vaultWarning)).toBeNull();
		const passwordMode = screen
			.getAllByRole('combobox')
			.find((s) => [...(s as HTMLSelectElement).options].some((o) => o.value === 'saved'))!;
		await userEvent.setup().selectOptions(passwordMode, 'saved');
		expect(await screen.findByText(vaultWarning)).toBeTruthy();
		expect(screen.queryByText(guacdWarning)).toBeNull();
	});

	test('says nothing when the server is ready, or when it cannot tell', async () => {
		const readiness = open({ vault: true, guacd: true });
		await vi.waitFor(() => expect(readiness).toHaveBeenCalled());
		await Promise.resolve();
		expect(screen.queryByText(guacdWarning)).toBeNull();
		cleanup();
		const failing = open(new Error('Request failed'));
		await vi.waitFor(() => expect(failing).toHaveBeenCalled());
		await Promise.resolve();
		expect(screen.queryByText(guacdWarning)).toBeNull();
		expect(screen.queryByText(vaultWarning)).toBeNull();
	});

	test('a typed port is saved, and a port out of range is refused next to the field (BUGS L411)', async () => {
		const user = userEvent.setup();
		const save = vi.fn(async (_target: unknown, _password: string) => {});
		const settle = vi.fn();
		render(RemoteTargetSheet, {
			device: null,
			save,
			test: async () => '',
			canTest: false,
			readiness: async () => ({ vault: true, guacd: true }),
			settle
		});
		await user.type(screen.getByRole('textbox', { name: /devices.remote_name|Display name/ }), 'Lab box');
		await user.type(screen.getByRole('textbox', { name: /devices.remote_host|Host or IP/ }), '10.0.0.9');
		const port = screen.getByRole('spinbutton', { name: /devices.remote_port|Port/ });
		await user.clear(port);
		await user.type(port, '70000');
		await user.click(screen.getByRole('button', { name: 'actions.save' }));
		await vi.waitFor(() => expect(port).toHaveAccessibleDescription(/mesh.port_invalid/));
		expect(save).not.toHaveBeenCalled();
		await user.clear(port);
		await user.type(port, '8080');
		// switching the technology keeps a port typed by hand
		const technology = screen
			.getAllByRole('combobox')
			.find((s) => [...(s as HTMLSelectElement).options].some((o) => o.value === 'vnc'))!;
		await user.selectOptions(technology, 'vnc');
		expect(port).toHaveValue(8080);
		await user.click(screen.getByRole('button', { name: 'actions.save' }));
		await vi.waitFor(() => expect(save).toHaveBeenCalled());
		expect(save.mock.calls[0]?.[0]).toMatchObject({ host: '10.0.0.9', port: 8080 });
	});
});
