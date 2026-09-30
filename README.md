# KaziWise LMS API

A Go backend for a multi-tenant corporate learning platform. An
organisation's administrators build courses out of uploaded training
material, assign them to employees through campaigns, employees work
through the material and take assessments, and completed training earns a
shareable certificate.

This repository contains the **backend only**. There is no front end and no
container setup; the server is a single portable binary.

- Language: Go 1.26
- Database: PostgreSQL (Supabase Postgres)
- Auth: Supabase Auth, with an optional local mode for offline prototyping
- Storage: Supabase Storage, with an optional local disk driver
- Router: `chi`; access tokens are verified in-process (HS256/RS256/JWKS)

---

## 1. How it fits together

```
cmd/api            process entry point: config, migrations, storage, server
internal/api       HTTP layer: middleware, route table, request handlers
internal/auth      token verification and Supabase Auth REST client
internal/domain    core types and enums shared by every layer
internal/service   business rules that span more than one table
internal/store     PostgreSQL access (pgx); one file per area
internal/material  PDF and PPTX parsing into ordered pages
internal/storage   object storage drivers (Supabase, local disk)
internal/httpx     response envelope, error types, pagination
migrations         schema; migrations/seeds holds opt-in demo content
```

Requests flow `handler -> service -> store`. Handlers only decode input,
resolve the caller's identity, and translate errors; business rules stay in
`service` and `store` so they are testable without HTTP.

### Tenancy

Every row that belongs to a customer carries an `org_id`. The organisation
is resolved from the **database profile**, never trusted from the token, so
a stale Supabase `app_metadata` claim cannot leak another tenant's data.
Every query is scoped by `org_id`.

---

## 2. Setup

### 2.1 Prerequisites

- Go 1.26+
- A Supabase project (for Postgres, Auth and Storage), or a plain
  PostgreSQL database plus `AUTH_MODE=local` and `STORAGE_DRIVER=local`
  for offline work.

### 2.2 Configure

```sh
cp .env.example .env      # rename is fine on Windows
```

Fill in at least `DATABASE_URL`. For Supabase Auth also set `SUPABASE_URL`,
`SUPABASE_ANON_KEY`, `SUPABASE_SERVICE_ROLE_KEY` and `SUPABASE_JWT_SECRET`.
The full reference is in section 8 and in `.env.example`; every value there
is read directly by the backend at boot.

### 2.3 Run

```sh
go run ./cmd/api
```

On boot the process:

1. loads and validates the environment,
2. connects to Postgres (with retry while a pooler warms up),
3. applies `migrations/*.sql` when `RUN_MIGRATIONS=true`,
4. optionally seeds demo content and credentials,
5. serves the API until it receives `SIGINT`/`SIGTERM`, then drains
   in-flight requests for `SHUTDOWN_GRACE`.

### 2.4 Demo data (optional)

```sh
SEED_DEMO=true SEED_AUTH=true go run ./cmd/api
```

This applies `migrations/seeds/*.sql` (one organisation, one user per role,
a published course, a campaign) and then creates working credentials for
those profiles. Every demo account uses `SEED_PASSWORD` (default
`Password123!`). Never enable `SEED_DEMO` on a production database.

### 2.5 Tests

```sh
go build ./...
go vet ./...
go test ./...
```

---

## 3. Authentication modes

| `AUTH_MODE` | What it does |
| --- | --- |
| `supabase` | The client signs in with `supabase-js` and sends the Supabase access token as a bearer token. `POST /v1/auth/login` also works and returns a Supabase session. |
| `local` | Adds a locally signed JWT minted by this backend, so the prototype runs with no Supabase project. Requires `ALLOW_LOCAL_AUTH=true`. |

Local mode is refused when `APP_ENV=production`.

### Authenticating a request

```
Authorization: Bearer <access token>
```

The server verifies the token, then loads the matching profile to authorise
on stored role and organisation data. An inactive profile is refused with
`403 account_inactive`.

---

## 4. Roles and access

| Role | Intended use |
| --- | --- |
| `super_admin` | Platform operator; can author content and manage accounts. |
| `org_admin` | Runs one organisation: employees, courses, campaigns, reports. |
| `manager` | Read-only across their organisation plus the grading queue; can chase (remind) learners. |
| `learner` | Only their own assignments, progress, attempts and certificates. |

