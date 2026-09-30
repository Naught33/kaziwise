package store

import (
	"context"
	"fmt"

	"github.com/google/uuid"

	"github.com/kaziwise/kaziwise_backend/internal/domain"
)

const assetCols = `
	id, org_id, uploaded_by, kind, status, original_name, storage_path, bucket,
	mime_type, size_bytes, checksum, page_count, chapter_count, parse_error,
	created_at, updated_at`

func scanAsset(row interface{ Scan(...any) error }) (*domain.Asset, error) {
	var a domain.Asset
	err := row.Scan(&a.ID, &a.OrgID, &a.UploadedBy, &a.Kind, &a.Status, &a.OriginalName,
		&a.StoragePath, &a.Bucket, &a.MimeType, &a.SizeBytes, &a.Checksum,
		&a.PageCount, &a.ChapterCount, &a.ParseError, &a.CreatedAt, &a.UpdatedAt)
	if err != nil {
		return nil, mapErr(err)
	}
	return &a, nil
}

type CreateAssetParams struct {
	OrgID        uuid.UUID
	UploadedBy   *uuid.UUID
	Kind         domain.AssetKind
	OriginalName string
	StoragePath  string
	Bucket       string
	MimeType     *string
	SizeBytes    int64
	Checksum     *string
	PageCount    int
	ChapterCount int
	Status       domain.AssetStatus
	ParseError   *string
}

func (db *DB) CreateAsset(ctx context.Context, p CreateAssetParams) (*domain.Asset, error) {
	if p.Status == "" {
		p.Status = domain.AssetReady
	}
	return scanAsset(db.pool.QueryRow(ctx, `
		insert into assets
			(org_id, uploaded_by, kind, status, original_name, storage_path, bucket,
			 mime_type, size_bytes, checksum, page_count, chapter_count, parse_error)
		values ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13)
		returning `+assetCols,
		p.OrgID, p.UploadedBy, p.Kind, p.Status, p.OriginalName, p.StoragePath,
		p.Bucket, p.MimeType, p.SizeBytes, p.Checksum, p.PageCount, p.ChapterCount,
		p.ParseError))
}

// ReplaceAssetPages rewrites the parsed page/chapter map for an asset.
// It runs in one transaction with the asset's own update so a re-parse
// never leaves a half-written page list.
func (db *DB) ReplaceAssetPages(ctx context.Context, orgID, assetID uuid.UUID, pages []domain.AssetPage, pageCount, chapterCount int) error {
	return db.inTx(ctx, func(tx pgxTx) error {
		if _, err := tx.Exec(ctx, `delete from asset_pages where asset_id = $1 and org_id = $2`,
			assetID, orgID); err != nil {
			return mapErr(err)
		}
		batch := &pgxBatch{}
		for _, p := range pages {
			batch.add(`insert into asset_pages
				(asset_id, org_id, page_number, is_blank, chapter_index, chapter_title, text_content)
				values ($1,$2,$3,$4,$5,$6,$7)`,
				assetID, orgID, p.PageNumber, p.IsBlank, p.ChapterIndex,
				textPtr(p.ChapterTitle), textPtr(p.TextContent))
		}
		if err := sendAll(ctx, tx, batch); err != nil {
			return err
		}
		_, err := tx.Exec(ctx, `
			update assets set page_count = $3, chapter_count = $4, updated_at = now()
			where id = $1 and org_id = $2`, assetID, orgID, pageCount, chapterCount)
		return mapErr(err)
	})
}

func (db *DB) AssetByID(ctx context.Context, orgID, id uuid.UUID) (*domain.Asset, error) {
	return scanAsset(db.pool.QueryRow(ctx,
		`select `+assetCols+` from assets where org_id = $1 and id = $2`, orgID, id))
}

func (db *DB) DeleteAsset(ctx context.Context, orgID, id uuid.UUID) (*domain.Asset, error) {
	a, err := db.AssetByID(ctx, orgID, id)
	if err != nil {
		return nil, err
	}
	// Block deletion while a lesson still references the file, so a
	// course cannot be left pointing at a missing asset.
	var refs int
	if err := db.pool.QueryRow(ctx,
		`select count(*) from blocks where asset_id = $1`, id).Scan(&refs); err != nil {
		return nil, mapErr(err)
	}
	if refs > 0 {
		return nil, fmt.Errorf("%w: asset is used by %d lesson block(s); remove it from the course first", ErrConflict, refs)
	}
	if _, err := db.pool.Exec(ctx, `delete from assets where org_id = $1 and id = $2`, orgID, id); err != nil {
		return nil, mapErr(err)
	}
	return a, nil
}

