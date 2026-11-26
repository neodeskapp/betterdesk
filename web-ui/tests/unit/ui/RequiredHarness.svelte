<!-- Test scene for field-required.test.ts. Not shipped. -->
<script lang="ts">
	import Field from '../../../src/lib/ui/Field.svelte';
	import Input from '../../../src/lib/ui/Input.svelte';

	let { mode }: { mode: 'prop' | 'attribute' | 'none' } = $props();
</script>

{#if mode === 'prop'}
	<Field label="Name" required>
		{#snippet children(f)}<Input {...f} />{/snippet}
	</Field>
{:else if mode === 'attribute'}
	<Field label="Email">
		{#snippet children(f)}<Input {...f} type="email" required />{/snippet}
	</Field>
{:else}
	<Field label="Note">
		{#snippet children(f)}<Input {...f} />{/snippet}
	</Field>
{/if}