Authoring endpoints (create/publish/import etc.) require `super_admin` or
`org_admin`. Reporting and the grading queue also allow `manager`. A learner
who calls another user's resource receives `403`.

Registration rules:

- Creating a **new** organisation makes the founder its `org_admin`.
- Joining an existing tenant happens through an invite code; an invite may
  grant `learner` or `manager` but never an administrator.
- Only a `super_admin` may create another `super_admin`.

---

## 5. Response format

Every JSON response uses one of two shapes.

Success:

```json
{ "data": { }, "meta": { } }
```

`meta` appears on list endpoints:

```json
{ "meta": { "page": 1, "per_page": 25, "total": 132, "total_pages": 6 } }
```

Error:

```json
{ "error": { "code": "validation_error", "message": "Please correct the highlighted fields.",
             "fields": { "email": "Enter a valid email address." } } }
```

Select the format with `Accept: application/json` (default) or
`Accept: text/html` where a route renders a document. List endpoints accept
`?page=` and `?per_page=` and often `?search=`, `?status=` and
`?department=`.

---

## 6. Endpoint reference

`GET /v1` returns this same list as JSON, generated from the live router.

### Health

| Method | Path | Notes |
| --- | --- | --- |
| GET | `/health` | Liveness; no auth, no database. |
| GET | `/ready` | Readiness; checks the database. |

### Auth (public)

| Method | Path | Body |
| --- | --- | --- |
| POST | `/v1/auth/register` | `full_name`, `email`, `password`, optional `role`, `org_name`, `org_slug`, `invite_code` |
| POST | `/v1/auth/login` | `email`, `password`, optional `org_slug` |
| POST | `/v1/auth/orgs/resolve` | `email` or `org_slug`; resolves which tenant to sign in to |
| POST | `/v1/auth/accept-invite` | `full_name`, `email`, `password`, `invite_code` |
| POST | `/v1/auth/refresh` | `refresh_token`; callable with an expired access token |
| POST | `/v1/auth/forgot-password` | `email`; always answers the same way so it cannot enumerate accounts |

Registration returns the new profile, the organisation, and a session when
one is available. In local mode the response includes `access_token`.

If the same address exists in more than one organisation, `login` answers
`409 org_selection_required` and lists the candidate codes in
`error.fields.organisations`; send the matching one back as `org_slug`.

### Session (bearer)

| Method | Path | Notes |
| --- | --- | --- |
| GET | `/v1/me` | The signed-in profile. |
| POST | `/v1/logout` | Invalidates the current session. |
| POST | `/v1/change-password` | `current_password`, `new_password`. |

### Organisation and employees

| Method | Path | Role | Notes |
| --- | --- | --- | --- |
| GET | `/v1/org` | any | Current organisation. |
| PATCH | `/v1/org` | admin | `name`, `industry`, `country`, `timezone`. |
| GET | `/v1/org/departments` | any | Departments in use. |
| GET | `/v1/org/team` | any | The manager's team with progress. |
| GET | `/v1/employees` | any | Directory; `?search=&department=&status=`. |
| POST | `/v1/employees` | admin | `full_name`, `email`, optional `role`, `department`, `employee_number`, `job_title`, `phone`, `manager_id`, `password`. Without `password` the API returns `temporary_password`. |
| GET | `/v1/employees/{id}` | any | One employee. |
| PATCH | `/v1/employees/{id}` | admin | Any subset of the create fields plus `status`, `clear_manager`. |
| DELETE | `/v1/employees/{id}` | admin | Deactivates the employee. |
| POST | `/v1/employees/import` | admin | `multipart/form-data` CSV (`full_name,email,role,department,employee_number,job_title`). |
| POST | `/v1/employees/invite` | admin | `role` (`learner` or `manager`), optional `email`, `expires_in_hours`. Returns `code` and `accept_url`. |

### Courses and the builder

