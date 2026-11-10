-- Node console schema on an empty PostgreSQL database (Go server never ran against it).
-- Worktree betterdesk-go, dumped 2026-11-10 at commit 569710e2; 948fb226 changes no schema code.
-- Produced by adapter.init() of web-nodejs/services/dbAdapter.js plus the DatabaseSessionStore, then: pg_dump --schema-only --no-owner --no-privileges --no-comments  (PostgreSQL 16)
-- Do not edit: regenerate (see data.md, section "How the schema dumps were produced").

--
-- PostgreSQL database dump
--

\restrict fNKrZNfG4fDXhXNvkBkHF30XjmvyLexKhIXBfRX6ggfDxyrz9YaSwMieE19y8cK

-- Dumped from database version 16.15
-- Dumped by pg_dump version 16.15

SET statement_timeout = 0;
SET lock_timeout = 0;
SET idle_in_transaction_session_timeout = 0;
SET client_encoding = 'UTF8';
SET standard_conforming_strings = on;
SELECT pg_catalog.set_config('search_path', '', false);
SET check_function_bodies = false;
SET xmloption = content;
SET client_min_messages = warning;
SET row_security = off;

SET default_tablespace = '';

SET default_table_access_method = heap;

--
-- Name: access_tokens; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.access_tokens (
    id integer NOT NULL,
    token text NOT NULL,
    token_hash text,
    user_id integer NOT NULL,
    client_id text DEFAULT ''::text,
    client_uuid text DEFAULT ''::text,
    created_at timestamp with time zone DEFAULT now(),
    expires_at timestamp with time zone NOT NULL,
    last_used timestamp with time zone,
    ip_address text DEFAULT ''::text,
    revoked boolean DEFAULT false
);


--
-- Name: access_tokens_id_seq; Type: SEQUENCE; Schema: public; Owner: -
--

CREATE SEQUENCE public.access_tokens_id_seq
    AS integer
    START WITH 1
    INCREMENT BY 1
    NO MINVALUE
    NO MAXVALUE
    CACHE 1;


--
-- Name: access_tokens_id_seq; Type: SEQUENCE OWNED BY; Schema: public; Owner: -
--

ALTER SEQUENCE public.access_tokens_id_seq OWNED BY public.access_tokens.id;


--
-- Name: account_lockouts; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.account_lockouts (
    id integer NOT NULL,
    username text NOT NULL,
    locked_until timestamp with time zone NOT NULL,
    attempt_count integer DEFAULT 0,
    created_at timestamp with time zone DEFAULT now()
);


--
-- Name: account_lockouts_id_seq; Type: SEQUENCE; Schema: public; Owner: -
--

CREATE SEQUENCE public.account_lockouts_id_seq
    AS integer
    START WITH 1
    INCREMENT BY 1
    NO MINVALUE
    NO MAXVALUE
    CACHE 1;


--
-- Name: account_lockouts_id_seq; Type: SEQUENCE OWNED BY; Schema: public; Owner: -
--

ALTER SEQUENCE public.account_lockouts_id_seq OWNED BY public.account_lockouts.id;


--
-- Name: activity_sessions; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.activity_sessions (
    id integer NOT NULL,
    device_id text NOT NULL,
    app_name text DEFAULT ''::text NOT NULL,
    window_title text DEFAULT ''::text NOT NULL,
    category text DEFAULT 'other'::text,
    started_at timestamp with time zone NOT NULL,
    ended_at timestamp with time zone NOT NULL,
    duration_secs integer DEFAULT 0,
    created_at timestamp with time zone DEFAULT now()
);


--
-- Name: activity_sessions_id_seq; Type: SEQUENCE; Schema: public; Owner: -
--

CREATE SEQUENCE public.activity_sessions_id_seq
    AS integer
    START WITH 1
    INCREMENT BY 1
    NO MINVALUE
    NO MAXVALUE
    CACHE 1;


--
-- Name: activity_sessions_id_seq; Type: SEQUENCE OWNED BY; Schema: public; Owner: -
--

ALTER SEQUENCE public.activity_sessions_id_seq OWNED BY public.activity_sessions.id;


--
-- Name: activity_summaries; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.activity_summaries (
    id integer NOT NULL,
    device_id text NOT NULL,
    idle_seconds integer DEFAULT 0,
    session_count integer DEFAULT 0,
    total_active_secs integer DEFAULT 0,
    reported_at timestamp with time zone NOT NULL,
    received_at timestamp with time zone DEFAULT now()
);


--
-- Name: activity_summaries_id_seq; Type: SEQUENCE; Schema: public; Owner: -
--

CREATE SEQUENCE public.activity_summaries_id_seq
    AS integer
    START WITH 1
    INCREMENT BY 1
    NO MINVALUE
    NO MAXVALUE
    CACHE 1;


--
-- Name: activity_summaries_id_seq; Type: SEQUENCE OWNED BY; Schema: public; Owner: -
--

ALTER SEQUENCE public.activity_summaries_id_seq OWNED BY public.activity_summaries.id;


--
-- Name: address_books; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.address_books (
    username text NOT NULL,
    ab_type text DEFAULT 'legacy'::text NOT NULL,
    data text DEFAULT '{}'::text NOT NULL,
    updated_at timestamp with time zone DEFAULT now()
);


--
-- Name: agent_bundle_builds; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.agent_bundle_builds (
    id integer NOT NULL,
    branding_hash text NOT NULL,
    platform text NOT NULL,
    arch text DEFAULT 'x64'::text NOT NULL,
    format text NOT NULL,
    status text DEFAULT 'queued'::text NOT NULL,
    artifact_path text,
    artifact_size bigint DEFAULT 0,
    artifact_sha256 text,
    error_message text DEFAULT ''::text,
    client_commit text,
    generation_id text,
    config_fingerprint text,
    progress_percent integer DEFAULT 0 NOT NULL,
    progress_phase text DEFAULT ''::text,
    started_at timestamp with time zone,
    finished_at timestamp with time zone,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    updated_at timestamp with time zone DEFAULT now() NOT NULL
);


--
-- Name: agent_bundle_builds_id_seq; Type: SEQUENCE; Schema: public; Owner: -
--

CREATE SEQUENCE public.agent_bundle_builds_id_seq
    AS integer
    START WITH 1
    INCREMENT BY 1
    NO MINVALUE
    NO MAXVALUE
    CACHE 1;


--
-- Name: agent_bundle_builds_id_seq; Type: SEQUENCE OWNED BY; Schema: public; Owner: -
--

ALTER SEQUENCE public.agent_bundle_builds_id_seq OWNED BY public.agent_bundle_builds.id;


--
-- Name: agent_bundles; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.agent_bundles (
    id integer NOT NULL,
    bundle_id text NOT NULL,
    slug text,
    name text NOT NULL,
    branding text DEFAULT '{}'::text NOT NULL,
    branding_hash text DEFAULT ''::text NOT NULL,
    config_fingerprint text DEFAULT ''::text NOT NULL,
    client_commit text,
    active_generation_hash text,
    created_by integer,
    product_type text DEFAULT 'betterdesk-support'::text NOT NULL,
    revoked boolean DEFAULT false NOT NULL,
    download_count integer DEFAULT 0 NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    updated_at timestamp with time zone DEFAULT now() NOT NULL
);


--
-- Name: agent_bundles_id_seq; Type: SEQUENCE; Schema: public; Owner: -
--

CREATE SEQUENCE public.agent_bundles_id_seq
    AS integer
    START WITH 1
    INCREMENT BY 1
    NO MINVALUE
    NO MAXVALUE
    CACHE 1;


--
-- Name: agent_bundles_id_seq; Type: SEQUENCE OWNED BY; Schema: public; Owner: -
--

ALTER SEQUENCE public.agent_bundles_id_seq OWNED BY public.agent_bundles.id;


--
-- Name: alert_history; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.alert_history (
    id integer NOT NULL,
    rule_id integer NOT NULL,
    device_id text,
    severity text DEFAULT 'warning'::text,
    message text DEFAULT ''::text NOT NULL,
    triggered_at timestamp with time zone NOT NULL,
    acknowledged boolean DEFAULT false,
    acknowledged_by text,
    acknowledged_at timestamp with time zone,
    created_at timestamp with time zone DEFAULT now()
);


--
-- Name: alert_history_id_seq; Type: SEQUENCE; Schema: public; Owner: -
--

CREATE SEQUENCE public.alert_history_id_seq
    AS integer
    START WITH 1
    INCREMENT BY 1
    NO MINVALUE
    NO MAXVALUE
    CACHE 1;


--
-- Name: alert_history_id_seq; Type: SEQUENCE OWNED BY; Schema: public; Owner: -
--

ALTER SEQUENCE public.alert_history_id_seq OWNED BY public.alert_history.id;


