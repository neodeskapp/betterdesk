<!--
  Accessibility panel: a side sheet with the six options of
  a11y.svelte.ts. Reduce motion and More contrast are radio groups that
  follow the system by default; the other four are checkboxes. Each change
  applies and is saved right away (for the account when signed in, else in
  this browser) and is announced. Mounted once in the root layout, so the
  account menu, Your account › Preferences and the sign-in page open the
  same panel (a11y.open).
-->
<script lang="ts">
	import { t } from '#lib/i18n/i18n.svelte.ts';
	import Button from '#lib/ui/Button.svelte';
	import Checkbox from '#lib/ui/Checkbox.svelte';
	import Radio from '#lib/ui/Radio.svelte';
	import Sheet from '#lib/ui/Sheet.svelte';
	import { a11y, setA11y, FOLLOW, type FlagKey, type Follow, type FollowKey } from './a11y.svelte.ts';

	let { open = $bindable(false) }: { open?: boolean } = $props();

	const uid = $props.id();

	const follows: { key: FollowKey; legend: string; hint: string }[] = [
		{ key: 'reduceMotion', legend: 'accessibility.reduce_motion', hint: 'accessibility.reduce_motion_hint' },
		{ key: 'moreContrast', legend: 'accessibility.more_contrast', hint: 'accessibility.more_contrast_hint' }
	];
	const CHOICE_LABEL: Record<Follow, string> = {
		system: 'accessibility.follow_system',
		on: 'accessibility.choice_on',
		off: 'accessibility.choice_off'
	};
	const checks: { key: FlagKey; label: string; hint: string }[] = [
		{
			key: 'underlineLinks',
			label: 'accessibility.underline_links',
			hint: 'accessibility.underline_links_hint'
		},
		{ key: 'readableFont', label: 'accessibility.readable_font', hint: 'accessibility.readable_font_hint' },
		{
			key: 'noSingleKeyShortcuts',
			label: 'accessibility.single_key_shortcuts',
			hint: 'accessibility.single_key_shortcuts_hint'
		},
		{
			key: 'pauseLiveUpdates',
			label: 'accessibility.pause_live_updates',
			hint: 'accessibility.pause_live_updates_hint'
		}
	];
</script>

{#if open}
	<Sheet
		bind:open
		title={t('accessibility.title')}
		subtitle={t(a11y.account ? 'accessibility.subtitle_account' : 'accessibility.subtitle_browser')}
		class="nd-a11y-panel"
	>
		{#each follows as f (f.key)}
			<fieldset class="a11y-group" aria-describedby={`${uid}-${f.key}-hint`}>
				<legend class="a11y-legend">{t(f.legend)}</legend>
				<p class="a11y-hint" id={`${uid}-${f.key}-hint`}>{t(f.hint)}</p>
				<div class="a11y-choices">
					{#each FOLLOW as choice (choice)}
						<Radio
							name={`${uid}-${f.key}`}
							value={choice}
							group={a11y.settings[f.key]}
							label={t(CHOICE_LABEL[choice])}
							onchange={() => void setA11y(f.key, choice)}
						/>
					{/each}
				</div>
			</fieldset>
		{/each}
		<div class="a11y-checks">
			{#each checks as c (c.key)}
				<Checkbox
					label={t(c.label)}
					hint={t(c.hint)}
					checked={a11y.settings[c.key]}
					onchange={(e) => void setA11y(c.key, (e.currentTarget as HTMLInputElement).checked)}
				/>
			{/each}
		</div>

		{#snippet footer()}
			<Button variant="primary" onclick={() => (open = false)}>{t('accessibility.done')}</Button>
		{/snippet}
	</Sheet>
{/if}

<style>
	/* Each question on its own, on rules; the sheet body pads the ends */
	.a11y-group {
		margin: 0;
		padding: 0 0 var(--space-5);
		border: 0;
		border-bottom: 1px solid var(--rule);
	}
	.a11y-group + .a11y-group {
		padding-top: var(--space-5);
	}
	.a11y-legend {
		padding: 0;
		color: var(--text);
		font-size: var(--text-md);
		font-weight: 600;
		line-height: 1.3;
	}
	.a11y-hint {
		margin: var(--space-1) 0 var(--space-3);
		color: var(--text-muted);
		font-size: var(--text-sm);
	}
	.a11y-choices {
		display: flex;
		flex-wrap: wrap;
		gap: var(--space-2) var(--space-5);
	}
	.a11y-checks {
		display: grid;
		gap: var(--space-4);
		padding-top: var(--space-5);
	}
</style>
