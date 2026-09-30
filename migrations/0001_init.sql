-- =====================================================================
-- KaziWise LMS - initial schema
-- Target: Supabase Postgres (works on any Postgres 14+)
-- Multi-tenancy: every tenant-owned row carries org_id and is filtered
-- by it in the application layer. Row Level Security policies are
-- supplied for direct table access from the Supabase dashboard/PostgREST.
-- =====================================================================

create extension if not exists "pgcrypto";

-- ---------------------------------------------------------------------
-- Enumerated states
-- ---------------------------------------------------------------------
do $$ begin
  create type user_role     as enum ('super_admin', 'org_admin', 'manager', 'learner');
exception when duplicate_object then null; end $$;

do $$ begin
  create type user_status   as enum ('active', 'inactive', 'invited');
exception when duplicate_object then null; end $$;

do $$ begin
  create type course_status as enum ('draft', 'published', 'archived');
exception when duplicate_object then null; end $$;

do $$ begin
  create type lesson_kind   as enum ('content', 'assessment', 'mixed');
exception when duplicate_object then null; end $$;

do $$ begin
  create type block_type    as enum ('text', 'video', 'image', 'pdf', 'pptx', 'file', 'embed', 'quiz');
exception when duplicate_object then null; end $$;

do $$ begin
  create type asset_kind    as enum ('pdf', 'pptx', 'image', 'video', 'document', 'other');
exception when duplicate_object then null; end $$;

do $$ begin
  create type asset_status  as enum ('uploaded', 'processing', 'ready', 'failed');
exception when duplicate_object then null; end $$;

do $$ begin
  create type question_type as enum ('single_choice', 'multi_choice', 'short_text', 'long_text');
exception when duplicate_object then null; end $$;

do $$ begin
  create type campaign_status as enum ('draft', 'active', 'closed');
exception when duplicate_object then null; end $$;

do $$ begin
  create type audience_type as enum ('all', 'department', 'users');
exception when duplicate_object then null; end $$;

do $$ begin
  create type assignment_status as enum ('not_started', 'in_progress', 'pending_review', 'passed', 'failed', 'overdue');
exception when duplicate_object then null; end $$;

do $$ begin
  create type attempt_status as enum ('in_progress', 'pending_review', 'passed', 'failed');
exception when duplicate_object then null; end $$;

-- ---------------------------------------------------------------------
-- Organisations (tenants)
-- ---------------------------------------------------------------------
create table if not exists organisations (
  id          uuid primary key default gen_random_uuid(),
  name        text        not null,
  slug        text        not null unique,
  industry    text,
  country     text,
  timezone    text        not null default 'UTC',
  is_demo     boolean     not null default false,
  created_at  timestamptz not null default now(),
  updated_at  timestamptz not null default now()
);

-- ---------------------------------------------------------------------
-- People.  `profiles` is the application-side mirror of a Supabase Auth
-- user (auth_user_id). Roles and department live here, not in Auth.
-- ---------------------------------------------------------------------
create table if not exists profiles (
  id              uuid primary key default gen_random_uuid(),
  auth_user_id    uuid unique,
  org_id          uuid not null references organisations(id) on delete cascade,
  email           text not null,
  full_name       text not null,
  role            user_role not null default 'learner',
  department      text,
  employee_number text,
  job_title       text,
  phone           text,
  status          user_status not null default 'active',
  avatar_url      text,
  manager_id      uuid references profiles(id) on delete set null,
  must_reset      boolean not null default false,
  -- bcrypt hash, only populated when AUTH_MODE=local is used. In Supabase
  -- mode credentials live in auth.users and this stays null.
  password_hash   text,
  last_seen_at    timestamptz,
  created_at      timestamptz not null default now(),
  updated_at      timestamptz not null default now(),
  unique (org_id, email),
  unique (org_id, employee_number)
);

create index if not exists idx_profiles_org_role  on profiles (org_id, role);
create index if not exists idx_profiles_org_dept  on profiles (org_id, department);
create index if not exists idx_profiles_manager   on profiles (org_id, manager_id);

create table if not exists departments (
  id         uuid primary key default gen_random_uuid(),
  org_id     uuid not null references organisations(id) on delete cascade,
  name       text not null,
  created_at timestamptz not null default now(),
  unique (org_id, name)
);

