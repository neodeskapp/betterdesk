import { afterEach, beforeEach, describe, expect, test, vi } from 'vitest';
import {
	claimViewerWindow,
	openInViewerTab,
	releaseViewerWindow,
	VIEWER_CHANNEL,
	VIEWER_WINDOW
} from '../../../src/lib/remote/viewer-window.ts';

/** The channels the code opened; a message on one reaches the others, as in a browser */
class FakeChannel {
	static all: FakeChannel[] = [];
	onmessage: ((ev: MessageEvent) => void) | null = null;
	sent: unknown[] = [];
	closed = false;
	constructor(readonly name: string) {
		FakeChannel.all.push(this);
	}
	postMessage(data: unknown) {
		this.sent.push(data);
	}
	close() {
		this.closed = true;
	}
	/** Another tab answers on this channel */
	receive(data: unknown) {
		this.onmessage?.({ data } as MessageEvent);
	}
}

const URL_A = 'https://console.test/remote/123456789';

/** The one channel the code opened */
function channel(): FakeChannel {
	const [bc] = FakeChannel.all;
	if (!bc) throw new Error('no channel opened');
	return bc;
}

function fakeTab(href: string) {
	return { location: { href } } as unknown as Window;
}

describe('openInViewerTab', () => {
	let open: ReturnType<typeof vi.fn>;

	beforeEach(() => {
		vi.useFakeTimers();
		FakeChannel.all = [];
		vi.stubGlobal('BroadcastChannel', FakeChannel);
		open = vi.fn();
		vi.stubGlobal('open', open);
	});
	afterEach(() => {
		vi.useRealTimers();
		vi.unstubAllGlobals();
	});

	test('a second Connect opens the named viewer tab inside the click, which brings it to the front', () => {
		const viewer = fakeTab('https://console.test/remote/111111111');
		open.mockReturnValue(viewer);
		expect(openInViewerTab(URL_A, '123456789', 'vm-03')).toBe(true);
		// Opening the tab by name is what makes the browser show it
		expect(open).toHaveBeenCalledWith('', VIEWER_WINDOW);
		const bc = channel();
		expect(bc.name).toBe(VIEWER_CHANNEL);
		expect(bc.sent).toEqual([{ type: 'add-session', deviceId: '123456789', deviceName: 'vm-03' }]);
		bc.receive({ type: 'session-added', deviceId: '123456789' });
		vi.advanceTimersByTime(1000);
		// The open viewer kept its sessions: no reload
		expect(viewer.location.href).toBe('https://console.test/remote/111111111');
		expect(bc.closed).toBe(true);
	});

	test('no viewer open yet: the new tab loads the device', () => {
		const tab = fakeTab('about:blank');
		open.mockReturnValue(tab);
		expect(openInViewerTab(URL_A, '123456789')).toBe(true);
		expect(tab.location.href).toBe(URL_A);
		expect(FakeChannel.all).toHaveLength(0);
	});

	test('a tab with the name but no viewer that answers loads the device', () => {
		const tab = fakeTab('https://console.test/devices');
		open.mockReturnValue(tab);
		openInViewerTab(URL_A, '123456789');
		// Another device's answer is not this one's
		channel().receive({ type: 'session-added', deviceId: '999999999' });
		vi.advanceTimersByTime(299);
		expect(tab.location.href).toBe('https://console.test/devices');
		vi.advanceTimersByTime(1);
		expect(tab.location.href).toBe(URL_A);
	});

	test('a blocked tab says so', () => {
		open.mockReturnValue(null);
		expect(openInViewerTab(URL_A, '123456789')).toBe(false);
	});
});

describe('the viewer tab name', () => {
	afterEach(() => {
		window.name = '';
	});

	test('the viewer claims it and gives it back when it leaves', () => {
		claimViewerWindow();
		expect(window.name).toBe(VIEWER_WINDOW);
		releaseViewerWindow();
		expect(window.name).toBe('');
	});

	test('releasing leaves a name that is not the viewer`s alone', () => {
		window.name = 'other';
		releaseViewerWindow();
		expect(window.name).toBe('other');
	});
});