--
-- Name: alert_rules; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.alert_rules (
    id integer NOT NULL,
    name text NOT NULL,
    description text DEFAULT ''::text,
    enabled boolean DEFAULT true,
    condition_type text NOT NULL,
    condition_op text DEFAULT 'gt'::text NOT NULL,
    condition_value real DEFAULT 0 NOT NULL,
    severity text DEFAULT 'warning'::text,
    scope_device_id text,
    cooldown_secs integer DEFAULT 300,
    notify_emails text DEFAULT ''::text,
    created_by text,
    created_at timestamp with time zone DEFAULT now(),
    updated_at timestamp with time zone DEFAULT now()
);


--
-- Name: alert_rules_id_seq; Type: SEQUENCE; Schema: public; Owner: -
--

CREATE SEQUENCE public.alert_rules_id_seq
    AS integer
    START WITH 1
    INCREMENT BY 1
    NO MINVALUE
    NO MAXVALUE
    CACHE 1;


--
-- Name: alert_rules_id_seq; Type: SEQUENCE OWNED BY; Schema: public; Owner: -
--

ALTER SEQUENCE public.alert_rules_id_seq OWNED BY public.alert_rules.id;


--
-- Name: audit_alarms; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.audit_alarms (
    id integer NOT NULL,
    alarm_type integer NOT NULL,
    alarm_name text DEFAULT ''::text,
    host_id text DEFAULT ''::text,
    peer_id text DEFAULT ''::text,
    ip text DEFAULT ''::text,
    details jsonb DEFAULT '{}'::jsonb,
    created_at timestamp with time zone DEFAULT now()
);


--
-- Name: audit_alarms_id_seq; Type: SEQUENCE; Schema: public; Owner: -
--

CREATE SEQUENCE public.audit_alarms_id_seq
    AS integer
    START WITH 1
    INCREMENT BY 1
    NO MINVALUE
    NO MAXVALUE
    CACHE 1;


--
-- Name: audit_alarms_id_seq; Type: SEQUENCE OWNED BY; Schema: public; Owner: -
--

ALTER SEQUENCE public.audit_alarms_id_seq OWNED BY public.audit_alarms.id;


--
-- Name: audit_connections; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.audit_connections (
    id integer NOT NULL,
    host_id text NOT NULL,
    host_uuid text DEFAULT ''::text,
    peer_id text DEFAULT ''::text,
    peer_name text DEFAULT ''::text,
    action text NOT NULL,
    conn_type integer DEFAULT 0,
    session_id text DEFAULT ''::text,
    ip text DEFAULT ''::text,
    created_at timestamp with time zone DEFAULT now()
);


--
-- Name: audit_connections_id_seq; Type: SEQUENCE; Schema: public; Owner: -
--

CREATE SEQUENCE public.audit_connections_id_seq
    AS integer
    START WITH 1
    INCREMENT BY 1
    NO MINVALUE
    NO MAXVALUE
    CACHE 1;


--
-- Name: audit_connections_id_seq; Type: SEQUENCE OWNED BY; Schema: public; Owner: -
--

ALTER SEQUENCE public.audit_connections_id_seq OWNED BY public.audit_connections.id;


--
-- Name: audit_files; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.audit_files (
    id integer NOT NULL,
    host_id text NOT NULL,
    host_uuid text DEFAULT ''::text,
    peer_id text DEFAULT ''::text,
    direction integer DEFAULT 0,
    path text DEFAULT ''::text,
    is_file boolean DEFAULT true,
    num_files integer DEFAULT 0,
    files_json jsonb DEFAULT '[]'::jsonb,
    ip text DEFAULT ''::text,
    peer_name text DEFAULT ''::text,
    created_at timestamp with time zone DEFAULT now()
);


--
-- Name: audit_files_id_seq; Type: SEQUENCE; Schema: public; Owner: -
--

CREATE SEQUENCE public.audit_files_id_seq
    AS integer
    START WITH 1
    INCREMENT BY 1
    NO MINVALUE
    NO MAXVALUE
    CACHE 1;


--
-- Name: audit_files_id_seq; Type: SEQUENCE OWNED BY; Schema: public; Owner: -
--

ALTER SEQUENCE public.audit_files_id_seq OWNED BY public.audit_files.id;


--
-- Name: audit_log; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.audit_log (
    id integer NOT NULL,
    user_id integer,
    action text NOT NULL,
    details text,
    ip_address text,
    created_at timestamp with time zone DEFAULT now()
);


--
-- Name: audit_log_id_seq; Type: SEQUENCE; Schema: public; Owner: -
--

CREATE SEQUENCE public.audit_log_id_seq
    AS integer
    START WITH 1
    INCREMENT BY 1
    NO MINVALUE
    NO MAXVALUE
    CACHE 1;


--
-- Name: audit_log_id_seq; Type: SEQUENCE OWNED BY; Schema: public; Owner: -
--

ALTER SEQUENCE public.audit_log_id_seq OWNED BY public.audit_log.id;


--
-- Name: branding_config; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.branding_config (
    key text NOT NULL,
    value text NOT NULL,
    updated_at timestamp with time zone DEFAULT now()
);


--
-- Name: branding_profiles; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.branding_profiles (
    id integer NOT NULL,
    name text NOT NULL,
    description text DEFAULT ''::text,
    data text NOT NULL,
    is_active integer DEFAULT 0,
    created_at timestamp with time zone DEFAULT now(),
    updated_at timestamp with time zone DEFAULT now()
);


--
-- Name: branding_profiles_id_seq; Type: SEQUENCE; Schema: public; Owner: -
--

CREATE SEQUENCE public.branding_profiles_id_seq
    AS integer
    START WITH 1
    INCREMENT BY 1
    NO MINVALUE
    NO MAXVALUE
    CACHE 1;


--
-- Name: branding_profiles_id_seq; Type: SEQUENCE OWNED BY; Schema: public; Owner: -
--

ALTER SEQUENCE public.branding_profiles_id_seq OWNED BY public.branding_profiles.id;


--
-- Name: client_feature_policies; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.client_feature_policies (
    subject_type text NOT NULL,
    subject_id text NOT NULL,
    feature_key text NOT NULL,
    enabled boolean DEFAULT false NOT NULL,
    updated_at timestamp with time zone DEFAULT now()
);


--
-- Name: device_folder_assignments; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.device_folder_assignments (
    device_id text NOT NULL,
    folder_id integer NOT NULL,
    assigned_at timestamp with time zone DEFAULT now()
);


--
-- Name: device_group_members; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.device_group_members (
    device_group_id integer NOT NULL,
    peer_id text NOT NULL,
    created_at timestamp with time zone DEFAULT now()
);


--
-- Name: device_group_user_access; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.device_group_user_access (
    device_group_id integer NOT NULL,
    user_id integer NOT NULL,
    created_at timestamp with time zone DEFAULT now()
);


--
-- Name: device_group_user_group_access; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.device_group_user_group_access (
    device_group_id integer NOT NULL,
    user_group_id integer NOT NULL,
    created_at timestamp with time zone DEFAULT now()
);


--
-- Name: device_groups; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.device_groups (
    id integer NOT NULL,
    guid text NOT NULL,
    name text NOT NULL,
    note text DEFAULT ''::text,
    team_id text DEFAULT ''::text,
    source_type text DEFAULT 'manual'::text,
    tag_filter text DEFAULT ''::text,
    created_at timestamp with time zone DEFAULT now()
);


--
-- Name: device_groups_id_seq; Type: SEQUENCE; Schema: public; Owner: -
--

CREATE SEQUENCE public.device_groups_id_seq
    AS integer
    START WITH 1
    INCREMENT BY 1
    NO MINVALUE
    NO MAXVALUE
    CACHE 1;


--
-- Name: device_groups_id_seq; Type: SEQUENCE OWNED BY; Schema: public; Owner: -
--

ALTER SEQUENCE public.device_groups_id_seq OWNED BY public.device_groups.id;


--
-- Name: device_inventory; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.device_inventory (
    id integer NOT NULL,
    device_id text NOT NULL,
    hardware jsonb DEFAULT '{}'::jsonb,
    software jsonb DEFAULT '{}'::jsonb,
    collected_at timestamp with time zone,
    received_at timestamp with time zone DEFAULT now()
);


--
-- Name: device_inventory_id_seq; Type: SEQUENCE; Schema: public; Owner: -
--

CREATE SEQUENCE public.device_inventory_id_seq
    AS integer
    START WITH 1
    INCREMENT BY 1
    NO MINVALUE
    NO MAXVALUE
    CACHE 1;


--
-- Name: device_inventory_id_seq; Type: SEQUENCE OWNED BY; Schema: public; Owner: -
--

ALTER SEQUENCE public.device_inventory_id_seq OWNED BY public.device_inventory.id;


--
-- Name: device_telemetry; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.device_telemetry (
    id integer NOT NULL,
    device_id text NOT NULL,
    cpu_usage_percent real DEFAULT 0,
    memory_used_bytes bigint DEFAULT 0,
    memory_total_bytes bigint DEFAULT 0,
    uptime_secs integer DEFAULT 0,
    "timestamp" timestamp with time zone,
    received_at timestamp with time zone DEFAULT now()
);


