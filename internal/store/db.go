// Package store is the Postgres data-access layer. Every method that
// touches tenant data takes an orgID, so organisation isolation is
// enforced in one place rather than being left to each call site.
package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/kaziwise/kaziwise_backend/internal/config"
)

// DB wraps the connection pool.
type DB struct {
	pool *pgxpool.Pool
	log  *slog.Logger
}

// ErrNotFound is returned when a scoped lookup matches no row.
var ErrNotFound = errors.New("record not found")

// ErrConflict is returned on unique-constraint violations.
var ErrConflict = errors.New("record already exists")

func Connect(ctx context.Context, cfg *config.Config, log *slog.Logger) (*DB, error) {
	pc, err := pgxpool.ParseConfig(cfg.DatabaseURL)
	if err != nil {
		return nil, fmt.Errorf("parse DATABASE_URL: %w", err)
	}
	pc.MaxConns = cfg.DBMaxConns
	pc.MinConns = cfg.DBMinConns
	pc.MaxConnLifetime = time.Hour
	pc.MaxConnIdleTime = 15 * time.Minute
	pc.HealthCheckPeriod = 30 * time.Second
	// Supabase's transaction pooler (port 6543) hands the same client
	// connection to different backends, so a session-level prepared
	// statement can be prepared twice on one backend and every cached
	// statement risks 42P05. "exec" keeps parameters server-side without
	// naming a statement, which works on both a direct connection and any
	// pooler; a direct or session-pooler URI may use "cache_statement"
	// instead.
	switch cfg.DBQueryMode {
	case "cache_statement":
		pc.ConnConfig.DefaultQueryExecMode = pgx.QueryExecModeCacheStatement
	case "cache_describe":
		pc.ConnConfig.DefaultQueryExecMode = pgx.QueryExecModeCacheDescribe
	case "describe_exec":
		pc.ConnConfig.DefaultQueryExecMode = pgx.QueryExecModeDescribeExec
	case "exec":
		pc.ConnConfig.DefaultQueryExecMode = pgx.QueryExecModeExec
	case "simple_protocol":
		pc.ConnConfig.DefaultQueryExecMode = pgx.QueryExecModeSimpleProtocol
	}
	if cfg.DBStatementTO > 0 {
		pc.ConnConfig.RuntimeParams["statement_timeout"] =
			fmt.Sprintf("%d", cfg.DBStatementTO.Milliseconds())
		pc.ConnConfig.RuntimeParams["application_name"] = "kaziwise-api"
	}

	deadline := time.Now().Add(cfg.DBConnectRetry)
	for {
		pool, err := pgxpool.NewWithConfig(ctx, pc)
		if err == nil {
			pingCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
			err = pool.Ping(pingCtx)
			cancel()
			if err == nil {
				return &DB{pool: pool, log: log}, nil
			}
			pool.Close()
		}
		if time.Now().After(deadline) {
			return nil, fmt.Errorf("database unreachable after %s: %w", cfg.DBConnectRetry, err)
		}
		log.Warn("database not ready, retrying", "error", err)
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(2 * time.Second):
		}
	}
}

func (db *DB) Pool() *pgxpool.Pool { return db.pool }

func (db *DB) Close() { db.pool.Close() }

func (db *DB) Ping(ctx context.Context) error { return db.pool.Ping(ctx) }

// ---------------------------------------------------------------------
// Transaction helper
// ---------------------------------------------------------------------

// inTx runs fn inside a transaction, rolling back on error or panic.
func (db *DB) inTx(ctx context.Context, fn func(pgxTx) error) error {
	tx, err := db.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.ReadCommitted})
	if err != nil {
		return err
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback(context.WithoutCancel(ctx))
		}
	}()
	if err := fn(tx); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return err
	}
	committed = true
	return nil
}

// InTx is the public entry point used by the service layer.
func (db *DB) InTx(ctx context.Context, fn func(pgxTx) error) error { return db.inTx(ctx, fn) }

// ---------------------------------------------------------------------
// Error mapping
// ---------------------------------------------------------------------

// mapErr converts driver errors into the store's sentinel errors.
func mapErr(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	}
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		switch pgErr.Code {
		case "23505": // unique_violation
			return fmt.Errorf("%w: %s", ErrConflict, constraintMessage(pgErr))
		case "23503": // foreign_key_violation
			return fmt.Errorf("%w: referenced record does not exist (%s)", ErrConflict, pgErr.ConstraintName)
		case "23514": // check_violation
			return fmt.Errorf("check constraint %q violated", pgErr.ConstraintName)
		}
	}
	return err
}

func constraintMessage(pgErr *pgconn.PgError) string {
	if pgErr.ConstraintName != "" {
		return pgErr.ConstraintName
	}
	return pgErr.Detail
}

// ---------------------------------------------------------------------
// Small SQL helpers
// ---------------------------------------------------------------------

// listBuilder accumulates the WHERE/ORDER/LIMIT clauses shared by the
// list endpoints, so filtering always includes the org scope.
type listBuilder struct {
	where []string
	args  []any
}

func (l *listBuilder) add(clause string, args ...any) *listBuilder {
	l.where = append(l.where, clause)
	l.args = append(l.args, args...)
	return l
}

// arg returns the placeholder for the next query argument, e.g. "$3".
// Clauses must embed it themselves: add() never formats its input, which
// is why call sites use this instead of a "%d" verb.
func (l *listBuilder) arg() string { return "$" + itoa(len(l.args)+1) }

