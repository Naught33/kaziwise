import {
  createContext,
  useCallback,
  useContext,
  useEffect,
  useMemo,
  useState,
  type ReactNode,
} from "react";
import { api, ApiError, cache, tokens } from "./api";
import type { AuthResponse, Organisation, User } from "./types";

export type { AuthResponse };

interface AuthState {
  user: User | null;
  org: Organisation | null;
  loading: boolean;
  /** Set when the session could not be restored, so the login screen can explain why. */
  expiredNotice: string | null;
  login: (email: string, password: string, orgSlug?: string) => Promise<AuthResponse>;
  register: (payload: RegisterPayload) => Promise<AuthResponse>;
  logout: () => Promise<void>;
  refreshProfile: () => Promise<void>;
  setOrg: (org: Organisation | null) => void;
  clearNotice: () => void;
}

export interface RegisterPayload {
  full_name: string;
  email: string;
  password: string;
  role?: string;
  department?: string;
  job_title?: string;
  org_name?: string;
  org_slug?: string;
  invite_code?: string;
}

const AuthContext = createContext<AuthState | null>(null);

export function AuthProvider({ children }: { children: ReactNode }) {
  const [user, setUser] = useState<User | null>(cache.user as User | null);
  const [org, setOrgState] = useState<Organisation | null>(cache.org as Organisation | null);
  const [loading, setLoading] = useState<boolean>(Boolean(tokens.access));
  const [expiredNotice, setExpiredNotice] = useState<string | null>(null);

  /** Re-read the signed-in profile. Called on boot and after a role change. */
  const refreshProfile = useCallback(async () => {
    if (!tokens.access) {
      setUser(null);
      setOrgState(null);
      return;
    }
    try {
      // /v1/me wraps the profile as {user, org, permissions}. It is not the
      // same shape as the login response, whose user and org sit at the top
      // level, so reading it as a bare User would stash the wrapper itself and
      // leave user.role undefined on every page load after a refresh.
      const me = await api.get<{ user: User; org?: Organisation }>("/v1/me");
      if (!me?.user) {
        tokens.clear();
        setUser(null);
        setOrgState(null);
        return;
      }
      setUser(me.user);
      cache.saveUser(me.user);
      if (me.org) {
        setOrgState(me.org);
        cache.saveOrg(me.org);
      } else {
        // A learner without org read access is fine; keep whatever we have.
        try {
          const o = await api.get<Organisation>("/v1/org");
          setOrgState(o);
          cache.saveOrg(o);
        } catch {
          // Keep the cached org rather than dropping it.
        }
      }
    } catch (err) {
      if (err instanceof ApiError && err.status === 401) {
        tokens.clear();
        setUser(null);
        setOrgState(null);
      }
    } finally {
      setLoading(false);
    }
  }, []);

  // On boot, if a token is present, confirm it is still good.
  useEffect(() => {
    if (tokens.access) {
      void refreshProfile();
    } else {
      setLoading(false);
    }
  }, [refreshProfile]);

  const applySession = useCallback((res: AuthResponse) => {
    tokens.save(res.access_token, res.refresh_token);
    setUser(res.user);
    cache.saveUser(res.user);
    if (res.org) {
      setOrgState(res.org);
      cache.saveOrg(res.org);
    }
    setExpiredNotice(null);
    return res;
  }, []);

  const login = useCallback(
    async (email: string, password: string, orgSlug?: string) => {
      const res = await api.post<AuthResponse>("/v1/auth/login", {
        email,
        password,
        ...(orgSlug ? { org_slug: orgSlug } : {}),
      });
      return applySession(res);
    },
    [applySession],
  );

  const register = useCallback(
    async (payload: RegisterPayload) => {
      const res = await api.post<AuthResponse>("/v1/auth/register", payload);
      return applySession(res);
    },
    [applySession],
  );

  const logout = useCallback(async () => {
    try {
      await api.post("/v1/logout");
    } catch {
      // Even if the server call fails, drop the local session.
    }
    tokens.clear();
    setUser(null);
    setOrgState(null);
  }, []);

  const setOrg = useCallback((next: Organisation | null) => {
    setOrgState(next);
    if (next) cache.saveOrg(next);
  }, []);

  const clearNotice = useCallback(() => setExpiredNotice(null), []);

  const value = useMemo<AuthState>(
    () => ({
      user,
      org,
      loading,
      expiredNotice,
      login,
      register,
      logout,
      refreshProfile,
      setOrg,
      clearNotice,
    }),
    [user, org, loading, expiredNotice, login, register, logout, refreshProfile, setOrg, clearNotice],
  );

  return <AuthContext.Provider value={value}>{children}</AuthContext.Provider>;
}

export function useAuth(): AuthState {
  const ctx = useContext(AuthContext);
  if (!ctx) throw new Error("useAuth must be used inside <AuthProvider>");
  return ctx;
}