-- ---------------------------------------------------------------------
-- Invites.  A manager or admin creates one to add a colleague without
-- knowing their password. Redeeming an invite is the only way to join an
-- existing tenant; the code is single use and expires.
-- ---------------------------------------------------------------------
create table if not exists invites (
  id          uuid primary key default gen_random_uuid(),
  org_id      uuid not null references organisations(id) on delete cascade,
  code        text not null unique,
  email       text,
  role        user_role not null default 'learner',
  invited_by  uuid references profiles(id) on delete set null,
  expires_at  timestamptz,
  accepted_at timestamptz,
  accepted_by uuid references profiles(id) on delete set null,
  created_at  timestamptz not null default now()
);

create index if not exists idx_invites_org  on invites (org_id);
create index if not exists idx_invites_open on invites (code) where accepted_at is null;

-- ---------------------------------------------------------------------
-- Uploaded binaries (Supabase Storage objects + local disk driver)
-- ---------------------------------------------------------------------
create table if not exists assets (
  id            uuid primary key default gen_random_uuid(),
  org_id        uuid not null references organisations(id) on delete cascade,
  uploaded_by   uuid references profiles(id) on delete set null,
  kind          asset_kind not null,
  status        asset_status not null default 'uploaded',
  original_name text not null,
  storage_path  text not null,
  bucket        text not null,
  mime_type     text,
  size_bytes    bigint not null default 0,
  checksum      text,
  page_count    integer not null default 0,
  chapter_count integer not null default 0,
  parse_error   text,
  created_at    timestamptz not null default now(),
  updated_at    timestamptz not null default now()
);

create index if not exists idx_assets_org on assets (org_id, created_at desc);

-- One row per page/slide of a paginated asset.
-- is_blank = true marks a deliberate empty page. A blank page is a
-- CHAPTER DELIMITER: it is never rendered to the learner but it opens a
-- new chapter in the rendered course.
create table if not exists asset_pages (
  id             uuid primary key default gen_random_uuid(),
  asset_id       uuid not null references assets(id) on delete cascade,
  org_id         uuid not null references organisations(id) on delete cascade,
  page_number    integer not null,               -- 1-based source page
  is_blank       boolean not null default false,
  chapter_index  integer not null default 1,     -- 1-based chapter
  chapter_title  text,
  text_content   text,
  unique (asset_id, page_number)
);

create index if not exists idx_asset_pages_asset on asset_pages (asset_id, page_number);

-- ---------------------------------------------------------------------
-- Course engine: course -> module -> lesson -> content block
-- ---------------------------------------------------------------------
create table if not exists courses (
  id                uuid primary key default gen_random_uuid(),
  org_id            uuid not null references organisations(id) on delete cascade,
  title             text not null,
  code              text,
  description       text,
  category          text,
  cover_asset_id    uuid references assets(id) on delete set null,
  status            course_status not null default 'draft',
  estimated_minutes integer not null default 0,
  created_by        uuid references profiles(id) on delete set null,
  published_at      timestamptz,
  created_at        timestamptz not null default now(),
  updated_at        timestamptz not null default now(),
  unique (org_id, code)
);

create index if not exists idx_courses_org_status on courses (org_id, status, updated_at desc);

create table if not exists modules (
  id          uuid primary key default gen_random_uuid(),
  course_id   uuid not null references courses(id) on delete cascade,
  org_id      uuid not null references organisations(id) on delete cascade,
  title       text not null,
  description text,
  position    integer not null default 0,
  created_at  timestamptz not null default now(),
  updated_at  timestamptz not null default now()
);

create index if not exists idx_modules_course on modules (course_id, position);

create table if not exists lessons (
  id                uuid primary key default gen_random_uuid(),
  module_id         uuid not null references modules(id) on delete cascade,
  course_id         uuid not null references courses(id) on delete cascade,
  org_id            uuid not null references organisations(id) on delete cascade,
  title             text not null,
  summary           text,
  kind              lesson_kind not null default 'content',
  position          integer not null default 0,
  estimated_minutes integer not null default 0,
  -- Completion rule: how many of the lesson's blocks must be consumed
  require_all_blocks boolean not null default true,
  created_at        timestamptz not null default now(),
  updated_at        timestamptz not null default now()
);

