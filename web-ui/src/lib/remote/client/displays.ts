/**
 * The device's displays as its peer info describes them (RustDesk
 * DisplayInfo), and whether it can stream several of them at once.
 *
 * Coordinates: a display sits at x, y on the device's desktop, the space
 * mouse events and the cursor position use. Its picture is width x height
 * pixels. On macOS (Retina) and on scaled Wayland desktops the picture has
 * `scale` pixels per desktop unit, so the display covers width / scale x
 * height / scale of the desktop; everywhere else scale is 1.
 */

/** One display of the device */
export interface RDDisplay {
	idx: number;
	name: string;
	x: number;
	y: number;
	/** Size of the picture in pixels */
	width: number;
	height: number;
	/** Picture pixels per desktop unit, at least 1 */
	scale: number;
	/** The display at the desktop's origin */
	primary: boolean;
}

/** The fields of PeerInfo this module reads */
export interface RDPeerDisplays {
	version?: string | null;
	platform?: string | null;
	displays?:
		| {
				x?: number | null;
				y?: number | null;
				width?: number | null;
				height?: number | null;
				name?: string | null;
				scale?: number | null;
		  }[]
		| null;
}

/** RustDesk 1.2.4 streams several displays to one connection (MIN_VER_MULTI_UI_SESSION) */
export const DISPLAYS_TOGETHER_SINCE = '1.2.4';

/** An integer the way Rust's i64 parse reads it: anything else is 0 */
function part(s: string | undefined): number {
	return s !== undefined && /^[+-]?\d+$/.test(s) ? Number(s) : 0;
}

/**
 * A version as one number, the way RustDesk compares them
 * (hbb_common::get_version_number): 1.2.4 is 1002040, a "-3" suffix adds 3.
 */
export function versionNumber(version: string): number {
	const [main, suffix] = String(version || '').split('-');
	let n = 0;
	let last = 0;
	for (const x of (main ?? '').split('.')) {
		last = part(x);
		n = n * 1000 + last;
	}
	n += last * 9;
	return n + part(suffix);
}

/** The displays of a peer info, in the device's order */
export function displaysOf(info: RDPeerDisplays | null | undefined): RDDisplay[] {
	const list = info?.displays ?? [];
	return list.map((d, idx) => {
		const x = Number(d.x) || 0;
		const y = Number(d.y) || 0;
		const scale = Number(d.scale) > 1 ? Number(d.scale) : 1;
		return {
			idx,
			name: d.name || `Display ${idx + 1}`,
			x,
			y,
			width: Number(d.width) || 0,
			height: Number(d.height) || 0,
			scale,
			primary: x === 0 && y === 0
		};
	});
}

/**
 * Whether the device can stream more than one display to this connection:
 * it runs RustDesk 1.2.4 or later (its peer info says so) and has more than
 * one display. Older devices switch between displays one at a time.
 */
export function canShowDisplaysTogether(info: RDPeerDisplays | null | undefined): boolean {
	if (!info || (info.displays?.length ?? 0) < 2) return false;
	return versionNumber(info.version || '') >= versionNumber(DISPLAYS_TOGETHER_SINCE);
}

/**
 * Desktop units per picture pixel for mouse events on a display, and
 * picture pixels per desktop unit for the cursor on it. A macOS device
 * converts both itself, but only for the display it treats as current
 * (Retina in its connection.rs); the others need the conversion here.
 */
export function pointScale(d: RDDisplay, current: number, platform: string): number {
	if (d.idx === current || d.scale <= 1) return 1;
	return /^mac/i.test(platform) ? 1 / d.scale : 1;
}