| Method | Path | Role | Notes |
| --- | --- | --- | --- |
| GET | `/v1/courses` | any | Library; `?search=&status=&category=`. |
| POST | `/v1/courses` | admin | `title`, `code`, optional `description`, `category`, `estimated_minutes`. |
| GET | `/v1/courses/{id}` | any | One course. |
| PATCH | `/v1/courses/{id}` | admin | Any editable field. |
| DELETE | `/v1/courses/{id}` | admin | Deletes a course with no assignments. |
| GET | `/v1/courses/{id}/outline` | any | Course → modules → lessons → blocks. |
| POST | `/v1/courses/{id}/publish` | admin | Publishes the course. |
| POST | `/v1/courses/{id}/unpublish` | admin | Returns it to draft. |
| POST | `/v1/courses/{id}/modules` | admin | `title`. |
| PATCH | `/v1/courses/{id}/modules/{moduleId}` | admin | `title`. |
| DELETE | `/v1/courses/{id}/modules/{moduleId}` | admin | Deletes a module. |
| POST | `/v1/courses/{id}/modules/reorder` | admin | `{ "ids": [...] }`. |
| GET | `/v1/courses/{id}/modules/{moduleId}/lessons` | any | Lessons in a module. |
| POST | `/v1/courses/{id}/modules/{moduleId}/lessons` | admin | `title`. |
| PATCH | `/v1/courses/lessons/{id}` | admin | `title`. |
| DELETE | `/v1/courses/lessons/{id}` | admin | Deletes a lesson. |
| POST | `/v1/courses/modules/{moduleId}/lessons/reorder` | admin | `{ "ids": [...] }`. |
| GET | `/v1/courses/lessons/{id}/blocks` | any | Content blocks. |
| POST | `/v1/courses/lessons/{id}/blocks` | admin | `type` (`text`, `image`, `video`, `asset`), `content`, optional `asset_id`, `page_from`, `page_to`. |
| PATCH | `/v1/courses/blocks/{id}` | admin | Editable block fields. |
| DELETE | `/v1/courses/blocks/{id}` | admin | Deletes a block. |
| POST | `/v1/courses/lessons/{id}/blocks/reorder` | admin | `{ "ids": [...] }`. |

### Assets (uploaded material)

| Method | Path | Role | Notes |
| --- | --- | --- | --- |
| GET | `/v1/assets` | admin | Uploaded documents. |
| POST | `/v1/assets/upload` | admin | `multipart/form-data`: `file` (PDF or PPTX), optional `title`. Parsed synchronously into pages. |
| GET | `/v1/assets/{id}` | admin | Asset metadata. |
| GET | `/v1/assets/{id}/pages` | admin | Extracted pages, in order. |
| DELETE | `/v1/assets/{id}` | admin | Deletes the asset and its object. |

Uploads are capped at `MAX_UPLOAD_MB`. Blank pages delimit chapters: a
blank page or slide starts a new chapter, and the player skips blank pages
when rendering.

### Questions

| Method | Path | Role | Notes |
| --- | --- | --- | --- |
| GET | `/v1/courses/{id}/questions` | manager+ | Question bank. |
| POST | `/v1/courses/{id}/questions` | admin | See section 7. |
| PATCH | `/v1/courses/questions/{questionId}` | admin | Editable question fields. |
| DELETE | `/v1/courses/questions/{questionId}` | admin | Deletes a question. |
| POST | `/v1/courses/{id}/questions/reorder` | admin | `{ "ids": [...] }`. |
| GET | `/v1/courses/lessons/{id}/questions` | manager+ | Questions attached to a lesson. |
| POST | `/v1/courses/lessons/{id}/questions` | admin | Adds a question to that lesson. The path lesson wins over `lesson_id` in the body. |

### Campaigns and assignments

