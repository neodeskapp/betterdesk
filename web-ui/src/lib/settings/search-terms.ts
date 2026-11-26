/**
 * Other words people type for a setting, in English and German, whatever
 * language the console shows. The search looks at them next to the
 * setting's label, section and hints in the console language
 * (search-index.ts). They are never shown, so they need no translation;
 * other languages find settings by their translated labels.
 *
 * Keyed by tab (words for every setting on it) and by element id (words
 * for that setting). A test checks that every key is a tab or an id of the
 * index.
 */

import { PERMISSION_GROUPS, permRowId } from '#lib/people/permissions.ts';

export const TAB_TERMS: Readonly<Record<string, string>> = {
	account: 'account profile my own konto profil mein eigenes',
	general: 'server allgemein',
	branding: 'appearance look theme colours design aussehen erscheinungsbild darstellung farben',
	brand: 'branding brand white label logo name company firma marke sign-in page download',
	auth: 'sign-in login security anmeldung sicherheit',
	ldap: 'ldap active directory ad directory verzeichnis anmeldung',
	sso: 'sso oidc oauth openid single sign-on azure okta google anmeldung',
	joining: 'join enrollment enroll registration approve beitritt registrierung freigabe',
	'server-backups': 'server backup sicherung datensicherung',
	'server-updates': 'server update upgrade version aktualisierung',
	email: 'email e-mail mail smtp',
	time: 'time zeit uhr clock',
	meshcentral: 'mesh meshcentral meshagent',
	'server-config-files': 'server advanced erweitert expert config konfiguration dateien',
	policies: 'policy policies organization richtlinie richtlinien organisation',
	permissions: 'role roles permission rights access rolle rollen berechtigung rechte zugriff'
};

/** A permission is also found by its key (device.delete) */
const PERMISSION_KEYS: Readonly<Record<string, string>> = Object.fromEntries(
	PERMISSION_GROUPS.flatMap((g) => g.permissions.map((p) => [permRowId(p), p]))
);

