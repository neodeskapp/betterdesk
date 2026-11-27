/**
 * The one list of settings on the Settings page: every setting with the
 * element that shows it. The search on the Settings page and the search in
 * the top bar (Ctrl K) both read it; other words for a setting are in
 * search-terms.ts. The tabs are written by hand, so the list is kept true
 * by tests: e2e/settings-index.spec.ts opens every tab and fails when a
 * row's element is gone or a field on the page has no row, and
 * tests/unit/settings/search-index.test.ts checks the keys and words.
 * Add a row here when a tab gains a setting.
 *
 * Each entry: tab, element id it jumps to, label key, section key, up to two
 * hint keys (searched too), and the permission a person needs to see it
 * ('' for everyone who may open the tab, 'super_admin' for Roles and
 * permissions). The rows of Roles and permissions come from its groups
 * (lib/people/permissions.ts): one per permission, with its name, group
 * and sentence.
 */

import {
	PERMISSION_GROUPS,
	groupTitleKey,
	permHelpKey,
	permLabelKey,
	permRowId
} from '#lib/people/permissions.ts';

export type IndexRow = readonly [
	tab: string,
	id: string,
	label: string,
	section: string,
	hints: readonly string[],
	need: '' | 'server.config' | 'branding.edit' | 'super_admin'
];

/** Roles and permissions: the role picker, the views and one row per permission */
const PERMISSION_ROWS: readonly IndexRow[] = [
	['permissions', 'perm-role', 'permissions.pick_role', '', ['permissions.page_description'], 'super_admin'],
	[
		'permissions',
		'perm-views',
		'permissions.views_title',
		'',
		['permissions.diff_only', 'permissions.compare_label'],
		'super_admin'
	],
	...PERMISSION_GROUPS.flatMap((g) =>
		g.permissions.map((p): IndexRow => [
			'permissions',
			permRowId(p),
			permLabelKey(p),
			groupTitleKey(g.id),
			[permHelpKey(p)],
			'super_admin'
		])
	)
];