func (l *listBuilder) whereClause() string {
	if len(l.where) == 0 {
		return ""
	}
	return " where " + strings.Join(l.where, " and ")
}

func (l *listBuilder) values() []any { return l.args }

// orgScope is the mandatory tenant filter, always the first clause.
func orgScope(orgID any) *listBuilder {
	return (&listBuilder{}).add("org_id = $1", orgID)
}

// orgScopeAs is orgScope for a query whose main table is aliased. Every
// table in this schema carries org_id, so a query that joins two of them and
// leaves the column unqualified is rejected by Postgres as ambiguous. The
// alias keeps the tenant filter on the intended table.
func orgScopeAs(alias string, orgID any) *listBuilder {
	return (&listBuilder{}).add(alias+".org_id = $1", orgID)
}

// paginate applies limit/offset and returns the rows plus a total.
func (db *DB) paginate(ctx context.Context, q pgxQuerier, baseSQL, countSQL string, args []any, limit, offset int) (pgx.Rows, int, error) {
	// A filter built by hand rather than by httpx.ParsePage leaves Page at
	// its zero value, which turns (page-1)*perPage negative; Postgres
	// refuses a negative offset. Page 0 means the first page.
	if offset < 0 {
		offset = 0
	}
	var total int
	if err := q.QueryRow(ctx, countSQL, args...).Scan(&total); err != nil {
		return nil, 0, mapErr(err)
	}
	// $N placeholders after the caller's args.
	listArgs := append(append([]any{}, args...), limit, offset)
	li := len(args) + 1
	sql := fmt.Sprintf("%s limit $%d offset $%d", baseSQL, li, li+1)
	rows, err := q.Query(ctx, sql, listArgs...)
	if err != nil {
		return nil, 0, mapErr(err)
	}
	return rows, total, nil
}

// pgxQuerier is satisfied by both *pgxpool.Pool and pgx.Tx, which lets the
// same repository methods run inside or outside a transaction.
type pgxQuerier interface {
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
}

// pgxTx is the transaction subset the repository helpers need.
type pgxTx interface {
	pgxQuerier
	SendBatch(ctx context.Context, b *pgx.Batch) pgx.BatchResults
	Commit(ctx context.Context) error
	Rollback(ctx context.Context) error
}

// pgxBatch accumulates statements for one bulk round trip.
type pgxBatch struct {
	queue *pgx.Batch
	n     int
}

func (b *pgxBatch) add(sql string, args ...any) {
	if b.queue == nil {
		b.queue = &pgx.Batch{}
	}
	b.queue.Queue(sql, args...)
	b.n++
}

func (b *pgxBatch) count() int { return b.n }

func (b *pgxBatch) build() *pgx.Batch {
	if b.queue == nil {
		return &pgx.Batch{}
	}
	return b.queue
}

// sendAll runs the batch and closes the results, surfacing the first
// error. Closing is required: without it the connection is not returned to
// the pool until the results are drained.
func sendAll(ctx context.Context, tx pgxTx, b *pgxBatch) error {
	results := tx.SendBatch(ctx, b.build())
	defer results.Close()
	for i := 0; i < b.count(); i++ {
		if _, err := results.Exec(); err != nil {
			return mapErr(err)
		}
	}
	return nil
}

func jsonMarshal(v any) ([]byte, error) { return json.Marshal(v) }

// textPtr / timePtr convert optional values into query arguments.
func textPtr(s *string) any {
	if s == nil {
		return nil
	}
	return *s
}

func timePtr(t *time.Time) any {
	if t == nil {
		return nil
	}
	return *t
}

// prefixCols qualifies bare column names in a where clause with a table
// alias, so a shared clause builder can be reused across queries whose
// columns live on different tables.
func prefixCols(clauses []string, cols ...string) []string {
	out := make([]string, len(clauses))
	for i, c := range clauses {
		for _, col := range cols {
			c = strings.ReplaceAll(c, col+" ", "c."+col+" ")
			c = strings.ReplaceAll(c, col+"=", "c."+col+"=")
			c = strings.ReplaceAll(c, col+" ilike", "c."+col+" ilike")
		}
		out[i] = c
	}
	return out
}

// itoa is a tiny helper for building numbered placeholders.
func itoa(n int) string { return strconv.Itoa(n) }

// uuidStrings renders a UUID slice for use as an array query argument.
//
// The connection deliberately runs in pgx's "exec" mode (see Connect) so
// that prepared statements survive Supabase's transaction pooler, and exec
// mode never issues a Describe. Without it pgx has no server-supplied type
// for any parameter, so it encodes as text with an unknown type (OID 0). A
// bare uuid.UUID still encodes because pgx has a registered codec for the
// underlying [16]byte, but []uuid.UUID has no resolvable array element type
// at OID 0 and fails with "cannot find encode plan" before the query is
// sent. Strings have a registered text[] codec, so passing []string works
// under every exec mode; the ::uuid[] cast on the parameter lets the server
// coerce it back.
func uuidStrings(ids []uuid.UUID) []string {
	out := make([]string, len(ids))
	for i, id := range ids {
		out[i] = id.String()
	}
	return out
}

func round2(f float64) float64 { return math.Round(f*100) / 100 }

// nextPosition returns the next ordinal for a sibling row set. The result
// is advisory: positions are only ever compared for ordering.
func (db *DB) nextPosition(ctx context.Context, sql string, args ...any) int {
	var n int
	if err := db.pool.QueryRow(ctx, sql, args...).Scan(&n); err != nil || n < 1 {
		return 1
	}
	return n
}
