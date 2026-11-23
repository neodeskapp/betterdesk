<!--
  API tokens (/api-tokens): tokens that scripts, the neodesk command line
  and its MCP server sign in with. A token acts as the person who made it,
  with only the permissions they gave it, and is shown once. Admins make
  and revoke their own tokens; super admins can list and revoke every
  account's. Reached from the account menu.
-->
<script lang="ts">
	import { untrack } from 'svelte';
	import { listApiTokens, revokeApiToken, type ApiToken } from '#lib/api/apiTokens.ts';
	import { formatNumber } from '#lib/i18n/format.ts';
	import { t } from '#lib/i18n/i18n.svelte.ts';
	import {
		filterApiTokens,
		loginCommand,
		mayMakeTokens,
		stateTone,
		type ApiTokenFilter
	} from '#lib/people/apiTokens.ts';
	import { permLabel } from '#lib/people/permissions.ts';
	import NoAccess from '#lib/shell/NoAccess.svelte';
	import { onRefresh } from '#lib/shell/page.svelte.ts';
	import { session } from '#lib/state/session.svelte.ts';
	import Button from '#lib/ui/Button.svelte';
	import Checkbox from '#lib/ui/Checkbox.svelte';
	import CodeBlock from '#lib/ui/CodeBlock.svelte';
	import MenuButton from '#lib/ui/MenuButton.svelte';
	import MenuItem from '#lib/ui/MenuItem.svelte';
	import SearchField from '#lib/ui/SearchField.svelte';
	import SegmentedFilter from '#lib/ui/SegmentedFilter.svelte';
	import States from '#lib/ui/States.svelte';
	import Status from '#lib/ui/Status.svelte';
	import Steps from '#lib/ui/Steps.svelte';
	import Table from '#lib/ui/Table.svelte';
	import Time from '#lib/ui/Time.svelte';
	import Toolbar from '#lib/ui/Toolbar.svelte';
	import { confirm } from '#lib/ui/dialogs.svelte.ts';
	import type { Column } from '#lib/ui/table.ts';
	import { toast } from '#lib/ui/toasts.svelte.ts';
	import { ICONS } from '#lib/util/display.ts';
	import CreateDialog from './CreateDialog.svelte';
	import ShownOnceDialog from './ShownOnceDialog.svelte';

	const allowed = $derived(mayMakeTokens(session.user?.role));
	const superAdmin = $derived(session.isSuperAdmin);
	const DATE_ONLY: Intl.DateTimeFormatOptions = { hour: undefined, minute: undefined };

	let tokens = $state.raw<ApiToken[]>([]);
	let loaded = $state(false);
	let loadError = $state<string | null>(null);
	let everyone = $state(false);

	async function load(): Promise<void> {
		if (!allowed) return;
		try {
			tokens = await listApiTokens(everyone);
			loadError = null;
		} catch (err) {
			tokens = [];
			loadError = `${t('api_tokens.load_failed')} ${err instanceof Error ? err.message : ''}`.trim();
		} finally {
			loaded = true;
		}
	}

	$effect(() => {
		void everyone;
		untrack(() => void load());
	});
	$effect(() => onRefresh(() => void load()));

	let filter = $state<ApiTokenFilter>('active');
	let query = $state('');
	const shown = $derived(filterApiTokens(tokens, filter, query));
	const activeCount = $derived(tokens.filter((tok) => tok.state === 'active').length);
	const filterOptions = $derived([
		{ value: 'active' as const, label: t('api_tokens.filter_active') },
		{ value: 'all' as const, label: t('common.all') }
	]);

	let creating = $state(false);
	let made = $state<{ token: string; name: string } | null>(null);

	async function revoke(tok: ApiToken) {
		const ok = await confirm({
			title: t('api_tokens.revoke_title'),
			message: t('api_tokens.revoke_confirm', { name: tok.name }),
			confirmLabel: t('api_tokens.revoke'),
			confirmIcon: ICONS.revoke,
			danger: true
		});
		if (!ok) return;
		try {
			await revokeApiToken(tok.id);
			toast.success(t('api_tokens.revoked', { name: tok.name }));
			void load();
		} catch (err) {
			toast.error(`${t('api_tokens.revoke_failed')} ${err instanceof Error ? err.message : ''}`.trim());
		}
	}

	let menuFor = $state<string | null>(null);
	const origin = typeof location === 'undefined' ? '' : location.origin;

	const columns = $derived<Column<ApiToken>[]>([
		{ id: 'name', label: t('api_tokens.name'), cell: nameCell, class: 'col-name' },
		{ id: 'access', label: t('api_tokens.access'), cell: accessCell },
		{ id: 'permissions', label: t('api_tokens.permissions'), cell: permissionsCell, class: 'col-perms' },
		...(everyone ? [{ id: 'owner', label: t('api_tokens.owner'), cell: ownerCell }] : []),
		{ id: 'expires', label: t('api_tokens.expires'), cell: expiresCell },
		{ id: 'used', label: t('api_tokens.last_used'), cell: usedCell },
		{ id: 'state', label: t('api_tokens.status'), cell: stateCell },
		{ id: 'actions', label: t('common.actions'), labelHidden: true, cell: actionsCell, align: 'actions' }
	]);
</script>

