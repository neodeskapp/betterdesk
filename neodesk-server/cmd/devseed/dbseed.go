package main

import (
	"context"
	"database/sql"
	"fmt"
	"io"
	"slices"
	"strconv"
	"strings"

	"github.com/unitronix/betterdesk-server/console/store"
)

// The rows that go straight into the database: devices, help requests,
// pending enrollments, chat, inventory, activity and device history
// (seedGoDB), LAN registrations, tickets, audit, sign-in and session
// history (seedAuthDB), and billing sessions (seedBillingSessions). Each
// part replaces the demo rows an earlier run wrote instead of adding more.

func (d *demo) seedGoDB(ctx context.Context, w *writer, out io.Writer) error {
	cols, err := w.columns(ctx, "peers")
	if err != nil {
		return err
	}
	hasDeviceType := slices.Contains(cols, "device_type")

	err = w.db.InTx(ctx, func(tx *store.Tx) error {
		for _, p := range d.peerRows() {
			row := []col{
				{"id", p.ID}, {"uuid", p.UUID}, {"ip", p.IP}, {"user", p.User}, {"hostname", p.Hostname},
				{"os", p.OS}, {"version", p.Version}, {"status", "OFFLINE"}, {"last_online", p.LastOnline},
				{"created_at", p.CreatedAt}, {"banned", flag(p.Banned)}, {"ban_reason", p.BanReason},
				{"banned_at", p.BannedAt}, {"soft_deleted", flag(p.SoftDeleted)}, {"deleted_at", p.DeletedAt},
				{"note", p.Note}, {"tags", ""},
			}
			if hasDeviceType {
				row = append(row, col{"device_type", p.DeviceType})
			}
			if err := w.insert(ctx, tx, "peers", row, `ON CONFLICT(id) DO UPDATE SET hostname = excluded.hostname, os = excluded.os,
				version = excluded.version, "user" = excluded."user", last_online = excluded.last_online,
				banned = excluded.banned, ban_reason = excluded.ban_reason, banned_at = excluded.banned_at,
				soft_deleted = excluded.soft_deleted, deleted_at = excluded.deleted_at, note = excluded.note`); err != nil {
				return err
			}
		}

		const upsertConfig = `ON CONFLICT(key) DO UPDATE SET value = excluded.value`
		for _, p := range pendingDevices {
			if err := w.insert(ctx, tx, "server_config", []col{{"key", "pending_device_" + p.ID}, {"value", d.pendingJSON(p)}}, upsertConfig); err != nil {
				return err
			}
		}
		// The device page only shows availability history when this is on.
		if err := w.insert(ctx, tx, "server_config", []col{{"key", "availability_history_enabled"}, {"value", "true"}}, upsertConfig); err != nil {
			return err
		}

		if _, err := tx.Exec(ctx, `DELETE FROM help_requests WHERE device_id LIKE '9001000%'`); err != nil {
			return fmt.Errorf("clear the demo help requests: %w", err)
		}
		for i, h := range helpRequests {
			if err := w.insert(ctx, tx, "help_requests", []col{
				{"device_id", h.DeviceID}, {"hostname", h.Hostname}, {"message", h.Message}, {"status", h.Status},
				{"handled_by", h.HandledBy}, {"created_at", d.ago(float64(20 + i*90))}, {"updated_at", d.ago(float64(10 + i*60))},
			}, ""); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return err
	}

	if err := d.seedChat(ctx, w, out); err != nil {
		return err
	}
	if err := d.seedInventory(ctx, w, out); err != nil {
		return err
	}
	if err := d.seedActivity(ctx, w, out); err != nil {
		return err
	}
	if err := d.seedHistory(ctx, w, out); err != nil {
		return err
	}
	fmt.Fprintf(out, "Go DB: %d devices, %d pending enrollments, %d help requests\n", len(devices), len(pendingDevices), len(helpRequests))
	return nil
}

func (d *demo) seedChat(ctx context.Context, w *writer, out io.Writer) error {
	if !w.has("chat_messages") {
		return nil
	}
	var convs, texts []string
	for _, c := range conversations {
		convs = append(convs, c.ID)
		for _, m := range c.Messages {
			texts = append(texts, m.Text)
		}
	}
	err := w.db.InTx(ctx, func(tx *store.Tx) error {
		if _, err := tx.Exec(ctx, `DELETE FROM chat_messages WHERE conversation_id IN (`+marks(len(convs))+`)
			AND text IN (`+marks(len(texts))+`)`, append(anys(convs), anys(texts)...)...); err != nil {
			return fmt.Errorf("clear the demo chat: %w", err)
		}
		for _, c := range conversations {
			if c.Group != nil && w.has("chat_groups") {
				if err := w.insert(ctx, tx, "chat_groups", []col{
					{"id", c.ID}, {"name", c.Group.Name}, {"members", c.Group.Members}, {"created_by", "admin"},
					{"created_at", d.ago(4 * 24 * 60)},
				}, `ON CONFLICT(id) DO UPDATE SET name = excluded.name, members = excluded.members`); err != nil {
					return err
				}
			}
			for _, m := range d.chatRows(c) {
				if err := w.insert(ctx, tx, "chat_messages", []col{
					{"conversation_id", m.Conversation}, {"from_id", m.FromID}, {"from_name", m.FromName},
					{"to_id", m.To}, {"text", m.Text}, {"read", flag(m.Read)}, {"created_at", m.CreatedAt},
				}, ""); err != nil {
					return err
				}
			}
		}
		return nil
	})
	if err != nil {
		return err
	}
	fmt.Fprintf(out, "Go DB: %d chat conversations, %d messages\n", len(conversations), len(texts))
	return nil
}

func (d *demo) seedInventory(ctx context.Context, w *writer, out io.Writer) error {
	if !w.has("device_inventory") {
		return nil
	}
	telemetry := w.has("device_telemetry")
	err := w.db.InTx(ctx, func(tx *store.Tx) error {
		for _, r := range d.inventoryRows() {
			if err := w.insert(ctx, tx, "device_inventory", []col{
				{"device_id", r.DeviceID}, {"hardware", jsonText(r.Hardware)}, {"software", jsonText(r.Software)},
				{"collected_at", r.CollectedAt}, {"received_at", r.ReceivedAt},
			}, `ON CONFLICT(device_id) DO UPDATE SET hardware = excluded.hardware, software = excluded.software,
				collected_at = excluded.collected_at, received_at = excluded.received_at`); err != nil {
				return err
			}
			if !telemetry {
				continue
			}
			if err := w.insert(ctx, tx, "device_telemetry", []col{
				{"device_id", r.DeviceID}, {"cpu_usage_percent", r.CPU}, {"memory_used_bytes", r.MemoryUsed},
				{"memory_total_bytes", r.MemoryTotal}, {"uptime_secs", r.Uptime}, {"timestamp", r.CollectedAt},
				{"received_at", r.ReceivedAt},
			}, `ON CONFLICT(device_id) DO UPDATE SET cpu_usage_percent = excluded.cpu_usage_percent,
				memory_used_bytes = excluded.memory_used_bytes, memory_total_bytes = excluded.memory_total_bytes,
				uptime_secs = excluded.uptime_secs, timestamp = excluded.timestamp, received_at = excluded.received_at`); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return err
	}
	fmt.Fprintf(out, "Go DB: inventory and telemetry for %d devices\n", len(inventoryDevices))
	return nil
}

func (d *demo) seedActivity(ctx context.Context, w *writer, out io.Writer) error {
	if !w.has("activity_sessions") || !w.has("activity_summaries") {
		return nil
	}
	count := 0
	err := w.db.InTx(ctx, func(tx *store.Tx) error {
		ids := anys(activityDevices)
		for _, table := range []string{"activity_sessions", "activity_summaries"} {
			if _, err := tx.Exec(ctx, `DELETE FROM `+table+` WHERE device_id IN (`+marks(len(ids))+`)`, ids...); err != nil {
				return fmt.Errorf("clear the demo %s: %w", table, err)
			}
		}
		for _, id := range activityDevices {
			sessions, summaries := d.activityRows(id)
			for _, s := range sessions {
				if err := w.insert(ctx, tx, "activity_sessions", []col{
					{"device_id", s.DeviceID}, {"app_name", s.App}, {"window_title", s.Title}, {"category", s.Category},
					{"started_at", s.StartedAt}, {"ended_at", s.EndedAt}, {"duration_secs", s.Duration}, {"created_at", s.CreatedAt},
				}, ""); err != nil {
					return err
				}
				count++
			}
			for _, s := range summaries {
				if err := w.insert(ctx, tx, "activity_summaries", []col{
					{"device_id", s.DeviceID}, {"idle_seconds", s.Idle}, {"session_count", s.Sessions},
					{"total_active_secs", s.Active}, {"reported_at", s.ReportedAt}, {"received_at", s.ReceivedAt},
				}, ""); err != nil {
					return err
				}
			}
		}
		return nil
	})
	if err != nil {
		return err
	}
	fmt.Fprintf(out, "Go DB: %d activity sessions for %d devices\n", count, len(activityDevices))
	return nil
}

func (d *demo) seedHistory(ctx context.Context, w *writer, out io.Writer) error {
	want := []string{"peer_availability_events", "peer_connection_sessions", "peer_metrics"}
	var missing []string
	for _, t := range want {
		if !w.has(t) {
			missing = append(missing, t)
		}
	}
	if len(missing) > 0 {
		fmt.Fprintf(out, "Go DB: history tables missing, skipped: %s\n", strings.Join(missing, ", "))
		return nil
	}
	var ids []string
	for _, h := range historyDevices {
		ids = append(ids, h.ID)
	}
	var events, metrics, sessions int
	err := w.db.InTx(ctx, func(tx *store.Tx) error {
		for _, clear := range []string{
			`DELETE FROM peer_availability_events WHERE peer_id IN (` + marks(len(ids)) + `)`,
			`DELETE FROM peer_metrics WHERE peer_id IN (` + marks(len(ids)) + `)`,
			`DELETE FROM peer_connection_sessions WHERE host_id IN (` + marks(len(ids)) + `)`,
		} {
			if _, err := tx.Exec(ctx, clear, anys(ids)...); err != nil {
				return fmt.Errorf("clear the demo history: %w", err)
			}
		}
		for _, h := range historyDevices {
			evs, ms, ss := d.historyRows(h)
			for _, e := range evs {
				if err := w.insert(ctx, tx, "peer_availability_events", []col{
					{"peer_id", e.PeerID}, {"from_status", e.From}, {"to_status", e.To}, {"source", e.Source}, {"created_at", e.CreatedAt},
				}, ""); err != nil {
					return err
				}
				events++
			}
			for _, m := range ms {
				if err := w.insert(ctx, tx, "peer_metrics", []col{
					{"peer_id", m.PeerID}, {"cpu_usage", m.CPU}, {"memory_usage", m.Mem}, {"disk_usage", m.Disk}, {"created_at", m.CreatedAt},
				}, ""); err != nil {
					return err
				}
				metrics++
			}
			for _, s := range ss {
				if err := w.insert(ctx, tx, "peer_connection_sessions", []col{
					{"host_id", s.HostID}, {"peer_id", s.PeerID}, {"peer_name", s.PeerName}, {"peer_ip", s.PeerIP},
					{"username", s.Username}, {"transport", s.Transport}, {"relay_uuid", s.RelayUUID},
					{"started_at", s.StartedAt}, {"ended_at", s.EndedAt}, {"source", "signal"},
				}, ""); err != nil {
					return err
				}
				sessions++
			}
		}
		return nil
	})
	if err != nil {
		return err
	}
	fmt.Fprintf(out, "Go DB history for %d devices: %d availability events, %d metric samples, %d connection sessions\n",
		len(ids), events, metrics, sessions)
	return nil
}

// userIDs are the ids of the accounts there are, by name.
func userIDs(ctx context.Context, db *store.DB) (map[string]float64, error) {
	rows, err := db.Query(ctx, `SELECT id, username FROM users`)
	if err != nil {
		return nil, fmt.Errorf("list the users: %w", err)
	}
	defer rows.Close()
	ids := map[string]float64{}
	for rows.Next() {
		var id int64
		var name string
		if err := rows.Scan(&id, &name); err != nil {
			return nil, fmt.Errorf("list the users: %w", err)
		}
		ids[name] = float64(id)
	}
	return ids, rows.Err()
}

func (d *demo) seedAuthDB(ctx context.Context, w *writer, label string, out io.Writer) error {
	users := map[string]float64{}
	if w.has("users") {
		var err error
		if users, err = userIDs(ctx, w.db); err != nil {
			return err
		}
	}
	userOrNull := func(name string) any {
		switch name {
		case "":
			return nil
		case "1":
			return 1.0
		}
		if id, ok := users[name]; ok && id != 0 {
			return id
		}
		return nil
	}

	if w.has("pending_registrations") {
		if _, err := w.db.Exec(ctx, `DELETE FROM pending_registrations WHERE device_id LIKE '9003%'`); err != nil {
			return fmt.Errorf("clear the demo registrations: %w", err)
		}
		for _, p := range lanPendings {
			at := d.ago(p.MinutesAgo)
			if err := w.insert(ctx, w.db, "pending_registrations", []col{
				{"device_id", p.ID}, {"hostname", p.Hostname}, {"platform", p.Platform}, {"version", p.Version},
				{"ip_address", p.IP}, {"status", "pending"}, {"created_at", at}, {"updated_at", at},
			}, ""); err != nil {
				return err
			}
		}
	}

	if w.has("tickets") {
		if err := d.seedTickets(ctx, w); err != nil {
			return err
		}
	}

	if w.has("audit_log") {
		details := make([]string, len(auditEntries))
		for i, e := range auditEntries {
			details[i] = e.Details
		}
		if _, err := w.db.Exec(ctx, `DELETE FROM audit_log WHERE details IN (`+marks(len(details))+`)`, anys(details)...); err != nil {
			return fmt.Errorf("clear the demo audit log: %w", err)
		}
		for _, e := range auditEntries {
			if err := w.insert(ctx, w.db, "audit_log", []col{
				{"user_id", userOrNull(e.User)}, {"action", e.Action}, {"details", e.Details},
				{"ip_address", e.IP}, {"created_at", d.ago(e.MinutesAgo)},
			}, ""); err != nil {
				return err
			}
		}
	}

	if w.has("login_attempts") {
		if _, err := w.db.Exec(ctx, `DELETE FROM login_attempts WHERE ip_address IN ('203.0.113.45', '198.51.100.7', '10.0.2.41', '10.0.3.12')`); err != nil {
			return fmt.Errorf("clear the demo sign-ins: %w", err)
		}
		for _, a := range loginAttempts {
			if err := w.insert(ctx, w.db, "login_attempts", []col{
				{"username", a.Username}, {"ip_address", a.IP}, {"success", flag(a.Success)}, {"created_at", d.ago(a.MinutesAgo)},
			}, ""); err != nil {
				return err
			}
		}
	}

	if w.has("panel_sessions") {
		cols, err := w.columns(ctx, "panel_sessions")
		if err != nil {
			return err
		}
		if slices.Contains(cols, "is_active") && slices.Contains(cols, "end_reason") {
			if _, err := w.db.Exec(ctx, `DELETE FROM panel_sessions WHERE sid LIKE 'demo-ended-%'`); err != nil {
				return fmt.Errorf("clear the demo sessions: %w", err)
			}
			for i, s := range endedSessions {
				id, ok := users[s.Username]
				if !ok || id == 0 {
					continue
				}
				created := d.now - mul(s.StartMinutes, minute)
				ended := created + mul(s.LengthMinutes, minute)
				if err := w.insert(ctx, w.db, "panel_sessions", []col{
					{"sid", "demo-ended-" + s.Username + "-" + strconv.Itoa(i)}, {"session_json", "{}"},
					{"expires_at", stamp{ended, epochMillis}}, {"updated_at", stamp{ended, sqlSeconds}},
					{"user_id", id}, {"username", s.Username}, {"ip_address", s.IP}, {"user_agent", s.UserAgent},
					{"client_fingerprint", "demo" + strconv.FormatUint(uint64(seedFor(s.Username+strconv.Itoa(i))), 16)},
					{"created_at", stamp{created, epochMillis}}, {"last_activity_at", stamp{ended - 2*minute, epochMillis}},
					{"ended_at", stamp{ended, epochMillis}}, {"end_reason", s.Reason}, {"is_active", flag(false)},
				}, ""); err != nil {
					return err
				}
			}
		}
	}
	fmt.Fprintf(out, "Auth DB (%s): %d LAN registrations, %d tickets, audit, login and session history\n", label, len(lanPendings), len(tickets))
	return nil
}

func (d *demo) seedTickets(ctx context.Context, w *writer) error {
	titles := make([]string, len(tickets))
	for i, t := range tickets {
		titles[i] = t.Title
	}
	comments := w.has("ticket_comments")
	return w.db.InTx(ctx, func(tx *store.Tx) error {
		old, err := selectIDs(ctx, tx, `SELECT id FROM tickets WHERE title IN (`+marks(len(titles))+`)`, anys(titles)...)
		if err != nil {
			return fmt.Errorf("find the demo tickets: %w", err)
		}
		if len(old) > 0 {
			if comments {
				if _, err := tx.Exec(ctx, `DELETE FROM ticket_comments WHERE ticket_id IN (`+marks(len(old))+`)`, old...); err != nil {
					return fmt.Errorf("clear the demo ticket comments: %w", err)
				}
			}
			if _, err := tx.Exec(ctx, `DELETE FROM tickets WHERE id IN (`+marks(len(old))+`)`, old...); err != nil {
				return fmt.Errorf("clear the demo tickets: %w", err)
			}
		}
		for _, t := range d.ticketRows() {
			id, err := w.insertID(ctx, tx, "tickets", []col{
				{"title", t.Title}, {"description", t.Description}, {"status", t.Status}, {"priority", t.Priority},
				{"category", t.Category}, {"device_id", t.Device}, {"created_by", t.CreatedBy}, {"assigned_to", t.AssignedTo},
				{"sla_due_at", t.SLADueAt}, {"resolved_at", t.ResolvedAt}, {"closed_at", t.ClosedAt},
				{"created_at", t.CreatedAt}, {"updated_at", t.UpdatedAt},
			})
			if err != nil {
				return err
			}
			if !comments {
				continue
			}
			for _, c := range t.Comments {
				if err := w.insert(ctx, tx, "ticket_comments", []col{
					{"ticket_id", float64(id)}, {"author", c.Author}, {"body", c.Body}, {"is_internal", flag(c.Internal)},
					{"created_at", c.CreatedAt},
				}, ""); err != nil {
					return err
				}
			}
		}
		return nil
	})
}

// selectIDs reads one column of ids.
func selectIDs(ctx context.Context, q store.Querier, query string, args ...any) ([]any, error) {
	rows, err := q.Query(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var ids []any
	for rows.Next() {
		var id any
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

// contractTerms is a contract the API made, with its package.
type contractTerms struct {
	Org string
	ID  any
	Pkg pkgTerms
}

// seedBillingSessions writes the sessions and work reports, which have no
// API (the relay opens them), once the contracts exist, and keeps the
// contract counters in line with them.
func (d *demo) seedBillingSessions(ctx context.Context, w *writer, orgIDs map[string]any, made []contractTerms, out io.Writer) error {
	if !w.has("billing_sessions") {
		return nil
	}
	period := isoText(d.now)[:7]
	terms := map[string]pkgTerms{}
	contractIDs := map[string]any{}
	for _, c := range made {
		terms[c.Org] = c.Pkg
		contractIDs[c.Org] = c.ID
	}
	rows, used := d.billingRows(orgIDs, terms, contractIDs)
	reports, ledger := w.has("billing_work_reports"), w.has("billing_session_ledger")
	err := w.db.InTx(ctx, func(tx *store.Tx) error {
		old, err := selectIDs(ctx, tx, `SELECT id FROM billing_sessions WHERE id LIKE 'demo-bs-%'`)
		if err != nil {
			return fmt.Errorf("find the demo billing sessions: %w", err)
		}
		if len(old) > 0 {
			m := marks(len(old))
			if reports {
				if _, err := tx.Exec(ctx, `DELETE FROM billing_work_reports WHERE session_id IN (`+m+`)`, old...); err != nil {
					return fmt.Errorf("clear the demo work reports: %w", err)
				}
			}
			if ledger {
				if _, err := tx.Exec(ctx, `DELETE FROM billing_session_ledger WHERE session_id IN (`+m+`)`, old...); err != nil {
					return fmt.Errorf("clear the demo billing ledger: %w", err)
				}
			}
			if _, err := tx.Exec(ctx, `DELETE FROM billing_sessions WHERE id IN (`+m+`)`, old...); err != nil {
				return fmt.Errorf("clear the demo billing sessions: %w", err)
			}
		}
		for _, r := range rows {
			// The running session has no end: '' on SQLite, because the Go
			// session list scans ended_at into a string there, NULL in a
			// PostgreSQL timestamp column.
			var ended any = r.EndedAt
			if s, ok := ended.(string); ok && s == "" && w.db.IsPostgres() {
				ended = nil
			}
			if err := w.insert(ctx, tx, "billing_sessions", []col{
				{"id", r.ID}, {"org_id", r.OrgID}, {"contract_id", r.ContractID}, {"operator_id", r.Operator},
				{"operator_name", r.Operator}, {"device_id", r.DeviceID}, {"device_name", r.DeviceName},
				{"relay_uuid", r.RelayUUID}, {"transport", "rustdesk"}, {"status", r.Status}, {"billing_phase", r.Phase},
				{"started_at", r.StartedAt}, {"ended_at", ended}, {"raw_seconds", r.RawSeconds},
				{"billed_minutes", r.Billed}, {"included_minutes_used", r.Included}, {"overage_minutes", r.Overage},
				{"amount_included", 0.0}, {"amount_overage", r.Amount}, {"currency", r.Currency},
				{"created_at", r.CreatedAt}, {"updated_at", r.UpdatedAt},
			}, ""); err != nil {
				return err
			}
			if ledger {
				for _, l := range r.Ledger {
					if err := w.insert(ctx, tx, "billing_session_ledger", []col{
						{"session_id", r.ID}, {"event_type", l.Event}, {"details", l.Details}, {"created_at", l.At},
					}, ""); err != nil {
						return err
					}
				}
			}
			if reports && r.Report != nil {
				if err := w.insert(ctx, tx, "billing_work_reports", []col{
					{"session_id", r.ID}, {"operator_id", r.Operator}, {"summary", r.Report.Summary},
					{"category", r.Report.Category}, {"ticket_ref", r.Report.TicketRef}, {"created_at", r.ReportAt},
				}, ""); err != nil {
					return err
				}
			}
		}

		// Keep the contract counters in line with the sessions above.
		for _, c := range made {
			inc := usedSoFar(used, c.Org)
			idArg, err := w.args("billing_sessions", []col{{"contract_id", c.ID}})
			if err != nil {
				return err
			}
			overMinutes, overAmount, err := overageOf(ctx, tx, idArg[0])
			if err != nil {
				return err
			}
			set, err := w.args("billing_contracts", []col{
				{"remaining_minutes", jsMax(0, c.Pkg.IncludedMinutes-inc)}, {"current_period_ym", period}, {"id", c.ID},
			})
			if err != nil {
				return err
			}
			if _, err := tx.Exec(ctx, `UPDATE billing_contracts SET remaining_minutes = ?, current_period_ym = ?, updated_at = `+
				store.NowSQL(tx.Dialect())+` WHERE id = ?`, set...); err != nil {
				return fmt.Errorf("update the contract counters: %w", err)
			}
			if !w.has("billing_period_totals") {
				continue
			}
			if err := w.insert(ctx, tx, "billing_period_totals", []col{
				{"contract_id", c.ID}, {"package_id", c.Pkg.ID}, {"period_ym", period}, {"timezone", "UTC"},
				{"included_minutes_used", inc}, {"overage_minutes", overMinutes}, {"amount_overage", overAmount},
				{"included_limit_snapshot", c.Pkg.IncludedMinutes}, {"updated_at", now{}},
			}, `ON CONFLICT(contract_id, period_ym) DO UPDATE SET included_minutes_used = excluded.included_minutes_used,
				overage_minutes = excluded.overage_minutes, amount_overage = excluded.amount_overage,
				included_limit_snapshot = excluded.included_limit_snapshot, updated_at = excluded.updated_at`); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return err
	}
	fmt.Fprintf(out, "Go DB: %d billing sessions with work reports\n", len(rows))
	return nil
}

// overageOf sums the overage of a contract's demo sessions, as numbers.
func overageOf(ctx context.Context, q store.Querier, contractID any) (minutes, amount float64, err error) {
	var m, a sql.NullFloat64
	err = q.QueryRow(ctx, `SELECT COALESCE(SUM(overage_minutes), 0), COALESCE(SUM(amount_overage), 0)
		FROM billing_sessions WHERE contract_id = ? AND id LIKE 'demo-bs-%'`, contractID).Scan(&m, &a)
	if err != nil {
		return 0, 0, fmt.Errorf("sum the overage of contract %v: %w", contractID, err)
	}
	return m.Float64, a.Float64, nil
}
