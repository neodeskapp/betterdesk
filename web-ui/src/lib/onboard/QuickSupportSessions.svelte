<!--
  The account's open quick support sessions (Help requests page): who, the
  state, Open (the Quick support dialog on that session, with Connect) and
  End. Asked every 5 s while the tab is visible; nothing shows when there
  are none.
-->
<script lang="ts">
	import { endSession, listSessions, type SupportSession } from '#lib/api/onboard.ts';
	import { formatDate } from '#lib/i18n/format.ts';
	import { t } from '#lib/i18n/i18n.svelte.ts';
	import { poll } from '#lib/state/live.ts';
	import Button from '#lib/ui/Button.svelte';
	import ListRow from '#lib/ui/ListRow.svelte';
	import RowList from '#lib/ui/RowList.svelte';
	import Status from '#lib/ui/Status.svelte';
	import { toast } from '#lib/ui/toasts.svelte.ts';
	import { openQuickSupport, quickSupport } from './open.svelte.ts';
	import { stateKey, stateTone } from './quick-support.ts';

	let sessions = $state.raw<SupportSession[]>([]);
	let ending = $state('');

	async function load() {
		sessions = await listSessions();
	}

	$effect(() => {
		// A session made or ended in the dialog shows here when it closes
		void quickSupport.open;
		return poll(load, { every: 5000, immediate: true });
	});

	async function stop(s: SupportSession) {
		ending = s.id;
		try {
			await endSession(s.id);
			sessions = sessions.filter((x) => x.id !== s.id);
		} catch (err) {
			toast.error(err instanceof Error ? err.message : t('onboard.support.end_failed'));
		} finally {
			ending = '';
		}
	}
</script>

{#if sessions.length}
	<section class="qs-sessions" aria-labelledby="qs-sessions-title">
		<h2 id="qs-sessions-title">{t('onboard.support.open_title')}</h2>
		<RowList label={t('onboard.support.open_title')}>
			{#each sessions as s (s.id)}
				<ListRow
					title={s.name || s.hostname || t('onboard.support.the_person')}
					sub={s.device_id || undefined}
				>
					{#snippet end()}
						<Status
							tone={stateTone(s.status)}
							label={t(stateKey(s), {
								time: formatDate(s.expires_at, { year: undefined, month: undefined, day: undefined }),
								computer: s.hostname || s.device_id
							})}
						/>
						<Button size="sm" onclick={() => openQuickSupport(s)}>{t('onboard.support.open')}</Button>
						<Button size="sm" variant="danger-quiet" busy={ending === s.id} onclick={() => void stop(s)}
							>{t('onboard.support.end')}</Button
						>
					{/snippet}
				</ListRow>
			{/each}
		</RowList>
	</section>
{/if}

<style>
	.qs-sessions {
		display: flex;
		flex-direction: column;
		gap: var(--space-2);
	}
	.qs-sessions h2 {
		margin: 0;
	}
</style>