{#snippet nameCell(tok: ApiToken)}<span class="token-name">{tok.name}</span><span class="cell-sub mono"
		>{tok.hint}</span
	>{/snippet}

{#snippet accessCell(tok: ApiToken)}{tok.read_only
		? t('api_tokens.read_only')
		: t('api_tokens.read_write')}{#if tok.allow_list.length}<span class="cell-sub"
			>{t('api_tokens.from_addresses', { list: tok.allow_list.join(', ') })}</span
		>{/if}{/snippet}

{#snippet permissionsCell(tok: ApiToken)}<span title={tok.permissions.map((p) => permLabel(p, t)).join('\n')}
		>{t('api_tokens.permission_count', { count: formatNumber(tok.permissions.length) })}</span
	>{/snippet}

{#snippet ownerCell(tok: ApiToken)}{tok.username || t('api_tokens.owner_gone')}{/snippet}

{#snippet expiresCell(tok: ApiToken)}<Time
		value={tok.expires_at}
		relative={false}
		format={DATE_ONLY}
	/>{/snippet}

{#snippet usedCell(tok: ApiToken)}<Time
		value={tok.last_used_at}
		empty={t('api_tokens.never_used')}
	/>{#if tok.last_used_ip}<span class="cell-sub mono">{tok.last_used_ip}</span>{/if}{/snippet}

{#snippet stateCell(tok: ApiToken)}<Status
		tone={stateTone(tok.state)}
		label={t(`api_tokens.state_${tok.state}`)}
	/>{/snippet}

{#snippet actionsCell(tok: ApiToken)}{#if tok.state === 'active'}<MenuButton
			kebab
			label={t('api_tokens.actions_for', { name: tok.name })}
			bind:open={
				() => menuFor === tok.id, (v) => (menuFor = v ? tok.id : menuFor === tok.id ? null : menuFor)
			}
		>
			<MenuItem class="act-revoke" icon={ICONS.revoke} danger onselect={() => void revoke(tok)}
				>{t('api_tokens.revoke')}</MenuItem
			>
		</MenuButton>{/if}{/snippet}

{#if !allowed}
	<NoAccess />
{:else}
	<div class="page page-wide api-tokens-page">
		<div class="page-head">
			<h1>{t('api_tokens.title')}</h1>
			<p class="page-description">{t('api_tokens.page_description')}</p>
			<div class="page-actions">
				<Button variant="primary" icon={ICONS.add} class="act-create" onclick={() => (creating = true)}
					>{t('api_tokens.create')}</Button
				>
			</div>
		</div>

		{#if !loaded}
			<States kind="loading" message={t('api_tokens.loading')} />
		{:else}
			<div class="page-cols">
				<div class="page-main">
					<section class="section" aria-labelledby="api-tokens-list-title">
						<div class="section-head"><h2 id="api-tokens-list-title">{t('api_tokens.list_title')}</h2></div>
						<Toolbar>
							{#snippet search()}<SearchField label={t('api_tokens.search')} bind:value={query} />{/snippet}
							<SegmentedFilter label={t('api_tokens.status')} options={filterOptions} bind:value={filter} />
							{#if superAdmin}
								<Checkbox inline label={t('api_tokens.everyone')} bind:checked={everyone} />
							{/if}
						</Toolbar>
						{#if !loadError && tokens.length}
							<p class="count-line" aria-live="polite">
								{t('api_tokens.count', { count: formatNumber(activeCount) })}
							</p>
						{/if}
						<div class="api-tokens-table">
							<Table
								rows={shown}
								{columns}
								key={(tok) => tok.id}
								label={t('api_tokens.list_title')}
								error={loadError}
								onretry={() => void load()}
								emptyText={tokens.length ? t('api_tokens.no_match') : t('api_tokens.none')}
							/>
						</div>
					</section>
				</div>
				<aside class="page-side" aria-label={t('api_tokens.cli_title')}>
					<section class="section" aria-labelledby="api-tokens-cli-title">
						<div class="section-head"><h2 id="api-tokens-cli-title">{t('api_tokens.cli_title')}</h2></div>
						<Steps
							items={[t('api_tokens.cli_step1'), t('api_tokens.cli_step2'), t('api_tokens.cli_step3')]}
						/>
						<CodeBlock title={t('api_tokens.cli_command')} code={loginCommand(origin)} />
					</section>
					<section class="section" aria-labelledby="api-tokens-rules-title">
						<div class="section-head"><h2 id="api-tokens-rules-title">{t('api_tokens.rules_title')}</h2></div>
						<ul class="rules">
							<li>{t('api_tokens.rule_permissions')}</li>
							<li>{t('api_tokens.rule_lose')}</li>
							<li>{t('api_tokens.rule_accounts')}</li>
							<li>{t('api_tokens.rule_once')}</li>
						</ul>
					</section>
				</aside>
			</div>
		{/if}
	</div>
{/if}

{#if creating}
	<CreateDialog
		onclose={() => (creating = false)}
		oncreated={(token, name) => {
			creating = false;
			made = { token, name };
			void load();
		}}
	/>
{/if}

{#if made}
	<ShownOnceDialog token={made.token} name={made.name} {origin} onclose={() => (made = null)} />
{/if}

<style>
	.count-line {
		margin: 0;
		color: var(--text-muted);
	}
	.token-name {
		font-weight: 600;
	}
	.cell-sub {
		display: block;
		color: var(--text-muted);
		font-size: var(--text-sm);
	}
	.rules {
		margin: 0;
		padding-left: var(--space-5);
		max-width: var(--measure);
		display: grid;
		gap: var(--space-2);
	}
	.api-tokens-table :global(td.col-name) {
		min-width: 18ch;
	}
	.api-tokens-table :global(.time) {
		white-space: nowrap;
	}
</style>
