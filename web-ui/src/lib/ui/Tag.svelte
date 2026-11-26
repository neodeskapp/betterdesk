<!--
  Tag (GUIDE section 10): a value from a set (category, source, type, role,
  event kind) or a tag people set. A quiet token: a faint tint of the text
  colour, 3 px corners, never a pill, never coloured per value. `icon`: an
  ICONS meaning, only where it tells values apart faster than the word.
  `title`: the full text in a tooltip on hover and focus (Tip.svelte); long
  labels (over 24 characters) get it by default, since the token cuts them
  with an ellipsis.
-->
<script lang="ts">
	import Icon from './Icon.svelte';
	import Tip from './Tip.svelte';

	let { label, icon, title }: { label: string; icon?: string; title?: string } = $props();
	const hover = $derived(title ?? (label.length > 24 ? label : undefined));
</script>

<span class="tag"
	>{#if icon}<Icon name={icon} />{/if}{#if hover}<Tip text={hover}><span class="tag-text">{label}</span></Tip
		>{:else}<span class="tag-text">{label}</span>{/if}</span
>

<style>
	.tag {
		display: inline-flex;
		align-items: center;
		gap: var(--space-1);
		max-width: 100%;
		min-height: 22px;
		padding: 0 6px;
		border-radius: var(--radius-sm);
		background: var(--tag-bg);
		color: var(--text);
		font-size: var(--text-sm);
		font-weight: 400;
		line-height: 1.3;
		white-space: nowrap;
		vertical-align: middle;
	}
	.tag > :global(.material-icons) {
		font-size: 15px;
		color: var(--text-muted);
	}
	/* The tooltip's anchor lets the text shrink, so the ellipsis still shows */
	.tag :global(.tip) {
		display: flex;
		min-width: 0;
	}
	.tag-text {
		min-width: 0;
		overflow: hidden;
		text-overflow: ellipsis;
		/* Text people type: direction marks in it stay inside the token */
		unicode-bidi: isolate;
	}
	@media (forced-colors: active) {
		.tag {
			border: 1px solid CanvasText;
		}
	}
</style>