export const SEARCH_INDEX: readonly IndexRow[] = [
	[
		'general',
		'public-endpoints',
		'settings.public_endpoints_title',
		'',
		['settings.public_endpoints_desc', 'settings.general.endpoint_id_hint'],
		'server.config'
	],
	[
		'general',
		'public-server-id',
		'settings.public_endpoints_server_id',
		'settings.public_endpoints_title',
		['settings.general.endpoint_id_hint'],
		'server.config'
	],
	[
		'general',
		'public-relay-server',
		'settings.public_endpoints_relay',
		'settings.public_endpoints_title',
		['settings.general.endpoint_relay_hint'],
		'server.config'
	],
	[
		'general',
		'public-api-url',
		'settings.public_endpoints_api_url',
		'settings.public_endpoints_title',
		['settings.general.endpoint_api_hint'],
		'server.config'
	],
	[
		'general',
		'connection-mode-section',
		'settings.connection_mode_title',
		'',
		['settings.connection_mode_desc', 'settings.general.conn_p2p_desc'],
		'server.config'
	],
	[
		'general',
		'connection-mode',
		'settings.connection_mode_label',
		'settings.connection_mode_title',
		['settings.general.conn_mode_help', 'settings.connection_mode_desc'],
		'server.config'
	],
	[
		'general',
		'conn-mode-p2p',
		'settings.general.conn_p2p',
		'settings.connection_mode_title',
		['settings.general.conn_p2p_desc'],
		'server.config'
	],
	[
		'general',
		'conn-mode-relay',
		'settings.general.conn_relay',
		'settings.connection_mode_title',
		['settings.general.conn_relay_desc'],
		'server.config'
	],
	[
		'general',
		'conn-p2p-fallback-ms',
		'settings.general.conn_fallback_label',
		'settings.connection_mode_title',
		['settings.general.conn_fallback_hint'],
		'server.config'
	],
	[
		'general',
		'conn-same-nat-relay',
		'settings.general.conn_same_nat',
		'settings.connection_mode_title',
		['settings.general.conn_same_nat_hint'],
		'server.config'
	],
	[
		'general',
		'conn-allow-shared-nat-initiator',
		'settings.general.conn_shared_nat',
		'settings.connection_mode_title',
		['settings.general.conn_shared_nat_hint'],
		'server.config'
	],
	[
		'general',
		'conn-logged-in-only-initiator',
		'settings.general.conn_logged_in',
		'settings.connection_mode_title',
		['settings.general.conn_logged_in_hint'],
		'server.config'
	],
	[
		'general',
		'conn-operator-only-outbound',
		'settings.general.conn_operator_only',
		'settings.connection_mode_title',
		['settings.general.conn_operator_only_hint'],
		'server.config'
	],
	[
		'joining',
		'device-scope-settings-panel',
		'settings.device_scope_title',
		'',
		['settings.general.scope_note', 'settings.general.scope_open_desc'],
		'server.config'
	],
	[
		'joining',
		'device-scope',
		'settings.joining.scope_label',
		'settings.device_scope_title',
		['settings.joining.scope_help', 'settings.general.scope_note'],
		'server.config'
	],
	[
		'joining',
		'device-scope-open',
		'settings.general.scope_open',
		'settings.device_scope_title',
		['settings.general.scope_open_desc'],
		'server.config'
	],
	[
		'joining',
		'device-scope-restricted',
		'settings.general.scope_restricted',
		'settings.device_scope_title',
		['settings.general.scope_restricted_desc'],
		'server.config'
	],
	[
		'joining',
		'availability-history-settings-panel',
		'settings.availability_history_title',
		'',
		['settings.general.availability_note', 'settings.general.availability_enable_hint'],
		'server.config'
	],
	[
		'joining',
		'availability-history-enabled',
		'settings.availability_history_enable',
		'settings.availability_history_title',
		['settings.general.availability_enable_hint'],
		'server.config'
	],
	[
		'joining',
		'availability-history-retention',
		'settings.general.retention_label',
		'settings.availability_history_title',
		['settings.general.retention_hint'],
		'server.config'
	],
	[
		'meshcentral',
		'mesh-settings-panel',
		'mesh.settings_title',
		'',
		['settings.general.mesh_note2', 'settings.general.mesh_groups_note'],
		'server.config'
	],
	[
		'meshcentral',
		'mesh-settings-panel',
		'settings.general.mesh_status',
		'mesh.settings_title',
		[],
		'server.config'
	],
	[
		'meshcentral',
		'mesh-settings-panel',
		'settings.general.mesh_agents',
		'mesh.settings_title',
		[],
		'server.config'
	],
	[
		'meshcentral',
		'mesh-settings-panel',
		'settings.general.mesh_server_id',
		'mesh.settings_title',
		[],
		'server.config'
	],
	[
		'meshcentral',
		'mesh-settings-panel',
		'settings.general.mesh_cert',
		'mesh.settings_title',
		[],
		'server.config'
	],
	[
		'meshcentral',
		'mesh-groups-form',
		'mesh.groups_title',
		'',
		['settings.general.mesh_groups_note'],
		'server.config'
	],
	[
		'meshcentral',
		'mesh-msh-name',
		'settings.general.mesh_msh_name',
		'mesh.settings_title',
		['settings.general.mesh_msh_name_hint'],
		'server.config'
	],
	['meshcentral', 'mesh-recordings', 'settings.general.rec_title', '', [], 'server.config'],
	[
		'quicksupport',
		'qs-feedback-section',
		'quick_support_feedback.title',
		'',
		['quick_support_feedback.description'],
		'server.config'
	],
	[
		'quicksupport',
		'qs-feedback-enabled',
		'quick_support_feedback.enabled',
		'quick_support_feedback.title',
		['quick_support_feedback.enabled_help'],
		'server.config'
	],
	[
		'quicksupport',
		'qs-feedback-questions',
		'quick_support_feedback.questions',
		'quick_support_feedback.title',
		['quick_support_feedback.questions_help'],
		'server.config'
	],
	['general', 'server-info', 'settings.server_info', '', [], ''],
	['general', 'server-info', 'settings.general.console_version', 'settings.server_info', [], ''],
	['general', 'go-server-version', 'settings.general.server_version', 'settings.server_info', [], ''],
	['general', 'db-path', 'settings.general.database_file', 'settings.server_info', [], ''],
	['general', 'uptime', 'settings.general.running_for', 'settings.server_info', [], ''],
	[
		'branding',
		'branding-theme',
		'theme.title',
		'',
		['settings.appearance.theme_note', 'settings.appearance.mode_system'],
		''
	],
	['branding', 'theme-mode-selector', 'theme.auto', 'theme.title', ['settings.appearance.mode_system'], ''],
	['branding', 'theme-mode-selector', 'theme.light', 'theme.title', ['settings.appearance.mode_light'], ''],
	['branding', 'theme-mode-selector', 'theme.dark', 'theme.title', ['settings.appearance.mode_dark'], ''],
	['branding', 'theme-mode-selector', 'theme.custom', 'theme.title', ['settings.appearance.mode_custom'], ''],
	[
		'branding',
		'branding-theme-select',
		'branding.builtin_themes',
		'theme.title',
		['settings.appearance.builtin_hint'],
		''
	],
	[
		'branding',
		'branding-custom-colors',
		'settings.appearance.custom_colors',
		'',
		['settings.appearance.custom_colors_note'],
		''
	],
	[
		'branding',
		'color-bgPrimary',
		'settings.appearance.color_bg',
		'settings.appearance.custom_colors',
		[],
		''
	],
	[
		'branding',
		'color-bgSecondary',
		'settings.appearance.color_bg_subtle',
		'settings.appearance.custom_colors',
		[],
		''
	],
	[
		'branding',
		'color-bgTertiary',
		'settings.appearance.color_hover',
		'settings.appearance.custom_colors',
		[],
		''
	],
	[
		'branding',
		'color-bgElevated',
		'settings.appearance.color_float',
		'settings.appearance.custom_colors',
		[],
		''
	],
	[
		'branding',
		'color-textPrimary',
		'settings.appearance.color_text',
		'settings.appearance.custom_colors',
		[],
		''
	],
	[
		'branding',
		'color-textSecondary',
		'settings.appearance.color_text_muted',
		'settings.appearance.custom_colors',
		[],
		''
	],
	[
		'branding',
		'color-accentBlue',
		'settings.appearance.color_accent',
		'settings.appearance.custom_colors',
		[],
		''
	],
	[
		'branding',
		'color-accentGreen',
		'settings.appearance.color_success',
		'settings.appearance.custom_colors',
		[],
		''
	],
	[
		'branding',
		'color-accentRed',
		'settings.appearance.color_danger',
		'settings.appearance.custom_colors',
		[],
		''
	],
	[
		'branding',
		'color-accentYellow',
		'settings.appearance.color_warning',
		'settings.appearance.custom_colors',
		[],
		''
	],
	[
		'branding',
		'color-accentPurple',
		'settings.appearance.color_info',
		'settings.appearance.custom_colors',
		[],
		''
	],
	[
		'branding',
		'color-borderPrimary',
		'settings.appearance.color_border',
		'settings.appearance.custom_colors',
		[],
		''
	],
	[
		'branding',
		'color-borderSecondary',
		'settings.appearance.color_rule',
		'settings.appearance.custom_colors',
		[],
		''
	],
	[
		'brand',
		'branding-identity',
		'settings.appearance.identity',
		'',
		['branding.identity_desc', 'branding.logo_desc'],
		''
	],
	['brand', 'brand-name', 'branding.app_name', 'settings.appearance.identity', [], ''],
	[
		'brand',
		'branding-identity',
		'branding.logo_title',
		'settings.appearance.identity',
		['branding.logo_desc'],
		''
	],
	[
		'brand',
		'logo-text-input',
		'branding.logo_text',
		'settings.appearance.identity',
		['branding.logo_text_hint'],
		''
	],
	[
		'brand',
		'logo-text-accent',
		'branding.logo_text_accent',
		'settings.appearance.identity',
		['branding.logo_text_accent_hint'],
		''
	],
	[
		'brand',
		'logo-icon-name',
		'branding.logo_icon_name',
		'settings.appearance.identity',
		['branding.logo_icon_hint'],
		''
	],
	[
		'brand',
		'logo-svg-input',
		'branding.logo_svg_code',
		'settings.appearance.identity',
		['branding.logo_svg_hint'],
		''
	],
	[
		'brand',
		'logo-image-file',
		'branding.logo_image_upload',
		'settings.appearance.identity',
		['branding.logo_image_hint'],
		''
	],
	[
		'brand',
		'logo-image-url',
		'branding.logo_image_url',
		'settings.appearance.identity',
		['branding.logo_image_url_hint'],
		''
	],
	[
		'branding',
		'branding-fonts',
		'branding.typography_title',
		'',
		['branding.typography_desc', 'branding.font_heading_hint'],
		''
	],
	[
		'branding',
		'font-heading-search',
		'branding.font_heading',
		'branding.typography_title',
		['branding.font_heading_hint'],
		''
	],
	[
		'branding',
		'font-body-search',
		'branding.font_body',
		'branding.typography_title',
		['branding.font_body_hint'],
		''
	],
	['branding', 'branding-fonts', 'settings.appearance.font_source', 'branding.typography_title', [], ''],
	['branding', 'font-upload-name', 'branding.font_upload_name', 'branding.typography_title', [], ''],
	['branding', 'font-upload-file', 'branding.font_upload_choose', 'branding.typography_title', [], ''],
	[
		'brand',
		'branding-footer',
		'branding.footer_title',
		'',
		['branding.footer_desc', 'branding.footer_text_hint'],
		''
	],
	[
		'brand',
		'footer-text',
		'branding.footer_text',
		'branding.footer_title',
		['branding.footer_text_hint'],
		''
	],
	[
		'brand',
		'show-powered',
		'branding.show_powered',
		'branding.footer_title',
		['branding.show_powered_hint'],
		''
	],
	[
		'branding',
		'branding-background',
		'settings.appearance.console_background',
		'',
		['branding.background_desc', 'branding.bg_gradient_hint'],
		''
	],
	[
		'branding',
		'branding-background',
		'settings.appearance.background_type',
		'settings.appearance.console_background',
		[],
		''
	],
	['branding', 'bg-color', 'branding.bg_color', 'settings.appearance.console_background', [], ''],
	[
		'branding',
		'bg-gradient',
		'branding.bg_gradient',
		'settings.appearance.console_background',
		['branding.bg_gradient_hint'],
		''
	],
	['branding', 'bg-image-panel', 'desktop.wp_images', 'settings.appearance.console_background', [], ''],
	[
		'branding',
		'bg-image-file',
		'branding.bg_image_upload',
		'settings.appearance.console_background',
		['branding.bg_image_hint'],
		''
	],
	['branding', 'bg-image-url', 'branding.bg_image_url', 'settings.appearance.console_background', [], ''],
	['branding', 'bg-size', 'branding.bg_size', 'settings.appearance.console_background', [], ''],
	['branding', 'bg-blur', 'branding.bg_blur', 'settings.appearance.console_background', [], ''],
	[
		'branding',
		'bg-overlay',
		'branding.bg_overlay',
		'settings.appearance.console_background',
		['branding.bg_overlay_hint'],
		''
	],
	[
		'brand',
		'branding-login',
		'branding.login_title',
		'',
		['branding.login_desc', 'branding.login_promo_mode_hint'],
		''
	],
	['brand', 'login-title', 'branding.login_heading', 'branding.login_title', [], ''],
	['brand', 'login-subtitle', 'branding.login_subheading', 'branding.login_title', [], ''],
	[
		'brand',
		'login-promo-mode-selector',
		'branding.login_promo_mode',
		'branding.login_title',
		['branding.login_promo_mode_hint'],
		''
	],
	[
		'brand',
		'login-slides-panel',
		'branding.login_slides_title',
		'',
		['branding.login_slides_desc', 'branding.login_slides_max'],
		''
	],
	['brand', 'login-bg-config', 'settings.appearance.login_background', '', ['branding.bg_image_hint'], ''],
	[
		'brand',
		'login-bg-config',
		'settings.appearance.background_type',
		'settings.appearance.login_background',
		[],
		''
	],
	['brand', 'login-bg-color', 'branding.bg_color', 'settings.appearance.login_background', [], ''],
	['brand', 'login-bg-gradient', 'branding.bg_gradient', 'settings.appearance.login_background', [], ''],
	[
		'brand',
		'login-bg-image-file',
		'branding.bg_image_upload',
		'settings.appearance.login_background',
		['branding.bg_image_hint'],
		''
	],
	['brand', 'login-bg-image-url', 'branding.bg_image_url', 'settings.appearance.login_background', [], ''],
	['brand', 'login-bg-overlay', 'branding.bg_overlay', 'settings.appearance.login_background', [], ''],
	[
		'brand',
		'branding-agent',
		'branding.agent_title',
		'',
		['branding.agent_desc', 'branding.bg_image_hint'],
		''
	],
	['brand', 'branding-agent', 'settings.appearance.background_type', 'branding.agent_title', [], ''],
	['brand', 'agent-bg-color', 'branding.bg_color', 'branding.agent_title', [], ''],
	['brand', 'agent-bg-gradient', 'branding.bg_gradient', 'branding.agent_title', [], ''],
	[
		'brand',
		'agent-bg-image-file',
		'branding.bg_image_upload',
		'branding.agent_title',
		['branding.bg_image_hint'],
		''
	],
	['brand', 'agent-bg-image-url', 'branding.bg_image_url', 'branding.agent_title', [], ''],
	[
		'brand',
		'agent-show-powered',
		'branding.agent_show_powered',
		'branding.agent_title',
		['branding.agent_show_powered_hint'],
		''
	],
	[
		'brand',
		'branding-rdclient',
		'settings.appearance.rdclient',
		'',
		['settings.appearance.rdclient_note', 'branding.bg_image_hint'],
		''
	],
	[
		'brand',
		'branding-rdclient',
		'settings.appearance.background_type',
		'settings.appearance.rdclient',
		[],
		''
	],
	['brand', 'rdclient-bg-color', 'branding.bg_color', 'settings.appearance.rdclient', [], ''],
	['brand', 'rdclient-bg-gradient', 'branding.bg_gradient', 'settings.appearance.rdclient', [], ''],
	[
		'brand',
		'rdclient-bg-image-file',
		'branding.bg_image_upload',
		'settings.appearance.rdclient',
		['branding.bg_image_hint'],
		''
	],
	['brand', 'rdclient-bg-image-url', 'branding.bg_image_url', 'settings.appearance.rdclient', [], ''],
	['brand', 'rdclient-bg-size', 'branding.bg_size', 'settings.appearance.rdclient', [], ''],
	[
		'brand',
		'rdclient-bg-overlay',
		'branding.bg_overlay',
		'settings.appearance.rdclient',
		['branding.bg_overlay_hint'],
		''
	],
	['branding', 'custom-css', 'branding.custom_css_title', '', ['branding.custom_css_hint'], ''],
	[
		'branding',
		'branding-profiles',
		'settings.appearance.profiles',
		'',
		['settings.appearance.profiles_note'],
		'branding.edit'
	],
	[
		'branding',
		'branding-profile-select',
		'branding.profiles_label',
		'settings.appearance.profiles',
		[],
		'branding.edit'
	],
	[
		'branding',
		'branding-import-export',
		'branding.import_export_title',
		'',
		['branding.import_export_desc'],
		'branding.edit'
	],
	[
		'branding',
		'branding-import-input',
		'branding.import_theme',
		'branding.import_export_title',
		[],
		'branding.edit'
	],
	// Your account (/account): the signed-in person's own settings
	['account', 'profile-picture', 'account.picture_upload', 'account.profile', ['account.picture_note'], ''],
	['account', 'profile-status', 'chat.my_presence', 'account.profile', ['account.status_note'], ''],
	['account', 'password', 'account.password', '', ['settings.password_requirements'], ''],
	['account', 'current-password', 'settings.current_password', 'account.password', [], ''],
	[
		'account',
		'new-password',
		'settings.new_password',
		'account.password',
		['settings.password_requirements'],
		''
	],
	['account', 'confirm-password', 'settings.confirm_password', 'account.password', [], ''],
	['account', 'two-factor', 'settings.totp_title', '', ['settings.signin.totp_note'], ''],
	['account', 'sessions', 'account.sessions', '', ['security_audit.sessions_hint'], ''],
	['account', 'email-new', 'account.email_new', 'account.email_title', ['account.email_note'], ''],
	['account', 'preferences-theme', 'account.theme', 'account.preferences', ['account.theme_note'], ''],
	['account', 'preferences-language', 'settings.language', 'account.preferences', [], ''],
	['account', 'preferences-accessibility', 'accessibility.title', 'account.preferences', [], ''],
	[
		'joining',
		'enrollment-mode',
		'tokens.enrollment_mode',
		'settings.enrollment_title',
		['settings.joining.mode_note', 'settings.enrollment_mode_link'],
		'server.config'
	],
	[
		'joining',
		'enrollment-settings-section',
		'settings.enrollment_title',
		'',
		['settings.enrollment_desc', 'tokens.mode_open_desc'],
		'server.config'
	],
	[
		'joining',
		'enrollment-mode',
		'tokens.mode_open',
		'tokens.enrollment_mode',
		['tokens.mode_open_desc'],
		'server.config'
	],
	[
		'joining',
		'enrollment-mode',
		'tokens.mode_managed',
		'tokens.enrollment_mode',
		['tokens.mode_managed_desc'],
		'server.config'
	],
	[
		'joining',
		'enrollment-mode',
		'tokens.mode_locked',
		'tokens.enrollment_mode',
		['tokens.mode_locked_desc'],
		'server.config'
	],
	[
		'joining',
		'enrollment-rich-approve',
		'settings.enrollment_rich_approve',
		'settings.enrollment_title',
		['settings.enrollment_rich_approve_hint'],
		'server.config'
	],
	[
		'joining',
		'enrollment-tag-picker',
		'settings.enrollment_tag_picker',
		'settings.enrollment_title',
		['settings.enrollment_tag_picker_hint'],
		'server.config'
	],
	[
		'auth',
		'client-sessions-form',
		'settings.client_sessions_title',
		'',
		['settings.client_sessions_desc', 'settings.client_sessions_expiry_hint'],
		'server.config'
	],
	[
		'auth',
		'client-sessions-expiry-days',
		'settings.client_sessions_expiry_days',
		'settings.client_sessions_title',
		['settings.client_sessions_expiry_hint'],
		'server.config'
	],
	[
		'auth',
		'client-sessions-sliding',
		'settings.client_sessions_sliding',
		'settings.client_sessions_title',
		['settings.client_sessions_sliding_hint'],
		'server.config'
	],
	[
		'auth',
		'client-sessions-max-days',
		'settings.client_sessions_max_days',
		'settings.client_sessions_title',
		['settings.client_sessions_max_days_hint'],
		'server.config'
	],
	[
		'ldap',
		'ldap-form',
		'settings.ldap_title',
		'',
		['settings.ldap_desc', 'settings.signin.ldap_enabled_hint'],
		'server.config'
	],
	[
		'ldap',
		'ldap-enabled',
		'settings.ldap_enabled',
		'settings.ldap_title',
		['settings.signin.ldap_enabled_hint'],
		'server.config'
	],
	['ldap', 'ldap-host', 'settings.ldap_host', 'settings.ldap_title', [], 'server.config'],
	['ldap', 'ldap-port', 'settings.ldap_port', 'settings.ldap_title', [], 'server.config'],
	['ldap', 'ldap-use-tls', 'settings.ldap_use_tls', 'settings.ldap_title', [], 'server.config'],
	['ldap', 'ldap-start-tls', 'settings.ldap_start_tls', 'settings.ldap_title', [], 'server.config'],
	['ldap', 'ldap-skip-tls-verify', 'settings.ldap_skip_verify', 'settings.ldap_title', [], 'server.config'],
	[
		'ldap',
		'ldap-conn-timeout',
		'settings.ldap_timeout',
		'settings.ldap_title',
		['settings.ldap_timeout_hint'],
		'server.config'
	],
	[
		'ldap',
		'ldap-direct-bind',
		'settings.ldap_direct_bind',
		'settings.ldap_title',
		['settings.ldap_direct_bind_hint'],
		'server.config'
	],
	[
		'ldap',
		'ldap-bind-dn',
		'settings.ldap_bind_dn',
		'settings.ldap_title',
		['settings.ldap_bind_dn_hint'],
		'server.config'
	],
	['ldap', 'ldap-bind-password', 'settings.ldap_bind_password', 'settings.ldap_title', [], 'server.config'],
	[
		'ldap',
		'ldap-base-dn',
		'settings.ldap_base_dn',
		'settings.ldap_title',
		['settings.ldap_base_dn_hint'],
		'server.config'
	],
	[
		'ldap',
		'ldap-user-filter',
		'settings.ldap_user_filter',
		'settings.ldap_title',
		['settings.ldap_user_filter_hint'],
		'server.config'
	],
	[
		'ldap',
		'ldap-direct-bind-dn',
		'settings.ldap_direct_bind_dn',
		'settings.ldap_title',
		['settings.ldap_direct_bind_dn_hint'],
		'server.config'
	],
	['ldap', 'ldap-user-attr-id', 'settings.ldap_attr_id', 'settings.ldap_title', [], 'server.config'],
	['ldap', 'ldap-user-attr-email', 'settings.ldap_attr_email', 'settings.ldap_title', [], 'server.config'],
	['ldap', 'ldap-user-attr-name', 'settings.ldap_attr_name', 'settings.ldap_title', [], 'server.config'],
	[
		'ldap',
		'ldap-group-base-dn',
		'settings.ldap_group_base_dn',
		'settings.ldap_title',
		['settings.ldap_group_base_dn_hint'],
		'server.config'
	],
	['ldap', 'ldap-group-filter', 'settings.ldap_group_filter', 'settings.ldap_title', [], 'server.config'],
	[
		'ldap',
		'ldap-group-attr-name',
		'settings.ldap_group_attr_name',
		'settings.ldap_title',
		[],
		'server.config'
	],
	[
		'auth',
		'require-two-factor',
		'settings.signin.require_2fa',
		'settings.signin.console_title',
		['settings.signin.require_2fa_hint'],
		'server.config'
	],
	[
		'auth',
		'console-signin-section',
		'settings.signin.console_title',
		'',
		['settings.signin.console_note', 'settings.signin.require_2fa'],
		'server.config'
	],
	[
		'auth',
		'single-session',
		'settings.signin.single_session',
		'settings.signin.console_title',
		['settings.signin.single_session_hint'],
		'server.config'
	],
	[
		'auth',
		'impersonation-mail',
		'settings.signin.impersonation_mail',
		'settings.signin.console_title',
		['settings.signin.impersonation_mail_hint'],
		'server.config'
	],
	[
		'auth',
		'inactive-accounts',
		'settings.inactive_label',
		'settings.signin.console_title',
		['settings.inactive_hint'],
		'server.config'
	],
	[
		'auth',
		'inactive-accounts-days',
		'settings.inactive_days',
		'settings.signin.console_title',
		['settings.inactive_days_hint'],
		'server.config'
	],
	// The four notice switches are one row: their ids name your own
	// password, which the Sign-in tab has no row for
	['auth', 'security-notices', 'settings.notices.title', '', ['settings.notices.note'], 'server.config'],
	[
		'ldap',
		'ldap-allow-signup',
		'settings.ldap_allow_signup',
		'settings.ldap_title',
		['settings.ldap_allow_signup_hint'],
		'server.config'
	],
	[
		'ldap',
		'ldap-default-role',
		'settings.ldap_default_role',
		'settings.ldap_title',
		['settings.ldap_default_role_hint'],
		'server.config'
	],
	[
		'ldap',
		'ldap-group-role-map',
		'settings.ldap_group_role_map',
		'settings.ldap_title',
		['settings.ldap_group_role_map_hint'],
		'server.config'
	],
	[
		'sso',
		'oidc-form',
		'settings.oidc_title',
		'',
		['settings.oidc_desc', 'settings.oidc_enabled_hint'],
		'server.config'
	],
	[
		'sso',
		'oidc-enabled',
		'settings.oidc_enabled',
		'settings.oidc_title',
		['settings.oidc_enabled_hint'],
		'server.config'
	],
	[
		'sso',
		'oidc-display-name',
		'settings.oidc_display_name',
		'settings.oidc_title',
		['settings.oidc_display_name_hint'],
		'server.config'
	],
	[
		'sso',
		'oidc-issuer-url',
		'settings.oidc_issuer_url',
		'settings.oidc_title',
		['settings.oidc_issuer_url_hint'],
		'server.config'
	],
	[
		'sso',
		'oidc-allowed-private-cidrs',
		'settings.oidc_allowed_private_cidrs',
		'settings.oidc_title',
		['settings.oidc_allowed_private_cidrs_hint'],
		'server.config'
	],
	['sso', 'oidc-client-id', 'settings.oidc_client_id', 'settings.oidc_title', [], 'server.config'],
	['sso', 'oidc-client-secret', 'settings.oidc_client_secret', 'settings.oidc_title', [], 'server.config'],
	[
		'sso',
		'oidc-redirect-url',
		'settings.oidc_redirect_url',
		'settings.oidc_title',
		['settings.oidc_redirect_url_hint'],
		'server.config'
	],
	[
		'sso',
		'oidc-panel-url',
		'settings.oidc_panel_url',
		'settings.oidc_title',
		['settings.oidc_panel_url_hint'],
		'server.config'
	],
	['sso', 'oidc-scopes', 'settings.oidc_scopes', 'settings.oidc_title', [], 'server.config'],
	['sso', 'oidc-use-pkce', 'settings.oidc_use_pkce', 'settings.oidc_title', [], 'server.config'],
	['sso', 'oidc-auto-discovery', 'settings.oidc_auto_discovery', 'settings.oidc_title', [], 'server.config'],
	['sso', 'oidc-allow-signup', 'settings.oidc_allow_signup', 'settings.oidc_title', [], 'server.config'],
	[
		'sso',
		'oidc-prefer-sso',
		'settings.oidc_prefer_sso',
		'settings.oidc_title',
		['settings.oidc_prefer_sso_hint'],
		'server.config'
	],
	[
		'sso',
		'oidc-authorization-url',
		'settings.oidc_authorization_url',
		'settings.oidc_title',
		[],
		'server.config'
	],
	['sso', 'oidc-token-url', 'settings.oidc_token_url', 'settings.oidc_title', [], 'server.config'],
	['sso', 'oidc-userinfo-url', 'settings.oidc_userinfo_url', 'settings.oidc_title', [], 'server.config'],
	['sso', 'oidc-claim-username', 'settings.oidc_claim_username', 'settings.oidc_title', [], 'server.config'],
	['sso', 'oidc-claim-email', 'settings.oidc_claim_email', 'settings.oidc_title', [], 'server.config'],
	['sso', 'oidc-claim-name', 'settings.oidc_claim_name', 'settings.oidc_title', [], 'server.config'],
	['sso', 'oidc-claim-groups', 'settings.oidc_claim_groups', 'settings.oidc_title', [], 'server.config'],
	['sso', 'oidc-default-role', 'settings.oidc_default_role', 'settings.oidc_title', [], 'server.config'],
	[
		'sso',
		'oidc-group-role-map',
		'settings.oidc_group_role_map',
		'settings.oidc_title',
		['settings.oidc_group_role_map_hint'],
		'server.config'
	],
	[
		'server-backups',
		'backup-create',
		'backups_page.make_title',
		'',
		['backups_page.make_note_short', 'backups_page.download_title'],
		'server.config'
	],
	[
		'server-backups',
		'scheduled-backup-panel',
		'backups_page.schedule_title',
		'',
		['backups_page.schedule_note'],
		'server.config'
	],
	[
		'server-backups',
		'scheduled-backup-enabled',
		'backups_page.enabled',
		'backups_page.schedule_title',
		['backups_page.schedule_note'],
		'server.config'
	],
	[
		'server-backups',
		'scheduled-backup-interval',
		'backups_page.how_often',
		'backups_page.schedule_title',
		['backups_page.how_often_help'],
		'server.config'
	],
	[
		'server-backups',
		'scheduled-backup-time',
		'backups_page.time_of_day',
		'backups_page.schedule_title',
		['backups_page.how_often_help'],
		'server.config'
	],
	[
		'server-backups',
		'scheduled-backup-retention',
		'backup.schedule_retention',
		'backups_page.schedule_title',
		['backups_page.keep_help'],
		'server.config'
	],
	[
		'server-backups',
		'scheduled-backup-destination-type',
		'backups_page.where',
		'backups_page.schedule_title',
		['backups_page.where_help', 'backups_page.test_folder'],
		'server.config'
	],
	[
		'server-backups',
		'scheduled-backup-path',
		'backups_page.folder',
		'backups_page.schedule_title',
		['backups_page.folder_help'],
		'server.config'
	],
	[
		'server-backups',
		'backup-restore',
		'settings.backup_tab.restore_title',
		'',
		['backups_page.restore_note', 'settings.backup_tab.restore_file_hint'],
		'server.config'
	],
	[
		'server-backups',
		'restore-file-input',
		'settings.backup_tab.restore_file',
		'settings.backup_tab.restore_title',
		['settings.backup_tab.restore_file_hint'],
		'server.config'
	],
	[
		'server-backups',
		'restore-database',
		'settings.backup_tab.opt_database',
		'',
		['settings.backup_tab.opt_database_desc'],
		'server.config'
	],
	[
		'server-backups',
		'restore-uploads',
		'settings.backup_tab.opt_uploads',
		'',
		['settings.backup_tab.opt_uploads_desc'],
		'server.config'
	],
	[
		'server-backups',
		'restore-secrets',
		'settings.backup_tab.opt_secrets',
		'',
		['settings.backup_tab.opt_secrets_desc'],
		'server.config'
	],
	[
		'server-backups',
		'restore-env',
		'settings.backup_tab.opt_env',
		'',
		['settings.backup_tab.opt_env_desc'],
		'server.config'
	],
	[
		'server-backups',
		'restore-godb',
		'settings.backup_tab.opt_godb',
		'',
		['settings.backup_tab.opt_godb_desc'],
		'server.config'
	],
	['server-backups', 'backup-stat-users', 'backup.stat_users', '', [], 'server.config'],
	['server-backups', 'backup-stat-settings', 'backup.stat_settings', '', [], 'server.config'],
	['server-backups', 'backup-stat-folders', 'backup.stat_folders', '', [], 'server.config'],
	['server-backups', 'backup-stat-groups', 'backup.stat_groups', '', [], 'server.config'],
	[
		'server-backups',
		'backup-stat-strategies',
		'settings.backup_tab.stat_strategies',
		'',
		[],
		'server.config'
	],
	['server-backups', 'backup-stat-backend', 'settings.backup_tab.stat_backend', '', [], 'server.config'],
	['server-backups', 'backup-stat-database', 'backup.stat_database', '', [], 'server.config'],
	[
		'server-backups',
		'backup-part-server_keys',
		'backup.stat_keys',
		'backups_page.holds_title',
		[],
		'server.config'
	],
	['server-backups', 'backup-part-env', 'backup.stat_env', 'backups_page.holds_title', [], 'server.config'],
	['server-backups', 'backup-stat-uploads', 'settings.backup_tab.stat_uploads', '', [], 'server.config'],
	[
		'server-updates',
		'update-status-headline',
		'updates.update_available',
		'',
		['updates.install_blocked_hint', 'settings.updates_tab.docker_install_note'],
		'server.config'
	],
	[
		'server-backups',
		'update-backups-panel',
		'backups_page.updates_title',
		'',
		['settings.updates_tab.backups_note', 'updates.backups_title'],
		'server.config'
	],
	[
		'server-updates',
		'update-channel-panel',
		'updates.channel_title',
		'',
		['settings.updates_tab.channel_note', 'settings.updates_tab.channel_stable_desc'],
		'server.config'
	],
	[
		'server-updates',
		'update-channel-select',
		'updates.channel_title',
		'',
		['settings.updates_tab.channel_stable_desc', 'settings.updates_tab.channel_development_desc'],
		'server.config'
	],
	[
		'server-updates',
		'update-channel-select',
		'settings.updates_tab.channel_stable',
		'updates.channel_title',
		['settings.updates_tab.channel_stable_desc'],
		'server.config'
	],
	[
		'server-updates',
		'update-channel-select',
		'settings.updates_tab.channel_development',
		'updates.channel_title',
		['settings.updates_tab.channel_development_desc'],
		'server.config'
	],
	[
		'server-backups',
		'backup-retention-input',
		'settings.updates_tab.retention_label',
		'backups_page.updates_title',
		['backups_page.update_keep_help', 'backups_page.keep_all_updates'],
		'server.config'
	],
	[
		'server-backups',
		'backup-danger',
		'backups_page.prune_title',
		'backups_page.danger_title',
		['backups_page.prune_action'],
		'server.config'
	],
	['server-updates', 'update-local-version', 'updates.current_version', '', [], 'server.config'],
	['server-updates', 'update-remote-version', 'settings.updates_tab.newest_version', '', [], 'server.config'],
	['server-updates', 'update-status-headline', 'updates.status', '', [], 'server.config'],
	['email', 'email-smtp-host', 'settings.email.host_label', '', [], 'server.config'],
	['email', 'email-smtp-port', 'settings.email.smtp_port', '', ['settings.email.port_hint'], 'server.config'],
	[
		'email',
		'email-smtp-secure',
		'settings.email.secure_label',
		'',
		['settings.email.secure_hint'],
		'server.config'
	],
	[
		'email',
		'email-smtp-auth',
		'settings.email.auth_label',
		'',
		['settings.email.auth_hint'],
		'server.config'
	],
	['email', 'email-smtp-user', 'settings.email.smtp_user', '', [], 'server.config'],
	[
		'email',
		'email-smtp-pass',
		'settings.email.smtp_pass',
		'',
		['settings.email.pass_hint_masked'],
		'server.config'
	],
	['email', 'email-smtp-from', 'settings.email.smtp_from', '', ['settings.email.from_hint'], 'server.config'],
	[
		'email',
		'email-smtp-alert',
		'settings.email.alert_email',
		'',
		['settings.email.alert_email_hint'],
		'server.config'
	],
	['time', 'time-billing-timezone', 'settings.time.billing_timezone', '', [], 'server.config'],
	// Your own time zone (Your account, Preferences) has the same label: it
	// comes after the console's, so a search for the time zone lists that first
	[
		'account',
		'preferences-time-zone',
		'users.time_zone',
		'account.preferences',
		['account.time_zone_note'],
		''
	],
	[
		'time',
		'time-billing-timezone-custom',
		'settings.time.custom_zone_label',
		'',
		['settings.time.custom_timezone_hint'],
		'server.config'
	],
	[
		'time',
		'time-clock-ntp-servers',
		'settings.time.servers_label',
		'',
		['settings.time.servers_hint'],
		'server.config'
	],
	[
		'time',
		'time-clock-require-sync',
		'commercialization.clock.require_sync',
		'',
		['settings.time.require_sync_hint'],
		'server.config'
	],
	[
		'time',
		'time-clock-max-skew',
		'settings.time.max_skew_label',
		'',
		['settings.time.max_skew_hint'],
		'server.config'
	],
	[
		'time',
		'time-clock-trust-os',
		'settings.time.trust_os_label',
		'',
		['settings.time.trust_os_hint'],
		'server.config'
	],
	['time', 'time-clock-offset', 'commercialization.clock.status_offset', '', [], 'server.config'],
	['time', 'time-clock-ntp-server', 'commercialization.clock.status_server', '', [], 'server.config'],
	['time', 'time-clock-os-sync', 'commercialization.clock.status_os', '', [], 'server.config'],
	['time', 'time-clock-last-error', 'commercialization.clock.status_error', '', [], 'server.config'],
	['server-config-files', 'advanced-config', 'settings.advanced_tab.restart_title', '', [], 'server.config'],
	[
		'brand',
		'client-app',
		'settings.branding.client_app',
		'',
		['client_branding.page_description'],
		'branding.edit'
	],
	[
		'brand',
		'client-app-company',
		'client_branding.company_name',
		'settings.branding.client_app',
		['client_branding.company_hint'],
		'branding.edit'
	],
	[
		'brand',
		'client-app-support',
		'client_branding.support_contact',
		'settings.branding.client_app',
		['client_branding.support_hint'],
		'branding.edit'
	],
	['brand', 'client-app-phone', 'client_branding.phone', 'settings.branding.client_app', [], 'branding.edit'],
	['brand', 'client-app-email', 'client_branding.email', 'settings.branding.client_app', [], 'branding.edit'],
	[
		'brand',
		'client-app-website',
		'client_branding.website',
		'settings.branding.client_app',
		[],
		'branding.edit'
	],
	[
		'brand',
		'client-app-accent',
		'client_branding.accent_color',
		'settings.branding.client_app',
		['client_branding.accent_hint'],
		'branding.edit'
	],
	[
		'brand',
		'client-app-logo',
		'client_branding.logo',
		'settings.branding.client_app',
		['client_branding.logo_hint'],
		'branding.edit'
	],
	// Rules > Policies: the settings of the organization picked on the page
	[
		'policies',
		'pol-connection-mode',
		'policies.connection_mode',
		'policies.connection_title',
		['policies.help_mode'],
		''
	],
	[
		'policies',
		'pol-connection-max-session-duration',
		'policies.max_session',
		'policies.connection_title',
		['policies.help_max_session'],
		''
	],
	[
		'policies',
		'pol-connection-idle-lock-timeout',
		'policies.idle_lock',
		'policies.connection_title',
		['policies.help_idle_lock'],
		''
	],
	[
		'policies',
		'pol-connection-wallpaper',
		'policies.wallpaper_policy',
		'policies.connection_title',
		['policies.help_wallpaper'],
		''
	],
	[
		'policies',
		'pol-features-allow-file-transfer',
		'policies.allow_file_transfer',
		'policies.features_title',
		['policies.help_file_transfer'],
		''
	],
	[
		'policies',
		'pol-features-allow-clipboard',
		'policies.allow_clipboard',
		'policies.features_title',
		['policies.help_clipboard'],
		''
	],
	[
		'policies',
		'pol-features-allow-audio',
		'policies.allow_audio',
		'policies.features_title',
		['policies.help_audio'],
		''
	],
	[
		'policies',
		'pol-features-allow-recording',
		'policies.allow_recording',
		'policies.features_title',
		['policies.help_recording'],
		''
	],
	[
		'policies',
		'pol-security-require-2fa',
		'policies.require_2fa',
		'policies.security_title',
		['policies.help_2fa'],
		''
	],
	[
		'policies',
		'pol-security-password-min-length',
		'policies.password_min_length',
		'policies.security_title',
		['policies.help_password_min'],
		''
	],
	[
		'policies',
		'pol-security-password-require-special',
		'policies.password_require_special',
		'policies.security_title',
		['policies.help_password_special'],
		''
	],
	[
		'policies',
		'pol-security-password-max-age-days',
		'policies.password_max_age',
		'policies.security_title',
		['policies.help_password_age'],
		''
	],
	[
		'policies',
		'pol-security-allowed-operators',
		'policies.allowed_operators',
		'policies.security_title',
		['policies.help_operators'],
		''
	],
	[
		'policies',
		'pol-network-block-direct-p2p',
		'policies.block_direct_p2p',
		'policies.network_title',
		['policies.help_block_p2p'],
		''
	],
	[
		'policies',
		'pol-network-allowed-relay-servers',
		'policies.allowed_relays',
		'policies.network_title',
		['policies.help_relays'],
		''
	],
	[
		'policies',
		'pol-network-ip-allowlist',
		'policies.ip_allowlist',
		'policies.network_title',
		['policies.help_ip_ranges'],
		''
	],
	[
		'policies',
		'pol-update-auto-update',
		'policies.auto_update',
		'policies.update_title',
		['policies.help_auto_update'],
		''
	],
	[
		'policies',
		'pol-update-channel',
		'policies.update_channel',
		'policies.update_title',
		['policies.update_channel_hint'],
		''
	],
	...PERMISSION_ROWS
];

