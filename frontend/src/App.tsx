import { useEffect, useState } from 'react';
import { Navigate, Outlet, Route, Routes, useLocation, useNavigate } from 'react-router-dom';
import SidebarLayout from './components/SidebarLayout';
import AccountsPage from './pages/AccountsPage';
import DashboardPage from './pages/DashboardPage';
import KeysPage from './pages/KeysPage';
import LoginPage from './pages/LoginPage';
import ModelsPage from './pages/ModelsPage';
import AuditPage from './pages/AuditPage';
import SettingsPage from './pages/SettingsPage';
import { apiFetch } from './api';

function ProtectedLayout() {
  const navigate = useNavigate();
  const location = useLocation();
  const [authenticated, setAuthenticated] = useState<boolean | null>(null);

  useEffect(() => {
    let active = true;
    void apiFetch<{ authenticated: boolean }>('/api/auth/status')
      .then((status) => {
        if (active) setAuthenticated(status.authenticated);
      })
      .catch(() => {
        if (active) setAuthenticated(false);
      });
    const unauthorized = () => {
      setAuthenticated(false);
      navigate('/login', { replace: true, state: { from: location.pathname } });
    };
    window.addEventListener('atom2api:unauthorized', unauthorized);
    return () => {
      active = false;
      window.removeEventListener('atom2api:unauthorized', unauthorized);
    };
  }, [location.pathname, navigate]);

  if (authenticated === null) {
    return (
      <div className="flex min-h-screen items-center justify-center bg-zinc-50" role="status">
        <span className="h-7 w-7 animate-spin rounded-full border-2 border-zinc-200 border-t-blue-600" />
        <span className="sr-only">正在验证会话</span>
      </div>
    );
  }
  if (!authenticated) return <Navigate to="/login" replace state={{ from: location.pathname }} />;

  return (
    <SidebarLayout>
      <Outlet />
    </SidebarLayout>
  );
}

export default function App() {
  return (
    <Routes>
      <Route path="/login" element={<LoginPage />} />
      <Route element={<ProtectedLayout />}>
        <Route index element={<DashboardPage />} />
        <Route path="audit" element={<AuditPage />} />
        <Route path="accounts" element={<AccountsPage />} />
        <Route path="keys" element={<KeysPage />} />
        <Route path="models" element={<ModelsPage />} />
        <Route path="settings" element={<SettingsPage />} />
      </Route>
      <Route path="*" element={<Navigate to="/" replace />} />
    </Routes>
  );
}
