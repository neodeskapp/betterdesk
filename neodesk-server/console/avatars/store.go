package avatars

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5/pgconn"

	"github.com/unitronix/betterdesk-server/console/store"
)

// Avatar is a stored picture: a row of user_avatars.
type Avatar struct {
	UserID   int64
	MimeType string
	Data     []byte
	// Version is ImageVersion of Data, as stored.
	Version string
	// Source is "oidc" for a picture from the identity provider and
	// "profile" for one from the avatar address of the profile.
	Source    string
	SourceURL store.NullString
	UpdatedAt store.Time
}

// getUserAvatar returns the picture of a user, or nil when there is none.
func getUserAvatar(ctx context.Context, q store.Querier, userID int64) (*Avatar, error) {
	a := &Avatar{}
	err := store.SelectRow(ctx, q, `SELECT user_id, mime_type, data, version, source, source_url, updated_at
		FROM user_avatars WHERE user_id = ?`, userID).
		Scan(&a.UserID, &a.MimeType, &a.Data, &a.Version, &a.Source, &a.SourceURL, &a.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read the picture of user %d: %w", userID, err)
	}
	return a, nil
}

// setUserAvatar stores a user's picture in place of the one they had.
func setUserAvatar(ctx context.Context, q store.Querier, userID int64, p Picture, source string) error {
	_, err := q.Exec(ctx, `INSERT INTO user_avatars (user_id, mime_type, data, version, source, source_url, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, `+store.NowSQL(q.Dialect())+`)
		ON CONFLICT (user_id) DO UPDATE SET mime_type = excluded.mime_type, data = excluded.data,
			version = excluded.version, source = excluded.source, source_url = excluded.source_url,
			updated_at = excluded.updated_at`,
		userID, p.MimeType, p.Data, p.Version, source, p.SourceURL)
	if err != nil {
		return fmt.Errorf("store the picture of user %d: %w", userID, err)
	}
	return nil
}

// deleteUserAvatar removes a user's picture and returns the number of
// rows removed. With a source, the picture goes only when it came from
// there.
func deleteUserAvatar(ctx context.Context, q store.Querier, userID int64, source string) (int64, error) {
	var (
		res sql.Result
		err error
	)
	if source != "" {
		res, err = q.Exec(ctx, `DELETE FROM user_avatars WHERE user_id = ? AND source = ?`, userID, source)
	} else {
		res, err = q.Exec(ctx, `DELETE FROM user_avatars WHERE user_id = ?`, userID)
	}
	if err != nil {
		return 0, fmt.Errorf("remove the picture of user %d: %w", userID, err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("remove the picture of user %d: %w", userID, err)
	}
	return n, nil
}

// DirectoryEntry is one user with a picture, as the list of pictures
// names them.
type DirectoryEntry struct {
	ID            int64  `json:"id"`
	Username      string `json:"username"`
	AvatarVersion string `json:"avatar_version"`
}

// listUserAvatarVersions returns every user with a picture, by user id.
func listUserAvatarVersions(ctx context.Context, q store.Querier) ([]DirectoryEntry, error) {
	rows, err := q.Query(ctx, `SELECT a.user_id, u.username, a.version FROM user_avatars a
		INNER JOIN users u ON u.id = a.user_id ORDER BY a.user_id`)
	if err != nil {
		return nil, fmt.Errorf("list the pictures: %w", err)
	}
	defer rows.Close()
	entries := []DirectoryEntry{}
	for rows.Next() {
		var e DirectoryEntry
		if err := rows.Scan(&e.ID, &e.Username, &e.AvatarVersion); err != nil {
			return nil, fmt.Errorf("read a picture entry: %w", err)
		}
		entries = append(entries, e)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list the pictures: %w", err)
	}
	return entries, nil
}

// sqliteCodes are the names of the SQLite result codes a write of a
// picture can end with.
var sqliteCodes = map[int]string{
	1:    "SQLITE_ERROR",
	5:    "SQLITE_BUSY",
	8:    "SQLITE_READONLY",
	13:   "SQLITE_FULL",
	19:   "SQLITE_CONSTRAINT",
	787:  "SQLITE_CONSTRAINT_FOREIGNKEY",
	1299: "SQLITE_CONSTRAINT_NOTNULL",
	1555: "SQLITE_CONSTRAINT_PRIMARYKEY",
	2067: "SQLITE_CONSTRAINT_UNIQUE",
}

// databaseErrorCode is the code the database driver of the old console
// gave a failure: the SQLSTATE on PostgreSQL ("23503" for a user that
// does not exist), the name of the result code on SQLite
// ("SQLITE_CONSTRAINT_FOREIGNKEY"). It is "" for other errors.
func databaseErrorCode(err error) string {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		return pgErr.Code
	}
	var coded interface{ Code() int }
	if errors.As(err, &coded) {
		return sqliteCodes[coded.Code()]
	}
	return ""
}
