#!/bin/sh
# Helpers for database addresses in the start scripts. A PostgreSQL URL
# carries the user name and the password; these functions give the parts
# that may appear in a log line.

# dsn_authority DSN: host[:port] of a URL, after the last @ and before the
# path and the query.
dsn_authority() {
    _dsn_rest="${1#*://}"
    _dsn_rest="${_dsn_rest%%\?*}"
    case "$_dsn_rest" in
        *@*) _dsn_rest="${_dsn_rest##*@}" ;;
    esac
    printf '%s\n' "${_dsn_rest%%/*}"
}

# dsn_for_log DSN: the database address without user name, password and
# query string (postgres://host:port/name), a SQLite path as it is, and
# for a key=value connection string only that it is one.
dsn_for_log() {
    case "$1" in
        *://*)
            _dsn_rest="${1#*://}"
            _dsn_rest="${_dsn_rest%%\?*}"
            case "$_dsn_rest" in
                *@*) _dsn_rest="${_dsn_rest##*@}" ;;
            esac
            printf '%s://%s\n' "${1%%://*}" "$_dsn_rest"
            ;;
        *=*)
            printf '%s\n' "(connection string, not shown)"
            ;;
        *)
            printf '%s\n' "$1"
            ;;
    esac
}

# dsn_host DSN: the host of a URL (without the brackets of an IPv6 address).
dsn_host() {
    _dsn_auth=$(dsn_authority "$1")
    case "$_dsn_auth" in
        \[*\]*) _dsn_auth="${_dsn_auth#\[}"; printf '%s\n' "${_dsn_auth%%\]*}" ;;
        *) printf '%s\n' "${_dsn_auth%%:*}" ;;
    esac
}

# dsn_port DSN: the port of a URL, 5432 when it names none.
dsn_port() {
    _dsn_auth=$(dsn_authority "$1")
    _dsn_auth="${_dsn_auth##*\]}"
    case "$_dsn_auth" in
        *:*) _dsn_port="${_dsn_auth##*:}" ;;
        *) _dsn_port="" ;;
    esac
    printf '%s\n' "${_dsn_port:-5432}"
}