| Method | Path | Role | Notes |
| --- | --- | --- | --- |
| GET | `/v1/campaigns` | manager+ | Campaigns; `?status=`. |
| POST | `/v1/campaigns` | admin | `name`, `course_id`, optional `description`, `due_at`. |
| GET | `/v1/campaigns/{id}` | manager+ | One campaign. |
| PATCH | `/v1/campaigns/{id}` | admin | Editable fields. |
| DELETE | `/v1/campaigns/{id}` | admin | Deletes a draft campaign. |
| GET | `/v1/campaigns/{id}/audience` | manager+ | Who will be / was assigned. |
| POST | `/v1/campaigns/{id}/launch` | admin | Creates assignments for the audience. |
| POST | `/v1/campaigns/{id}/close` | admin | Stops the campaign. |
| GET | `/v1/campaigns/{id}/remind` | manager+ | Reminder history. |
| POST | `/v1/campaigns/{id}/remind` | manager+ | Sends reminders to non-completers. |
| GET | `/v1/assignments` | any | Tracking, scoped to the caller's role. |
| GET | `/v1/assignments/{id}` | any | One assignment (own record for a learner). |
| GET | `/v1/assignments/{id}/remind` | manager+ | Reminder history. |
| POST | `/v1/assignments/{id}/remind` | manager+ | Chases one learner. |

### Learner

| Method | Path | Role | Notes |
| --- | --- | --- | --- |
| GET | `/v1/dashboard` | any | The signed-in learner's summary. |
| GET | `/v1/me/training` | any | Training list with progress. |
| GET | `/v1/courses/{id}/play` | any | Player payload: learner-safe questions plus rendered pages. |
| POST | `/v1/me/lessons/{id}/start` | learner | Optional `?assignment_id=`. |
| POST | `/v1/me/lessons/{id}/complete` | learner | Optional `?assignment_id=`. |
| POST | `/v1/courses/blocks/{id}/pages` | learner | Records a page view. |
| GET | `/v1/courses/blocks/{id}/pages` | learner | Page-view progress. |
| GET | `/v1/courses/{courseId}/lessons/{id}/progress` | any | Progress for one lesson. |
| GET | `/v1/learners` | manager+ | Learners with progress; `?unassigned=true`, `?course_id=`. |
| GET | `/v1/learners/{id}` | manager+ | One learner. |
| GET | `/v1/learners/{id}/summary` | manager+ | Progress summary. |
| GET | `/v1/learners/{id}/certificates` | any | A learner's certificates (staff or self). |
| GET | `/v1/me/certificates` | any | The caller's own certificates. |

### Assessment

| Method | Path | Role | Notes |
| --- | --- | --- | --- |
| POST | `/v1/assignments/{assignmentId}/attempts` | learner | Starts or resumes an attempt. |
| GET | `/v1/attempts` | any | Attempts, scoped to the caller. |
| POST | `/v1/attempts/{attemptId}/answers` | learner | Saves answers without submitting. |
| POST | `/v1/attempts/{attemptId}/submit` | learner | Submits; auto-grades objective questions. |
| GET | `/v1/attempts/{attemptId}/result` | any | Result and score. |
| GET | `/v1/attempts/{attemptId}/paper` | learner | Learner-safe question paper, answer keys stripped. |
| GET | `/v1/attempts/{attemptId}/answers` | any | Grading script: the attempt plus saved answers. |
| POST | `/v1/attempts/{attemptId}/grade` | manager+ | `{ "grades": [{ "answer_id", "points", "feedback" }] }`. |
| POST | `/v1/attempts/{attemptId}/grade-answer` | manager+ | One grade: `answer_id`, `points`, `feedback`. |
| GET | `/v1/grading/pending` | manager+ | Attempts awaiting manual grading. |

### Certificates

| Method | Path | Role | Notes |
| --- | --- | --- | --- |
| GET | `/v1/certificates` | any | Issued certificates. |
| POST | `/v1/certificates` | any | Issues one for a completed assignment. |
| GET | `/v1/certificates/{id}` | any | One certificate. |
| GET | `/v1/certificates/{id}/html` | any | Printable HTML render. |
| POST | `/v1/certificates/{id}/revoke` | admin | Revokes a certificate. |
| GET | `/v1/certificates/public/{code}` | none | Public HTML render — share this URL. |
| GET | `/v1/certificates/public/{code}.json` | none | Public verification as JSON. |

Certificates are HTML, not PDF: the same URL can be reopened or shared at
any time, and revoked certificates stop verifying.

### Reporting and audit

