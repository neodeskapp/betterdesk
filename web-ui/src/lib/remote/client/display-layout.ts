/**
 * Where the device's displays go in the viewer, and where a point of a
 * display's picture is on the device's desktop.
 *
 * Layouts: single (one display, the others one click away), arranged (all
 * displays where they sit on the device's desktop, scaled down together)
 * and grid (all displays in equal cells). Inside its tile a display shows
 * fitted or at its own size (1:1); at its own size a picture larger than the
 * tile follows the pointer, so every part of it can be reached.
 */
import type { RDDisplay } from './displays.ts';

export type DisplayLayout = 'single' | 'arranged' | 'grid';
export type DisplayZoom = 'fit' | '1:1';

export const DISPLAY_LAYOUTS: readonly DisplayLayout[] = ['single', 'arranged', 'grid'];

export interface Rect {
	x: number;
	y: number;
	w: number;
	h: number;
}

export interface Size {
	w: number;
	h: number;
}

/** The part of the device's desktop a display covers, in desktop units */
export function footprint(d: RDDisplay): Rect {
	return { x: d.x, y: d.y, w: d.width / d.scale, h: d.height / d.scale };
}

/** The smallest rectangle around the displays' footprints */
export function desktopBounds(displays: readonly RDDisplay[]): Rect {
	if (!displays.length) return { x: 0, y: 0, w: 0, h: 0 };
	let left = Infinity;
	let top = Infinity;
	let right = -Infinity;
	let bottom = -Infinity;
	for (const d of displays) {
		const f = footprint(d);
		left = Math.min(left, f.x);
		top = Math.min(top, f.y);
		right = Math.max(right, f.x + f.w);
		bottom = Math.max(bottom, f.y + f.h);
	}
	return { x: left, y: top, w: right - left, h: bottom - top };
}

/** A rectangle made smaller by `by` on each side (never below zero) */
function inset(r: Rect, by: number): Rect {
	const dx = Math.min(by, r.w / 2);
	const dy = Math.min(by, r.h / 2);
	return { x: r.x + dx, y: r.y + dy, w: r.w - 2 * dx, h: r.h - 2 * dy };
}

/**
 * The displays where they sit on the device's desktop, scaled together to
 * fit the area and centred in it. Neighbours keep `gap` pixels between them.
 */
export function arrangedTiles(displays: readonly RDDisplay[], area: Size, gap = 8): Map<number, Rect> {
	const tiles = new Map<number, Rect>();
	const b = desktopBounds(displays);
	if (!b.w || !b.h || area.w <= 0 || area.h <= 0) return tiles;
	const k = Math.min(area.w / b.w, area.h / b.h);
	const ox = (area.w - b.w * k) / 2;
	const oy = (area.h - b.h * k) / 2;
	for (const d of displays) {
		const f = footprint(d);
		const r = { x: ox + (f.x - b.x) * k, y: oy + (f.y - b.y) * k, w: f.w * k, h: f.h * k };
		tiles.set(d.idx, inset(r, gap / 2));
	}
	return tiles;
}

/** Columns and rows for n cells in an area, the ones that show the displays largest */
export function gridShape(
	displays: readonly RDDisplay[],
	area: Size,
	gap = 8
): { cols: number; rows: number } {
	const n = displays.length;
	let best = { cols: 1, rows: Math.max(1, n), shown: -1 };
	for (let cols = 1; cols <= Math.max(1, n); cols++) {
		const rows = Math.ceil(n / cols);
		const cw = (area.w - gap * (cols - 1)) / cols;
		const ch = (area.h - gap * (rows - 1)) / rows;
		if (cw <= 0 || ch <= 0) continue;
		let shown = 0;
		for (const d of displays) {
			const f = footprint(d);
			if (!f.w || !f.h) continue;
			const k = Math.min(cw / f.w, ch / f.h);
			shown += f.w * k * f.h * k;
		}
		// The same picture area with fewer empty cells wins
		if (shown > best.shown + 0.5) best = { cols, rows, shown };
	}
	return { cols: best.cols, rows: best.rows };
}

/** The displays in equal cells, in the device's order, left to right and top to bottom */
export function gridTiles(displays: readonly RDDisplay[], area: Size, gap = 8): Map<number, Rect> {
	const tiles = new Map<number, Rect>();
	if (!displays.length || area.w <= 0 || area.h <= 0) return tiles;
	const { cols, rows } = gridShape(displays, area, gap);
	const cw = (area.w - gap * (cols - 1)) / cols;
	const ch = (area.h - gap * (rows - 1)) / rows;
	displays.forEach((d, i) => {
		const col = i % cols;
		const row = Math.floor(i / cols);
		// A last row with fewer cells is centred
		const inRow = row === rows - 1 ? displays.length - row * cols : cols;
		const shift = ((cols - inRow) * (cw + gap)) / 2;
		tiles.set(d.idx, { x: shift + col * (cw + gap), y: row * (ch + gap), w: cw, h: ch });
	});
	return tiles;
}

