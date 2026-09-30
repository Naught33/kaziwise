-- =====================================================================
-- KaziWise LMS - 0003
--
-- The create paths for attempts, campaigns, certificates, lessons, blocks
-- and questions all return their new row with `insert ... returning
-- <cols>`, but those projections were table-aliased (`b.id, b.title, ...`).
-- An INSERT has no table alias in scope, so every one of those endpoints
-- failed with:
--
--   ERROR: missing FROM-clause entry for table "b"  (SQLSTATE 42P01)
--
-- That is a 500 on course/block/question/campaign/attempt/certificate
-- creation, which is why the builder could not save a new block.
--
-- This migration only adds the bookkeeping columns the code already
-- expects. Nothing here is a behavioural change to existing data.
-- =====================================================================

-- Scope note: the repository layer stamps updated_at on exactly these
-- tables - assets, assignments, attempt_answers, blocks, campaigns, lessons,
-- modules and questions. Every one of them already declares the column
-- except assignments, so this migration adds that one plus the other child
-- tables whose domain structs carry the field.
--
-- assignments: several progress rollups stamp updated_at on every touch.
-- The column was never created, so refreshAssignmentProgress -- which runs
-- inside StartLesson, CompleteLesson and every attempt -- aborted with
--   ERROR: column "updated_at" of relation "assignments" does not exist
-- and the surrounding transaction was rolled back. That is the 500 on
-- POST /v1/me/lessons/{id}/start.
alter table assignments
  add column if not exists updated_at timestamptz not null default now();

-- asset_pages: ReplaceAssetPages stamps the asset row, but the page rows
-- were written without one. Kept for parity with the other child tables.
alter table asset_pages
  add column if not exists updated_at timestamptz not null default now();

-- attempt_answers already has it; the rest of the progress/child tables
-- are appended so the schema matches what the repository layer writes.
alter table block_progress
  add column if not exists updated_at timestamptz not null default now();

alter table lesson_progress
  add column if not exists updated_at timestamptz not null default now();

-- questions: ListQuestions orders by q.created_at, and the create path
-- stamps updated_at through the same projection.
alter table question_options
  add column if not exists updated_at timestamptz not null default now();

-- certificates: revoked/issued bookkeeping.
alter table certificates
  add column if not exists updated_at timestamptz not null default now();

-- attempts: started/submitted/graded transitions.
alter table attempts
  add column if not exists updated_at timestamptz not null default now();

-- audit_logs and reminders are append-only but the domain structs carry
-- created_at/updated_at, so keep the projection honest.
alter table audit_logs
  add column if not exists updated_at timestamptz not null default now();

alter table reminders
  add column if not exists updated_at timestamptz not null default now();

alter table campaign_audience
  add column if not exists updated_at timestamptz not null default now();

alter table departments
  add column if not exists updated_at timestamptz not null default now();

alter table invites
  add column if not exists updated_at timestamptz not null default now();
