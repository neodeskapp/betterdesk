/**
 * Phones and the viewer (capabilities.ts phoneSessionGate): a phone gets
 * the session in landscape from 640 px wide; in portrait, and in landscape
 * below 640, it gets the "needs a larger screen" notice, which says that
 * turning the phone helps where it would. Desktops (a fine pointer) and
 * tablets are never stopped.
 */
import { describe, expect, test } from 'vitest';
import { phoneSessionGate } from '../../../src/lib/remote/capabilities.ts';

describe('phones', () => {
	test('landscape from 640 px opens the session', () => {
		expect(phoneSessionGate(844, 390, false)).toEqual({ blocked: false, turnHelps: false });
		expect(phoneSessionGate(640, 360, false)).toEqual({ blocked: false, turnHelps: false });
		expect(phoneSessionGate(700, 400, false)).toEqual({ blocked: false, turnHelps: false });
	});

	test('portrait gets the notice, which says turning helps when the long side reaches 640', () => {
		expect(phoneSessionGate(390, 844, false)).toEqual({ blocked: true, turnHelps: true });
		expect(phoneSessionGate(360, 600, false)).toEqual({ blocked: true, turnHelps: false });
	});

	test('landscape under 640 gets the notice without the turning line', () => {
		expect(phoneSessionGate(600, 360, false)).toEqual({ blocked: true, turnHelps: false });
	});

	test('desktops and tablets are never stopped', () => {
		expect(phoneSessionGate(390, 844, true)).toEqual({ blocked: false, turnHelps: false });
		expect(phoneSessionGate(1024, 768, false)).toEqual({ blocked: false, turnHelps: false });
		expect(phoneSessionGate(820, 1180, false)).toEqual({ blocked: false, turnHelps: false });
	});
});
