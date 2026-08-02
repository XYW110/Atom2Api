import { useEffect, useState, type ReactNode } from 'react';
import { NavLink, useLocation, useNavigate } from 'react-router-dom';
import { ArrowUpCircle, Atom, Boxes, ChevronRight, ExternalLink, FileSearch, KeyRound, LayoutDashboard, LogOut, Menu, Settings, Users, X } from 'lucide-react';
import { Avatar, Button, Modal, ModalBody, ModalContent, ModalFooter, ModalHeader, Tooltip, useDisclosure } from '@heroui/react';
import { apiFetch, errorMessage, jsonRequest, type VersionInfo } from '../api';
import { StatusDot } from './PageShell';
import { useToast } from './Toast';

const menuItems = [
  { name: '仪表盘', path: '/', icon: LayoutDashboard },
  { name: '请求审计', path: '/audit', icon: FileSearch },
  { name: '账号管理', path: '/accounts', icon: Users },
  { name: '密钥管理', path: '/keys', icon: KeyRound },
  { name: '模型管理', path: '/models', icon: Boxes },
  { name: '系统设置', path: '/settings', icon: Settings },
];

function Brand() {
  return (
    <div className="flex h-16 items-center gap-3 px-5">
      <div className="flex h-9 w-9 items-center justify-center rounded-lg bg-blue-500 text-white"><Atom size={21} strokeWidth={2.2} /></div>
      <div className="min-w-0"><p className="truncate text-sm font-semibold text-white">Atom2Api</p><p className="truncate text-[11px] text-zinc-400">OpenAI Gateway</p></div>
    </div>
  );
}

function Navigation({ onNavigate }: { onNavigate?: () => void }) {
  return (
    <nav aria-label="主导航" className="flex-1 px-3 py-4">
      <p className="mb-2 px-3 text-[11px] font-semibold uppercase text-zinc-500">工作台</p>
      <div className="space-y-1">
        {menuItems.map((item) => {
          const Icon = item.icon;
          return (
            <NavLink key={item.path} to={item.path} end={item.path === '/'} onClick={onNavigate} className={({ isActive }) => `group flex min-h-10 items-center gap-3 rounded-md px-3 text-sm font-medium transition-colors focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-blue-400 ${isActive ? 'bg-white/10 text-white' : 'text-zinc-400 hover:bg-white/5 hover:text-zinc-100'}`}>
              {({ isActive }) => <><Icon size={18} className={isActive ? 'text-blue-400' : 'text-zinc-500 group-hover:text-zinc-300'} /><span className="flex-1">{item.name}</span>{isActive ? <ChevronRight size={14} className="text-zinc-500" /> : null}</>}
            </NavLink>
          );
        })}
      </div>
    </nav>
  );
}

function displayVersion(value?: string) {
  if (!value) return '检查中…';
  return value === 'dev' || value.startsWith('v') ? value : `v${value}`;
}

function SidebarFooter({ onLogout, loggingOut, versionInfo, onShowUpdate }: { onLogout: () => void; loggingOut: boolean; versionInfo: VersionInfo | null; onShowUpdate: () => void }) {
  return (
    <div className="border-t border-white/10 p-3">
      <div className="mb-2 rounded-md bg-white/5 px-3 py-2.5">
        <div className="flex items-center justify-between gap-2"><span className="text-xs font-medium text-zinc-300">网关状态</span><span className="flex items-center gap-1.5 text-[11px] text-emerald-400"><StatusDot />正常</span></div>
        <p className="mt-1 text-[11px] text-zinc-500">当前版本 {displayVersion(versionInfo?.current_version)}</p>
        {versionInfo?.update_available ? <Button fullWidth className="mt-2 justify-between bg-amber-400/15 px-2 text-left text-xs text-amber-200 data-[hover=true]:bg-amber-400/25" radius="sm" size="sm" startContent={<ArrowUpCircle size={14} />} variant="light" onPress={onShowUpdate}><span>发现新版本</span><span className="font-mono">{displayVersion(versionInfo.latest_version)}</span></Button> : null}
      </div>
      <Button fullWidth aria-label="退出登录" className="justify-start text-zinc-400 data-[hover=true]:bg-white/5 data-[hover=true]:text-white" isLoading={loggingOut} radius="sm" startContent={loggingOut ? null : <LogOut size={17} />} variant="light" onPress={onLogout}>退出登录</Button>
    </div>
  );
}