/** Space-separated element ids per tab or page, for #<id> links into another one */
export const TAB_IDS: Readonly<Record<string, string>> = {
	account:
		'api-tokens confirm-password current-password email email-new new-password password password-requirements preferences preferences-accessibility preferences-language preferences-theme preferences-time-zone preferences-time-zone-control profile profile-picture profile-status profile-status-control req-length security-events sessions two-factor',
	general:
		'conn-allow-shared-nat-initiator conn-logged-in-only-initiator conn-mode-p2p conn-mode-relay conn-operator-only-outbound conn-p2p-fallback-ms conn-same-nat-relay connection-diagram connection-diagram-title connection-mode connection-mode-advanced connection-mode-effect connection-mode-fields connection-mode-form connection-mode-info connection-mode-section connection-mode-source connection-mode-title db-path general-form general-save go-server-version public-api-url public-endpoints public-endpoints-form public-endpoints-title public-relay-server public-server-id server-info server-info-title uptime',
	auth: 'client-sessions-expiry-days client-sessions-form client-sessions-max-days client-sessions-max-group client-sessions-settings-section client-sessions-sliding console-sessions-form console-signin-section for-everyone-title impersonation-mail inactive-accounts inactive-accounts-days inactive-accounts-days-group require-two-factor security-notice-email_changed security-notice-new_signin security-notice-password_changed security-notice-two_factor_changed security-notices single-session two-factor-policy-form',
	ldap: 'auth-panel-ldap ldap-advanced ldap-allow-signup ldap-attrs ldap-base-dn ldap-bind ldap-bind-dn ldap-bind-password ldap-bind-search-fields ldap-config-fields ldap-conn-timeout ldap-connection ldap-default-role ldap-direct-bind ldap-direct-bind-dn ldap-direct-bind-fields ldap-enabled ldap-form ldap-group-attr-name ldap-group-base-dn ldap-group-filter ldap-group-role-map ldap-groups ldap-host ldap-port ldap-skip-tls-verify ldap-start-tls ldap-test-btn ldap-test-result ldap-use-tls ldap-user-attr-email ldap-user-attr-id ldap-user-attr-name ldap-user-filter',
	sso: 'auth-panel-oidc oidc-accounts oidc-advanced oidc-allow-signup oidc-allowed-private-cidrs oidc-authorization-url oidc-auto-discovery oidc-claim-email oidc-claim-groups oidc-claim-name oidc-claim-username oidc-claims oidc-client-id oidc-client-secret oidc-config-fields oidc-default-role oidc-display-name oidc-enabled oidc-form oidc-group-role-map oidc-issuer-url oidc-manual-endpoints oidc-panel-url oidc-prefer-sso oidc-provider oidc-redirect-url oidc-redirect-url-warning oidc-roles oidc-scopes oidc-test-btn oidc-test-result oidc-token-url oidc-use-pkce oidc-userinfo-url',
	joining:
		'availability-history-enabled availability-history-form availability-history-retention availability-history-settings-panel availability-retention-group availability-title device-scope device-scope-form device-scope-open device-scope-restricted device-scope-settings-panel device-scope-title device-scope-warning enrollment-mode enrollment-pending-hint enrollment-rich-approve enrollment-settings-section enrollment-tag-picker joining-mode-section joining-mode-title',
	branding:
		'bg-blur bg-blur-value bg-color bg-color-panel bg-color-picker bg-effects-group bg-file-name bg-gradient bg-gradient-panel bg-image-file bg-image-library bg-image-panel bg-image-url bg-overlay bg-overlay-group bg-overlay-value bg-size bg-upload-status branding-background branding-background-title branding-builtin-themes branding-colors-title branding-css branding-css-title branding-custom-colors branding-danger-title branding-export-btn branding-fonts branding-fonts-title branding-form branding-ie-status branding-ie-title branding-import-export branding-import-input branding-profile-apply-btn branding-profile-delete-btn branding-profile-duplicate-btn branding-profile-menu branding-profile-new-btn branding-profile-save-btn branding-profile-select branding-profile-status branding-profiles branding-profiles-title branding-readability-alert branding-readability-text branding-reset-btn branding-revert-btn branding-save-btn branding-theme branding-theme-apply branding-theme-gallery branding-theme-select branding-theme-title branding-toc color-accentBlue color-accentGreen color-accentPurple color-accentRed color-accentYellow color-bgElevated color-bgPrimary color-bgSecondary color-bgTertiary color-borderPrimary color-borderSecondary color-textPrimary color-textSecondary custom-css font-body-clear font-body-current font-body-dropdown font-body-search font-body-slot font-body-value font-heading-clear font-heading-current font-heading-dropdown font-heading-search font-heading-slot font-heading-value font-local-count font-local-info font-local-list font-source-google font-source-label font-source-local font-source-upload font-upload-file font-upload-name theme-mode-selector',
	brand:
		'agent-bg-color agent-bg-color-panel agent-bg-color-picker agent-bg-file-name agent-bg-gradient agent-bg-gradient-panel agent-bg-image-file agent-bg-image-panel agent-bg-image-url agent-bg-upload-status agent-show-powered brand-name branding-agent branding-agent-title branding-footer branding-footer-title branding-identity branding-identity-title branding-login branding-login-title branding-rdclient branding-rdclient-title cb-contact-title cb-look-title client-app client-app-accent client-app-company client-app-email client-app-logo client-app-phone client-app-support client-app-title client-app-website footer-text login-bg-color login-bg-color-panel login-bg-color-picker login-bg-config login-bg-file-name login-bg-gradient login-bg-gradient-panel login-bg-image-file login-bg-image-panel login-bg-image-url login-bg-overlay login-bg-overlay-group login-bg-overlay-value login-bg-upload-status login-promo-mode-selector login-slides-add login-slides-list login-slides-max-hint login-slides-panel login-slides-restore login-subtitle login-title logo-file-name logo-icon-name logo-icon-panel logo-image-file logo-image-panel logo-image-url logo-svg-input logo-svg-panel logo-text-accent logo-text-input logo-text-panel rdclient-bg-color rdclient-bg-color-panel rdclient-bg-color-picker rdclient-bg-file-name rdclient-bg-gradient rdclient-bg-gradient-panel rdclient-bg-image-file rdclient-bg-image-panel rdclient-bg-image-url rdclient-bg-overlay rdclient-bg-overlay-group rdclient-bg-overlay-value rdclient-bg-size rdclient-bg-upload-status show-powered',
	email:
		'email-smtp-alert email-smtp-auth email-smtp-auth-fields email-smtp-form email-smtp-from email-smtp-host email-smtp-pass email-smtp-port email-smtp-save-btn email-smtp-secure email-smtp-user email-title',
	time: 'time-billing-month-hint time-billing-timezone time-billing-timezone-custom time-check-status time-clock-advanced time-clock-detail time-clock-last-error time-clock-max-skew time-clock-ntp-server time-clock-ntp-servers time-clock-offset time-clock-os-sync time-clock-require-sync time-clock-settings-form time-clock-trust-os time-custom-zone time-detect-timezone-btn time-ntp time-ntp-title time-save-clock-settings-btn time-side-title time-timesync-check-btn time-zone time-zone-now time-zone-title',
	meshcentral:
		'mesh-agents-online mesh-body mesh-cert-banner mesh-cert-state mesh-copy-server-id mesh-download-msh mesh-group-add mesh-groups-form mesh-groups-list mesh-groups-status mesh-groups-title mesh-install mesh-msh-name mesh-recordings mesh-recordings-table mesh-server-id mesh-settings-panel mesh-state mesh-status-enabled mesh-title',
	quicksupport: 'qs-feedback-add qs-feedback-enabled qs-feedback-questions qs-feedback-section',
	policies:
		'pol-connection-idle-lock-timeout pol-connection-max-session-duration pol-connection-mode pol-connection-wallpaper pol-features-allow-audio pol-features-allow-clipboard pol-features-allow-file-transfer pol-features-allow-recording pol-network-allowed-relay-servers pol-network-block-direct-p2p pol-network-ip-allowlist pol-security-allowed-operators pol-security-password-max-age-days pol-security-password-min-length pol-security-password-require-special pol-security-require-2fa pol-update-auto-update pol-update-channel',
	'server-backups':
		'backup-contents-title backup-create backup-create-title backup-danger backup-download-btn backup-download-status backup-part-env backup-part-server_keys backup-prune-now backup-restore backup-restore-title backup-retention-all backup-retention-count backup-retention-input backup-retention-save backup-side-title backup-smb-domain backup-smb-host backup-smb-mount-path backup-smb-password backup-smb-share backup-smb-status backup-smb-test-btn backup-smb-username backup-stat-backend backup-stat-database backup-stat-env backup-stat-folders backup-stat-groups backup-stat-keys backup-stat-settings backup-stat-strategies backup-stat-uploads backup-stat-users backup-stats backup-summary restore-actions restore-database restore-env restore-file-hint restore-file-input restore-godb restore-json-note restore-options restore-options-hint restore-result restore-secrets restore-start-btn restore-status restore-uploads scheduled-backup-destination-type scheduled-backup-details scheduled-backup-enabled scheduled-backup-form scheduled-backup-interval scheduled-backup-panel scheduled-backup-path scheduled-backup-path-group scheduled-backup-retention scheduled-backup-run-btn scheduled-backup-smb-form scheduled-backup-smb-panel scheduled-backup-status scheduled-backup-test-btn scheduled-backup-time scheduled-backup-title smb-title update-backups-list update-backups-panel',
	'server-updates':
		'update-alerts update-available-heading update-backup-toggle update-changelog update-channel-active-badge update-channel-branch update-channel-development update-channel-docker-hint update-channel-effect update-channel-panel update-channel-select update-channel-stable update-channel-panel-title update-check-btn update-details-section update-docker-commands update-docker-desc update-docker-images update-docker-info update-docker-install-note update-files-summary update-install-blocked-hint update-install-btn update-local-version update-progress update-progress-fill update-progress-text update-rebuild-server-btn update-remote-version update-stale-detail update-stale-warning update-status-badge update-status-desc update-status-headline update-status-row updates-side-title',
	permissions: PERMISSION_ROWS.map((r) => r[1]).join(' '),
	'server-config-files':
		'advanced-config-file-list advanced-config-meta advanced-config-path advanced-config-placeholder advanced-config-reload advanced-config-restart advanced-config-restart-hint advanced-config-save advanced-config-textarea advanced-config-toolbar advanced-files-label advanced-restart-desc advanced-restart-title advanced-config advanced-config-title advanced-restart-zone'
};
