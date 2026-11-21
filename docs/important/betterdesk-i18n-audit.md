# BetterDesk i18n audit

- Strict audit command: `npm run i18n:check` from repo root; it runs `scripts/i18n-check.js --system console`.
- The web console's language files are in `betterdesk-server/console/static/files/lang` (26 locales). The baseline is the union of the EN and PL keys.
- Run without `--system`, the script also audits the agent client (`betterdesk-agent-client/src/locales`) and MGMT (`betterdesk-mgmt/src/locales`) locales when those folders exist. Neither is in this repository, so it skips them.
- Missing keys, extra keys, empty values and files that don't parse fail the audit. Values still in English are counted but don't fail it; `--verbose` lists them.
- `POST /api/panel/languages/:code/fix` is disabled on purpose (it answers 410), so English fallback values are never inserted into target locales.
