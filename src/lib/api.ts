/**
 * KaziWise API client.
 *
 * The Go backend answers with an envelope: {data, meta} on success and
 * {error:{code,message,fields}} on failure. Everything here unwraps that
 * once so the rest of the app only ever sees the payload.
 */

const RAW_BASE = (import.meta.env.VITE_API_URL ?? "").replace(/\/+$/, "");
export const API_BASE = RAW_BASE || "";

export interface ApiErrorBody {
  code: string;
  message: string;
  fields?: Record<string, unknown>;
  /** Present on 409 org_selection_required. */
  organisations?: string[];
}

export class ApiError extends Error {
  readonly status: number;
  readonly code: string;
  readonly fields: Record<string, unknown>;
  /** Candidate org slugs when one email exists in several tenants. */
  organisations?: string[];

  constructor(status: number, body: ApiErrorBody, fallback: string) {
    super(body.message || fallback);
    this.name = "ApiError";
    this.status = status;
    this.code = body.code || "unknown";
    this.fields = body.fields ?? {};
    const f = body.fields as { organisations?: unknown } | undefined;
    if (Array.isArray(f?.organisations)) {
      this.organisations = f.organisations as string[];
    }
    if (Array.isArray(body.organisations)) this.organisations = body.organisations;
  }

  /** True when the server rejected specific form fields. */
  get isValidation(): boolean {
    return this.status === 422 || Object.keys(this.fields).length > 0;
  }

  get fieldMessages(): Record<string, string> {
    const out: Record<string, string> = {};
    for (const [k, v] of Object.entries(this.fields)) {
      if (typeof v === "string") out[k] = v;
    }
    return out;
  }
}

interface Envelope<T> {
  data?: T;
  meta?: { total?: number; page?: number; per_page?: number };
  error?: ApiErrorBody;
}

export interface Page<T> {
  items: T[];
  total: number;
  page: number;
  perPage: number;
}

/* ------------------------------------------------------------------ */
/* Token storage                                                       */
/* ------------------------------------------------------------------ */

const ACCESS_KEY = "kaziwise.access";
const REFRESH_KEY = "kaziwise.refresh";
const USER_KEY = "kaziwise.user";
const ORG_KEY = "kaziwise.org";

export const tokens = {
  get access(): string | null {
    return localStorage.getItem(ACCESS_KEY);
  },
  get refresh(): string | null {
    return localStorage.getItem(REFRESH_KEY);
  },
  save(access: string, refresh?: string): void {
    localStorage.setItem(ACCESS_KEY, access);
    if (refresh) localStorage.setItem(REFRESH_KEY, refresh);
  },
  clear(): void {
    localStorage.removeItem(ACCESS_KEY);
    localStorage.removeItem(REFRESH_KEY);
    localStorage.removeItem(USER_KEY);
    localStorage.removeItem(ORG_KEY);
  },
};

export const cache = {
  get user(): unknown {
    const raw = localStorage.getItem(USER_KEY);
    return raw ? JSON.parse(raw) : null;
  },
  get org(): unknown {
    const raw = localStorage.getItem(ORG_KEY);
    return raw ? JSON.parse(raw) : null;
  },
  saveUser(user: unknown): void {
    localStorage.setItem(USER_KEY, JSON.stringify(user));
  },
  saveOrg(org: unknown): void {
    localStorage.setItem(ORG_KEY, JSON.stringify(org));
  },
};

/* ------------------------------------------------------------------ */
/* Core request                                                        */
/* ------------------------------------------------------------------ */

type Method = "GET" | "POST" | "PUT" | "PATCH" | "DELETE";

interface RequestOptions {
  method?: Method;
  body?: unknown;
  /** Send as-is (used for FormData uploads). */
  raw?: BodyInit;
  headers?: Record<string, string>;
  signal?: AbortSignal;
  /** Skip the automatic refresh-and-retry. */
  noRetry?: boolean;
  query?: Record<string, string | number | boolean | undefined | null>;
}

function buildUrl(path: string, query?: RequestOptions["query"]): string {
  const url = `${API_BASE}${path}`;
  if (!query) return url;
  const qs = new URLSearchParams();
  for (const [k, v] of Object.entries(query)) {
    if (v === undefined || v === null || v === "") continue;
    qs.set(k, String(v));
  }
  const s = qs.toString();
  return s ? `${url}?${s}` : url;
}

async function rawRequest(path: string, opts: RequestOptions): Promise<Response> {
  const headers: Record<string, string> = { ...(opts.headers ?? {}) };
  const access = tokens.access;
  if (access) headers.Authorization = `Bearer ${access}`;

  let body: BodyInit | undefined;
  if (opts.raw !== undefined) {
    body = opts.raw;
  } else if (opts.body !== undefined) {
    headers["Content-Type"] = "application/json";
    body = JSON.stringify(opts.body);
  }

  return fetch(buildUrl(path, opts.query), {
    method: opts.method ?? "GET",
    headers,
    body,
    signal: opts.signal,
    credentials: "include",
  });
}

/**
 * A single refresh is shared by all callers that hit a 401 at the same
 * time, so a screen firing several requests does not stampede the refresh
 * endpoint and invalidate its own fresh token.
 */
let refreshInFlight: Promise<boolean> | null = null;

