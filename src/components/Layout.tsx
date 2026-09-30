import { useEffect, useRef, useState, type ReactNode } from "react";
import { NavLink, useLocation, useNavigate } from "react-router-dom";
import { useAuth } from "../lib/auth";
import { canManage, canViewReports, formatDate, ROLE_LABELS } from "../lib/format";
import { Avatar, Badge, Button, Icon } from "./ui";

interface NavItem {
  to: string;
  label: string;
  icon: string;
  /** Restrict to some roles; managers and admins see reports. */
  reportsOnly?: boolean;
  adminOnly?: boolean;
}

const STAFF_NAV: NavItem[] = [
  { to: "/dashboard", label: "Dashboard", icon: "dashboard" },
  { to: "/employees", label: "Employees", icon: "users" },
  { to: "/courses", label: "Course Library", icon: "book" },
  { to: "/campaigns", label: "Training Campaigns", icon: "megaphone" },
  { to: "/reports", label: "Training Reports", icon: "chart", reportsOnly: true },
  { to: "/certificates", label: "Certificates", icon: "award" },
];

/** Bottom navigation for the phone-first learner experience (section 9). */
const LEARNER_NAV: NavItem[] = [
  { to: "/learn", label: "Home", icon: "home" },
  { to: "/learn/training", label: "Learning", icon: "book" },
  { to: "/learn/certificates", label: "Certificates", icon: "award" },
  { to: "/learn/profile", label: "Profile", icon: "user" },
];

export function Layout({ children, title }: { children: ReactNode; title?: string }) {
  const { user, org, logout } = useAuth();
  const nav = useNavigate();
  const loc = useLocation();
  const [menuOpen, setMenuOpen] = useState(false);
  const menuRef = useRef<HTMLDivElement>(null);

  const role = user?.role;
  const learnerMode = !canViewReports(role);
  const items = learnerMode ? LEARNER_NAV : STAFF_NAV.filter((i) => {
    if (i.adminOnly) return canManage(role);
    return true;
  });

  // Close the account menu on outside click or Escape.
  useEffect(() => {
    if (!menuOpen) return;
    const onDown = (e: MouseEvent) => {
      if (menuRef.current && !menuRef.current.contains(e.target as Node)) setMenuOpen(false);
    };
    const onKey = (e: KeyboardEvent) => {
      if (e.key === "Escape") setMenuOpen(false);
    };
    document.addEventListener("mousedown", onDown);
    document.addEventListener("keydown", onKey);
    return () => {
      document.removeEventListener("mousedown", onDown);
      document.removeEventListener("keydown", onKey);
    };
  }, [menuOpen]);

  useEffect(() => setMenuOpen(false), [loc.pathname]);

  const showDemo = import.meta.env.VITE_SHOW_DEMO_BADGE !== "false" && org?.is_demo;

  return (
    <div className="shell">
      {/* Desktop sidebar */}
      <aside className="sidebar">
        <div className="brand">
          <div className="brand-mark">KW</div>
          <div>
            <div className="brand-name">KaziWise</div>
            <div className="brand-tag">Train. Track. Improve.</div>
          </div>
        </div>

        <nav className="side-nav" aria-label="Main">
          {items.map((item) => (
            <NavLink
              key={item.to}
              to={item.to}
              end={item.to === "/learn" || item.to === "/dashboard"}
              className={({ isActive }) => `side-link${isActive ? " active" : ""}`}
            >
              <Icon name={item.icon} size={18} />
              <span>{item.label}</span>
            </NavLink>
          ))}
        </nav>

        <div className="side-foot">
          <div className="side-foot-label">Signed in as</div>
          <div className="row" style={{ gap: 8 }}>
            <Avatar name={user?.full_name} />
            <div style={{ minWidth: 0 }}>
              <div className="side-foot-name">{user?.full_name}</div>
              <div className="side-foot-role">{role ? ROLE_LABELS[role] : ""}</div>
            </div>
          </div>
          <Button
            variant="ghost"
            size="sm"
            className="side-signout"
            icon={<Icon name="logout" size={16} />}
            onClick={async () => {
              await logout();
              nav("/login", { replace: true });
            }}
          >
            Sign out
          </Button>
        </div>
      </aside>

      <div className="main">
        {/* Header: organisation breadcrumb, demo indicator, avatar */}
        <header className="topbar">
          <div className="topbar-left">
            <div className="brand-mark brand-mark-sm">KW</div>
            <nav className="crumbs" aria-label="Breadcrumb">
              <span className="crumb-org">{org?.name ?? "KaziWise"}</span>
              {title && (
                <>
                  <Icon name="chevronRight" size={14} />
                  <span className="crumb-current">{title}</span>
                </>
              )}
            </nav>
          </div>

          <div className="topbar-right">
            {showDemo && <Badge tone="warning">Demo Mode</Badge>}
            <div ref={menuRef} style={{ position: "relative" }}>
              <button
                type="button"
                className="avatar-btn"
                onClick={() => setMenuOpen((v) => !v)}
                aria-haspopup="menu"
                aria-expanded={menuOpen}
                aria-label="Account menu"
              >
                <Avatar name={user?.full_name} />
              </button>
              {menuOpen && (
                <div className="menu" role="menu">
                  <div className="menu-head">
                    <div style={{ fontWeight: 650, color: "var(--nav)" }}>{user?.full_name}</div>
                    <div className="small muted">{user?.email}</div>
                    <div style={{ marginTop: 6 }}>
                      <Badge tone="primary">{role ? ROLE_LABELS[role] : ""}</Badge>
                    </div>
                    {org && (
                      <div className="small muted" style={{ marginTop: 6 }}>
                        Joined {formatDate(user?.created_at)}
                      </div>
                    )}
                  </div>
                  <button
                    className="menu-item"
                    role="menuitem"
                    onClick={() => {
                      setMenuOpen(false);
                      nav(learnerMode ? "/learn/profile" : "/profile");
                    }}
                  >
                    <Icon name="user" size={16} /> Profile
                  </button>
                  {!user?.must_reset_password && (
                    <button
                      className="menu-item"
                      role="menuitem"
                      onClick={() => {
                        setMenuOpen(false);
                        nav("/change-password");
                      }}
                    >
                      <Icon name="lock" size={16} /> Change password
                    </button>
                  )}
                  <button
                    className="menu-item"
                    role="menuitem"
                    onClick={async () => {
                      await logout();
                      nav("/login", { replace: true });
                    }}
                  >
                    <Icon name="logout" size={16} /> Sign out
                  </button>
                </div>
              )}
            </div>
          </div>
        </header>

        <main className="content">{children}</main>
      </div>

      {/* Mobile bottom navigation */}
      {learnerMode && (
        <nav className="bottomnav" aria-label="Main">
          {items.map((item) => (
            <NavLink
              key={item.to}
              to={item.to}
              end={item.to === "/learn"}
              className={({ isActive }) => `bottom-link${isActive ? " active" : ""}`}
            >
              <Icon name={item.icon} size={22} />
              <span>{item.label}</span>
            </NavLink>
          ))}
        </nav>
      )}
    </div>
  );
}

export function PageHeader({
  title,
  subtitle,
  actions,
}: {
  title: string;
  subtitle?: string;
  actions?: ReactNode;
}) {
  return (
    <div className="page-header">
      <div>
        <h1>{title}</h1>
        {subtitle && <p className="soft" style={{ marginTop: 4 }}>{subtitle}</p>}
      </div>
      {actions && <div className="row-wrap">{actions}</div>}
    </div>
  );
}
