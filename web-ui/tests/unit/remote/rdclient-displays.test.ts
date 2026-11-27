/**
 * A RustDesk session with a device of several displays
 * (src/lib/remote/client/client.ts with display-streams.ts): the messages
 * that ask the device for displays, the video of each display reaching the
 * screen that shows it, and older devices switching one display at a time.
 */
import { afterEach, beforeEach, describe, expect, test, vi } from 'vitest';
import { RDClient } from '../../../src/lib/remote/client/client.ts';
import type { MessageInit, PeerInfo } from '../../../src/lib/remote/client/generated/protos.ts';
import type { RDDisplayDecoder } from '../../../src/lib/remote/client/display-streams.ts';

beforeEach(() => {
	vi.spyOn(HTMLCanvasElement.prototype, 'getContext').mockReturnValue(null);
	vi.stubGlobal('requestAnimationFrame', () => 1);
	vi.stubGlobal('cancelAnimationFrame', () => {});
	// A secure context: the browser plays the VP9 the devices below send
	vi.stubGlobal('VideoDecoder', vi.fn());
});
afterEach(() => {
	vi.unstubAllGlobals();
	vi.restoreAllMocks();
	document.body.replaceChildren();
});

const DISPLAYS = [
	{ x: 0, y: 0, width: 1920, height: 1080, name: 'Display 1' },
	{ x: 1920, y: 0, width: 1280, height: 1024, name: 'Display 2' },
	{ x: -1280, y: 360, width: 2560, height: 1440, name: 'Display 3', scale: 2 }
];

/** A streaming session whose device has the three displays; `sent` gets what goes to the device */
async function session(version: string, platform = 'Windows') {
	const canvas = document.createElement('canvas');
	document.body.append(canvas);
	const c = new RDClient(canvas, { deviceId: '900100012', disableAudio: true });
	await c.proto.load();
	c.conn.sendRelay = () => {};
	const sent: MessageInit[] = [];
	vi.spyOn(c, '_sendPeerMessage').mockImplementation((m: MessageInit) => void sent.push(m));
	c._startSession();
	const info = { version, platform, displays: DISPLAYS, currentDisplay: 0 } as unknown as PeerInfo;
	c._peerInfo = info;
	c._processPeerInfo(info);
	return { c, sent };
}

/** The misc messages sent of one kind */
const miscs = (sent: MessageInit[], key: 'captureDisplays' | 'refreshVideoDisplay' | 'switchDisplay') =>
	sent.map((m) => m.misc?.[key]).filter((v) => v !== undefined && v !== null);

/** Stands in for a decoder: records what it was given */
function fake(d: RDDisplayDecoder) {
	const got: number[] = [];
	d.initialized = true;
	d.currentCodec = 'vp9';
	vi.spyOn(d, 'init').mockResolvedValue(undefined);
	vi.spyOn(d, 'decode').mockImplementation(async (chunk) => void got.push(chunk.data.length));
	return got;
}

/** A VP9 frame of `size` bytes from a display, as the device sends it */
function frameFrom(c: RDClient, display: number, size: number) {
	const wire = c.proto.serializeMessage({
		videoFrame: { vp9s: { frames: [{ data: new Uint8Array(size), key: true }] }, display }
	});
	return c.proto.decodeMessage(wire).videoFrame!;
}

function tileCanvas(): HTMLCanvasElement {
	const box = document.createElement('div');
	const canvas = document.createElement('canvas');
	box.append(canvas);
	document.body.append(box);
	return canvas;
}