async function refreshAccessToken(): Promise<boolean> {
  if (refreshInFlight) return refreshInFlight;

  refreshInFlight = (async () => {
    const refreshToken = tokens.refresh;
    if (!refreshToken) return false;
    try {
      const res = await rawRequest("/v1/auth/refresh", {
        method: "POST",
        body: { refresh_token: refreshToken },
        noRetry: true,
      });
      if (!res.ok) {
        // A rejected refresh means the session is gone for good.
        tokens.clear();
        return false;
      }
      const json = (await res.json()) as Envelope<{
        access_token: string;
        refresh_token?: string;
      }>;
      const data = json.data;
      if (!data?.access_token) return false;
      tokens.save(data.access_token, data.refresh_token);
      return true;
    } catch {
      return false;
    } finally {
      refreshInFlight = null;
    }
  })();

  return refreshInFlight;
}

async function parseError(res: Response): Promise<ApiError> {
  let body: ApiErrorBody = { code: "unknown", message: "" };
  try {
    const json = (await res.json()) as Envelope<unknown>;
    if (json.error) body = json.error;
  } catch {
    // Non-JSON error (proxy/HTML). Fall back to the status text.
  }
  const fallback =
    res.status === 401
      ? "Your session has expired. Sign in again."
      : res.status === 403
        ? "You do not have permission to do that."
        : res.status >= 500
          ? "The server had a problem. Try again in a moment."
          : "That request could not be completed.";
  return new ApiError(res.status, body, fallback);
}

async function request<T>(path: string, opts: RequestOptions = {}): Promise<T> {
  const res = await rawRequest(path, opts);

  if (res.status === 401 && !opts.noRetry) {
    const refreshed = await refreshAccessToken();
    if (refreshed) {
      const retry = await rawRequest(path, opts);
      if (!retry.ok) throw await parseError(retry);
      return unwrap<T>(await retry.json());
    }
    tokens.clear();
    throw await parseError(res);
  }

  if (res.status === 204) return undefined as T;
  if (!res.ok) throw await parseError(res);

  const contentType = res.headers.get("content-type") ?? "";
  if (!contentType.includes("application/json")) {
    return undefined as T;
  }
  return unwrap<T>(await res.json());
}

function unwrap<T>(json: Envelope<T>): T {
  return (json.data ?? (undefined as T)) as T;
}

export const api = {
  get: <T>(path: string, opts?: Omit<RequestOptions, "method" | "body">) =>
    request<T>(path, { ...opts, method: "GET" }),
  post: <T>(path: string, body?: unknown, opts?: Omit<RequestOptions, "method" | "body">) =>
    request<T>(path, { ...opts, method: "POST", body }),
  patch: <T>(path: string, body?: unknown, opts?: Omit<RequestOptions, "method" | "body">) =>
    request<T>(path, { ...opts, method: "PATCH", body }),
  del: <T>(path: string, opts?: Omit<RequestOptions, "method" | "body">) =>
    request<T>(path, { ...opts, method: "DELETE" }),

  /** Multipart upload; the browser sets the boundary itself. */
  upload: <T>(path: string, form: FormData, opts?: { signal?: AbortSignal }) =>
    request<T>(path, { method: "POST", raw: form, ...opts }),

  /** Paginated list helper: normalises {data:[...],meta:{...}}. */
  async page<T>(
    path: string,
    query?: RequestOptions["query"],
    signal?: AbortSignal,
  ): Promise<Page<T>> {
    const json = await requestEnvelope<T[]>(path, { method: "GET", query, signal });
    return {
      items: json.data ?? [],
      total: json.meta?.total ?? json.data?.length ?? 0,
      page: json.meta?.page ?? 1,
      perPage: json.meta?.per_page ?? 20,
    };
  },
};

async function requestEnvelope<T>(
  path: string,
  opts: RequestOptions = {},
): Promise<Envelope<T>> {
  const res = await rawRequest(path, opts);
  if (res.status === 401 && !opts.noRetry) {
    const refreshed = await refreshAccessToken();
    if (refreshed) {
      const retry = await rawRequest(path, opts);
      if (!retry.ok) throw await parseError(retry);
      return (await retry.json()) as Envelope<T>;
    }
    tokens.clear();
    throw await parseError(res);
  }
  if (res.status === 204) return {};
  if (!res.ok) throw await parseError(res);
  const ct = res.headers.get("content-type") ?? "";
  if (!ct.includes("application/json")) return {};
  return (await res.json()) as Envelope<T>;
}

/**
 * Download a file the API returns inline (report exports). Uses a normal
 * anchor so the browser handles the save dialog.
 */
export async function downloadFile(path: string, fallbackName: string): Promise<void> {
  const res = await rawRequest(path, { method: "GET" });
  if (!res.ok) throw await parseError(res);

  const disposition = res.headers.get("content-disposition") ?? "";
  const match = /filename="?([^"]+)"?/.exec(disposition);
  const name = match?.[1] ?? fallbackName;

  const blob = await res.blob();
  const url = URL.createObjectURL(blob);
  const a = document.createElement("a");
  a.href = url;
  a.download = name;
  document.body.appendChild(a);
  a.click();
  a.remove();
  // Give the browser a beat to start the download before revoking.
  setTimeout(() => URL.revokeObjectURL(url), 1000);
}
