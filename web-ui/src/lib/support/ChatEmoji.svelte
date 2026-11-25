<!--
  One emoji of the chat's set (EMOJI_SET and QUICK_REACTIONS in chat.ts)
  drawn from Twemoji, so reactions and the picker look the same on every
  system: static/img/emoji/<code points>.svg, the character as the image's
  text. A character outside the set stays text. Messages are never parsed:
  emoji people type stay text, and the wire carries the character.
-->
<script lang="ts" module>
	import { EMOJI_SET, QUICK_REACTIONS } from './chat.ts';

	const SHIPPED: ReadonlySet<string> = new Set<string>([...EMOJI_SET, ...QUICK_REACTIONS]);

	/** The Twemoji file of an emoji of the set: its code points in hex, without the emoji variation selector */
	export function emojiFile(emoji: string): string | null {
		if (!SHIPPED.has(emoji)) return null;
		const points = Array.from(emoji)
			.map((c) => c.codePointAt(0) ?? 0)
			.filter((p) => p !== 0xfe0f)
			.map((p) => p.toString(16));
		return `${points.join('-')}.svg`;
	}
</script>

<script lang="ts">
	import { asset } from '$app/paths';

	let { emoji }: { emoji: string } = $props();

	const file = $derived(emojiFile(emoji));
</script>

{#if file}<img
		class="chat-emoji"
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
</style>