describe('a device of RustDesk 1.2.4 or later', () => {
	test('a display shown on its own: the device is asked for it, and its video reaches its own screen', async () => {
		const { c, sent } = await session('1.5.3');
		expect(c.canShowDisplaysTogether()).toBe(true);
		// The session starts with the display the device streams: nothing to ask yet
		expect(miscs(sent, 'captureDisplays')).toEqual([]);
		c.showDisplay(2, tileCanvas());
		expect(miscs(sent, 'captureDisplays').at(-1)).toMatchObject({ set: [0, 2] });
		expect(miscs(sent, 'refreshVideoDisplay')).toEqual([2]);
		const main = fake(c.video);
		const own = fake(c.displays.views.get(2)!.decoder);
		await c._handleVideoFrame(frameFrom(c, 2, 30));
		await c._handleVideoFrame(frameFrom(c, 0, 10));
		// Display 1 has no screen: its frame is dropped
		await c._handleVideoFrame(frameFrom(c, 1, 20));
		expect(own).toEqual([30]);
		expect(main).toEqual([10]);
		// Each frame is acknowledged, shown or not, so the device keeps its pace
		expect(sent.filter((m) => m.misc?.videoReceived)).toHaveLength(3);
		const traffic = c.getDisplayTraffic();
		expect(traffic.map((t) => [t.idx, t.shown, t.streaming])).toEqual([
			[0, 'main', true],
			[1, 'none', false],
			[2, 'own', true]
		]);
		c.disconnect();
	});

	test("each screen knows its display's place: clicks go to that place of the desktop", async () => {
		const { c } = await session('1.5.3', 'Mac OS');
		c.showDisplay(2, tileCanvas());
		const own = c.displays.views.get(2)!.extra.renderer;
		expect(own.origin).toEqual({ x: -1280, y: 360 });
		// A Retina display the Mac does not treat as current: desktop units, not pixels
		expect(own.units).toBe(0.5);
		expect(c.renderer.origin).toEqual({ x: 0, y: 0 });
		c.disconnect();
	});

	test('the cursor goes to every screen; each draws it on its own display only', async () => {
		const { c } = await session('1.5.3');
		c.showDisplay(1, tileCanvas());
		const wire = c.proto.serializeMessage({ cursorPosition: { x: 2000, y: 50 } });
		c._dispatchMessage(c.proto.decodeMessage(wire));
		const own = c.displays.views.get(1)!.extra.renderer;
		own.setRemoteSize(1280, 1024);
		c.renderer.setRemoteSize(1920, 1080);
		expect(own._cursorOnPicture()).toEqual({ x: 80, y: 50 });
		expect(c.renderer._cursorOnPicture()).toBeNull();
		c.disconnect();
	});

	test('a hidden window stops its display; every display on its own leaves the session canvas without one', async () => {
		const { c, sent } = await session('1.5.3');
		c.showDisplay(1, tileCanvas());
		c.setDisplayVisible(1, false);
		expect(miscs(sent, 'captureDisplays').at(-1)).toMatchObject({ set: [0] });
		c.setDisplayVisible(1, true);
		c.setMainDisplay(null);
		expect(miscs(sent, 'captureDisplays').at(-1)).toMatchObject({ set: [1] });
		c.disconnect();
	});

	test('switching the display of the session canvas keeps the displays shown on their own', async () => {
		const { c, sent } = await session('1.5.3');
		c.showDisplay(2, tileCanvas());
		c.switchMonitor(1);
		expect(miscs(sent, 'switchDisplay').at(-1)).toMatchObject({ display: 1 });
		// The switch stopped display 0 on the device and started 1; 2 stays as it was
		expect(miscs(sent, 'captureDisplays').at(-1)).toMatchObject({ set: [0, 2] });
		expect(c.displays.captureSet()).toEqual([1, 2]);
		expect(c.renderer.origin).toEqual({ x: 1920, y: 0 });
		c.disconnect();
	});
});