--
-- Name: device_telemetry_id_seq; Type: SEQUENCE; Schema: public; Owner: -
--

CREATE SEQUENCE public.device_telemetry_id_seq
    AS integer
    START WITH 1
    INCREMENT BY 1
    NO MINVALUE
    NO MAXVALUE
    CACHE 1;


--
-- Name: device_telemetry_id_seq; Type: SEQUENCE OWNED BY; Schema: public; Owner: -
--

ALTER SEQUENCE public.device_telemetry_id_seq OWNED BY public.device_telemetry.id;


--
-- Name: dlp_events; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.dlp_events (
    id integer NOT NULL,
    device_id text NOT NULL,
    event_source text DEFAULT 'unknown'::text NOT NULL,
    event_type text DEFAULT 'info'::text NOT NULL,
    policy_id integer,
    policy_name text DEFAULT ''::text,
    action text DEFAULT 'log'::text,
    details jsonb DEFAULT '{}'::jsonb,
    created_at timestamp with time zone DEFAULT now()
);


--
-- Name: dlp_events_id_seq; Type: SEQUENCE; Schema: public; Owner: -
--

CREATE SEQUENCE public.dlp_events_id_seq
    AS integer
    START WITH 1
    INCREMENT BY 1
    NO MINVALUE
    NO MAXVALUE
    CACHE 1;


--
-- Name: dlp_events_id_seq; Type: SEQUENCE OWNED BY; Schema: public; Owner: -
--

ALTER SEQUENCE public.dlp_events_id_seq OWNED BY public.dlp_events.id;


--
-- Name: dlp_policies; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.dlp_policies (
    id integer NOT NULL,
    name text NOT NULL,
    description text DEFAULT ''::text,
    policy_type text DEFAULT ''::text,
    action text DEFAULT 'log'::text,
    scope text DEFAULT ''::text,
    enabled boolean DEFAULT true,
    rules jsonb DEFAULT '[]'::jsonb,
    created_at timestamp with time zone DEFAULT now(),
    updated_at timestamp with time zone DEFAULT now()
);


--
-- Name: dlp_policies_id_seq; Type: SEQUENCE; Schema: public; Owner: -
--

CREATE SEQUENCE public.dlp_policies_id_seq
    AS integer
    START WITH 1
    INCREMENT BY 1
    NO MINVALUE
    NO MAXVALUE
    CACHE 1;


--
-- Name: dlp_policies_id_seq; Type: SEQUENCE OWNED BY; Schema: public; Owner: -
--

ALTER SEQUENCE public.dlp_policies_id_seq OWNED BY public.dlp_policies.id;


--
-- Name: email_notification_log; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.email_notification_log (
    id integer NOT NULL,
    event_type text NOT NULL,
    event_id text NOT NULL,
    recipient text NOT NULL,
    sent_at timestamp with time zone DEFAULT now()
);


--
-- Name: email_notification_log_id_seq; Type: SEQUENCE; Schema: public; Owner: -
--

CREATE SEQUENCE public.email_notification_log_id_seq
    AS integer
    START WITH 1
    INCREMENT BY 1
    NO MINVALUE
    NO MAXVALUE
    CACHE 1;


--
-- Name: email_notification_log_id_seq; Type: SEQUENCE OWNED BY; Schema: public; Owner: -
--

ALTER SEQUENCE public.email_notification_log_id_seq OWNED BY public.email_notification_log.id;


--
-- Name: folders; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.folders (
    id integer NOT NULL,
    name text NOT NULL,
    color text DEFAULT '#6366f1'::text,
    icon text DEFAULT 'folder'::text,
    sort_order integer DEFAULT 0,
    created_at timestamp with time zone DEFAULT now()
);


--
-- Name: folders_id_seq; Type: SEQUENCE; Schema: public; Owner: -
--

CREATE SEQUENCE public.folders_id_seq
    AS integer
    START WITH 1
    INCREMENT BY 1
    NO MINVALUE
    NO MAXVALUE
    CACHE 1;


--
-- Name: folders_id_seq; Type: SEQUENCE OWNED BY; Schema: public; Owner: -
--

ALTER SEQUENCE public.folders_id_seq OWNED BY public.folders.id;


--
-- Name: login_attempts; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.login_attempts (
    id integer NOT NULL,
    username text NOT NULL,
    ip_address text DEFAULT ''::text,
    success boolean DEFAULT false,
    created_at timestamp with time zone DEFAULT now()
);


--
-- Name: login_attempts_id_seq; Type: SEQUENCE; Schema: public; Owner: -
--

CREATE SEQUENCE public.login_attempts_id_seq
    AS integer
    START WITH 1
    INCREMENT BY 1
    NO MINVALUE
    NO MAXVALUE
    CACHE 1;


--
-- Name: login_attempts_id_seq; Type: SEQUENCE OWNED BY; Schema: public; Owner: -
--

ALTER SEQUENCE public.login_attempts_id_seq OWNED BY public.login_attempts.id;


--
-- Name: network_checks; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.network_checks (
    id integer NOT NULL,
    target_id integer NOT NULL,
    status text DEFAULT 'unknown'::text NOT NULL,
    rtt_ms real,
    status_code integer,
    error_msg text,
    checked_at timestamp with time zone DEFAULT now()
);


--
-- Name: network_checks_id_seq; Type: SEQUENCE; Schema: public; Owner: -
--

CREATE SEQUENCE public.network_checks_id_seq
    AS integer
    START WITH 1
    INCREMENT BY 1
    NO MINVALUE
    NO MAXVALUE
    CACHE 1;


--
-- Name: network_checks_id_seq; Type: SEQUENCE OWNED BY; Schema: public; Owner: -
--

ALTER SEQUENCE public.network_checks_id_seq OWNED BY public.network_checks.id;


--
-- Name: network_targets; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.network_targets (
    id integer NOT NULL,
    name text NOT NULL,
    host text DEFAULT ''::text,
    port integer,
    url text,
    check_type text DEFAULT 'ping'::text NOT NULL,
    timeout_ms integer DEFAULT 5000,
    interval_ms integer DEFAULT 60000,
    enabled boolean DEFAULT true,
    last_status text,
    last_check_at timestamp with time zone,
    last_rtt_ms real,
    created_at timestamp with time zone DEFAULT now(),
    updated_at timestamp with time zone DEFAULT now()
);


--
-- Name: network_targets_id_seq; Type: SEQUENCE; Schema: public; Owner: -
--

CREATE SEQUENCE public.network_targets_id_seq
    AS integer
    START WITH 1
    INCREMENT BY 1
    NO MINVALUE
    NO MAXVALUE
    CACHE 1;


--
-- Name: network_targets_id_seq; Type: SEQUENCE OWNED BY; Schema: public; Owner: -
--

ALTER SEQUENCE public.network_targets_id_seq OWNED BY public.network_targets.id;


--
-- Name: notification_reads; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.notification_reads (
    user_id integer NOT NULL,
    notification_id text NOT NULL,
    read_at timestamp with time zone DEFAULT now()
);


--
-- Name: panel_access_policies; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.panel_access_policies (
    subject_type text NOT NULL,
    subject_id text NOT NULL,
    resource_key text NOT NULL,
    access_level text DEFAULT 'none'::text NOT NULL,
    updated_at timestamp with time zone DEFAULT now()
);


--
-- Name: panel_sessions; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.panel_sessions (
    sid text NOT NULL,
    session_json text NOT NULL,
    expires_at timestamp with time zone NOT NULL,
    updated_at timestamp with time zone DEFAULT now() NOT NULL,
    user_id integer,
    username text,
    ip_address text,
    user_agent text,
    client_fingerprint text,
    created_at bigint,
    last_activity_at bigint,
    ended_at bigint,
    end_reason text,
    is_active boolean DEFAULT true NOT NULL
);


--
-- Name: peer; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.peer (
    id text NOT NULL,
    uuid text DEFAULT ''::text,
    pk bytea,
    note text DEFAULT ''::text,
    created_at timestamp with time zone DEFAULT now(),
    status_online boolean DEFAULT false,
    last_online timestamp with time zone,
    is_deleted boolean DEFAULT false,
    info jsonb DEFAULT '{}'::jsonb,
    ip text DEFAULT ''::text,
    "user" text DEFAULT ''::text,
    is_banned boolean DEFAULT false,
    banned_at timestamp with time zone,
    banned_reason text DEFAULT ''::text,
    folder_id integer
);


--
-- Name: peer_metrics; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.peer_metrics (
    id integer NOT NULL,
    peer_id text NOT NULL,
    cpu_usage real DEFAULT 0,
    memory_usage real DEFAULT 0,
    disk_usage real DEFAULT 0,
    created_at timestamp with time zone DEFAULT now()
);


--
-- Name: peer_metrics_id_seq; Type: SEQUENCE; Schema: public; Owner: -
--