function UpdateModal({ info, isOpen, onClose }: { info: VersionInfo | null; isOpen: boolean; onClose: () => void }) {
  if (!info) return null;
  return (
    <Modal isOpen={isOpen} radius="sm" scrollBehavior="inside" size="lg" onOpenChange={(open) => { if (!open) onClose(); }}>
      <ModalContent>
        {(close) => <>
          <ModalHeader>发现新版本</ModalHeader>
          <ModalBody className="gap-5">
            <div className="grid grid-cols-[1fr_auto_1fr] items-center gap-3 rounded-md border border-blue-100 bg-blue-50 px-4 py-3 text-center">
              <div><p className="text-[11px] text-blue-500">当前版本</p><p className="mt-1 font-mono text-sm font-semibold text-blue-900">{displayVersion(info.current_version)}</p></div>
              <ArrowUpCircle className="text-blue-500" size={20} />
              <div><p className="text-[11px] text-blue-500">最新版本</p><p className="mt-1 font-mono text-sm font-semibold text-blue-900">{displayVersion(info.latest_version)}</p></div>
            </div>
            <section>
              <h3 className="mb-2 text-sm font-semibold text-zinc-900">更新日志</h3>
              {info.release_notes ? <pre className="max-h-80 overflow-auto whitespace-pre-wrap break-words rounded-md bg-zinc-50 p-4 font-mono text-xs leading-5 text-zinc-600">{info.release_notes}</pre> : <p className="rounded-md bg-zinc-50 p-4 text-sm text-zinc-500">该版本未提供更新日志。</p>}
            </section>
          </ModalBody>
          <ModalFooter>
            <Button radius="sm" variant="light" onPress={close}>稍后查看</Button>
            {info.release_url ? <Button as="a" color="primary" endContent={<ExternalLink size={15} />} href={info.release_url} radius="sm" rel="noreferrer" target="_blank">查看 GitHub Release</Button> : null}
          </ModalFooter>
        </>}
      </ModalContent>
    </Modal>
  );
}

export default function SidebarLayout({ children }: { children: ReactNode }) {
  const navigate = useNavigate();
  const location = useLocation();
  const [isOpen, setIsOpen] = useState(false);
  const [loggingOut, setLoggingOut] = useState(false);
  const [versionInfo, setVersionInfo] = useState<VersionInfo | null>(null);
  const updateModal = useDisclosure();
  const { showToast } = useToast();
  const currentPage = menuItems.find((item) => item.path === location.pathname)?.name ?? '管理后台';

  useEffect(() => {
    let active = true;
    apiFetch<VersionInfo>('/api/version').then((info) => {
      if (active) setVersionInfo(info);
    }).catch(() => {
      // Version checks are best effort and must not block the management console.
    });
    return () => { active = false; };
  }, []);

  const handleLogout = async () => {
    setLoggingOut(true);
    try {
      await apiFetch('/api/auth/logout', jsonRequest('POST'));
      showToast('success', '退出成功', '管理会话已结束');
      navigate('/login', { replace: true });
    } catch (requestError) {
      showToast('error', '退出失败', errorMessage(requestError, '请稍后重试'));
    } finally {
      setLoggingOut(false);
    }
  };

  return (
    <div className="flex min-h-screen bg-[#f6f7f9] text-zinc-950">
      <aside className="fixed inset-y-0 left-0 z-40 hidden w-[248px] flex-col bg-zinc-950 md:flex"><Brand /><Navigation /><SidebarFooter loggingOut={loggingOut} onShowUpdate={updateModal.onOpen} versionInfo={versionInfo} onLogout={handleLogout} /></aside>
      {isOpen ? <button aria-label="关闭导航" className="fixed inset-0 z-40 bg-zinc-950/45 md:hidden" type="button" onClick={() => setIsOpen(false)} /> : null}
      <aside className={`fixed inset-y-0 left-0 z-50 flex w-[272px] flex-col bg-zinc-950 transition-transform duration-200 md:hidden ${isOpen ? 'translate-x-0' : '-translate-x-full'}`}>
        <div className="flex items-center justify-between pr-3"><Brand /><Button isIconOnly aria-label="关闭菜单" className="text-zinc-400" radius="sm" variant="light" onPress={() => setIsOpen(false)}><X size={19} /></Button></div>
        <Navigation onNavigate={() => setIsOpen(false)} /><SidebarFooter loggingOut={loggingOut} onShowUpdate={updateModal.onOpen} versionInfo={versionInfo} onLogout={handleLogout} />
      </aside>
      <div className="min-w-0 flex-1 md:pl-[248px]">
        <header className="sticky top-0 z-30 flex h-16 items-center gap-3 border-b border-zinc-200/90 bg-white/95 px-4 backdrop-blur-md sm:px-6 lg:px-8">
          <Button isIconOnly aria-label="打开菜单" className="md:hidden" radius="sm" variant="light" onPress={() => setIsOpen(true)}><Menu size={20} /></Button>
          <div className="hidden items-center gap-2 text-sm sm:flex"><span className="text-zinc-400">Atom2Api</span><ChevronRight size={14} className="text-zinc-300" /><span className="font-medium text-zinc-700">{currentPage}</span></div>
          <div className="ml-auto flex items-center gap-3"><div className="h-6 w-px bg-zinc-200" /><Tooltip content="管理员"><Avatar className="bg-zinc-900 text-xs text-white" name="AD" size="sm" /></Tooltip></div>
        </header>
        <main className="px-4 py-6 sm:px-6 lg:px-8 lg:py-8"><div className="mx-auto max-w-[1440px]">{children}</div></main>
      </div>
      <UpdateModal info={versionInfo?.update_available ? versionInfo : null} isOpen={updateModal.isOpen} onClose={updateModal.onClose} />
    </div>
  );
}