describe('an older device: one display at a time', () => {
	test('nothing is shown on its own; every frame goes to the session canvas', async () => {
		const { c, sent } = await session('1.2.3');
		expect(c.canShowDisplaysTogether()).toBe(false);
		c.showDisplay(1, tileCanvas());
		expect(c.displays.views.size).toBe(0);
		const main = fake(c.video);
		await c._handleVideoFrame(frameFrom(c, 1, 20));
		expect(main).toEqual([20]);
		expect(miscs(sent, 'captureDisplays')).toEqual([]);
		c.disconnect();
	});

	test('switching asks for that display alone, and clicks land on it', async () => {
		const { c, sent } = await session('1.2.3');
		c.switchMonitor(1);
		expect(miscs(sent, 'switchDisplay').at(-1)).toMatchObject({ display: 1 });
		expect(miscs(sent, 'captureDisplays').at(-1)).toMatchObject({ set: [1] });
		expect(c.renderer.origin).toEqual({ x: 1920, y: 0 });
		c.disconnect();
	});
});

describe('the device changes its displays during the session', () => {
	test('a later peer info brings only the displays: version, platform and the display shown stay', async () => {
		const { c } = await session('1.5.3', 'Mac OS');
		c.switchMonitor(1);
		// RustDesk's display service: the displays, nothing else, and current_display 0
		const wire = c.proto.serializeMessage({
			peerInfo: { displays: [...DISPLAYS, { x: 3200, y: 0, width: 1600, height: 900, name: 'Display 4' }] }
		});
		c._dispatchMessage(c.proto.decodeMessage(wire));
		expect(c.canShowDisplaysTogether()).toBe(true);
		expect(c.getDisplays()).toHaveLength(4);
		expect(c.getCurrentDisplay()).toBe(1);
		expect(c.peerInfo?.platform).toBe('Mac OS');
		expect(c.peerInfo?.version).toBe('1.5.3');
		expect(c.renderer.origin).toEqual({ x: 1920, y: 0 });
		c.disconnect();
	});
});

describe('virtual displays after the device adds or removes one', () => {
	/** A signed-in Windows device with Amyuni virtual displays; `n` of them plugged in */
	async function amyuni(n: number) {
		const { c } = await session('1.4.2');
		const additions = {
			is_installed: true,
			idd_impl: 'amyuni_idd',
			supported_privacy_mode_impl: [] as string[]
		};
		c._peerInfo!.platformAdditions = JSON.stringify(
			n ? { ...additions, amyuni_virtual_displays: n } : additions
		);
		c._processPeerInfo(c._peerInfo);
		return c;
	}
	/** The peer info RustDesk's display service sends when the displays change */
	const changed = (additions: object) =>
		({
			displays: DISPLAYS,
			currentDisplay: 0,
			platformAdditions: JSON.stringify(additions)
		}) as unknown as PeerInfo;

	test('an added one keeps the menu and counts it, so it can be removed', async () => {
		const c = await amyuni(0);
		c._handlePeerInfo(changed({ idd_impl: 'amyuni_idd', amyuni_virtual_displays: 1 }));
		expect(c.getVirtualDisplaySupport()).toMatchObject({
			supported: true,
			impl: 'amyuni_idd',
			amyuniCount: 1
		});
	});

	test('the last one removed counts none', async () => {
		const c = await amyuni(2);
		c._handlePeerInfo(changed({ idd_impl: 'amyuni_idd' }));
		expect(c.getVirtualDisplaySupport()).toMatchObject({ supported: true, amyuniCount: 0 });
	});

	test('RustDesk virtual displays follow the device', async () => {
		const { c } = await session('1.4.2');
		c._peerInfo!.platformAdditions = JSON.stringify({ is_installed: true, idd_impl: 'rustdesk_idd' });
		c._processPeerInfo(c._peerInfo);
		c._handlePeerInfo(changed({ idd_impl: 'rustdesk_idd', rustdesk_virtual_displays: [1, 3] }));
		expect(c.getVirtualDisplaySupport()).toMatchObject({ supported: true, rustdeskDisplays: [1, 3] });
		c._handlePeerInfo(changed({ idd_impl: 'rustdesk_idd' }));
		expect(c.getVirtualDisplaySupport()).toMatchObject({ supported: true, rustdeskDisplays: [] });
	});
});
