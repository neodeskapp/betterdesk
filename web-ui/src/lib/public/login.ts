/**
 * The sign-in page's rules, apart from the markup (public/js/login.js and
 * desktop-login.js): what the address asks for, when single sign-on starts
 * by itself, and which sentence a failed sign-in shows.
 */

import { ApiError, clientMessages } from '#lib/api/client.ts';
import type { OidcStatus } from '#lib/api/signin.ts';
import { has, t } from '#lib/i18n/i18n.svelte.ts';
import { safeReturnPath } from './returnUrl.ts';

export interface LoginQuery {
	/** Where to go afterwards (R3), already checked */
	returnPath: string | null;
	/** ?expired=1: the session ended (the server adds it only then, R4) */
	expired: boolean;
	/**
	 * ?error=: why a single sign-on did not sign the person in: an oidc_*
	 * code or username_collision (the name belongs to an account that signs
	 * in another way). Null for none or an unknown value.
	 */
	oidcError: string | null;
	/** ?error= with any value: a single sign-on just failed */
	ssoFailed: boolean;
	/** ?autoLaunch=0: show the password form although "Prefer SSO" is on */
	autoLaunch: boolean;
	/** ?desktop=1: the compact sign-in of the old desktop mode */
	desktop: boolean;
}

/** The codes besides oidc_* that the server's single sign-on sends back */
const SSO_CODES = ['username_collision'];

export function readLoginQuery(search: string): LoginQuery {
	const q = new URLSearchParams(search);
	const error = q.get('error');
	return {
		returnPath: safeReturnPath(q.get('return')),
		expired: q.get('expired') === '1' || q.get('expired') === 'true',
		oidcError: error && (/^oidc_[a-z_]{1,40}$/.test(error) || SSO_CODES.includes(error)) ? error : null,
		ssoFailed: error !== null,
		autoLaunch: q.get('autoLaunch') !== '0',
		desktop: q.get('desktop') === '1'
	};
}

const NOTICES = ['expired', 'error', 'reason'];

/** Whether the address carries one-time notices (expired, error, reason) */
export function hasNotices(href: string): boolean {
	const q = new URL(href).searchParams;
	return NOTICES.some((key) => q.has(key));
}

/** The address without the one-time notices, so a reload doesn't show them again */
export function withoutNotices(href: string): URL {
	const url = new URL(href);
	for (const key of NOTICES) url.searchParams.delete(key);
	return url;
}

/**
 * "Prefer SSO": go straight to the identity provider, unless the address
 * says ?autoLaunch=0, a single sign-on just failed (any ?error=, so a
 * refusal never sends the person round in a loop), the visitor is on the
 * local network (the server says so), or it is the compact desktop sign-in
 * (which never offered single sign-on).
 */
export function autoStartsSso(status: OidcStatus, query: LoginQuery): boolean {
	return (
		status.enabled &&
		status.prefer_sso &&
		!status.local_bypass &&
		query.autoLaunch &&
		!query.ssoFailed &&
		!query.desktop
	);
}

/**
 * The sentence for ?error=: its own when the strings have one, the general
 * one otherwise. A name taken by another account has a sentence of its own
 * for single sign-on (the password sign-in's speaks of the local password).
 */
export function oidcErrorText(code: string): string {
	const key = code === 'username_collision' ? 'auth.sso_username_collision' : `auth.${code}`;
	return has(key) ? t(key) : t('auth.oidc_error');
}

/**
 * Why a sign-in request failed, in the page's language: too many attempts
 * and an unreachable server get their own sentence; otherwise the server's
 * (translated) message, or `fallbackKey`.
 */
export function signInErrorText(err: unknown, fallbackKey: string): string {
	if (err instanceof ApiError) {
		if (err.status === 429) return t('auth.totp_rate_limited');
		if (err.status === 0) return clientMessages.network;
		if (err.status === 200) return t(fallbackKey);
		return err.message || t(fallbackKey);
	}
	return t(fallbackKey);
}

/** Digits only, six at most (the authenticator code field) */
export function codeDigits(value: string): string {
	return value.replace(/\D/g, '').slice(0, 6);
}
