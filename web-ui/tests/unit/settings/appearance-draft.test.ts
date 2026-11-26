/**
 * Settings › Appearance and Branding as one draft of settings (PATTERN.md
 * rule 1): the stored appearance as flat fields, one per setting, so the
 * save bar names each colour and each background part it changes; and
 * back for the save.
 */
import { describe, expect, test } from 'vitest';
import {
	brandingFromServer,
	flatBranding,
	nestBranding,
	slideNames
} from '../../../src/lib/settings/appearance.ts';

describe('the draft fields of the appearance', () => {
	test('each colour is a field of its own; everything else keeps its name', () => {
		const v = brandingFromServer({ appName: 'Desk', colors: { accentBlue: '#123456' } });
		const flat = flatBranding(v);
		expect(flat['colors.accentBlue']).toBe('#123456');
		expect(flat['colors.bgPrimary']).toBe('');
		expect(flat.appName).toBe('Desk');
		expect(flat).not.toHaveProperty('colors');
		expect(flat.loginSlides).toEqual(v.loginSlides);
	});

	test('back for the save: the same appearance, and no field of the draft leaks into it', () => {
		const v = brandingFromServer({
			appName: 'Desk',
			bgType: 'color',
			bgColor: '#000000',
			colors: { textPrimary: '#ffffff' }
		});
		const flat = {
			...flatBranding(v),
			'colors.accentRed': '#ff0000',
			logoUpload: 'logo.png',
			'client.phone': '1'
		};
		const back = nestBranding(flat);
		expect(back).toEqual({ ...v, colors: { ...v.colors, accentRed: '#ff0000' } });
		expect(back).not.toHaveProperty('logoUpload');
		expect(back).not.toHaveProperty('client.phone');
	});

	test('slides in words: their title, else their built-in name, else their picture', () => {
		const words = slideNames([
			{ id: 'a', slideKey: 'free', imageUrl: '/img/login-slides/free.jpg', title: '', body: '', overlay: 45 },
			{ id: 'b', slideKey: '', imageUrl: '/uploads/beach.jpg', title: '', body: '', overlay: 45 },
			{ id: 'c', slideKey: '', imageUrl: '', title: 'Welcome', body: '', overlay: 45 }
		]);
		expect(words).toEqual(['free', 'beach.jpg', 'Welcome']);
	});
});