CREATE SEQUENCE public.peer_metrics_id_seq
    AS integer
    START WITH 1
    INCREMENT BY 1
    NO MINVALUE
    NO MAXVALUE
    CACHE 1;


--
-- Name: peer_metrics_id_seq; Type: SEQUENCE OWNED BY; Schema: public; Owner: -
--

ALTER SEQUENCE public.peer_metrics_id_seq OWNED BY public.peer_metrics.id;


--
-- Name: peer_sysinfo; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.peer_sysinfo (
    peer_id text NOT NULL,
    hostname text DEFAULT ''::text,
    username text DEFAULT ''::text,
    platform text DEFAULT ''::text,
    version text DEFAULT ''::text,
    cpu_name text DEFAULT ''::text,
    cpu_cores integer DEFAULT 0,
    cpu_freq_ghz real DEFAULT 0,
    memory_gb real DEFAULT 0,
    os_full text DEFAULT ''::text,
    displays jsonb DEFAULT '[]'::jsonb,
    encoding jsonb DEFAULT '[]'::jsonb,
    features jsonb DEFAULT '{}'::jsonb,
    platform_additions jsonb DEFAULT '{}'::jsonb,
    raw_json jsonb DEFAULT '{}'::jsonb,
    updated_at timestamp with time zone DEFAULT now()
);


--
-- Name: pending_registrations; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.pending_registrations (
    id integer NOT NULL,
    device_id text NOT NULL,
    hostname text DEFAULT ''::text,
    platform text DEFAULT ''::text,
    version text DEFAULT ''::text,
    ip_address text DEFAULT ''::text,
    public_key text DEFAULT ''::text,
    uuid text DEFAULT ''::text,
    status text DEFAULT 'pending'::text,
    approved_by text,
    approved_at timestamp with time zone,
    rejected_reason text DEFAULT ''::text,
    access_token text,
    console_url text,
    server_address text,
    server_key text,
    created_at timestamp with time zone DEFAULT now(),
    updated_at timestamp with time zone DEFAULT now()
);


--
-- Name: pending_registrations_id_seq; Type: SEQUENCE; Schema: public; Owner: -
--

CREATE SEQUENCE public.pending_registrations_id_seq
    AS integer
    START WITH 1
    INCREMENT BY 1
    NO MINVALUE
    NO MAXVALUE
    CACHE 1;


--
-- Name: pending_registrations_id_seq; Type: SEQUENCE OWNED BY; Schema: public; Owner: -
--

ALTER SEQUENCE public.pending_registrations_id_seq OWNED BY public.pending_registrations.id;


--
-- Name: relay_sessions; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.relay_sessions (
    id text NOT NULL,
    initiator_id text NOT NULL,
    target_id text NOT NULL,
    initiator_pk text,
    target_pk text,
    status text DEFAULT 'pending'::text,
    created_at timestamp with time zone DEFAULT now(),
    expires_at timestamp with time zone NOT NULL
);


--
-- Name: remote_commands; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.remote_commands (
    id integer NOT NULL,
    device_id text NOT NULL,
    command_type text DEFAULT 'shell'::text NOT NULL,
    payload text DEFAULT ''::text NOT NULL,
    status text DEFAULT 'pending'::text,
    result text,
    created_by text NOT NULL,
    created_at timestamp with time zone DEFAULT now(),
    executed_at timestamp with time zone,
    completed_at timestamp with time zone
);


--
-- Name: remote_commands_id_seq; Type: SEQUENCE; Schema: public; Owner: -
--

CREATE SEQUENCE public.remote_commands_id_seq
    AS integer
    START WITH 1
    INCREMENT BY 1
    NO MINVALUE
    NO MAXVALUE
    CACHE 1;


--
-- Name: remote_commands_id_seq; Type: SEQUENCE OWNED BY; Schema: public; Owner: -
--

ALTER SEQUENCE public.remote_commands_id_seq OWNED BY public.remote_commands.id;


--
-- Name: saved_reports; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.saved_reports (
    id integer NOT NULL,
    title text NOT NULL,
    report_type text NOT NULL,
    filters jsonb DEFAULT '{}'::jsonb,
    payload jsonb DEFAULT '{}'::jsonb,
    created_by text DEFAULT 'admin'::text NOT NULL,
    created_at timestamp with time zone DEFAULT now()
);


--
-- Name: saved_reports_id_seq; Type: SEQUENCE; Schema: public; Owner: -
--

CREATE SEQUENCE public.saved_reports_id_seq
    AS integer
    START WITH 1
    INCREMENT BY 1
    NO MINVALUE
    NO MAXVALUE
    CACHE 1;


--
-- Name: saved_reports_id_seq; Type: SEQUENCE OWNED BY; Schema: public; Owner: -
--

ALTER SEQUENCE public.saved_reports_id_seq OWNED BY public.saved_reports.id;


--
-- Name: settings; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.settings (
    key text NOT NULL,
    value text NOT NULL,
    updated_at timestamp with time zone DEFAULT now()
);


--
-- Name: strategies; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.strategies (
    id integer NOT NULL,
    guid text NOT NULL,
    name text NOT NULL,
    user_group_guid text DEFAULT ''::text,
    device_group_guid text DEFAULT ''::text,
    enabled boolean DEFAULT true,
    permissions jsonb DEFAULT '{}'::jsonb,
    created_at timestamp with time zone DEFAULT now(),
    updated_at timestamp with time zone DEFAULT now()
);


--
-- Name: strategies_id_seq; Type: SEQUENCE; Schema: public; Owner: -
--

CREATE SEQUENCE public.strategies_id_seq
    AS integer
    START WITH 1
    INCREMENT BY 1
    NO MINVALUE
    NO MAXVALUE
    CACHE 1;


--
-- Name: strategies_id_seq; Type: SEQUENCE OWNED BY; Schema: public; Owner: -
--

ALTER SEQUENCE public.strategies_id_seq OWNED BY public.strategies.id;


--
-- Name: strategy_assignments; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.strategy_assignments (
    id integer NOT NULL,
    target_type text NOT NULL,
    target_key text NOT NULL,
    strategy_guid text DEFAULT ''::text NOT NULL,
    updated_at timestamp with time zone DEFAULT now()
);


--
-- Name: strategy_assignments_id_seq; Type: SEQUENCE; Schema: public; Owner: -
--

CREATE SEQUENCE public.strategy_assignments_id_seq
    AS integer
    START WITH 1
    INCREMENT BY 1
    NO MINVALUE
    NO MAXVALUE
    CACHE 1;


--
-- Name: strategy_assignments_id_seq; Type: SEQUENCE OWNED BY; Schema: public; Owner: -
--

ALTER SEQUENCE public.strategy_assignments_id_seq OWNED BY public.strategy_assignments.id;


--
-- Name: studio_flows; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.studio_flows (
    id text NOT NULL,
    name text NOT NULL,
    description text DEFAULT ''::text NOT NULL,
    flow_json text DEFAULT '{}'::text NOT NULL,
    status text DEFAULT 'draft'::text NOT NULL,
    version integer DEFAULT 1 NOT NULL,
    created_by text DEFAULT ''::text NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    updated_at timestamp with time zone DEFAULT now() NOT NULL
);


--
-- Name: tenant_devices; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.tenant_devices (
    id integer NOT NULL,
    tenant_id integer NOT NULL,
    device_id text NOT NULL,
    assigned_at timestamp with time zone DEFAULT now()
);


--
-- Name: tenant_devices_id_seq; Type: SEQUENCE; Schema: public; Owner: -
--

CREATE SEQUENCE public.tenant_devices_id_seq
    AS integer
    START WITH 1
    INCREMENT BY 1
    NO MINVALUE
    NO MAXVALUE
    CACHE 1;


--
-- Name: tenant_devices_id_seq; Type: SEQUENCE OWNED BY; Schema: public; Owner: -
--

ALTER SEQUENCE public.tenant_devices_id_seq OWNED BY public.tenant_devices.id;


--
-- Name: tenant_users; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.tenant_users (
    id integer NOT NULL,
    tenant_id integer NOT NULL,
    user_id integer NOT NULL,
    assigned_at timestamp with time zone DEFAULT now()
);


--
-- Name: tenant_users_id_seq; Type: SEQUENCE; Schema: public; Owner: -
--

CREATE SEQUENCE public.tenant_users_id_seq
    AS integer
    START WITH 1
    INCREMENT BY 1
    NO MINVALUE
    NO MAXVALUE
    CACHE 1;


--
-- Name: tenant_users_id_seq; Type: SEQUENCE OWNED BY; Schema: public; Owner: -
--

ALTER SEQUENCE public.tenant_users_id_seq OWNED BY public.tenant_users.id;


