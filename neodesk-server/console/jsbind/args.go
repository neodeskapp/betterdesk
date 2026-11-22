package jsbind

import (
	"errors"
	"fmt"
	"math"
	"strconv"
	"strings"

	"github.com/jackc/pgx/v5/pgconn"

	"github.com/unitronix/betterdesk-server/console/store"
)

// The errors better-sqlite3 threw before a statement ran. Their texts are
// the messages of the JavaScript errors.
var (
	// ErrBindType is the TypeError for a value SQLite has no type for: a
	// boolean, a list inside a list.
	ErrBindType = errors.New("SQLite3 can only bind numbers, strings, bigints, buffers, and null")
	// ErrTooFewValues and ErrTooManyValues are the RangeErrors for a
	// number of values that is not the number of placeholders.
	ErrTooFewValues  = errors.New("Too few parameter values were provided")
	ErrTooManyValues = errors.New("Too many parameter values were provided")
	// ErrTwoObjects is the TypeError for a second object among the values.
	ErrTwoObjects = errors.New("You cannot specify named parameters in two different objects")
)

// Args returns the parameters of a statement with the given number of
// placeholders, as the driver of the dialect bound the values.
//
// On SQLite an error is what better-sqlite3 threw instead of running the
// statement. A list gives one parameter per element, and an object gives
// none, because the driver read it as named parameters, which the
// console's statements do not have. On PostgreSQL every value becomes
// text or NULL and there is no error: the server reports a text it cannot
// read when the statement runs.
func Args(dialect store.Dialect, placeholders int, values ...Value) ([]any, error) {
	if dialect == store.Postgres {
		args := make([]any, len(values))
		for i, v := range values {
			args[i] = postgresArg(v)
		}
		return args, nil
	}
	args := make([]any, 0, len(values))
	object := false
	for _, v := range values {
		switch v.kind {
		case kindArray:
			for _, item := range v.items {
				arg, err := sqliteArg(item)
				if err != nil {
					return nil, err
				}
				args = append(args, arg)
			}
		case kindObject:
			if v.complex {
				return nil, ErrBindType
			}
			if object {
				return nil, ErrTwoObjects
			}
			object = true
		default:
			arg, err := sqliteArg(v)
			if err != nil {
				return nil, err
			}
			args = append(args, arg)
		}
	}
	switch {
	case len(args) < placeholders:
		return nil, ErrTooFewValues
	case len(args) > placeholders:
		return nil, ErrTooManyValues
	}
	return args, nil
}

// sqliteArg is one value as better-sqlite3 bound it: every number as a
// double, which SQLite stores as NULL when it is NaN.
func sqliteArg(v Value) (any, error) {
	switch v.kind {
	case kindUndefined, kindNull:
		return nil, nil
	case kindNumber:
		if math.IsNaN(v.num) {
			return nil, nil
		}
		return v.num, nil
	case kindString:
		return v.str, nil
	}
	return nil, ErrBindType
}

// postgresArg is one value as node-postgres sent it: null and undefined
// as NULL, a list as an array literal, an object as JSON, everything else
// as its text.
func postgresArg(v Value) any {
	switch v.kind {
	case kindUndefined, kindNull:
		return nil
	case kindArray:
		return postgresArray(v)
	case kindObject:
		return v.JSON()
	}
	return v.ToString()
}

func postgresArray(v Value) string {
	var b strings.Builder
	b.WriteByte('{')
	for i, item := range v.items {
		if i > 0 {
			b.WriteByte(',')
		}
		switch item.kind {
		case kindUndefined, kindNull:
			b.WriteString("NULL")
		case kindArray:
			b.WriteString(postgresArray(item))
		default:
			text, _ := postgresArg(item).(string)
			b.WriteByte('"')
			b.WriteString(strings.NewReplacer(`\`, `\\`, `"`, `\"`).Replace(text))
			b.WriteByte('"')
		}
	}
	b.WriteByte('}')
	return b.String()
}

// Message is err.message as a route's catch block saw it: the text of a
// driver error without what Go wrapped around it. Routes that decided by
// the message (err.message.includes('UNIQUE')) decide by this one.
//
// For a PostgreSQL error it is the server's message. For a SQLite error
// it is the engine's message, which names the broken constraint the same
// way in both drivers ("UNIQUE constraint failed: tenants.slug").
func Message(err error) string {
	if err == nil {
		return ""
	}
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		return pgErr.Message
	}
	for _, own := range []error{ErrBindType, ErrTooFewValues, ErrTooManyValues, ErrTwoObjects} {
		if errors.Is(err, own) {
			return own.Error()
		}
	}
	return err.Error()
}

// Cell is a column whose value may have any type: what SQLite keeps in an
// INTEGER column when it was given a fraction or text that is no number,
// and what the driver then handed to JSON. On PostgreSQL a column has its
// one type and a Cell reads it the same way.
//
// The JSON is a number for an integer or a double (null for an infinity),
// the text for text, true or false for a boolean and null for NULL. Use
// store.Time for times and store.Int64String where a PostgreSQL bigint
// has to come out as a string.
type Cell struct {
	v any
}

// Scan implements sql.Scanner.
func (c *Cell) Scan(src any) error {
	switch v := src.(type) {
	case nil, int64, float64, string, bool:
		c.v = v
	case []byte:
		c.v = string(v)
	default:
		return fmt.Errorf("jsbind: cannot scan %T into a Cell", src)
	}
	return nil
}

// Value is the column as a JavaScript value, for the decisions a route
// made on it (row.max_devices || 0).
func (c Cell) Value() Value {
	switch v := c.v.(type) {
	case int64:
		return Number(float64(v))
	case float64:
		return Number(v)
	case string:
		return String(v)
	case bool:
		return Bool(v)
	}
	return Null
}

// CellOf is a Cell that holds v: nil, an int64, a float64, a string or a
// bool.
func CellOf(v any) Cell { return Cell{v: v} }

// maxSafeInteger is the largest whole number a double holds exactly.
const maxSafeInteger = 1<<53 - 1

// MarshalJSON implements json.Marshaler.
func (c Cell) MarshalJSON() ([]byte, error) {
	switch v := c.v.(type) {
	case int64:
		if v > maxSafeInteger || v < -maxSafeInteger {
			return []byte(NumberString(float64(v))), nil
		}
		return strconv.AppendInt(nil, v, 10), nil
	case float64, string, bool:
		return []byte(c.Value().JSON()), nil
	}
	return []byte("null"), nil
}
