<!--
  The items of an account's menu (user-actions.ts) inside a MenuButton:
  one MenuGroup per group, the actions that end something last. The users
  list's row menu uses it; the user's page shows the same groups as
  buttons (UserActionButtons).
-->
<script lang="ts">
	import { t, tf } from '#lib/i18n/i18n.svelte.ts';
	import MenuGroup from '#lib/ui/MenuGroup.svelte';
	import MenuItem from '#lib/ui/MenuItem.svelte';
	import { actionLabel, type UserActionGroup, type UserActionId } from './user-actions.ts';

	interface Props {
		groups: readonly UserActionGroup[];
		onselect: (id: UserActionId) => void;
	}

	let { groups, onselect }: Props = $props();
</script>

{#each groups as g (g.id)}
	<MenuGroup class={g.cls} label={t(g.label)}>
		{#each g.actions as a (a.id)}
			<MenuItem class={a.cls} icon={a.icon} danger={a.danger} onselect={() => onselect(a.id)}
				>{actionLabel(a, t, tf)}</MenuItem
			>
		{/each}
	</MenuGroup>
{/each}
