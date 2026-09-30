-- =====================================================================
-- KaziWise LMS - Row Level Security
--
-- 0001_init.sql created the schema but left RLS switched off: the Go
-- backend is the only client and it scopes every query by org_id in the
-- repository layer. This migration turns RLS on for direct access
-- through PostgREST, the Supabase dashboard and the SQL editor.
--
-- Two facts shape the policy set:
--
--   1. The backend connects as the `postgres` role, which on Supabase is
--      NOT superuser and does NOT have bypassrls, so the policies must
--      admit it explicitly. backend_session() recognises it: a plain
--      libpq/pgx connection carries no Supabase JWT, so
--      request.jwt.claims is unset. PostgREST always injects that
--      setting, so a browser client can never claim to be the backend.
--
--   2. The tenant is taken from the org_id claim in the caller's JWT
--      (never from a query parameter), so a client cannot widen its own
--      scope by editing a request.
--
-- The org_id claim is written by the Supabase Auth custom hook or by
-- an admin; the backend itself does not depend on it, so a missing
-- claim simply yields no rows rather than a cross-tenant read.
-- =====================================================================

-- ---------------------------------------------------------------------
-- Helper functions. jwt_claims() replaces the direct cast in
-- jwt_role(): an empty (but present) request.jwt.claims would make
-- ''::jsonb raise instead of returning NULL.
-- ---------------------------------------------------------------------
create or replace function jwt_claims() returns jsonb language sql stable as $$
  select nullif(current_setting('request.jwt.claims', true), '')::jsonb
$$;

create or replace function current_org_id() returns uuid language sql stable as $$
  select nullif(jwt_claims() ->> 'org_id', '')::uuid
$$;

create or replace function jwt_role() returns text language sql stable as $$
  select coalesce(jwt_claims() ->> 'role', '')
$$;

create or replace function jwt_uid() returns uuid language sql stable as $$
  select nullif(jwt_claims() ->> 'sub', '')::uuid
$$;

-- True for the Go backend (no JWT on the connection) and for direct
-- psql/dashboard sessions, both of which are already privileged by
-- holding the database password.
create or replace function backend_session() returns boolean language sql stable as $$
  select jwt_claims() is null
$$;

-- ---------------------------------------------------------------------
-- Enable RLS everywhere a tenant row can live. schema_migrations is
-- deliberately excluded: it is process bookkeeping, not tenant data,
-- and the backend reads it on every boot.
-- ---------------------------------------------------------------------
do $$
declare
  t text;
  -- Content and directory tables: readable by the whole tenant,
  -- writable by an administrator of that tenant.
  tenant_tables text[] := array[
    'profiles', 'departments', 'invites', 'assets', 'asset_pages',
    'courses', 'modules', 'lessons', 'blocks', 'questions',
    'question_options', 'campaigns', 'campaign_audience',
    'reminders', 'audit_logs'
  ];
  -- Tables holding a learner's own work: additionally writable by the
  -- learner the row belongs to.
  learner_tables text[] := array[
    'lesson_progress', 'block_progress', 'assignments', 'attempts',
    'certificates'
  ];
  -- attempt_answers has no learner_id of its own; ownership is reached
  -- through the parent attempt.
  answer_tables text[] := array['attempt_answers'];
begin
  -- The organisation row is its own tenant scope: id, not org_id.
  execute 'alter table organisations enable row level security';
  execute 'drop policy if exists organisations_tenant on organisations';
  execute $p$
    create policy organisations_tenant on organisations for all
      using      (backend_session() or id = current_org_id())
      with check (backend_session()
                   or (id = current_org_id()
                       and jwt_role() in ('super_admin', 'org_admin')))
  $p$;

  foreach t in array tenant_tables loop
    execute format('alter table %I enable row level security', t);
    execute format('drop policy if exists %I on %I', t || '_tenant', t);
    execute format(
      'create policy %I on %I for all
         using      (backend_session() or org_id = current_org_id())
         with check (backend_session()
                      or (org_id = current_org_id()
                          and jwt_role() in (''super_admin'', ''org_admin'')))',
      t || '_tenant', t);
  end loop;

  foreach t in array learner_tables loop
    execute format('alter table %I enable row level security', t);
    execute format('drop policy if exists %I on %I', t || '_tenant', t);
    execute format(
      'create policy %I on %I for all
         using      (backend_session() or org_id = current_org_id())
         with check (backend_session()
                      or (org_id = current_org_id()
                          and jwt_role() in (''super_admin'', ''org_admin''))
                      or learner_id = jwt_uid())',
      t || '_tenant', t);
  end loop;

  foreach t in array answer_tables loop
    execute format('alter table %I enable row level security', t);
    execute format('drop policy if exists %I on %I', t || '_tenant', t);
    execute format(
      'create policy %I on %I for all
         using      (backend_session() or org_id = current_org_id())
         with check (backend_session()
                      or (org_id = current_org_id()
                          and jwt_role() in (''super_admin'', ''org_admin''))
                      or exists (select 1 from attempts a
                                 where a.id = attempt_id
                                   and a.learner_id = jwt_uid()))',
      t || '_tenant', t);
  end loop;
end $$;

-- ---------------------------------------------------------------------
-- The reporting view must not become a side door: without
-- security_invoker it is evaluated as its owner (postgres), which the
-- policies above admit in full, so a PostgREST caller would read every
-- tenant through it. security_invoker re-checks the caller's policies.
-- ---------------------------------------------------------------------
alter view v_learner_progress set (security_invoker = true);
