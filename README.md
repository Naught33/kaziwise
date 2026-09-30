# KaziWise web client

React + TypeScript client for the KaziWise LMS. It renders the interactive
prototype in `../KaziWise_LMS_Interactive_Prototype_Word.docx` and talks to the
Go API in `../kaziwise_backend`.

Train. Track. Improve.

## Requirements

- Node 20 or newer (developed on 22.15)
- The Go API running on `http://localhost:8080`, or a reachable API origin

## Getting started

```bash
npm install
cp .env.example .env.local   # optional; the defaults work locally
npm run dev
```

The app is served on `http://localhost:5173`. In development Vite proxies
`/v1`, `/health` and `/ready` to the Go API, so the browser only ever talks to
one origin and no CORS configuration is needed.

## Environment

Only variables prefixed with `VITE_` reach the browser bundle, so this file
must never hold a secret. The API holds the credentials, not the client.

| Variable | Purpose |
| --- | --- |
| `VITE_API_URL` | Absolute API origin. Leave empty in development to use the dev proxy; set it in production. |
| `VITE_API_PROXY` | Origin the dev server proxies to. Defaults to `http://localhost:8080`. |
| `VITE_SHOW_DEMO_BADGE` | Set to `false` to hide the demo chip in the header. |

## Scripts

| Command | What it does |
| --- | --- |
| `npm run dev` | Dev server with HMR on port 5173. |
| `npm run typecheck` | Strict TypeScript check, no emit. |
| `npm run build` | Typecheck, then a production bundle into `dist/`. |
| `npm run preview` | Serve the built bundle. |

`npm run build` runs the typecheck first, so a green build means the types
check too.

## Screens and routes

The numbering follows the prototype document.

| Screen | Route | Notes |
| --- | --- | --- |
| Login | `/login` | Multi-tenant email prompt when one address exists in several organisations. |
| Forgot password | `/forgot-password` | Always answers the same way, so it cannot be used to discover accounts. |
| Change password | `/change-password` | Available from the account menu. |
| Registration | `/register` | Creates an organisation or accepts an invite code. |
| 01 Admin Dashboard | `/dashboard` | KPIs, progress bar and a needs-attention list. |
| 02 Employees | `/employees` | Search, add, invite and CSV import. |
| 03 Course Library | `/courses` | Drafts are visually distinct from published courses. |
| 04 Course Builder | `/courses/:courseId/builder` | Modules, lessons, content blocks and the assessment. |
| 05 Training Campaigns | `/campaigns` | Audience, due date, pass mark, launch and reminders. |
| 06 Training Reports | `/reports` | Status breakdown, per-learner rows and exports. |
| 07 Certificates | `/certificates`, `/certificates/:id` | Issued certificates and the printable version. |
| 08 Learner View | `/learn` | Welcome, required training and recent certificates. |
| 08 Learner View | `/learn/training` | All assigned training. |
| 08 Learner View | `/learn/certificates` | The learner's own certificates. |
| 08 Learner View | `/learn/profile` | Progress, certificates and password. |
| 09 Course player | `/learn/courses/:courseId` | Text, video, PDF, slides and the assessment. |
| Public verification | `/c/:code` | No sign-in. The code in the URL is the credential. |

`/` and any unknown path redirect by role: staff land on `/dashboard`, learners
on `/learn`, and signed-out visitors on `/login`.

## Architecture

```
src/
  main.tsx            entry point: router and auth provider
  App.tsx             route table and the role guards
  lib/
    api.ts            fetch client: envelope unwrapping, bearer tokens,
                      single-flight refresh, uploads, downloads
    auth.tsx          session context, login/register/logout, org selection
    types.ts          mirrors of the Go domain and API types
    hooks.ts          useApi (loading, error, refetch) and useDebounced
    format.ts         dates, percentages, durations and role helpers
  components/
    ui.tsx            buttons, badges, cards, tables, modals, icons, states
    Field.tsx         text, textarea, select and checkbox fields
    Layout.tsx        sidebar, topbar, account menu, bottom navigation
  pages/              one file per screen group
  styles/
    tokens.css        colours, spacing, radii and type scale
    base.css          reset and global typography
    components.css    shared component styles
    layout.css        shell, sidebar, page and responsive styles
```

### The API client

`lib/api.ts` is the only place that talks to the network. It unwraps the
backend envelope once, so pages see plain payloads:

- success is `{data, meta?}`
- failure is `{error: {code, message, fields?}}`

A `401` triggers one shared refresh and retry. The refresh is single-flight, so
a screen that fires several requests at once cannot stampede the endpoint and
invalidate the token it just obtained. A rejected refresh clears the session.

Errors surface as `ApiError`, which carries the status, the machine-readable
code and any per-field messages, so forms can show the message under the exact
input that caused it.

### Roles

`super_admin` and `org_admin` can author content; `manager` can read and chase;
`learner` sees only their own records. The UI mirrors the API's own
permissions: `canManage` gates editing controls and `canViewReports` decides
between the staff shell and the learner shell. Hiding a control is a
convenience, never the enforcement point, since the API checks every request.

### Design tokens

`styles/tokens.css` holds the palette from the prototype: teal `#1B8A8F` for
primary actions, navy `#16324F` for navigation, `#F7F9FC` for the page and
`#FFFFFF` for surfaces, with green, amber and red reserved for status. Status is
always carried by text as well as colour, and every list has explicit loading,
error and empty states.

## Deployment

`npm run build` writes a static bundle to `dist/`. Serve it from any static host
and point `VITE_API_URL` at the public API origin. Because this is a
single-page app, the host must rewrite unknown paths to `index.html` or deep
links such as `/learn/courses/:id` and `/c/:code` will 404 on a hard refresh.

## Status

The client builds and typechecks clean. The screens have been written against
the Go API's real routes and payload shapes, but they have not been exercised
against a running API, because the backend needs Supabase credentials to boot.
End-to-end verification is the remaining step once a database is available.
