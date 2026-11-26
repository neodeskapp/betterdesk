/**
 * Moving a node without dragging (WCAG 2.5.7): while a node is selected,
 * the canvas shows Move left, up, down and right; one click moves it one
 * step, as an arrow key does.
 */
import { render, screen, within } from '@testing-library/svelte';
import userEvent from '@testing-library/user-event';
import { createRawSnippet } from 'svelte';
import { expect, test, vi } from 'vitest';
import NodeCanvas from '../../../src/lib/canvas/NodeCanvas.svelte';

// jsdom has no layout; the canvas measures its box with bind:clientWidth
globalThis.ResizeObserver ??= class {
	observe() {}
	unobserve() {}
	disconnect() {}
} as unknown as typeof ResizeObserver;

const node = createRawSnippet(() => ({ render: () => '<g></g>' }));

function props(over: Record<string, unknown> = {}) {
	return {
		nodes: [{ id: 'a', x: 100, y: 40 }],
		wires: [],
		nodeW: 160,
		nodeH: 60,
		ports: () => [],
		wireEnd: () => ({ x: 0, y: 0 }),
		view: { x: 0, y: 0, zoom: 1 },
		minZoom: 0.25,
		maxZoom: 2,
		wheelZoom: (z: number) => z,
		bend: 0.5,
		keyStep: 10,
		label: 'Flow',
		nodeName: () => 'Send mail (action)',
		node,
		...over
	};
}

test('a selected node has Move buttons that move it one step per click', async () => {
	const onmovestart = vi.fn();
	const onmove = vi.fn();
	const onmoveend = vi.fn();
	render(NodeCanvas, props({ selectedNode: 'a', onmovestart, onmove, onmoveend }));
	const group = screen.getByRole('group', { name: 'Move Send mail (action)' });
	await userEvent.click(within(group).getByRole('button', { name: 'Move right' }));
	expect(onmovestart).toHaveBeenCalledWith('a');
	expect(onmove).toHaveBeenLastCalledWith('a', 110, 40);
	expect(onmoveend).toHaveBeenCalledWith('a');
	await userEvent.click(within(group).getByRole('button', { name: 'Move up' }));
	expect(onmove).toHaveBeenLastCalledWith('a', 100, 30);
	await userEvent.click(within(group).getByRole('button', { name: 'Move down' }));
	expect(onmove).toHaveBeenLastCalledWith('a', 100, 50);
	await userEvent.click(within(group).getByRole('button', { name: 'Move left' }));
	expect(onmove).toHaveBeenLastCalledWith('a', 90, 40);
});

test('no Move buttons without a selected node, or when nodes cannot move', () => {
	const { unmount } = render(NodeCanvas, props({ selectedNode: null, onmove: vi.fn() }));
	expect(screen.queryByRole('button', { name: 'Move right' })).toBeNull();
	unmount();
	render(NodeCanvas, props({ selectedNode: 'a' }));
	expect(screen.queryByRole('button', { name: 'Move right' })).toBeNull();
});