/** Tiles for a layout; the single layout has none (the session's own screen shows one display) */
export function layoutTiles(
	layout: DisplayLayout,
	displays: readonly RDDisplay[],
	area: Size,
	gap = 8
): Map<number, Rect> {
	if (layout === 'arranged') return arrangedTiles(displays, area, gap);
	if (layout === 'grid') return gridTiles(displays, area, gap);
	return new Map();
}

/** Scale and offset of a picture in its tile, in the tile canvas' pixels */
export interface ViewTransform {
	scale: number;
	offsetX: number;
	offsetY: number;
}

/**
 * Where a picture goes in a tile. Fit: as large as fits, centred. 1:1: one
 * picture pixel per screen pixel (dpr canvas pixels); along an axis where it
 * is larger than the tile, `pan` (0 to 1) says how far it is moved, 0.5 is
 * the middle. All values in canvas pixels.
 */
export function viewTransform(
	picture: Size,
	tile: Size,
	zoom: DisplayZoom,
	dpr = 1,
	pan: { x: number; y: number } = { x: 0.5, y: 0.5 }
): ViewTransform {
	if (!picture.w || !picture.h) return { scale: 1, offsetX: 0, offsetY: 0 };
	const scale = zoom === '1:1' ? dpr : Math.min(tile.w / picture.w, tile.h / picture.h);
	const place = (room: number, size: number, p: number) =>
		size > room ? (room - size) * Math.max(0, Math.min(1, p)) || 0 : (room - size) / 2;
	return {
		scale,
		offsetX: place(tile.w, picture.w * scale, pan.x),
		offsetY: place(tile.h, picture.h * scale, pan.y)
	};
}

/** A point of the device's desktop */
export interface DesktopPoint {
	x: number;
	y: number;
}

/**
 * The desktop point under a canvas pixel of a display's tile, or null when
 * the pixel is beside the picture. `units` is desktop units per picture
 * pixel (pointScale in displays.ts).
 */
export function tileToDesktop(
	d: RDDisplay,
	picture: Size,
	t: ViewTransform,
	px: number,
	py: number,
	units = 1
): DesktopPoint | null {
	const fx = (px - t.offsetX) / t.scale;
	const fy = (py - t.offsetY) / t.scale;
	if (fx < 0 || fy < 0 || fx > picture.w || fy > picture.h) return null;
	return { x: d.x + Math.round(fx * units), y: d.y + Math.round(fy * units) };
}

/** The canvas pixel of a desktop point on a display's tile, or null when the point is on another display */
export function desktopToTile(
	d: RDDisplay,
	picture: Size,
	t: ViewTransform,
	gx: number,
	gy: number,
	units = 1
): DesktopPoint | null {
	const fx = (gx - d.x) / units;
	const fy = (gy - d.y) / units;
	if (fx < 0 || fy < 0 || fx >= picture.w || fy >= picture.h) return null;
	return { x: t.offsetX + fx * t.scale, y: t.offsetY + fy * t.scale };
}

/** The display whose footprint holds a desktop point, or -1 */
export function displayAt(displays: readonly RDDisplay[], gx: number, gy: number): number {
	for (const d of displays) {
		const f = footprint(d);
		if (gx >= f.x && gy >= f.y && gx < f.x + f.w && gy < f.y + f.h) return d.idx;
	}
	return -1;
}

/** One remote display per local screen, both left to right; the rest stay in the main window */
export interface SpreadPlan {
	/** The display for the screen the main window is on, or -1 */
	main: number;
	/** The other displays and the screen each opens on */
	windows: { display: number; screen: number }[];
	/** Displays without a screen of their own */
	left: number[];
}

/**
 * Pairs remote displays with local screens, both sorted left to right (then
 * top to bottom). The main window keeps the display paired with its own
 * screen; each other pair opens a window.
 */
export function planSpread(
	displays: readonly RDDisplay[],
	screens: readonly Rect[],
	currentScreen: number
): SpreadPlan {
	const byPlace = <T>(list: readonly T[], at: (t: T) => Rect) =>
		list
			.map((t, i) => ({ i, r: at(t) }))
			.sort((a, b) => a.r.x - b.r.x || a.r.y - b.r.y)
			.map((e) => e.i);
	const ds = byPlace(displays, footprint).map((i) => (displays[i] as RDDisplay).idx);
	const ss = byPlace(screens, (s) => s);
	const plan: SpreadPlan = { main: -1, windows: [], left: [] };
	ds.forEach((display, n) => {
		const screen = ss[n];
		if (screen === undefined) plan.left.push(display);
		else if (screen === currentScreen) plan.main = display;
		else plan.windows.push({ display, screen });
	});
	return plan;
}