func (db *DB) AssetPages(ctx context.Context, orgID, assetID uuid.UUID) ([]domain.AssetPage, error) {
	rows, err := db.pool.Query(ctx, `
		select id, asset_id, page_number, is_blank, chapter_index, chapter_title, text_content
		from asset_pages where org_id = $1 and asset_id = $2 order by page_number`, orgID, assetID)
	if err != nil {
		return nil, mapErr(err)
	}
	defer rows.Close()
	var out []domain.AssetPage
	for rows.Next() {
		var p domain.AssetPage
		if err := rows.Scan(&p.ID, &p.AssetID, &p.PageNumber, &p.IsBlank,
			&p.ChapterIndex, &p.ChapterTitle, &p.TextContent); err != nil {
			return nil, mapErr(err)
		}
		out = append(out, p)
	}
	return out, mapErr(rows.Err())
}

type AssetListFilter struct {
	Search  string
	Kind    string
	Status  string
	Page    int
	PerPage int
}

func (db *DB) ListAssets(ctx context.Context, orgID uuid.UUID, f AssetListFilter) ([]domain.Asset, int, error) {
	b := orgScope(orgID)
	if f.Search != "" {
		b.add("original_name ilike "+b.arg(), "%"+f.Search+"%")
	}
	if f.Kind != "" {
		b.add("kind = "+b.arg(), f.Kind)
	}
	if f.Status != "" {
		b.add("status = "+b.arg(), f.Status)
	}
	base := `select ` + assetCols + ` from assets` + b.whereClause() +
		` order by created_at desc`
	countSQL := `select count(*) from assets` + b.whereClause()

	rows, total, err := db.paginate(ctx, db.pool, base, countSQL, b.values(), f.PerPage, (f.Page-1)*f.PerPage)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	out := []domain.Asset{}
	for rows.Next() {
		a, err := scanAsset(rows)
		if err != nil {
			return nil, 0, err
		}
		out = append(out, *a)
	}
	return out, total, mapErr(rows.Err())
}

// ---------------------------------------------------------------------
// Block page progress
// ---------------------------------------------------------------------

// RecordBlockPageSeen is idempotent; re-reading a page is a no-op.
func (db *DB) RecordBlockPageSeen(ctx context.Context, orgID, blockID, learnerID uuid.UUID, page int) error {
	_, err := db.pool.Exec(ctx, `
		insert into block_progress (block_id, learner_id, org_id, page_number)
		values ($1,$2,$3,$4)
		on conflict (block_id, learner_id, page_number) do nothing`,
		blockID, learnerID, orgID, page)
	return mapErr(err)
}

func (db *DB) SeenBlockPages(ctx context.Context, orgID, blockID, learnerID uuid.UUID) ([]int, error) {
	rows, err := db.pool.Query(ctx, `
		select page_number from block_progress
		where org_id = $1 and block_id = $2 and learner_id = $3
		order by page_number`, orgID, blockID, learnerID)
	if err != nil {
		return nil, mapErr(err)
	}
	defer rows.Close()
	out := []int{}
	for rows.Next() {
		var n int
		if err := rows.Scan(&n); err != nil {
			return nil, mapErr(err)
		}
		out = append(out, n)
	}
	return out, mapErr(rows.Err())
}

// ---------------------------------------------------------------------
// Audit log
// ---------------------------------------------------------------------

type AuditParams struct {
	OrgID     *uuid.UUID
	ActorID   *uuid.UUID
	Action    string
	Entity    string
	EntityID  *string
	Meta      map[string]any
	IP        *string
	UserAgent *string
}

func (db *DB) Audit(ctx context.Context, p AuditParams) {
	meta := "{}"
	if len(p.Meta) > 0 {
		if b, err := jsonMarshal(p.Meta); err == nil {
			meta = string(b)
		}
	}
	// Auditing must never break the request that produced it.
	_, _ = db.pool.Exec(ctx, `
		insert into audit_logs (org_id, actor_id, action, entity, entity_id, meta, ip, user_agent)
		values ($1,$2,$3,$4,$5,$6::jsonb,$7,$8)`,
		p.OrgID, p.ActorID, p.Action, p.Entity, p.EntityID, meta, p.IP, p.UserAgent)
}

func (db *DB) ListAudit(ctx context.Context, orgID uuid.UUID, page, perPage int, entity string) ([]domain.AuditEntry, int, error) {
	b := orgScope(orgID)
	if entity != "" {
		b.add("entity = "+b.arg(), entity)
	}
	base := `select id, org_id, actor_id, action, entity, entity_id, meta, ip, user_agent, created_at
		from audit_logs` + b.whereClause() + ` order by created_at desc, id desc`
	countSQL := `select count(*) from audit_logs` + b.whereClause()

	rows, total, err := db.paginate(ctx, db.pool, base, countSQL, b.values(), perPage, (page-1)*perPage)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	out := []domain.AuditEntry{}
	for rows.Next() {
		var e domain.AuditEntry
		if err := rows.Scan(&e.ID, &e.OrgID, &e.ActorID, &e.Action, &e.Entity, &e.EntityID,
			&e.Meta, &e.IP, &e.UserAgent, &e.CreatedAt); err != nil {
			return nil, 0, mapErr(err)
		}
		out = append(out, e)
	}
	return out, total, mapErr(rows.Err())
}