--
-- Name: tenants; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.tenants (
    id integer NOT NULL,
    name text NOT NULL,
    slug text NOT NULL,
    contact_name text DEFAULT ''::text,
    contact_email text DEFAULT ''::text,
    max_devices integer DEFAULT 0,
    notes text DEFAULT ''::text,
    active boolean DEFAULT true,
    created_at timestamp with time zone DEFAULT now(),
    updated_at timestamp with time zone DEFAULT now()
);


--
-- Name: tenants_id_seq; Type: SEQUENCE; Schema: public; Owner: -
--

CREATE SEQUENCE public.tenants_id_seq
    AS integer
    START WITH 1
    INCREMENT BY 1
    NO MINVALUE
    NO MAXVALUE
    CACHE 1;


--
-- Name: tenants_id_seq; Type: SEQUENCE OWNED BY; Schema: public; Owner: -
--

ALTER SEQUENCE public.tenants_id_seq OWNED BY public.tenants.id;


--
-- Name: ticket_attachments; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.ticket_attachments (
    id integer NOT NULL,
    ticket_id integer NOT NULL,
    filename text NOT NULL,
    mimetype text DEFAULT 'application/octet-stream'::text,
    size_bytes integer DEFAULT 0,
    storage_path text NOT NULL,
    uploaded_by text NOT NULL,
    created_at timestamp with time zone DEFAULT now()
);


--
-- Name: ticket_attachments_id_seq; Type: SEQUENCE; Schema: public; Owner: -
--

CREATE SEQUENCE public.ticket_attachments_id_seq
    AS integer
    START WITH 1
    INCREMENT BY 1
    NO MINVALUE
    NO MAXVALUE
    CACHE 1;


--
-- Name: ticket_attachments_id_seq; Type: SEQUENCE OWNED BY; Schema: public; Owner: -
--

ALTER SEQUENCE public.ticket_attachments_id_seq OWNED BY public.ticket_attachments.id;


--
-- Name: ticket_comments; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.ticket_comments (
    id integer NOT NULL,
    ticket_id integer NOT NULL,
    author text NOT NULL,
    body text NOT NULL,
    is_internal boolean DEFAULT false,
    created_at timestamp with time zone DEFAULT now()
);


--
-- Name: ticket_comments_id_seq; Type: SEQUENCE; Schema: public; Owner: -
--

CREATE SEQUENCE public.ticket_comments_id_seq
    AS integer
    START WITH 1
    INCREMENT BY 1
    NO MINVALUE
    NO MAXVALUE
    CACHE 1;


--
-- Name: ticket_comments_id_seq; Type: SEQUENCE OWNED BY; Schema: public; Owner: -
--

ALTER SEQUENCE public.ticket_comments_id_seq OWNED BY public.ticket_comments.id;


--
-- Name: tickets; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.tickets (
    id integer NOT NULL,
    title text NOT NULL,
    description text DEFAULT ''::text,
    status text DEFAULT 'open'::text,
    priority text DEFAULT 'medium'::text,
    category text DEFAULT 'general'::text,
    device_id text,
    created_by text NOT NULL,
    assigned_to text,
    sla_due_at timestamp with time zone,
    resolved_at timestamp with time zone,
    closed_at timestamp with time zone,
    created_at timestamp with time zone DEFAULT now(),
    updated_at timestamp with time zone DEFAULT now()
);


--
-- Name: tickets_id_seq; Type: SEQUENCE; Schema: public; Owner: -
--

CREATE SEQUENCE public.tickets_id_seq
    AS integer
    START WITH 1
    INCREMENT BY 1
    NO MINVALUE
    NO MAXVALUE
    CACHE 1;


--
-- Name: tickets_id_seq; Type: SEQUENCE OWNED BY; Schema: public; Owner: -
--

ALTER SEQUENCE public.tickets_id_seq OWNED BY public.tickets.id;


--
-- Name: user_avatars; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.user_avatars (
    user_id integer NOT NULL,
    mime_type text NOT NULL,
    data bytea NOT NULL,
    version text NOT NULL,
    source text DEFAULT 'oidc'::text NOT NULL,
    source_url text DEFAULT ''::text,
    updated_at timestamp with time zone DEFAULT now()
);


--
-- Name: user_group_members; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.user_group_members (
    user_group_id integer NOT NULL,
    user_id integer NOT NULL,
    created_at timestamp with time zone DEFAULT now()
);


--
-- Name: user_groups; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.user_groups (
    id integer NOT NULL,
    guid text NOT NULL,
    name text NOT NULL,
    note text DEFAULT ''::text,
    team_id text DEFAULT ''::text,
    created_at timestamp with time zone DEFAULT now()
);


--
-- Name: user_groups_id_seq; Type: SEQUENCE; Schema: public; Owner: -
--

CREATE SEQUENCE public.user_groups_id_seq
    AS integer
    START WITH 1
    INCREMENT BY 1
    NO MINVALUE
    NO MAXVALUE
    CACHE 1;


--
-- Name: user_groups_id_seq; Type: SEQUENCE OWNED BY; Schema: public; Owner: -
--

ALTER SEQUENCE public.user_groups_id_seq OWNED BY public.user_groups.id;


--
-- Name: user_peer_grants; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.user_peer_grants (
    user_id integer NOT NULL,
    peer_id text NOT NULL,
    granted_at timestamp with time zone DEFAULT now()
);


--
-- Name: users; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.users (
    id integer NOT NULL,
    username text NOT NULL,
    password_hash text NOT NULL,
    role text DEFAULT 'admin'::text,
    auth_provider text DEFAULT 'local'::text,
    created_at timestamp with time zone DEFAULT now(),
    last_login timestamp with time zone,
    preferred_language text,
    totp_secret text,
    totp_enabled boolean DEFAULT false,
    totp_recovery_codes text,
    guid text DEFAULT ''::text,
    first_name text DEFAULT ''::text,
    last_name text DEFAULT ''::text,
    email text DEFAULT ''::text,
    phone text DEFAULT ''::text,
    role_display text DEFAULT ''::text,
    avatar_url text DEFAULT ''::text
);


--
-- Name: users_id_seq; Type: SEQUENCE; Schema: public; Owner: -
--

CREATE SEQUENCE public.users_id_seq
    AS integer
    START WITH 1
    INCREMENT BY 1
    NO MINVALUE
    NO MAXVALUE
    CACHE 1;


--
-- Name: users_id_seq; Type: SEQUENCE OWNED BY; Schema: public; Owner: -
--

ALTER SEQUENCE public.users_id_seq OWNED BY public.users.id;


--
-- Name: access_tokens id; Type: DEFAULT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.access_tokens ALTER COLUMN id SET DEFAULT nextval('public.access_tokens_id_seq'::regclass);


--
-- Name: account_lockouts id; Type: DEFAULT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.account_lockouts ALTER COLUMN id SET DEFAULT nextval('public.account_lockouts_id_seq'::regclass);


--
-- Name: activity_sessions id; Type: DEFAULT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.activity_sessions ALTER COLUMN id SET DEFAULT nextval('public.activity_sessions_id_seq'::regclass);


--
-- Name: activity_summaries id; Type: DEFAULT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.activity_summaries ALTER COLUMN id SET DEFAULT nextval('public.activity_summaries_id_seq'::regclass);


--
-- Name: agent_bundle_builds id; Type: DEFAULT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.agent_bundle_builds ALTER COLUMN id SET DEFAULT nextval('public.agent_bundle_builds_id_seq'::regclass);


--
-- Name: agent_bundles id; Type: DEFAULT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.agent_bundles ALTER COLUMN id SET DEFAULT nextval('public.agent_bundles_id_seq'::regclass);


--
-- Name: alert_history id; Type: DEFAULT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.alert_history ALTER COLUMN id SET DEFAULT nextval('public.alert_history_id_seq'::regclass);


--
-- Name: alert_rules id; Type: DEFAULT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.alert_rules ALTER COLUMN id SET DEFAULT nextval('public.alert_rules_id_seq'::regclass);


--
-- Name: audit_alarms id; Type: DEFAULT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.audit_alarms ALTER COLUMN id SET DEFAULT nextval('public.audit_alarms_id_seq'::regclass);


--
-- Name: audit_connections id; Type: DEFAULT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.audit_connections ALTER COLUMN id SET DEFAULT nextval('public.audit_connections_id_seq'::regclass);


--
-- Name: audit_files id; Type: DEFAULT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.audit_files ALTER COLUMN id SET DEFAULT nextval('public.audit_files_id_seq'::regclass);


--
-- Name: audit_log id; Type: DEFAULT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.audit_log ALTER COLUMN id SET DEFAULT nextval('public.audit_log_id_seq'::regclass);


--
-- Name: branding_profiles id; Type: DEFAULT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.branding_profiles ALTER COLUMN id SET DEFAULT nextval('public.branding_profiles_id_seq'::regclass);


--
-- Name: device_groups id; Type: DEFAULT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.device_groups ALTER COLUMN id SET DEFAULT nextval('public.device_groups_id_seq'::regclass);


