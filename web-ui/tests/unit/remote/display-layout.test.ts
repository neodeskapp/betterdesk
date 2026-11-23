/**
 * Several displays of one device (src/lib/remote/client/displays.ts and
 * display-layout.ts): which devices stream them together, where each goes
 * in the arranged and grid layouts, and which desktop point a pixel of a
 * tile is (negative origins, a Retina display beside ordinary ones).
 */
import { describe, expect, test } from 'vitest';
import {
	arrangedTiles,
	desktopToTile,
	displayAt,
	gridShape,
	gridTiles,
	planSpread,
	tileToDesktop,
	viewTransform
} from '../../../src/lib/remote/client/display-layout.ts';
import {
	canShowDisplaysTogether,
	displaysOf,
	pointScale,
	versionNumber
} from '../../../src/lib/remote/client/displays.ts';

/** The mock device's three displays: one left of the primary, a Retina one at a negative origin */
const PEER = {
	version: '1.5.3',
	platform: 'Windows',
	displays: [
		{ x: 0, y: 0, width: 1920, height: 1080, name: 'Display 1' },
		{ x: 1920, y: 0, width: 1280, height: 1024, name: 'Display 2' },
		{ x: -1280, y: 360, width: 2560, height: 1440, name: 'Display 3', scale: 2 }
	]
};
const DISPLAYS = displaysOf(PEER);
const [D0, , D2] = DISPLAYS as [(typeof DISPLAYS)[0], (typeof DISPLAYS)[0], (typeof DISPLAYS)[0]];

describe('which devices stream several displays together', () => {
	test('RustDesk numbers its versions as RustDesk does', () => {
		expect(versionNumber('1.2.4')).toBe(1002040);
		expect(versionNumber('1.1.10')).toBe(1001100);
		expect(versionNumber('1.2.3-2')).toBe(1002032);
		expect(versionNumber('NeoDesk-Web/1.4.9')).toBe(0);
	});

	test('from 1.2.4 with more than one display; older devices and single displays switch', () => {
		expect(canShowDisplaysTogether(PEER)).toBe(true);
		expect(canShowDisplaysTogether({ ...PEER, version: '1.2.4' })).toBe(true);
		expect(canShowDisplaysTogether({ ...PEER, version: '1.2.3' })).toBe(false);
		expect(canShowDisplaysTogether({ ...PEER, version: '' })).toBe(false);
		expect(canShowDisplaysTogether({ ...PEER, displays: PEER.displays.slice(0, 1) })).toBe(false);
		expect(canShowDisplaysTogether(null)).toBe(false);
	});

	test('a display reads its place, size and scale; the one at the origin is the primary', () => {
		expect(D2).toEqual({
			idx: 2,
			name: 'Display 3',
			x: -1280,
			y: 360,
			width: 2560,
			height: 1440,
			scale: 2,
			primary: false
		});
		expect(D0.primary).toBe(true);
		expect(D0.scale).toBe(1);
	});
});

describe('the arranged layout', () => {
	test('places the displays as on the desktop, a Retina display at its desktop size', () => {
		const tiles = arrangedTiles(DISPLAYS, { w: 1120, h: 540 }, 8);
		// The desktop is 4480 x 1080 units from x -1280: a quarter of it fits, centred vertically
		expect(tiles.get(0)).toEqual({ x: 324, y: 139, w: 472, h: 262 });
		expect(tiles.get(1)).toEqual({ x: 804, y: 139, w: 312, h: 248 });
		// 2560 pixels at scale 2 cover 1280 units, so this tile is as wide as Display 2's
		expect(tiles.get(2)).toEqual({ x: 4, y: 229, w: 312, h: 172 });
	});

	test('nothing to place in an area without room', () => {
		expect(arrangedTiles(DISPLAYS, { w: 0, h: 400 }).size).toBe(0);
		expect(arrangedTiles([], { w: 800, h: 400 }).size).toBe(0);
	});
});

describe('the grid layout', () => {
	test('picks the columns that show the displays largest', () => {
		expect(gridShape(DISPLAYS, { w: 1200, h: 600 }, 8)).toEqual({ cols: 2, rows: 2 });
		expect(gridShape(DISPLAYS, { w: 1800, h: 400 }, 8)).toEqual({ cols: 3, rows: 1 });
		expect(gridShape(DISPLAYS, { w: 500, h: 1400 }, 8)).toEqual({ cols: 1, rows: 3 });
	});

	test('equal cells in the device order, a shorter last row centred', () => {
		const tiles = gridTiles(DISPLAYS, { w: 1200, h: 600 }, 8);
		expect(tiles.get(0)).toEqual({ x: 0, y: 0, w: 596, h: 296 });
		expect(tiles.get(1)).toEqual({ x: 604, y: 0, w: 596, h: 296 });
		expect(tiles.get(2)).toEqual({ x: 302, y: 304, w: 596, h: 296 });
	});
});

