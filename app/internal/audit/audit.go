// Package audit writes and lists audit_log entries (§7.1).
package audit

import (
	"context"
	"encoding/json"
	"net/netip"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

type Execer interface {
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
}

type Querier interface {
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

type Entry struct {
	Actor      uuid.NullUUID
	Action     string // e.g. "login.success", "user.role", "post.publish"
	EntityType string
	EntityID   string
	Diff       any // marshalled to jsonb; nil stores null
	IP         string
}

// Write inserts one entry. Pass the caller's tx so the entry commits with the change it records.
func Write(ctx context.Context, db Execer, e Entry) error {
	var diff []byte
	if e.Diff != nil {
		b, err := json.Marshal(e.Diff)
		if err != nil {
			return err
		}
		diff = b
	}
	var ip *netip.Addr
	if a, err := netip.ParseAddr(e.IP); err == nil {
		ip = &a
	}
	_, err := db.Exec(ctx, `
INSERT INTO audit_log (actor_user_id, action, entity_type, entity_id, diff, ip)
VALUES ($1, $2, $3, $4, $5, $6)`, e.Actor, e.Action, e.EntityType, e.EntityID, diff, ip)
	return err
}

// Change is a before/after pair for one field, the shape used by every diff.
type Change struct {
	From any `json:"from"`
	To   any `json:"to"`
}

type Row struct {
	ID         int64           `json:"id"`
	ActorID    *string         `json:"actor_id"`
	ActorName  *string         `json:"actor_name"`
	Action     string          `json:"action"`
	EntityType string          `json:"entity_type"`
	EntityID   string          `json:"entity_id"`
	Diff       json.RawMessage `json:"diff"`
	IP         *string         `json:"ip"`
	CreatedAt  time.Time       `json:"created_at"`
}

type Filter struct {
	Actor      string // uuid
	Action     string // exact or prefix ending in "."
	EntityType string
	EntityID   string
	From, To   *time.Time
	Page, Size int
}

const sqlList = `
SELECT a.id, a.actor_user_id::text, u.username, a.action, a.entity_type, a.entity_id, a.diff, host(a.ip), a.created_at,
       count(*) OVER ()
FROM audit_log a LEFT JOIN users u ON u.id = a.actor_user_id
WHERE ($1 = '' OR a.actor_user_id::text = $1)
  AND ($2 = '' OR a.action = $2 OR (right($2, 1) = '.' AND starts_with(a.action, $2)))
  AND ($3 = '' OR a.entity_type = $3)
  AND ($4 = '' OR a.entity_id = $4)
  AND ($5::timestamptz IS NULL OR a.created_at >= $5)
  AND ($6::timestamptz IS NULL OR a.created_at < $6)
ORDER BY a.created_at DESC, a.id DESC
LIMIT $7 OFFSET $8`

func List(ctx context.Context, db Querier, f Filter) ([]Row, int64, error) {
	rows, err := db.Query(ctx, sqlList, f.Actor, f.Action, f.EntityType, f.EntityID, f.From, f.To, f.Size, (f.Page-1)*f.Size)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	out := []Row{}
	var total int64
	for rows.Next() {
		var r Row
		var diff []byte
		if err := rows.Scan(&r.ID, &r.ActorID, &r.ActorName, &r.Action, &r.EntityType, &r.EntityID, &diff, &r.IP,
			&r.CreatedAt, &total); err != nil {
			return nil, 0, err
		}
		if diff != nil {
			r.Diff = diff
		}
		out = append(out, r)
	}
	return out, total, rows.Err()
}
