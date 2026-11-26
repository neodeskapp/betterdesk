<script lang="ts">
	import SettingRow from '../../../../src/lib/ui/settings/SettingRow.svelte';
	import KeepCount from '../../../../src/lib/ui/settings/KeepCount.svelte';

	let { error = '' }: { error?: string } = $props();
	let keep = $state({ all: false, count: 0 });
</script>

<SettingRow id="r-keep" label="Keep update backups" help="Older ones are deleted after each update." group>
	{#snippet control(f)}
		<KeepCount
			id="keep"
			labelledby={f.labelledby}
			describedby={f.describedby}
			all={keep.all}
			count={keep.count}
			onchange={(v) => (keep = v)}
			allLabel="Keep all update backups"
			countLabel="Keep this many"
			{error}
		/>
	{/snippet}
</SettingRow>