| Method | Path | Role | Notes |
| --- | --- | --- | --- |
| GET | `/v1/dashboard/kpis` | manager+ | Dashboard counters. |
| GET | `/v1/dashboard/attention` | manager+ | Overdue and needs-review items. |
| GET | `/v1/reports/overview` | manager+ | Completion summary. |
| GET | `/v1/reports/rows` | manager+ | Detailed rows. |
| GET | `/v1/reports/departments` | manager+ | Progress by department. |
| GET | `/v1/reports/export/csv` | manager+ | CSV download. |
| GET | `/v1/reports/export/pdf` | manager+ | Printable HTML report (no PDF binary is generated; the spec calls for an HTML render). |
| GET | `/v1/reports/export/xlsx` | manager+ | Spreadsheet-compatible HTML for opening in Excel. |
| GET | `/v1/audit` | any | Audit trail; `?entity=`. |

---

## 7. Assessments

Four question types are supported.

| `type` | Behaviour |
| --- | --- |
| `single_choice` | One correct option. Auto-graded. |
| `multi_choice` | One or more correct options. Auto-graded. |
| `short_text` | Typed sentence; optional `min_length` / `max_length`. Manually graded. |
| `long_text` | Typed paragraph; optional `min_length` / `max_length`. Manually graded. |

Creating a question:

```json
{
  "type": "multi_choice",
  "prompt": "Which of these are fire hazards?",
  "points": 4,
  "options": [
    { "label": "Overloaded sockets", "is_correct": true },
    { "label": "Blocked exits", "is_correct": true },
    { "label": "Water fountain", "is_correct": false }
  ]
}
```

Rules enforced by the API:

- A choice question needs at least two options and a unique label per
  option.
- `single_choice` needs exactly one correct option; `multi_choice` needs at
  least one but not all.
- A written question must not carry options; `min_length` cannot exceed
  `max_length`.

Learners never receive `is_correct`: the player payload strips it.

---

## 8. Environment reference

Full skeleton with comments: `.env.example`. The essentials:

| Variable | Purpose |
| --- | --- |
| `APP_ENV`, `APP_NAME`, `APP_BASE_URL`, `HTTP_ADDR` | Identity and listen address. |
| `HTTP_*_TIMEOUT`, `SHUTDOWN_GRACE`, `REQUEST_TIMEOUT` | Server timeouts. |
| `DATABASE_URL`, `DB_*` | Postgres connection and pool. |
| `RUN_MIGRATIONS`, `MIGRATION_DIR` | Schema application. |
| `AUTH_MODE`, `ALLOW_LOCAL_AUTH` | Supabase or local token verification. |
| `SUPABASE_URL`, `SUPABASE_ANON_KEY`, `SUPABASE_SERVICE_ROLE_KEY`, `SUPABASE_JWT_SECRET`, `SUPABASE_JWT_ALG` | Supabase endpoints and token verification. |
| `JWT_ISSUER`, `JWT_AUDIENCE`, `ACCESS_TOKEN_TTL`, `REFRESH_TOKEN_TTL` | Token checks and lifetimes. |
| `STORAGE_DRIVER`, `STORAGE_BUCKET_*`, `LOCAL_DISK_ROOT` | Where uploads live. |
| `MAX_UPLOAD_MB`, `PRESIGNED_URL_TTL`, `DOWNLOAD_URL_TTL` | Upload and link limits. |
| `CORS_ALLOWED_ORIGINS`, `RATE_LIMIT_PER_MIN`, `TRUSTED_PROXIES` | Browser and abuse controls. |
| `COOKIE_DOMAIN`, `COOKIE_SECURE` | Session cookies. |
| `SEED_DEMO`, `SEED_AUTH`, `SEED_ORG_*`, `SEED_PASSWORD` | Optional demo bootstrap. |

Startup fails fast on an invalid or incomplete configuration.

---

## 9. Front-end integration

- Base URL: `APP_BASE_URL` (for example `http://localhost:8080`).
- Send `Authorization: Bearer <token>` on every non-public route.
- Read the payload from `data`; read list pagination from `meta`.
- On `401`, sign in again; on `403`, the role is not permitted.
- On `422`, render `error.fields` next to the matching form inputs.
- CORS is allow-listed; add your dev origin to `CORS_ALLOWED_ORIGINS`.
- For uploads send `multipart/form-data` with a `file` part; do not set
  `Content-Type` manually.
- Certificate sharing uses the public URL returned in the share payload.
- `GET /v1` lists every route currently served, with its auth requirement.
