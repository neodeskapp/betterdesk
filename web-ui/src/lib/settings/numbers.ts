/**
 * Number fields of the settings tabs: what a save takes as typed.
 */

/**
 * A whole number from min to max as typed into a number field, or null:
 * the save refuses anything else with a message at the field instead of
 * turning it into the default or the limit.
 */
export function wholeInRange(value: unknown, min: number, max: number): number | null {
	if (value === '' || value === null || value === undefined) return null;
	const n = Number(value);
	return Number.isInteger(n) && n >= min && n <= max ? n : null;
}
