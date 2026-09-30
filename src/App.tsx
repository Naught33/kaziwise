import { Navigate, Route, Routes } from "react-router-dom";
import { useAuth } from "./lib/auth";
import { canViewReports } from "./lib/format";
import { Layout } from "./components/Layout";
import { Spinner } from "./components/ui";

import Login, { ChangePassword, ForgotPassword } from "./pages/Login";
import Register from "./pages/Register";
import AdminDashboard from "./pages/AdminDashboard";
import Employees from "./pages/Employees";
import CourseLibrary from "./pages/CourseLibrary";
import CourseBuilder from "./pages/CourseBuilder";
import Campaigns from "./pages/Campaigns";
import Reports from "./pages/Reports";
import {
  Certificates,
  CertificateView,
  LearnerCertificates,
  PublicCertificate,
} from "./pages/Certificates";
import LearnerHome, { MyTraining, Profile } from "./pages/LearnerHome";
import CoursePlayer from "./pages/CoursePlayer";

/** Sends people to the right home for their role. */
function RootRedirect() {
  const { user, loading } = useAuth();
  if (loading) return <FullPageSpinner />;
  if (!user) return <Navigate to="/login" replace />;
  return <Navigate to={canViewReports(user.role) ? "/dashboard" : "/learn"} replace />;
}

/** Requires a session. */
function RequireAuth({ children }: { children: JSX.Element }) {
  const { user, loading } = useAuth();
  if (loading) return <FullPageSpinner />;
  if (!user) return <Navigate to="/login" replace />;
  return children;
}

/** Staff only. Managers and admins both see the admin screens. */
function RequireStaff({ children }: { children: JSX.Element }) {
  const { user } = useAuth();
  if (!user) return <Navigate to="/login" replace />;
  if (!canViewReports(user.role)) return <Navigate to="/learn" replace />;
  return children;
}

function FullPageSpinner() {
  return (
    <div className="fullpage-spinner">
      <Spinner size={28} />
      <span className="small muted">Loading KaziWise</span>
    </div>
  );
}

/** Wraps a page in the app shell. */
function Shell({ title, children }: { title: string; children: JSX.Element }) {
  return (
    <Layout title={title}>
      {children}
    </Layout>
  );
}

export default function App() {
  return (
    <Routes>
      {/* Public */}
      <Route path="/login" element={<Login />} />
      <Route path="/register" element={<Register />} />
      <Route path="/forgot-password" element={<ForgotPassword />} />
      {/* Public certificate verification: the code in the URL is the credential. */}
      <Route path="/c/:code" element={<PublicCertificate />} />

      {/* Authenticated */}
      <Route
        path="/change-password"
        element={
          <RequireAuth>
            <ChangePassword />
          </RequireAuth>
        }
      />

      {/* Admin / manager shell */}
      <Route
        path="/dashboard"
        element={
          <RequireStaff>
            <Shell title="Dashboard">
              <AdminDashboard />
            </Shell>
          </RequireStaff>
        }
      />
      <Route
        path="/employees"
        element={
          <RequireStaff>
            <Shell title="Employees">
              <Employees />
            </Shell>
          </RequireStaff>
        }
      />
      <Route
        path="/courses"
        element={
          <RequireStaff>
            <Shell title="Course Library">
              <CourseLibrary />
            </Shell>
          </RequireStaff>
        }
      />
      <Route
        path="/courses/:courseId/builder"
        element={
          <RequireStaff>
            <Shell title="Course Builder">
              <CourseBuilder />
            </Shell>
          </RequireStaff>
        }
      />
      <Route
        path="/campaigns"
        element={
          <RequireStaff>
            <Shell title="Training Campaigns">
              <Campaigns />
            </Shell>
          </RequireStaff>
        }
      />
      <Route
        path="/reports"
        element={
          <RequireStaff>
            <Shell title="Training Reports">
              <Reports />
            </Shell>
          </RequireStaff>
        }
      />
      <Route
        path="/certificates"
        element={
          <RequireStaff>
            <Shell title="Certificates">
              <Certificates />
            </Shell>
          </RequireStaff>
        }
      />
      <Route
        path="/certificates/:id"
        element={
          <RequireStaff>
            <Shell title="Certificate">
              <CertificateView />
            </Shell>
          </RequireStaff>
        }
      />
      {/* Staff reach their profile from the account menu. */}
      <Route
        path="/profile"
        element={
          <RequireAuth>
            <Shell title="Profile">
              <Profile />
            </Shell>
          </RequireAuth>
        }
      />

      {/* Learner shell: phone-first with bottom navigation */}
      <Route
        path="/learn"
        element={
          <RequireAuth>
            <Shell title="Home">
              <LearnerHome />
            </Shell>
          </RequireAuth>
        }
      />
      <Route
        path="/learn/training"
        element={
          <RequireAuth>
            <Shell title="Learning">
              <MyTraining />
            </Shell>
          </RequireAuth>
        }
      />
      <Route
        path="/learn/certificates"
        element={
          <RequireAuth>
            <Shell title="Certificates">
              <LearnerCertificates />
            </Shell>
          </RequireAuth>
        }
      />
      <Route
        path="/learn/profile"
        element={
          <RequireAuth>
            <Shell title="Profile">
              <Profile />
            </Shell>
          </RequireAuth>
        }
      />
      <Route
        path="/learn/courses/:courseId"
        element={
          <RequireAuth>
            <Shell title="Course">
              <CoursePlayer />
            </Shell>
          </RequireAuth>
        }
      />

      <Route path="/" element={<RootRedirect />} />
      <Route path="*" element={<RootRedirect />} />
    </Routes>
  );
}