--
-- Name: device_inventory id; Type: DEFAULT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.device_inventory ALTER COLUMN id SET DEFAULT nextval('public.device_inventory_id_seq'::regclass);


--
-- Name: device_telemetry id; Type: DEFAULT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.device_telemetry ALTER COLUMN id SET DEFAULT nextval('public.device_telemetry_id_seq'::regclass);


--
-- Name: dlp_events id; Type: DEFAULT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.dlp_events ALTER COLUMN id SET DEFAULT nextval('public.dlp_events_id_seq'::regclass);


--
-- Name: dlp_policies id; Type: DEFAULT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.dlp_policies ALTER COLUMN id SET DEFAULT nextval('public.dlp_policies_id_seq'::regclass);


--
-- Name: email_notification_log id; Type: DEFAULT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.email_notification_log ALTER COLUMN id SET DEFAULT nextval('public.email_notification_log_id_seq'::regclass);


--
-- Name: folders id; Type: DEFAULT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.folders ALTER COLUMN id SET DEFAULT nextval('public.folders_id_seq'::regclass);


--
-- Name: login_attempts id; Type: DEFAULT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.login_attempts ALTER COLUMN id SET DEFAULT nextval('public.login_attempts_id_seq'::regclass);


--
-- Name: network_checks id; Type: DEFAULT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.network_checks ALTER COLUMN id SET DEFAULT nextval('public.network_checks_id_seq'::regclass);


--
-- Name: network_targets id; Type: DEFAULT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.network_targets ALTER COLUMN id SET DEFAULT nextval('public.network_targets_id_seq'::regclass);


--
-- Name: peer_metrics id; Type: DEFAULT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.peer_metrics ALTER COLUMN id SET DEFAULT nextval('public.peer_metrics_id_seq'::regclass);


--
-- Name: pending_registrations id; Type: DEFAULT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.pending_registrations ALTER COLUMN id SET DEFAULT nextval('public.pending_registrations_id_seq'::regclass);


--
-- Name: remote_commands id; Type: DEFAULT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.remote_commands ALTER COLUMN id SET DEFAULT nextval('public.remote_commands_id_seq'::regclass);


--
-- Name: saved_reports id; Type: DEFAULT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.saved_reports ALTER COLUMN id SET DEFAULT nextval('public.saved_reports_id_seq'::regclass);


--
-- Name: strategies id; Type: DEFAULT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.strategies ALTER COLUMN id SET DEFAULT nextval('public.strategies_id_seq'::regclass);


--
-- Name: strategy_assignments id; Type: DEFAULT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.strategy_assignments ALTER COLUMN id SET DEFAULT nextval('public.strategy_assignments_id_seq'::regclass);


--
-- Name: tenant_devices id; Type: DEFAULT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.tenant_devices ALTER COLUMN id SET DEFAULT nextval('public.tenant_devices_id_seq'::regclass);


--
-- Name: tenant_users id; Type: DEFAULT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.tenant_users ALTER COLUMN id SET DEFAULT nextval('public.tenant_users_id_seq'::regclass);


--
-- Name: tenants id; Type: DEFAULT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.tenants ALTER COLUMN id SET DEFAULT nextval('public.tenants_id_seq'::regclass);


--
-- Name: ticket_attachments id; Type: DEFAULT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.ticket_attachments ALTER COLUMN id SET DEFAULT nextval('public.ticket_attachments_id_seq'::regclass);


--
-- Name: ticket_comments id; Type: DEFAULT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.ticket_comments ALTER COLUMN id SET DEFAULT nextval('public.ticket_comments_id_seq'::regclass);


--
-- Name: tickets id; Type: DEFAULT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.tickets ALTER COLUMN id SET DEFAULT nextval('public.tickets_id_seq'::regclass);


--
-- Name: user_groups id; Type: DEFAULT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.user_groups ALTER COLUMN id SET DEFAULT nextval('public.user_groups_id_seq'::regclass);


--
-- Name: users id; Type: DEFAULT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.users ALTER COLUMN id SET DEFAULT nextval('public.users_id_seq'::regclass);


--
-- Name: access_tokens access_tokens_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.access_tokens
    ADD CONSTRAINT access_tokens_pkey PRIMARY KEY (id);


--
-- Name: access_tokens access_tokens_token_key; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.access_tokens
    ADD CONSTRAINT access_tokens_token_key UNIQUE (token);


--
-- Name: account_lockouts account_lockouts_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.account_lockouts
    ADD CONSTRAINT account_lockouts_pkey PRIMARY KEY (id);


--
-- Name: account_lockouts account_lockouts_username_key; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.account_lockouts
    ADD CONSTRAINT account_lockouts_username_key UNIQUE (username);


--
-- Name: activity_sessions activity_sessions_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.activity_sessions
    ADD CONSTRAINT activity_sessions_pkey PRIMARY KEY (id);


--
-- Name: activity_summaries activity_summaries_device_id_reported_at_key; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.activity_summaries
    ADD CONSTRAINT activity_summaries_device_id_reported_at_key UNIQUE (device_id, reported_at);


--
-- Name: activity_summaries activity_summaries_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.activity_summaries
    ADD CONSTRAINT activity_summaries_pkey PRIMARY KEY (id);


--
-- Name: address_books address_books_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.address_books
    ADD CONSTRAINT address_books_pkey PRIMARY KEY (username, ab_type);


--
-- Name: agent_bundle_builds agent_bundle_builds_branding_hash_platform_arch_format_key; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.agent_bundle_builds
    ADD CONSTRAINT agent_bundle_builds_branding_hash_platform_arch_format_key UNIQUE (branding_hash, platform, arch, format);


--
-- Name: agent_bundle_builds agent_bundle_builds_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.agent_bundle_builds
    ADD CONSTRAINT agent_bundle_builds_pkey PRIMARY KEY (id);


--
-- Name: agent_bundles agent_bundles_bundle_id_key; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.agent_bundles
    ADD CONSTRAINT agent_bundles_bundle_id_key UNIQUE (bundle_id);


--
-- Name: agent_bundles agent_bundles_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.agent_bundles
    ADD CONSTRAINT agent_bundles_pkey PRIMARY KEY (id);


--
-- Name: alert_history alert_history_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.alert_history
    ADD CONSTRAINT alert_history_pkey PRIMARY KEY (id);


--
-- Name: alert_rules alert_rules_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.alert_rules
    ADD CONSTRAINT alert_rules_pkey PRIMARY KEY (id);


--
-- Name: audit_alarms audit_alarms_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.audit_alarms
    ADD CONSTRAINT audit_alarms_pkey PRIMARY KEY (id);


--
-- Name: audit_connections audit_connections_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.audit_connections
    ADD CONSTRAINT audit_connections_pkey PRIMARY KEY (id);


--
-- Name: audit_files audit_files_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.audit_files
    ADD CONSTRAINT audit_files_pkey PRIMARY KEY (id);


--
-- Name: audit_log audit_log_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.audit_log
    ADD CONSTRAINT audit_log_pkey PRIMARY KEY (id);


--
-- Name: branding_config branding_config_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.branding_config
    ADD CONSTRAINT branding_config_pkey PRIMARY KEY (key);


--
-- Name: branding_profiles branding_profiles_name_key; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.branding_profiles
    ADD CONSTRAINT branding_profiles_name_key UNIQUE (name);


--
-- Name: branding_profiles branding_profiles_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.branding_profiles
    ADD CONSTRAINT branding_profiles_pkey PRIMARY KEY (id);


--
-- Name: client_feature_policies client_feature_policies_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.client_feature_policies
    ADD CONSTRAINT client_feature_policies_pkey PRIMARY KEY (subject_type, subject_id, feature_key);


--
-- Name: device_folder_assignments device_folder_assignments_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.device_folder_assignments
    ADD CONSTRAINT device_folder_assignments_pkey PRIMARY KEY (device_id);


--
-- Name: device_group_members device_group_members_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.device_group_members
    ADD CONSTRAINT device_group_members_pkey PRIMARY KEY (device_group_id, peer_id);


--
-- Name: device_group_user_access device_group_user_access_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.device_group_user_access
    ADD CONSTRAINT device_group_user_access_pkey PRIMARY KEY (device_group_id, user_id);


--
-- Name: device_group_user_group_access device_group_user_group_access_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.device_group_user_group_access
    ADD CONSTRAINT device_group_user_group_access_pkey PRIMARY KEY (device_group_id, user_group_id);


--
-- Name: device_groups device_groups_guid_key; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.device_groups
    ADD CONSTRAINT device_groups_guid_key UNIQUE (guid);


--
-- Name: device_groups device_groups_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.device_groups
    ADD CONSTRAINT device_groups_pkey PRIMARY KEY (id);


--
-- Name: device_inventory device_inventory_device_id_key; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.device_inventory
    ADD CONSTRAINT device_inventory_device_id_key UNIQUE (device_id);


--
-- Name: device_inventory device_inventory_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.device_inventory
    ADD CONSTRAINT device_inventory_pkey PRIMARY KEY (id);


