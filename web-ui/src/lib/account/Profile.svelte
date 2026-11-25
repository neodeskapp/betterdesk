<!--
  Your account › Profile (#profile): the signed-in person's picture, name
  and role, and My status (online, away, busy), which the account menu
  sets too. Name and picture come from the account (Users, or the
  directory or single sign-on provider); this part shows them.
-->
<script lang="ts">
	import { has, t } from '#lib/i18n/i18n.svelte.ts';
	import { session } from '#lib/state/session.svelte.ts';
	import { myPresence, setPresence, PRESENCE_VALUES } from '#lib/state/presence.svelte.ts';
	import Avatar from '#lib/ui/Avatar.svelte';
	import ChoiceList from '#lib/ui/ChoiceList.svelte';

	const user = $derived(session.user);
	const person = $derived(
		user ? { id: user.id, username: user.username, avatar_version: user.avatarVersion } : null
	);
	const role = $derived.by(() => {
		const r = user?.role ?? '';
		if (!r) return '';
		const key = `users.role_${r}`;
		if (has(key)) return t(key);
		const words = r.replace(/_/g, ' ');
		return words.charAt(0).toUpperCase() + words.slice(1);
	});
	const choices = $derived(PRESENCE_VALUES.map((s) => ({ value: s, title: t(`chat.presence_${s}`) })));
</script>

<section class="section" id="profile" aria-labelledby="profile-title">
	<h2 id="profile-title">{t('account.profile')}</h2>
	<div class="person">
		<Avatar user={person} size="lg" presence={myPresence.status} />
		<div class="person-text">
			<span class="person-name">{user?.username ?? ''}</span>
			{#if role}<span class="person-role">{role}</span>{/if}
		</div>
	</div>
	<div class="status" id="profile-status">
		<h3 id="profile-status-title">{t('chat.my_presence')}</h3>
		<p class="form-hint" id="profile-status-note">{t('account.status_note')}</p>
		<ChoiceList
			{choices}
			name="profile-status"
			labelledby="profile-status-title"
			describedby="profile-status-note"
			value={myPresence.status}
			onchange={(v, checked) => {
				if (checked) setPresence(v);
			}}
		/>
	</div>
</section>

<style>
	.person {
		display: flex;
		align-items: center;
		gap: var(--space-3);
	}
	.person-text {
		display: flex;
		flex-direction: column;
		min-width: 0;
	}
	.person-name {
		overflow-wrap: anywhere;
		font-size: var(--text-md);
		font-weight: 600;
	}
	.person-role {
		color: var(--text-muted);
	}
	.status {
		display: flex;
		flex-direction: column;
		gap: var(--space-2);
		max-width: var(--measure);
		margin-top: var(--space-4);
	}
	h3 {
		margin: 0;
		font-size: var(--text-md);
	}
	.form-hint {
		margin: 0;
		color: var(--text-muted);
		font-size: var(--text-sm);
	}
</style>
