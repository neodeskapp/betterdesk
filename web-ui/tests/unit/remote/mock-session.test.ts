/**
 * The dev mock device: it walks the viewer through the connecting steps of
 * the page's scenario (first device only), takes the password "demo" and
 * the code 123456, and its file transfer and local files answer in the
 * shapes the file sheet reads.
 */
import { afterEach, beforeEach, describe, expect, test, vi } from 'vitest';
import type { ClientOptions } from '../../../src/lib/remote/rdclient.ts';

type MockModule = typeof import('../../../src/lib/remote/client/mock-session.ts');

const FIRST = '900100012';
const w = window as unknown as Record<string, unknown>;

/** The mock reads the page's scenario and first device when it loads */
async function loadMock(scenario: string): Promise<MockModule> {
	vi.resetModules();
	w.__mockRemote = { scenario };
	w.__initialDeviceId = FIRST;
	return import('../../../src/lib/remote/client/mock-session.ts');
}

function session(m: MockModule, deviceId = FIRST) {
	const canvas = document.createElement('canvas');
	document.body.appendChild(canvas);
	const s = new m.MockRemoteSession(canvas, { deviceId } as ClientOptions);
	const events: string[] = [];
	const states: string[] = [];
	for (const name of ['password_required', 'login_error', 'login_success', '2fa_required', 'session_start'])
		s.on(name, () => events.push(name));
	s.on('error', (msg: never) => events.push('error: ' + String(msg)));
	s.on('state', (state: never) => states.push(state));
	return { s, events, states };
}

beforeEach(() => {
	vi.useFakeTimers({ toFake: ['setTimeout', 'clearTimeout', 'setInterval', 'clearInterval', 'Date'] });
	vi.spyOn(HTMLCanvasElement.prototype, 'getContext').mockReturnValue(null);
	vi.stubGlobal('requestAnimationFrame', () => 1);
	vi.stubGlobal('cancelAnimationFrame', () => {});
});

afterEach(() => {
	vi.useRealTimers();
	vi.unstubAllGlobals();
	document.body.innerHTML = '';
	Reflect.deleteProperty(window, '__mockRemote');
	Reflect.deleteProperty(window, '__initialDeviceId');
});

describe('mock device scenarios', () => {
	test('ok: connects without a password and streams', async () => {
		const m = await loadMock('ok');
		const { s, events, states } = session(m);
		void s.connect();
		await vi.advanceTimersByTimeAsync(5000);
		expect(events).toEqual(['session_start']);
		expect(states).toEqual(['connecting', 'waiting_password', 'streaming']);
		expect(s.getMonitors().map((d) => d.name)).toEqual(['Display 1', 'Display 2', 'Display 3']);
		// Display 3 sits left of the primary one; the device streams the three together
		expect(s.getDisplays()[2]).toMatchObject({ x: -1600, y: 180, width: 1600, height: 900 });
		expect(s.canShowDisplaysTogether()).toBe(true);
		s.disconnect();
		expect(s.state).toBe('disconnected');
	});

	test('one-at-a-time: an older device switches its displays, it shows none on its own', async () => {
		const m = await loadMock('one-at-a-time');
		const { s } = session(m);
		void s.connect();
		await vi.advanceTimersByTimeAsync(5000);
		expect(s.canShowDisplaysTogether()).toBe(false);
		s.showDisplay(1, document.createElement('canvas'));
		expect(s.displays.views.size).toBe(0);
		s.switchMonitor(2);
		expect(s.getMonitors().find((d) => d.current)?.idx).toBe(2);
		expect(Reflect.get(window, '__ndMockDevice')).toMatchObject({ streaming: [2] });
		s.disconnect();
	});

	test('password: only "demo" signs in', async () => {
		const m = await loadMock('password');
		const { s, events } = session(m);
		void s.connect();
		await vi.advanceTimersByTimeAsync(5000);
		expect(events).toEqual(['password_required']);
		void s.authenticate('wrong');
		await vi.advanceTimersByTimeAsync(1000);
		expect(events).toEqual(['password_required', 'login_error']);
		void s.authenticate('demo');
		await vi.advanceTimersByTimeAsync(1500);
		expect(events).toEqual(['password_required', 'login_error', 'login_success', 'session_start']);
		expect(s.state).toBe('streaming');
		s.disconnect();
	});

	test('2fa: the code 123456 after the password', async () => {
		const m = await loadMock('2fa');
		const { s, events } = session(m);
		void s.connect();
		await vi.advanceTimersByTimeAsync(5000);
		void s.authenticate('demo');
		await vi.advanceTimersByTimeAsync(1000);
		expect(s.state).toBe('waiting_2fa');
		void s.submit2FA('000000');
		await vi.advanceTimersByTimeAsync(1000);
		expect(s.state).toBe('waiting_2fa');
		void s.submit2FA('123456');
		await vi.advanceTimersByTimeAsync(1500);
		expect(events.slice(-3)).toEqual(['2fa_required', 'login_success', 'session_start']);
		s.disconnect();
	});

	test('refused: the first device fails, a device opened later connects', async () => {
		const m = await loadMock('refused');
		const first = session(m);
		void first.s.connect();
		await vi.advanceTimersByTimeAsync(10000);
		expect(first.events).toEqual(['error: Relay refused: the remote user declined the connection']);
		expect(first.s.state).toBe('error');

		const later = session(m, '900100013');
		void later.s.connect();
		await vi.advanceTimersByTimeAsync(5000);
		expect(later.events).toEqual(['session_start']);
		later.s.disconnect();
	});
});

describe('mock files', () => {
	test('the device lists its home folder without hidden files until asked', async () => {
		const m = await loadMock('ok');
		const { s } = session(m);
		const dirs: { path: string; entries: { name: string }[] }[] = [];
		s.on('file_dir', (d: never) => dirs.push(d));
		s.fileTransfer.browseDir('');
		await vi.advanceTimersByTimeAsync(400);
		expect(dirs[0]?.path).toBe('/home/demo');
		expect(dirs[0]?.entries.map((e) => e.name)).not.toContain('.bashrc');
		s.fileTransfer.setShowHidden(true);
		await vi.advanceTimersByTimeAsync(400);
		expect(dirs[1]?.entries.map((e) => e.name)).toContain('.bashrc');
	});

	test('a local folder walks to the shape the file transfer uploads', async () => {
		const m = await loadMock('ok');
		const local = new m.MockLocalFiles();
		expect(local.hasRoot).toBe(true);
		expect(local.currentPath).toBe('Review files');
		const scripts = (await local.listCurrent()).find((e) => e.name === 'Scripts');
		const walk = await local.walkFolder(scripts);
		expect(walk.rootName).toBe('Scripts');
		expect(walk.files.map((f) => [f.relativePath, f.size])).toEqual([
			['Scripts/cleanup.ps1', 6 * 1024],
			['Scripts/collect-logs.sh', 3 * 1024]
		]);

		const { s } = session(m);
		s.fileTransfer.uploadFolder(walk, '/home/demo');
		await vi.advanceTimersByTimeAsync(3000);
		expect(s.fileTransfer._tree['/home/demo']?.some((e) => e.name === 'Scripts' && e.isDir)).toBe(true);
		expect(s.fileTransfer._tree['/home/demo/Scripts']?.map((e) => e.name)).toEqual([
			'cleanup.ps1',
			'collect-logs.sh'
		]);
	});
});