--
-- Name: device_telemetry device_telemetry_device_id_key; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.device_telemetry
    ADD CONSTRAINT device_telemetry_device_id_key UNIQUE (device_id);


--
-- Name: device_telemetry device_telemetry_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.device_telemetry
    ADD CONSTRAINT device_telemetry_pkey PRIMARY KEY (id);


--
-- Name: dlp_events dlp_events_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.dlp_events
    ADD CONSTRAINT dlp_events_pkey PRIMARY KEY (id);


--
-- Name: dlp_policies dlp_policies_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.dlp_policies
    ADD CONSTRAINT dlp_policies_pkey PRIMARY KEY (id);


--
-- Name: email_notification_log email_notification_log_event_type_event_id_recipient_key; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.email_notification_log
    ADD CONSTRAINT email_notification_log_event_type_event_id_recipient_key UNIQUE (event_type, event_id, recipient);


--
-- Name: email_notification_log email_notification_log_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.email_notification_log
    ADD CONSTRAINT email_notification_log_pkey PRIMARY KEY (id);


--
-- Name: folders folders_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.folders
    ADD CONSTRAINT folders_pkey PRIMARY KEY (id);


--
-- Name: login_attempts login_attempts_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.login_attempts
    ADD CONSTRAINT login_attempts_pkey PRIMARY KEY (id);


--
-- Name: network_checks network_checks_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.network_checks
    ADD CONSTRAINT network_checks_pkey PRIMARY KEY (id);


--
-- Name: network_targets network_targets_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.network_targets
    ADD CONSTRAINT network_targets_pkey PRIMARY KEY (id);


--
-- Name: notification_reads notification_reads_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.notification_reads
    ADD CONSTRAINT notification_reads_pkey PRIMARY KEY (user_id, notification_id);


--
-- Name: panel_access_policies panel_access_policies_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.panel_access_policies
    ADD CONSTRAINT panel_access_policies_pkey PRIMARY KEY (subject_type, subject_id, resource_key);


--
-- Name: panel_sessions panel_sessions_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.panel_sessions
    ADD CONSTRAINT panel_sessions_pkey PRIMARY KEY (sid);


--
-- Name: peer_metrics peer_metrics_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.peer_metrics
    ADD CONSTRAINT peer_metrics_pkey PRIMARY KEY (id);


--
-- Name: peer peer_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.peer
    ADD CONSTRAINT peer_pkey PRIMARY KEY (id);


--
-- Name: peer_sysinfo peer_sysinfo_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.peer_sysinfo
    ADD CONSTRAINT peer_sysinfo_pkey PRIMARY KEY (peer_id);


--
-- Name: pending_registrations pending_registrations_device_id_key; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.pending_registrations
    ADD CONSTRAINT pending_registrations_device_id_key UNIQUE (device_id);


--
-- Name: pending_registrations pending_registrations_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.pending_registrations
    ADD CONSTRAINT pending_registrations_pkey PRIMARY KEY (id);


--
-- Name: relay_sessions relay_sessions_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.relay_sessions
    ADD CONSTRAINT relay_sessions_pkey PRIMARY KEY (id);


--
-- Name: remote_commands remote_commands_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.remote_commands
    ADD CONSTRAINT remote_commands_pkey PRIMARY KEY (id);


--
-- Name: saved_reports saved_reports_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.saved_reports
    ADD CONSTRAINT saved_reports_pkey PRIMARY KEY (id);


--
-- Name: settings settings_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.settings
    ADD CONSTRAINT settings_pkey PRIMARY KEY (key);


--
-- Name: strategies strategies_guid_key; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.strategies
    ADD CONSTRAINT strategies_guid_key UNIQUE (guid);


--
-- Name: strategies strategies_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.strategies
    ADD CONSTRAINT strategies_pkey PRIMARY KEY (id);


--
-- Name: strategy_assignments strategy_assignments_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.strategy_assignments
    ADD CONSTRAINT strategy_assignments_pkey PRIMARY KEY (id);


--
-- Name: strategy_assignments strategy_assignments_target_type_target_key_key; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.strategy_assignments
    ADD CONSTRAINT strategy_assignments_target_type_target_key_key UNIQUE (target_type, target_key);


--
-- Name: studio_flows studio_flows_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.studio_flows
    ADD CONSTRAINT studio_flows_pkey PRIMARY KEY (id);


--
-- Name: tenant_devices tenant_devices_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.tenant_devices
    ADD CONSTRAINT tenant_devices_pkey PRIMARY KEY (id);


--
-- Name: tenant_devices tenant_devices_tenant_id_device_id_key; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.tenant_devices
    ADD CONSTRAINT tenant_devices_tenant_id_device_id_key UNIQUE (tenant_id, device_id);


--
-- Name: tenant_users tenant_users_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.tenant_users
    ADD CONSTRAINT tenant_users_pkey PRIMARY KEY (id);


--
-- Name: tenant_users tenant_users_tenant_id_user_id_key; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.tenant_users
    ADD CONSTRAINT tenant_users_tenant_id_user_id_key UNIQUE (tenant_id, user_id);


--
-- Name: tenants tenants_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.tenants
    ADD CONSTRAINT tenants_pkey PRIMARY KEY (id);


--
-- Name: tenants tenants_slug_key; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.tenants
    ADD CONSTRAINT tenants_slug_key UNIQUE (slug);


--
-- Name: ticket_attachments ticket_attachments_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.ticket_attachments
    ADD CONSTRAINT ticket_attachments_pkey PRIMARY KEY (id);


--
-- Name: ticket_comments ticket_comments_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.ticket_comments
    ADD CONSTRAINT ticket_comments_pkey PRIMARY KEY (id);


--
-- Name: tickets tickets_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.tickets
    ADD CONSTRAINT tickets_pkey PRIMARY KEY (id);


--
-- Name: user_avatars user_avatars_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.user_avatars
    ADD CONSTRAINT user_avatars_pkey PRIMARY KEY (user_id);


--
-- Name: user_group_members user_group_members_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.user_group_members
    ADD CONSTRAINT user_group_members_pkey PRIMARY KEY (user_group_id, user_id);


--
-- Name: user_groups user_groups_guid_key; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.user_groups
    ADD CONSTRAINT user_groups_guid_key UNIQUE (guid);


--
-- Name: user_groups user_groups_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.user_groups
    ADD CONSTRAINT user_groups_pkey PRIMARY KEY (id);


--
-- Name: user_peer_grants user_peer_grants_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.user_peer_grants
    ADD CONSTRAINT user_peer_grants_pkey PRIMARY KEY (user_id, peer_id);


--
-- Name: users users_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.users
    ADD CONSTRAINT users_pkey PRIMARY KEY (id);


--
-- Name: users users_username_key; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.users
    ADD CONSTRAINT users_username_key UNIQUE (username);


--
-- Name: idx_access_tokens_hash; Type: INDEX; Schema: public; Owner: -
--

CREATE UNIQUE INDEX idx_access_tokens_hash ON public.access_tokens USING btree (token_hash) WHERE (token_hash IS NOT NULL);


--
-- Name: idx_activity_device; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_activity_device ON public.activity_sessions USING btree (device_id);


--
-- Name: idx_activity_time; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_activity_time ON public.activity_sessions USING btree (started_at);


--
-- Name: idx_agent_bundle_builds_hash; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_agent_bundle_builds_hash ON public.agent_bundle_builds USING btree (branding_hash);


--
-- Name: idx_agent_bundle_builds_status; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_agent_bundle_builds_status ON public.agent_bundle_builds USING btree (status);


--
-- Name: idx_agent_bundles_bundle_id; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_agent_bundles_bundle_id ON public.agent_bundles USING btree (bundle_id);


--
-- Name: idx_agent_bundles_hash; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_agent_bundles_hash ON public.agent_bundles USING btree (branding_hash);


--
-- Name: idx_agent_bundles_slug; Type: INDEX; Schema: public; Owner: -
--

CREATE UNIQUE INDEX idx_agent_bundles_slug ON public.agent_bundles USING btree (slug) WHERE ((slug IS NOT NULL) AND (slug <> ''::text));


--
-- Name: idx_alert_history_device; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_alert_history_device ON public.alert_history USING btree (device_id);


--
-- Name: idx_alert_history_rule; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_alert_history_rule ON public.alert_history USING btree (rule_id);


--
-- Name: idx_audit_alarms_type; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_audit_alarms_type ON public.audit_alarms USING btree (alarm_type, created_at);


--
-- Name: idx_audit_conn_host; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_audit_conn_host ON public.audit_connections USING btree (host_id, created_at);


--
-- Name: idx_audit_conn_peer; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_audit_conn_peer ON public.audit_connections USING btree (peer_id, created_at);


--
-- Name: idx_audit_files_host; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_audit_files_host ON public.audit_files USING btree (host_id, created_at);


--
-- Name: idx_audit_log_action; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_audit_log_action ON public.audit_log USING btree (action, created_at);