describe('a picture in its tile', () => {
	test('fit: as large as fits, centred', () => {
		expect(viewTransform({ w: 1920, h: 1080 }, { w: 960, h: 600 }, 'fit')).toEqual({
			scale: 0.5,
			offsetX: 0,
			offsetY: 30
		});
	});

	test('1:1: one picture pixel per screen pixel, moved along where it is larger than the tile', () => {
		const picture = { w: 1920, h: 1080 };
		const tile = { w: 960, h: 600 };
		expect(viewTransform(picture, tile, '1:1', 1, { x: 0, y: 1 })).toEqual({
			scale: 1,
			offsetX: 0,
			offsetY: -480
		});
		expect(viewTransform(picture, tile, '1:1', 1)).toEqual({ scale: 1, offsetX: -480, offsetY: -240 });
		// Two canvas pixels per screen pixel: the picture takes twice the canvas pixels
		expect(viewTransform(picture, { w: 4000, h: 3000 }, '1:1', 2)).toEqual({
			scale: 2,
			offsetX: 80,
			offsetY: 420
		});
	});
});

describe('a pixel of a tile on the desktop', () => {
	const picture = { w: 2560, h: 1440 };
	const quarter = viewTransform(picture, { w: 640, h: 360 }, 'fit');

	test('a display at a negative origin adds its origin to the picture pixel', () => {
		expect(tileToDesktop(D2, picture, quarter, 320, 180)).toEqual({ x: 0, y: 1080 });
		expect(tileToDesktop(D2, picture, quarter, 0, 0)).toEqual({ x: -1280, y: 360 });
		expect(tileToDesktop(D2, picture, quarter, -1, 10)).toBeNull();
		expect(tileToDesktop(D2, picture, quarter, 10, 361)).toBeNull();
	});

	test('a Retina display the Mac does not treat as current gets desktop units, not pixels', () => {
		const units = pointScale(D2, 0, 'Mac OS');
		expect(units).toBe(0.5);
		expect(tileToDesktop(D2, picture, quarter, 320, 180, units)).toEqual({ x: -640, y: 720 });
		// The current display, and every display of another system, the device converts itself
		expect(pointScale(D2, 2, 'Mac OS')).toBe(1);
		expect(pointScale(D2, 0, 'Windows')).toBe(1);
		expect(pointScale(D0, 2, 'Mac OS')).toBe(1);
	});

	test('the cursor goes back to the same pixel, and only on its own display', () => {
		expect(desktopToTile(D2, picture, quarter, 0, 1080)).toEqual({ x: 320, y: 180 });
		expect(desktopToTile(D2, picture, quarter, -640, 720, 0.5)).toEqual({ x: 320, y: 180 });
		expect(desktopToTile(D2, picture, quarter, 100, 100)).toBeNull();
	});

	test('the display under a desktop point', () => {
		expect(displayAt(DISPLAYS, -1, 400)).toBe(2);
		expect(displayAt(DISPLAYS, 1919, 0)).toBe(0);
		expect(displayAt(DISPLAYS, 1920, 0)).toBe(1);
		expect(displayAt(DISPLAYS, -1, 0)).toBe(-1);
	});
});

describe('spread across local screens', () => {
	const screens = [
		{ x: 0, y: 0, w: 1920, h: 1080 },
		{ x: -1920, y: 0, w: 1920, h: 1080 },
		{ x: 1920, y: 0, w: 2560, h: 1440 }
	];

	test('pairs displays and screens left to right; the main window keeps the one of its screen', () => {
		expect(planSpread(DISPLAYS, screens, 0)).toEqual({
			main: 0,
			windows: [
				{ display: 2, screen: 1 },
				{ display: 1, screen: 2 }
			],
			left: []
		});
	});

	test('displays beyond the last screen stay in the main window', () => {
		expect(planSpread(DISPLAYS, screens.slice(0, 2), 1)).toEqual({
			main: 2,
			windows: [{ display: 0, screen: 0 }],
			left: [1]
		});
	});
});