create index if not exists idx_lessons_course on lessons (course_id, position);

create table if not exists blocks (
  id          uuid primary key default gen_random_uuid(),
  lesson_id   uuid not null references lessons(id) on delete cascade,
  org_id      uuid not null references organisations(id) on delete cascade,
  type        block_type not null,
  position    integer not null default 0,
  title       text,
  body        text,                       -- text/embed payload (markdown or URL)
  asset_id    uuid references assets(id) on delete set null,
  page_from   integer,                    -- first source page for paginated assets
  page_to     integer,                    -- last source page (inclusive)
  chapter_from integer,
  chapter_to   integer,
  created_at  timestamptz not null default now(),
  updated_at  timestamptz not null default now()
);

create index if not exists idx_blocks_lesson on blocks (lesson_id, position);

-- Records which page/slide a learner has consumed (paginated assets).
create table if not exists block_progress (
  id         uuid primary key default gen_random_uuid(),
  block_id   uuid not null references blocks(id) on delete cascade,
  learner_id uuid not null references profiles(id) on delete cascade,
  org_id     uuid not null references organisations(id) on delete cascade,
  page_number integer not null,
  seen_at    timestamptz not null default now(),
  unique (block_id, learner_id, page_number)
);

create table if not exists lesson_progress (
  id          uuid primary key default gen_random_uuid(),
  lesson_id   uuid not null references lessons(id) on delete cascade,
  learner_id  uuid not null references profiles(id) on delete cascade,
  org_id      uuid not null references organisations(id) on delete cascade,
  status      text not null default 'in_progress', -- in_progress | completed
  started_at  timestamptz not null default now(),
  completed_at timestamptz,
  unique (lesson_id, learner_id)
);

-- ---------------------------------------------------------------------
-- Assessments (manually authored). Four question types are supported.
--   single_choice - one correct option
--   multi_choice  - one or more correct options
--   short_text    - typed sentence, min/max length enforced
--   long_text     - typed paragraph(s), min/max length enforced
-- single_choice / multi_choice are auto-graded.
-- short_text / long_text are graded manually by a manager or org admin.
-- ---------------------------------------------------------------------
create table if not exists questions (
  id           uuid primary key default gen_random_uuid(),
  course_id    uuid not null references courses(id) on delete cascade,
  lesson_id    uuid references lessons(id) on delete cascade,
  org_id       uuid not null references organisations(id) on delete cascade,
  type         question_type not null,
  prompt       text not null,
  hint         text,
  explanation  text,
  points       numeric(6,2) not null default 1,
  position     integer not null default 0,
  min_length   integer,     -- short_text / long_text
  max_length   integer,     -- short_text / long_text
  is_required  boolean not null default true,
  created_at   timestamptz not null default now(),
  updated_at   timestamptz not null default now()
);

create index if not exists idx_questions_course on questions (course_id, position);

create table if not exists question_options (
  id          uuid primary key default gen_random_uuid(),
  question_id uuid not null references questions(id) on delete cascade,
  org_id      uuid not null references organisations(id) on delete cascade,
  label       text not null,
  is_correct  boolean not null default false,
  position    integer not null default 0
);

create index if not exists idx_question_options_q on question_options (question_id, position);

-- ---------------------------------------------------------------------
-- Campaigns: assign a published course to a population with rules
-- ---------------------------------------------------------------------
create table if not exists campaigns (
  id               uuid primary key default gen_random_uuid(),
  org_id           uuid not null references organisations(id) on delete cascade,
  course_id        uuid not null references courses(id) on delete restrict,
  name             text not null,
  description      text,
  status           campaign_status not null default 'draft',
  audience_type    audience_type not null default 'all',
  audience_dept    text,
  due_date         date,
  pass_mark        numeric(5,2) not null default 80,  -- percentage 0-100
  issue_certificate boolean not null default true,
  max_attempts     integer not null default 3,
  require_learning boolean not null default true,     -- must finish content first
  created_by       uuid references profiles(id) on delete set null,
  launched_at      timestamptz,
  closed_at        timestamptz,
  created_at       timestamptz not null default now(),
  updated_at       timestamptz not null default now()
);