export const TERMS: Readonly<Record<string, string>> = {
	'pol-features-allow-audio': 'audio sound ton',
	'pol-connection-mode': 'consent approval accept zustimmung freigabe annehmen',
	'pol-features-allow-clipboard': 'clipboard zwischenablage',
	...PERMISSION_KEYS,
	// General
	'public-endpoints': 'address domain hostname adresse domäne host',
	'public-server-id': 'hbbs id server rendezvous address adresse',
	'public-relay-server': 'hbbr relay address adresse',
	'public-api-url': 'api url address adresse',
	'connection-mode-section': 'p2p direct relay connection verbindung direkt',
	'conn-mode-p2p': 'p2p peer to peer direkt',
	'conn-mode-relay': 'force relay always immer erzwingen',
	'conn-p2p-fallback-ms': 'timeout fallback delay wartezeit verzögerung',
	'conn-same-nat-relay': 'nat lan same network gleiches netz',
	'conn-allow-shared-nat-initiator': 'nat lan without login ohne anmeldung',
	'conn-logged-in-only-initiator': 'hardening login required härtung anmeldung pflicht',
	'conn-operator-only-outbound': 'hardening operator härtung',
	'device-scope-settings-panel': 'visibility who sees devices sichtbarkeit wer sieht geräte',
	'availability-history-settings-panel': 'uptime online history verlauf erreichbarkeit',
	'availability-history-enabled': 'uptime online history verlauf erreichbarkeit',
	'availability-history-retention': 'retention keep days aufbewahrung tage',
	'mesh-settings-panel': 'meshcentral meshagent mesh',
	'mesh-groups-form': 'meshcentral mesh groups gruppen',
	'mesh-msh-name': 'msh meshagent',
	'mesh-recordings': 'recording aufzeichnung',
	'server-info': 'version about info über',
	'go-server-version': 'version',
	'db-path': 'database sqlite datenbank',
	uptime: 'uptime laufzeit',
	// Appearance
	'branding-theme': 'theme dark light mode dunkel hell modus farbschema',
	'theme-mode-selector': 'dark light mode dunkel hell modus system',
	'branding-theme-select': 'theme preset vorlage',
	'branding-custom-colors': 'colors colours palette farben',
	'color-accentBlue': 'primary brand colour color hauptfarbe markenfarbe',
	'branding-identity': 'company firm name logo firma name',
	'brand-name': 'company title product name firmenname titel',
	'logo-text-input': 'logo wordmark schriftzug',
	'logo-image-file': 'logo upload png svg hochladen',
	'logo-image-url': 'logo url link',
	'branding-fonts': 'font typeface typography schrift schriftart',
	'font-heading-search': 'font typeface schriftart',
	'font-body-search': 'font typeface schriftart',
	'branding-footer': 'footer imprint impressum',
	'footer-text': 'imprint copyright impressum',
	'show-powered': 'powered by attribution hinweis',
	'branding-background': 'wallpaper background image hintergrundbild',
	'bg-image-file': 'wallpaper hintergrundbild',
	'bg-blur': 'blur unschärfe',
	'branding-login': 'login page sign in screen anmeldeseite loginseite',
	'login-title': 'login heading welcome willkommen',
	'login-slides-panel': 'slideshow carousel diashow',
	'login-bg-config': 'login wallpaper hintergrundbild',
	'branding-agent': 'download page agent installer client download herunterladen',
	'branding-rdclient': 'web viewer browser remote fernsteuerung',
	'custom-css': 'css stylesheet style',
	'branding-profiles': 'profile preset vorlage profil',
	'branding-import-export': 'import export json',
	// Your account
	'profile-picture': 'profile picture photo avatar image profilbild foto bild',
	'profile-status': 'status presence online away busy anwesenheit abwesend beschäftigt',
	password: 'password change passwort kennwort ändern',
	'current-password': 'password passwort kennwort',
	'new-password': 'change password passwort ändern kennwort',
	'confirm-password': 'password repeat passwort wiederholen',
	'two-factor':
		'2fa mfa totp authenticator otp two factor zwei faktor recovery codes wiederherstellungscodes',
	sessions: 'sessions signed in devices browsers sign out sitzungen angemeldete geräte browser',
	'preferences-theme': 'theme dark light mode dunkel hell modus design',
	'preferences-language': 'language sprache',
	'preferences-accessibility':
		'accessibility a11y contrast text size motion barrierefreiheit kontrast schriftgröße',
	// Sign-in
	'require-two-factor': '2fa mfa enforce force pflicht erzwingen',
	'enrollment-settings-section':
		'enrollment registration approve join new devices registrierung freigabe beitreten',
	'enrollment-rich-approve': 'approve approval freigabe genehmigung',
	'enrollment-tag-picker': 'tags labels schlagwörter',
	'client-sessions-form': 'client token session expiry timeout ablauf sitzung',
	'client-sessions-expiry-days': 'expiry timeout ablauf',
	'client-sessions-sliding': 'sliding refresh verlängern',
	'client-sessions-max-days': 'expiry maximum lifetime ablauf',
	'ldap-form': 'ldap active directory ad directory verzeichnis',
	'ldap-enabled': 'ldap active directory ad',
	'ldap-bind-password': 'password passwort',
	'oidc-form': 'sso single sign-on oidc oauth openid keycloak entra azure google okta authentik',
	'oidc-enabled': 'sso single sign-on oidc oauth',
	'oidc-client-secret': 'secret geheimnis',
	'oidc-redirect-url': 'callback redirect weiterleitung',
	// Backup
	'backup-create': 'download backup now export herunterladen sichern',
	'scheduled-backup-panel': 'schedule automatic cron zeitplan automatisch',
	'scheduled-backup-enabled': 'schedule automatic zeitplan automatisch',
	'scheduled-backup-time': 'night nightly daily time clock hour nachts nächtlich täglich uhrzeit',
	'scheduled-backup-retention': 'retention keep copies aufbewahrung behalten kopien',
	'scheduled-backup-destination-type': 'smb nas share network folder netzlaufwerk freigabe',
	'scheduled-backup-path': 'folder path ordner',
	'backup-restore': 'restore import wiederherstellen zurückspielen',
	// Updates
	'update-backups-panel': 'backup before update sicherung vorher',
	'update-channel-panel': 'beta nightly stable channel kanal',
	'backup-retention-input': 'retention keep backups aufbewahrung sicherungen',
	'backup-danger': 'prune delete old update backups aufräumen löschen',
	'update-local-version': 'version installed installiert',
	// Email
	'email-smtp-host': 'smtp mail server host postausgang',
	'email-smtp-secure': 'tls ssl starttls encryption verschlüsselung',
	'email-smtp-user': 'smtp login user benutzer',
	'email-smtp-pass': 'smtp password passwort kennwort',
	'email-smtp-from': 'sender from absender',
	'email-smtp-alert': 'alerts notifications warnungen benachrichtigungen',
	// Time
	'time-billing-timezone': 'timezone tz zeitzone',
	'time-clock-ntp-servers': 'ntp clock sync zeitsynchronisation',
	'time-clock-require-sync': 'billing abrechnung',
	'time-clock-max-skew': 'skew drift abweichung',
	// Advanced
	'advanced-config': 'restart reboot neustart neu starten'
};