--
-- Name: idx_audit_log_created; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_audit_log_created ON public.audit_log USING btree (created_at);


--
-- Name: idx_client_feature_policies_subject; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_client_feature_policies_subject ON public.client_feature_policies USING btree (subject_type, subject_id);


--
-- Name: idx_dlp_events_device; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_dlp_events_device ON public.dlp_events USING btree (device_id);


--
-- Name: idx_dlp_events_source; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_dlp_events_source ON public.dlp_events USING btree (event_source);


--
-- Name: idx_dlp_events_time; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_dlp_events_time ON public.dlp_events USING btree (created_at);


--
-- Name: idx_email_notification_log_event; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_email_notification_log_event ON public.email_notification_log USING btree (event_type, event_id);


--
-- Name: idx_network_checks_target; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_network_checks_target ON public.network_checks USING btree (target_id);


--
-- Name: idx_network_checks_time; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_network_checks_time ON public.network_checks USING btree (checked_at);


--
-- Name: idx_notification_reads_user; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_notification_reads_user ON public.notification_reads USING btree (user_id, read_at);


--
-- Name: idx_panel_access_policies_subject; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_panel_access_policies_subject ON public.panel_access_policies USING btree (subject_type, subject_id);


--
-- Name: idx_panel_sessions_active; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_panel_sessions_active ON public.panel_sessions USING btree (is_active, expires_at);


--
-- Name: idx_panel_sessions_expires; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_panel_sessions_expires ON public.panel_sessions USING btree (expires_at);


--
-- Name: idx_panel_sessions_user_active; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_panel_sessions_user_active ON public.panel_sessions USING btree (user_id, is_active);


--
-- Name: idx_peer_deleted; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_peer_deleted ON public.peer USING btree (is_deleted);


--
-- Name: idx_peer_metrics_peer_time; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_peer_metrics_peer_time ON public.peer_metrics USING btree (peer_id, created_at);


--
-- Name: idx_peer_online; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_peer_online ON public.peer USING btree (status_online) WHERE (NOT is_deleted);


--
-- Name: idx_pending_reg_device; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_pending_reg_device ON public.pending_registrations USING btree (device_id);


--
-- Name: idx_pending_reg_status; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_pending_reg_status ON public.pending_registrations USING btree (status);


--
-- Name: idx_relay_sessions_expires; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_relay_sessions_expires ON public.relay_sessions USING btree (expires_at);


--
-- Name: idx_remote_commands_device; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_remote_commands_device ON public.remote_commands USING btree (device_id, status);


--
-- Name: idx_saved_reports_type; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_saved_reports_type ON public.saved_reports USING btree (report_type);


--
-- Name: idx_strategy_assignments_strategy; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_strategy_assignments_strategy ON public.strategy_assignments USING btree (strategy_guid);


--
-- Name: idx_studio_flows_owner; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_studio_flows_owner ON public.studio_flows USING btree (created_by, updated_at);


--
-- Name: idx_tenant_devices_device; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_tenant_devices_device ON public.tenant_devices USING btree (device_id);


--
-- Name: idx_tenant_devices_tenant; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_tenant_devices_tenant ON public.tenant_devices USING btree (tenant_id);


--
-- Name: idx_tenant_users_tenant; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_tenant_users_tenant ON public.tenant_users USING btree (tenant_id);


--
-- Name: idx_token_hash_lookup; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_token_hash_lookup ON public.access_tokens USING btree (token_hash) WHERE (NOT revoked);


--
-- Name: idx_token_lookup; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_token_lookup ON public.access_tokens USING btree (token) WHERE (NOT revoked);


--
-- Name: idx_user_peer_grants_user; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_user_peer_grants_user ON public.user_peer_grants USING btree (user_id);


--
-- Name: access_tokens access_tokens_user_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.access_tokens
    ADD CONSTRAINT access_tokens_user_id_fkey FOREIGN KEY (user_id) REFERENCES public.users(id) ON DELETE CASCADE;


--
-- Name: alert_history alert_history_rule_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.alert_history
    ADD CONSTRAINT alert_history_rule_id_fkey FOREIGN KEY (rule_id) REFERENCES public.alert_rules(id) ON DELETE CASCADE;


--
-- Name: audit_log audit_log_user_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.audit_log
    ADD CONSTRAINT audit_log_user_id_fkey FOREIGN KEY (user_id) REFERENCES public.users(id) ON DELETE SET NULL;


--
-- Name: device_folder_assignments device_folder_assignments_folder_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.device_folder_assignments
    ADD CONSTRAINT device_folder_assignments_folder_id_fkey FOREIGN KEY (folder_id) REFERENCES public.folders(id) ON DELETE CASCADE;


--
-- Name: device_group_members device_group_members_device_group_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.device_group_members
    ADD CONSTRAINT device_group_members_device_group_id_fkey FOREIGN KEY (device_group_id) REFERENCES public.device_groups(id) ON DELETE CASCADE;


--
-- Name: device_group_user_access device_group_user_access_device_group_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.device_group_user_access
    ADD CONSTRAINT device_group_user_access_device_group_id_fkey FOREIGN KEY (device_group_id) REFERENCES public.device_groups(id) ON DELETE CASCADE;


--
-- Name: device_group_user_access device_group_user_access_user_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.device_group_user_access
    ADD CONSTRAINT device_group_user_access_user_id_fkey FOREIGN KEY (user_id) REFERENCES public.users(id) ON DELETE CASCADE;


--
-- Name: device_group_user_group_access device_group_user_group_access_device_group_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.device_group_user_group_access
    ADD CONSTRAINT device_group_user_group_access_device_group_id_fkey FOREIGN KEY (device_group_id) REFERENCES public.device_groups(id) ON DELETE CASCADE;


--
-- Name: device_group_user_group_access device_group_user_group_access_user_group_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.device_group_user_group_access
    ADD CONSTRAINT device_group_user_group_access_user_group_id_fkey FOREIGN KEY (user_group_id) REFERENCES public.user_groups(id) ON DELETE CASCADE;


--
-- Name: dlp_events dlp_events_policy_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.dlp_events
    ADD CONSTRAINT dlp_events_policy_id_fkey FOREIGN KEY (policy_id) REFERENCES public.dlp_policies(id) ON DELETE SET NULL;


--
-- Name: network_checks network_checks_target_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.network_checks
    ADD CONSTRAINT network_checks_target_id_fkey FOREIGN KEY (target_id) REFERENCES public.network_targets(id) ON DELETE CASCADE;


--
-- Name: notification_reads notification_reads_user_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.notification_reads
    ADD CONSTRAINT notification_reads_user_id_fkey FOREIGN KEY (user_id) REFERENCES public.users(id) ON DELETE CASCADE;


--
-- Name: tenant_devices tenant_devices_tenant_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.tenant_devices
    ADD CONSTRAINT tenant_devices_tenant_id_fkey FOREIGN KEY (tenant_id) REFERENCES public.tenants(id) ON DELETE CASCADE;


--
-- Name: tenant_users tenant_users_tenant_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.tenant_users
    ADD CONSTRAINT tenant_users_tenant_id_fkey FOREIGN KEY (tenant_id) REFERENCES public.tenants(id) ON DELETE CASCADE;


--
-- Name: ticket_attachments ticket_attachments_ticket_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.ticket_attachments
    ADD CONSTRAINT ticket_attachments_ticket_id_fkey FOREIGN KEY (ticket_id) REFERENCES public.tickets(id) ON DELETE CASCADE;


--
-- Name: ticket_comments ticket_comments_ticket_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.ticket_comments
    ADD CONSTRAINT ticket_comments_ticket_id_fkey FOREIGN KEY (ticket_id) REFERENCES public.tickets(id) ON DELETE CASCADE;


--
-- Name: user_avatars user_avatars_user_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.user_avatars
    ADD CONSTRAINT user_avatars_user_id_fkey FOREIGN KEY (user_id) REFERENCES public.users(id) ON DELETE CASCADE;


--
-- Name: user_group_members user_group_members_user_group_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.user_group_members
    ADD CONSTRAINT user_group_members_user_group_id_fkey FOREIGN KEY (user_group_id) REFERENCES public.user_groups(id) ON DELETE CASCADE;


--
-- Name: user_group_members user_group_members_user_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.user_group_members
    ADD CONSTRAINT user_group_members_user_id_fkey FOREIGN KEY (user_id) REFERENCES public.users(id) ON DELETE CASCADE;


--
-- Name: user_peer_grants user_peer_grants_user_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.user_peer_grants
    ADD CONSTRAINT user_peer_grants_user_id_fkey FOREIGN KEY (user_id) REFERENCES public.users(id) ON DELETE CASCADE;


--
-- PostgreSQL database dump complete
--

\unrestrict fNKrZNfG4fDXhXNvkBkHF30XjmvyLexKhIXBfRX6ggfDxyrz9YaSwMieE19y8cK

