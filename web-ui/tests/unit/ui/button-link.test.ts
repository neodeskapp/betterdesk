/** A Button with href that is disabled must not navigate (WCAG 4.1.2) */
import { render, screen } from '@testing-library/svelte';
import { createRawSnippet } from 'svelte';
import { expect, test } from 'vitest';
import Button from '../../../src/lib/ui/Button.svelte';

const children = createRawSnippet(() => ({ render: () => '<span>Download</span>' }));

test('a disabled link button has no href and says it is disabled', () => {
	render(Button, { href: '/downloads/agent.msi', disabled: true, children });
	const link = screen.getByRole('link', { name: 'Download' });
	expect(link).not.toHaveAttribute('href');
	expect(link).toHaveAttribute('aria-disabled', 'true');
});

test('an enabled link button keeps its href', () => {
	render(Button, { href: '/downloads/agent.msi', children });
	expect(screen.getByRole('link', { name: 'Download' })).toHaveAttribute('href', '/downloads/agent.msi');
});