create index if not exists idx_campaigns_org on campaigns (org_id, status, created_at desc);

create table if not exists campaign_audience (
  campaign_id uuid not null references campaigns(id) on delete cascade,
  profile_id  uuid not null references profiles(id) on delete cascade,
  org_id      uuid not null references organisations(id) on delete cascade,
  primary key (campaign_id, profile_id)
);

-- One row per learner per campaign. This is the tracking ledger.
create table if not exists assignments (
  id               uuid primary key default gen_random_uuid(),
  org_id           uuid not null references organisations(id) on delete cascade,
  campaign_id      uuid not null references campaigns(id) on delete cascade,
  course_id        uuid not null references courses(id) on delete cascade,
  learner_id       uuid not null references profiles(id) on delete cascade,
  status           assignment_status not null default 'not_started',
  assigned_at      timestamptz not null default now(),
  due_date         date,
  started_at       timestamptz,
  last_activity_at timestamptz not null default now(),
  completed_at     timestamptz,
  lessons_total    integer not null default 0,
  lessons_done     integer not null default 0,
  progress_percent numeric(5,2) not null default 0,
  attempts_used    integer not null default 0,
  best_score       numeric(6,2),
  final_score      numeric(6,2),
  passed_at        timestamptz,
  overdue_notified_at timestamptz,
  unique (campaign_id, learner_id)
);

create index if not exists idx_assignments_learner on assignments (learner_id, status);
create index if not exists idx_assignments_org      on assignments (org_id, campaign_id, status);
create index if not exists idx_assignments_due      on assignments (org_id, due_date)
  where status in ('not_started', 'in_progress', 'pending_review', 'overdue');

create table if not exists attempts (
  id             uuid primary key default gen_random_uuid(),
  org_id         uuid not null references organisations(id) on delete cascade,
  assignment_id  uuid not null references assignments(id) on delete cascade,
  learner_id     uuid not null references profiles(id) on delete cascade,
  attempt_number integer not null default 1,
  status         attempt_status not null default 'in_progress',
  max_score      numeric(8,2) not null default 0,
  score          numeric(8,2) not null default 0,
  percent        numeric(5,2) not null default 0,
  passed         boolean,
  started_at     timestamptz not null default now(),
  submitted_at   timestamptz,
  graded_at      timestamptz,
  graded_by      uuid references profiles(id) on delete set null,
  unique (assignment_id, attempt_number)
);

create table if not exists attempt_answers (
  id            uuid primary key default gen_random_uuid(),
  attempt_id    uuid not null references attempts(id) on delete cascade,
  question_id   uuid not null references questions(id) on delete cascade,
  org_id        uuid not null references organisations(id) on delete cascade,
  response_text text,
  selected_option_ids uuid[] not null default '{}',
  is_correct    boolean,
  points_awarded numeric(6,2),
  auto_graded   boolean not null default false,
  feedback      text,
  graded_by     uuid references profiles(id) on delete set null,
  graded_at     timestamptz,
  updated_at    timestamptz not null default now(),
  unique (attempt_id, question_id)
);

-- ---------------------------------------------------------------------
-- Certificates (HTML rendered, publicly verifiable)
-- ---------------------------------------------------------------------
create table if not exists certificates (
  id                uuid primary key default gen_random_uuid(),
  org_id            uuid not null references organisations(id) on delete cascade,
  learner_id        uuid not null references profiles(id) on delete cascade,
  course_id         uuid not null references courses(id) on delete restrict,
  campaign_id       uuid references campaigns(id) on delete set null,
  attempt_id        uuid references attempts(id) on delete set null,
  certificate_number text not null unique,   -- KZW-2026-000123
  verification_code text not null unique,    -- public lookup key
  learner_name      text not null,
  course_title      text not null,
  score             numeric(6,2) not null,
  completed_at      timestamptz not null,
  issued_at         timestamptz not null default now(),
  revoked_at        timestamptz,
  revoked_reason    text
);

create index if not exists idx_certificates_learner on certificates (learner_id, issued_at desc);
create index if not exists idx_certificates_org     on certificates (org_id, issued_at desc);

