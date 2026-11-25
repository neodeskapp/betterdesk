<!--
  One emoji of the chat's set (emoji.ts) drawn from Twemoji, so reactions
  and the picker look the same on every system: static/img/emoji/<code
  points>.svg, with the character as the picture's text. A character
  outside the set stays text. Messages are never parsed: emoji people
  type stay text, and the wire carries the character.
-->
<script lang="ts">
	import { asset } from '$app/paths';
	import { emojiFile } from './emoji.ts';

	let { emoji, size = 'text' }: { emoji: string; size?: 'text' | 'large' } = $props();

	const file = $derived(emojiFile(emoji));
</script>

{#if file}<img
		class={['chat-emoji', size === 'large' && 'is-large']}
		src={asset(`img/emoji/${file}` as 'img/emoji/1f44d.svg')}
		alt={emoji}
		width="18"
		height="18"
		draggable="false"
	/>{:else}{emoji}{/if}

<style>
	.chat-emoji {
		display: inline-block;
		width: 1.15em;
		height: 1.15em;
		vertical-align: -0.2em;
	}
	.chat-emoji.is-large {
		width: 24px;
		height: 24px;
		vertical-align: middle;
	}
</style>
