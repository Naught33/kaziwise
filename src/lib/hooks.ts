import { useCallback, useEffect, useRef, useState } from "react";
import { ApiError } from "./api";

export interface AsyncState<T> {
  data: T | null;
  error: ApiError | null;
  loading: boolean;
  /** True only for the first load, so refreshes do not flash skeletons. */
  initial: boolean;
  reload: () => void;
  setData: (updater: T | ((prev: T | null) => T | null)) => void;
}

/**
 * GET a resource and keep it in component state.
 *
 * The abort guard matters here: the tables and dashboards refetch on every
 * filter change, and a slow earlier response must not overwrite a newer one.
 */
export function useApi<T>(
  fetcher: (signal: AbortSignal) => Promise<T>,
  deps: unknown[] = [],
): AsyncState<T> {
  const [data, setDataState] = useState<T | null>(null);
  const [error, setError] = useState<ApiError | null>(null);
  const [loading, setLoading] = useState(true);
  const [initial, setInitial] = useState(true);
  const [tick, setTick] = useState(0);

  const fetcherRef = useRef(fetcher);
  fetcherRef.current = fetcher;

  useEffect(() => {
    const ctrl = new AbortController();
    let alive = true;

    setLoading(true);
    fetcherRef
      .current(ctrl.signal)
      .then((res) => {
        if (!alive) return;
        setDataState(res);
        setError(null);
      })
      .catch((err: unknown) => {
        if (!alive || ctrl.signal.aborted) return;
        if (err instanceof ApiError) {
          setError(err);
        } else if (err instanceof DOMException && err.name === "AbortError") {
          return;
        } else {
          setError(
            new ApiError(0, { code: "network", message: "Cannot reach the KaziWise server." }, ""),
          );
        }
      })
      .finally(() => {
        if (!alive) return;
        setLoading(false);
        setInitial(false);
      });

    return () => {
      alive = false;
      ctrl.abort();
    };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [tick, ...deps]);

  const reload = useCallback(() => setTick((t) => t + 1), []);

  const setData = useCallback((updater: T | ((prev: T | null) => T | null)) => {
    setDataState((prev) => (typeof updater === "function" ? (updater as (p: T | null) => T | null)(prev) : updater));
  }, []);

  return { data, error, loading, initial, reload, setData };
}

/** Debounce a rapidly changing value (search boxes). */
export function useDebounced<T>(value: T, delay = 300): T {
  const [debounced, setDebounced] = useState(value);
  useEffect(() => {
    const t = setTimeout(() => setDebounced(value), delay);
    return () => clearTimeout(t);
  }, [value, delay]);
  return debounced;
}
