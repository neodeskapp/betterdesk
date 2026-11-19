package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
)

func runDemo(ctx context.Context, args []string, getenv func(string) string, out, errOut io.Writer) error {
	f, err := parseFlags(args,
		[]string{"go-db", "auth-db", "panel", "admin-user", "admin-password", "cookie-file", "online-file", "now"},
		[]string{"force"})
	if err != nil {
		return err
	}
	port := getenv("PORT")
	if port == "" {
		port = "5000"
	}
	goDB := databaseFrom(f.get("go-db", ""), getenv)
	panel := f.get("panel", "http://127.0.0.1:"+port)
	adminUser := f.get("admin-user", "admin")
	adminPassword := f.get("admin-password", getenv("ADMIN_PASSWORD"))
	cookieFile := f.get("cookie-file", "")
	onlineFile := f.get("online-file", "/app/data/dev-online-ids.json")
	if getenv("NODE_ENV") == "production" && !f.switches["force"] {
		return errors.New("refusing to seed with NODE_ENV=production; pass --force for a throwaway container")
	}
	if adminPassword == "" && cookieFile == "" {
		return errors.New("pass --cookie-file <storageState.json> or --admin-password (or set ADMIN_PASSWORD)")
	}
	at, err := seedTime(f.get("now", ""))
	if err != nil {
		return err
	}
	d := newDemo(at)

	goStore, err := openDatabase(ctx, goDB, true)
	if err != nil {
		return err
	}
	defer goStore.Close()
	mainWriter, err := newWriter(ctx, goStore.DB)
	if err != nil {
		return err
	}
	if err := d.seedGoDB(ctx, mainWriter, out); err != nil {
		return err
	}

	// Newer installs keep the console tables in the server's database;
	// only older SQLite ones still have a separate auth.db.
	auth, authWriter := goStore, mainWriter
	if authPath := authDatabase(f.get("auth-db", ""), goDB, getenv); authPath != goDB {
		if auth, err = openDatabase(ctx, authPath, true); err != nil {
			return err
		}
		defer auth.Close()
		if authWriter, err = newWriter(ctx, auth.DB); err != nil {
			return err
		}
	}
	if err := d.seedAuthDB(ctx, authWriter, auth.label, out); err != nil {
		return err
	}

	c := newConsoleClient(panel, out, errOut)
	if err := c.signIn(ctx, cookieFile, adminUser, adminPassword); err != nil {
		return err
	}
	if err := d.seedViaAPI(ctx, c); err != nil {
		return err
	}
	orgIDs, err := d.seedOrganizations(ctx, c)
	if err != nil {
		return err
	}
	made, err := d.seedBilling(ctx, c, orgIDs)
	if err != nil {
		return err
	}
	if err := mainWriter.refresh(ctx); err != nil {
		return err
	}
	if err := d.seedBillingSessions(ctx, mainWriter, orgIDs, made, out); err != nil {
		return err
	}
	if err := d.seedSavedReports(ctx, c); err != nil {
		return err
	}
	ids := onlineIDs()
	if err := os.WriteFile(onlineFile, []byte(mustStringify(anys(ids))), 0o666); err != nil {
		return fmt.Errorf("write the online ids: %w", err)
	}
	fmt.Fprintf(out, "%d device IDs for heartbeats written to %s\n", len(ids), onlineFile)
	return nil
}

// authDatabase is where the console's user tables are: --auth-db, else an
// older install's auth.db (AUTH_DB_PATH, default /app/data/auth.db) when
// the file exists, else the server's database. PostgreSQL has only the
// one.
func authDatabase(flag, goDB string, getenv func(string) string) string {
	if flag != "" {
		return flag
	}
	if postgresURL.MatchString(goDB) {
		return goDB
	}
	legacy := getenv("AUTH_DB_PATH")
	if legacy == "" {
		legacy = "/app/data/auth.db"
	}
	if _, err := os.Stat(legacy); err == nil {
		return legacy
	}
	return goDB
}