-- ---------------------------------------------------------------------
-- Reminders + audit trail
-- ---------------------------------------------------------------------
create table if not exists reminders (
  id            uuid primary key default gen_random_uuid(),
  org_id        uuid not null references organisations(id) on delete cascade,
  assignment_id uuid references assignments(id) on delete cascade,
  learner_id    uuid references profiles(id) on delete cascade,
  campaign_id   uuid references campaigns(id) on delete cascade,
  channel       text not null default 'in_app',
  subject       text,
  body          text,
  sent_by       uuid references profiles(id) on delete set null,
  created_at    timestamptz not null default now()
);

create index if not exists idx_reminders_org on reminders (org_id, created_at desc);

create table if not exists audit_logs (
  id         bigserial primary key,
  org_id     uuid,
  actor_id   uuid,
  action     text not null,
  entity     text not null,
  entity_id  text,
  meta       jsonb not null default '{}'::jsonb,
  ip         text,
  user_agent text,
  created_at timestamptz not null default now()
);

create index if not exists idx_audit_org on audit_logs (org_id, created_at desc);

-- ---------------------------------------------------------------------
-- updated_at triggers
-- ---------------------------------------------------------------------
-- Certificate numbering. A single sequence keeps the number unique
-- across all tenants, which is what a public verification code must be.
create sequence if not exists cert_number_seq start 1;

create or replace function touch_updated_at() returns trigger as $$
begin
  new.updated_at = now();
  return new;
end;
$$ language plpgsql;

do $$
declare t text;
begin
  foreach t in array array[
    'organisations','profiles','assets','courses','modules','lessons','blocks',
    'questions','campaigns','assignments'
  ] loop
    execute format(
      'drop trigger if exists trg_touch_%1$s on %1$s;
       create trigger trg_touch_%1$s before update on %1$s
       for each row execute function touch_updated_at();', t);
  end loop;
end $$;

-- ---------------------------------------------------------------------
-- Derived view used by the reporting endpoints
-- ---------------------------------------------------------------------
create or replace view v_learner_progress as
select
  a.org_id,
  a.campaign_id,
  c.name        as campaign_name,
  a.course_id,
  co.title      as course_title,
  a.learner_id,
  p.full_name   as learner_name,
  p.email       as learner_email,
  p.department  as department,
  m.full_name   as manager_name,
  a.status,
  a.assigned_at,
  a.due_date,
  a.started_at,
  a.last_activity_at,
  a.completed_at,
  a.progress_percent,
  a.attempts_used,
  a.best_score,
  a.final_score,
  a.overdue_notified_at,
  case when a.due_date is not null
        and a.status in ('not_started','in_progress','pending_review')
        and a.due_date < current_date then true else false end as is_overdue
from assignments a
join campaigns c  on c.id = a.campaign_id
join courses  co  on co.id = a.course_id
join profiles p   on p.id = a.learner_id
left join profiles m on m.id = p.manager_id;

-- ---------------------------------------------------------------------
-- Row Level Security (defence in depth).
-- NOTE: the Go backend connects with a privileged role and enforces
-- org_id in the repository layer. These policies protect direct access
-- through the Supabase dashboard / PostgREST / SQL editor.
-- Enable per table with:  alter table <t> enable row level security;
-- ---------------------------------------------------------------------
create or replace function current_org_id() returns uuid language sql stable as $$
  select nullif(current_setting('request.jwt.claim.org_id', true), '')::uuid
$$;

create or replace function jwt_role() returns text language sql stable as $$
  select coalesce(current_setting('request.jwt.claims', true)::jsonb ->> 'role', '')
$$;

-- Example policies (applied to the tenant tables):
--
-- alter table courses enable row level security;
-- create policy courses_tenant_read on courses for select
--   using (org_id = current_org_id());
-- create policy courses_tenant_write on courses for all
--   using (org_id = current_org_id() and jwt_role() in ('org_admin','super_admin'))
--   with check (org_id = current_org_id() and jwt_role() in ('org_admin','super_admin'));
--
-- The same pattern applies to profiles, modules, lessons, blocks, questions,
-- campaign_audience, assignments, attempts, attempt_answers and certificates.
