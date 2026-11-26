<!--
  Your account › Profile (#profile): the signed-in person's picture, name
  and role, and My status (online, away, busy), which the account menu
  sets too. The picture comes from single sign-on, or the person uploads
  one here (it then replaces the single sign-on picture) and can remove
  it. Name and role come from the account (Users, or the directory or
  single sign-on provider).
-->
<script lang="ts">
	import { has, t } from '#lib/i18n/i18n.svelte.ts';
	import { ApiError } from '#lib/api/client.ts';
	import { MAX_AVATAR_BYTES, removeMyAvatar, uploadMyAvatar } from '#lib/api/users.ts';
	import { session } from '#lib/state/session.svelte.ts';
	import { setDirectoryVersion } from '#lib/state/avatars.svelte.ts';
	import { myPresence, setPresence, PRESENCE_VALUES } from '#lib/state/presence.svelte.ts';
	import { shrinkPicture } from '#lib/util/picture.ts';
	import Avatar from '#lib/ui/Avatar.svelte';
	import Button from '#lib/ui/Button.svelte';
	import ChoiceList from '#lib/ui/ChoiceList.svelte';
	import FormStatus from '#lib/ui/FormStatus.svelte';

	const user = $derived(session.user);
	const person = $derived(
		user
			? {
					id: user.id,
					username: user.username,
					display_name: user.displayName,
					avatar_version: user.avatarVersion ?? null
				}
			: null
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

	let input = $state<HTMLInputElement | null>(null);
	let busy = $state<'' | 'upload' | 'remove'>('');
	let status = $state<{ message: string; tone: 'ok' | 'error' | 'neutral' }>({
		message: '',
		tone: 'neutral'
	});

	function setVersion(version: string | null) {
		if (!session.user) return;
		session.user = { ...session.user, avatarVersion: version };
		setDirectoryVersion(session.user.id, version);
	}

	function refusal(err: unknown): string {
		if (err instanceof ApiError) {
			if (err.code === 'too_large') return t('account.picture_too_large');
			if (err.code === 'not_an_image' || err.code === 'type_mismatch' || err.code === 'empty')
				return t('account.picture_not_image');
			if (err.isPermission || err.isNetwork) return err.message;
		}
		return t('account.picture_failed');
	}

	async function chosen() {
		const file = input?.files?.[0];
		if (!file || busy) return;
		busy = 'upload';
		status = { message: '', tone: 'neutral' };
		try {
			const picture = await shrinkPicture(file, MAX_AVATAR_BYTES);
			setVersion(await uploadMyAvatar(picture));
			status = { message: t('account.picture_saved'), tone: 'ok' };
		} catch (err) {
			status = { message: refusal(err), tone: 'error' };
		} finally {
			busy = '';
			if (input) input.value = '';
		}
	}

	async function remove() {
		if (busy) return;
		busy = 'remove';
		status = { message: '', tone: 'neutral' };
		try {
			await removeMyAvatar();
			setVersion(null);
			status = { message: t('account.picture_removed'), tone: 'ok' };
		} catch (err) {
			status = { message: refusal(err), tone: 'error' };
		} finally {
			busy = '';
		}
	}
</script>

<section class="section" id="profile" aria-labelledby="profile-title">
	<h2 id="profile-title">{t('account.profile')}</h2>
	<div class="person">
		<Avatar user={person} size="lg" presence={myPresence.status} />
		<div class="person-text">
			<span class="person-name">{user?.displayName || user?.username || ''}</span>
			{#if user?.displayName && user.displayName !== user.username}<span class="person-role"
					>{user.username}</span
				>{/if}
			{#if role}<span class="person-role">{role}</span>{/if}
		</div>
	</div>
	<div class="picture" id="profile-picture">
		<div class="picture-actions">
			<input
				bind:this={input}
				class="picture-input"
				type="file"
				accept="image/png,image/jpeg,image/webp,image/gif"
				tabindex="-1"
				aria-hidden="true"
				onchange={chosen}
			/>
			<Button
				size="sm"
				icon="upload"
				busy={busy === 'upload'}
				disabled={!!busy && busy !== 'upload'}
				aria-describedby="profile-picture-note"
				onclick={() => input?.click()}
				>{user?.avatarVersion ? t('account.picture_change') : t('account.picture_upload')}</Button
			>
			{#if user?.avatarVersion}
				<Button
					size="sm"
					variant="ghost"
					busy={busy === 'remove'}
					disabled={!!busy && busy !== 'remove'}
					onclick={remove}>{t('account.picture_remove')}</Button
				>
			{/if}
		</div>
		<p class="form-hint" id="profile-picture-note">{t('account.picture_note')}</p>
		<FormStatus message={status.message} tone={status.tone} />
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
	.picture {
		display: flex;
		flex-direction: column;
		gap: var(--space-2);
		max-width: var(--measure);
		margin-top: var(--space-3);
	}
	.picture-actions {
		display: flex;
		flex-wrap: wrap;
		gap: var(--space-2);
	}
	.picture-input {
		display: none;
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
