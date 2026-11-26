<!--
  The idle warning (session-guard.js): shortly before an idle sign-out, a
  notice at the top with "Stay signed in". Not modal, like the old one:
  any use of the page hides it, the button also tells the server.

  For keyboard and screen reader users (WCAG 2.2.1): focus moves to "Stay
  signed in" when it appears and back to where it was when it goes, it is
  announced right away, and the time left is said every 10 seconds. A
  screen reader in browse mode sends no keys to the page, so reading alone
  doesn't count as activity; this is how they still hear it in time.

  The second notice warns before a session's fixed end (under 20 hours
  after sign-in, session-guard.svelte.ts): that end can't be moved, so it
  says when it comes and to save the work. OK hides it.
-->
<script lang="ts">
	import { untrack } from 'svelte';
	import { formatDate } from '#lib/i18n/format.ts';
	import { t, tf } from '#lib/i18n/i18n.svelte.ts';
	import Button from '#lib/ui/Button.svelte';
	import Icon from '#lib/ui/Icon.svelte';
	import { announce } from '#lib/ui/announce.svelte.ts';
	import { dismissEnding, guard, staySignedIn } from './session-guard.svelte.ts';

	const uid = $props.id();
	let now = $state(Date.now());
	let stay = $state<HTMLElement | null>(null);
	let ok = $state<HTMLElement | null>(null);

	const left = $derived(
		guard.warning === null
			? 0
			: guard.deadline === null
				? guard.warning
				: Math.max(1, Math.ceil((guard.deadline - now) / 1000))
	);
	const body = $derived(
		guard.warning === null
			? ''
			: tf(
					'auth.session_idle_warning_body',
					'You will be signed out due to inactivity in {{seconds}} seconds.'
				).replace('{{seconds}}', String(left))
	);
	const endBody = $derived(
		guard.ending === null
			? ''
			: tf(
					'auth.session_end_warning_body',
					'Your session ends at {time}. Save your work now, then sign in again.',
					{ time: formatDate(guard.ending, { year: undefined, month: undefined, day: undefined }) }
				)
	);

	/** Focus goes to `target` while a notice shows, and back to where it was after */
	function takeFocus(target: HTMLElement | null, box: string): () => void {
		const back = document.activeElement instanceof HTMLElement ? document.activeElement : null;
		target?.focus();
		return () => {
			const active = document.activeElement;
			// Only when focus is still in the notice (or dropped to the page with it)
			if (!active || active === document.body || active.closest(box)) back?.focus();
		};
	}

	// Runs when the warning appears (not on each second of the countdown)
	$effect(() => {
		const button = stay;
		if (guard.warning === null || !button) return;
		const giveBack = takeFocus(button, '.idle-warning');
		now = Date.now();
		untrack(() => announce(`${t('auth.session_idle_warning_title')}. ${body}`, { assertive: true }));
		const timer = setInterval(() => {
			now = Date.now();
			const s = guard.deadline === null ? 0 : Math.ceil((guard.deadline - now) / 1000);
			if (s > 0 && s % 10 === 0)
				announce(tf('auth.session_seconds_left', '{seconds} seconds left', { seconds: s }));
		}, 1000);
		return () => {
			clearInterval(timer);
			// Svelte removes the notice after this; give focus back once it is gone
			queueMicrotask(giveBack);
		};
	});

	$effect(() => {
		const button = ok;
		if (guard.ending === null || !button) return;
		const giveBack = takeFocus(button, '.idle-warning');
		untrack(() =>
			announce(`${tf('auth.session_end_warning_title', 'Your session ends soon')}. ${endBody}`, {
				assertive: true
			})
		);
		return () => queueMicrotask(giveBack);
	});
</script>

{#if guard.warning !== null}
	<div
		class="idle-warning"
		role="alertdialog"
		aria-labelledby={`${uid}-title`}
		aria-describedby={`${uid}-body`}
	>
		<Icon name="timer" />
		<div class="idle-text">
			<strong id={`${uid}-title`}>{t('auth.session_idle_warning_title')}</strong>
			<p id={`${uid}-body`}>{body}</p>
		</div>
		<Button bind:element={stay} variant="primary" onclick={staySignedIn}
			>{t('auth.session_stay_signed_in')}</Button
		>
	</div>
{:else if guard.ending !== null}
	<div
		class="idle-warning"
		role="alertdialog"
		aria-labelledby={`${uid}-end-title`}
		aria-describedby={`${uid}-end-body`}
	>
		<Icon name="timer" />
		<div class="idle-text">
			<strong id={`${uid}-end-title`}>{tf('auth.session_end_warning_title', 'Your session ends soon')}</strong
			>
			<p id={`${uid}-end-body`}>{endBody}</p>
		</div>
		<Button bind:element={ok} variant="primary" onclick={dismissEnding}>{tf('common.ok', 'OK')}</Button>
	</div>
{/if}

<style>
	.idle-warning {
		position: fixed;
		top: var(--space-3);
		left: 50%;
		z-index: var(--layer-toast);
		display: flex;
		flex-wrap: wrap;
		align-items: center;
		gap: var(--space-3);
		box-sizing: border-box;
		width: max-content;
		max-width: calc(100vw - 2 * var(--space-4));
		padding: var(--space-3) var(--space-4);
		border: 1px solid var(--rule);
		border-inline-start: 3px solid var(--warning);
		border-radius: var(--radius);
		background: var(--bg-float);
		box-shadow: var(--shadow-float);
		color: var(--text);
		transform: translateX(-50%);
	}
	.idle-warning > :global(.material-icons) {
		color: var(--warning);
	}
	.idle-text {
		display: flex;
		flex: 1;
		flex-direction: column;
		min-width: 0;
	}
	.idle-text p {
		color: var(--text-muted);
		font-size: var(--text-sm);
	}
</style>
