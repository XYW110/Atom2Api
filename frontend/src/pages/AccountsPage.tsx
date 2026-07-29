import { useCallback, useEffect, useMemo, useRef, useState } from 'react';
import { AlertCircle, Check, CheckCircle2, Copy, ExternalLink, Pause, Play, Plus, RefreshCw, Search, Trash2, Users } from 'lucide-react';
import { Button, Chip, Input, Modal, ModalBody, ModalContent, ModalFooter, ModalHeader, Progress, Skeleton, Table, TableBody, TableCell, TableColumn, TableHeader, TableRow, Tooltip, useDisclosure } from '@heroui/react';
import { EmptyState, PageShell } from '../components/PageShell';
import { useToast } from '../components/Toast';
import { apiFetch, type Account, errorMessage, formatDateTime, formatTokens, jsonRequest } from '../api';

const statusMeta = {
  active: { label: '运行中', color: 'success' as const },
  paused: { label: '已暂停', color: 'warning' as const },
  error: { label: '异常', color: 'danger' as const },
  syncing: { label: '同步中', color: 'primary' as const },
};

interface OAuthState {
  id: string;
  login_url: string;
  expires_at: string;
  status: 'pending' | 'complete' | 'expired';
}

function accountUsage(account: Account) {
  const visible = (account.plan.rate_limit_windows || []).filter((window) => window.show_enable === 1).sort((a, b) => a.window_size_seconds - b.window_size_seconds)[0];
  if (visible) return { percent: visible.usage_percent, label: `${visible.calls_used.toLocaleString()} / ${visible.call_limit.toLocaleString()} 次`, reset: visible.reset_at_display };
  const current = account.plan.current_usage;
  if (current) return { percent: current.usage_percent, label: `${formatTokens(current.window_tokens_used)} / ${formatTokens(current.window_token_limit)} tokens`, reset: current.reset_at_display };
  return { percent: 0, label: '等待额度数据', reset: '' };
}

export default function AccountsPage() {
  const [accounts, setAccounts] = useState<Account[]>([]);
  const [query, setQuery] = useState('');
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState('');
  const [busy, setBusy] = useState('');
  const [oauth, setOAuth] = useState<OAuthState | null>(null);
  const [oauthCopied, setOAuthCopied] = useState(false);
  const [deleting, setDeleting] = useState<Account | null>(null);
  const oauthErrorNotified = useRef(false);
  const oauthModal = useDisclosure();
  const deleteModal = useDisclosure();
  const { showToast } = useToast();

  const load = useCallback(async () => {
    try {
      const response = await apiFetch<{ data: Account[] }>('/api/accounts');
      setAccounts(response.data || []);
      setError('');
    } catch (requestError) {
      setError(requestError instanceof Error ? requestError.message : '无法加载账号');
    } finally {
      setLoading(false);
    }
  }, []);

  useEffect(() => { void load(); }, [load]);

  useEffect(() => {
    if (!oauth || oauth.status !== 'pending') return undefined;
    let active = true;
    let polling = false;
    const poll = async () => {
      if (polling) return;
      polling = true;
      try {
        const result = await apiFetch<{ status: OAuthState['status']; account?: Account }>(`/api/oauth/${oauth.id}`);
        if (!active) return;
        oauthErrorNotified.current = false;
        setOAuth((current) => current ? { ...current, status: result.status } : current);
        if (result.status === 'complete') {
          await load();
          showToast('success', '账号连接成功', result.account?.name ? `“${result.account.name}”权益与模型已同步` : '账号权益与模型已同步');
        } else if (result.status === 'expired') {
          showToast('error', '账号连接失败', '授权会话已过期，请重新发起连接');
        }
      } catch (requestError) {
        if (active) {
          const message = errorMessage(requestError, 'OAuth 状态检查失败');
          setError(message);
          if (!oauthErrorNotified.current) {
            oauthErrorNotified.current = true;
            showToast('error', '授权状态检查失败', message);
          }
        }
      } finally {
        polling = false;
      }
    };
    const interval = window.setInterval(() => void poll(), 2000);
    void poll();
    return () => { active = false; window.clearInterval(interval); };
  }, [oauth?.id, oauth?.status, load, showToast]);

  const filtered = useMemo(() => {
    const normalized = query.trim().toLowerCase();
    return accounts.filter((account) => !normalized || account.name.toLowerCase().includes(normalized) || account.user.username.toLowerCase().includes(normalized) || account.id.includes(normalized));
  }, [accounts, query]);

  const startOAuth = async () => {
    setBusy('oauth');
    setError('');
    setOAuthCopied(false);
    oauthErrorNotified.current = false;
    try {
      const session = await apiFetch<Omit<OAuthState, 'status'>>('/api/oauth/start', jsonRequest('POST'));
      setOAuth({ ...session, status: 'pending' });
      oauthModal.onOpen();
      showToast('success', '授权流程已启动', '可复制 OAuth 链接，或直接前往 AtomGit 授权');
    } catch (requestError) {
      showToast('error', '无法启动授权', errorMessage(requestError, '请稍后重试'));
    } finally {
      setBusy('');
    }
  };

  const copyOAuthURL = async () => {
    if (!oauth?.login_url) return;
    try {
      await navigator.clipboard.writeText(oauth.login_url);
      setOAuthCopied(true);
      showToast('success', '复制成功', 'OAuth 链接已复制到剪贴板');
    } catch (copyError) {
      showToast('error', '复制失败', errorMessage(copyError, '请手动选择并复制 OAuth 链接'));
    }
  };

  const syncAccount = async (account: Account) => {
    setBusy(`sync:${account.id}`);
    try {
      await apiFetch(`/api/accounts/${account.id}/sync`, jsonRequest('POST'));
      await load();
      showToast('success', '同步成功', `“${account.name}”额度与模型已更新`);
    } catch (requestError) {
      await load();
      showToast('error', '同步失败', `“${account.name}”：${errorMessage(requestError, '未知错误')}`);
    } finally {
      setBusy('');
    }
  };

  const toggleAccount = async (account: Account) => {
    setBusy(`toggle:${account.id}`);
    try {
      await apiFetch(`/api/accounts/${account.id}`, jsonRequest('PATCH', { enabled: !account.enabled }));
      await load();
      showToast('success', account.enabled ? '账号已暂停' : '账号已恢复', `“${account.name}”调度状态已更新`);
    } catch (requestError) {
      showToast('error', account.enabled ? '暂停账号失败' : '恢复账号失败', errorMessage(requestError, '请稍后重试'));
    } finally {
      setBusy('');
    }
  };

  const removeAccount = async () => {
    if (!deleting) return;
    const accountName = deleting.name;
    setBusy(`delete:${deleting.id}`);
    try {
      await apiFetch(`/api/accounts/${deleting.id}`, { method: 'DELETE' });
      deleteModal.onClose();
      setDeleting(null);
      await load();
      showToast('success', '账号已删除', `“${accountName}”已从路由中移除`);
    } catch (requestError) {
      showToast('error', '删除账号失败', errorMessage(requestError, '请稍后重试'));
    } finally {
      setBusy('');
    }
  };

  const activeCount = accounts.filter((account) => account.enabled && account.status === 'active').length;
  const providerTokens = accounts.reduce((sum, account) => sum + (account.provider_usage?.total_tokens || 0), 0);

  return (
    <PageShell title="账号管理" description="AtomGit OAuth 账户、Coding Plan 权益与滚动额度" action={{ label: '连接 AtomGit', icon: Plus, onPress: () => void startOAuth() }}>
      {error ? <div className="flex items-start gap-2 rounded-md border border-red-200 bg-red-50 px-4 py-3 text-sm text-red-700" role="alert"><AlertCircle className="mt-0.5 shrink-0" size={16} />{error}</div> : null}

      <section className="grid gap-4 sm:grid-cols-3">
        <div className="rounded-lg border border-zinc-200 bg-white px-5 py-4"><p className="text-xs text-zinc-500">已连接账号</p><p className="mt-1 text-xl font-semibold text-zinc-900">{accounts.length}</p></div>
        <div className="rounded-lg border border-zinc-200 bg-white px-5 py-4"><p className="text-xs text-zinc-500">当前可调度</p><p className="mt-1 text-xl font-semibold text-emerald-600">{activeCount}</p></div>
        <div className="rounded-lg border border-zinc-200 bg-white px-5 py-4"><p className="text-xs text-zinc-500">Coding Plan 近 60 天 Tokens</p><p className="mt-1 text-xl font-semibold text-zinc-900">{formatTokens(providerTokens)}</p></div>
      </section>

      <section className="overflow-hidden rounded-lg border border-zinc-200 bg-white">
        <div className="border-b border-zinc-100 p-4"><Input aria-label="搜索账号" className="w-full sm:max-w-xs" classNames={{ inputWrapper: 'h-10 rounded-md border border-zinc-200 bg-white shadow-none' }} placeholder="搜索名称、用户名或账号 ID" radius="sm" startContent={<Search size={16} className="text-zinc-400" />} value={query} variant="bordered" onValueChange={setQuery} /></div>
        {loading ? <div className="space-y-3 p-5">{Array.from({ length: 4 }).map((_, index) => <Skeleton key={index} className="h-14 w-full rounded-md" />)}</div> : (
          <div className="overflow-x-auto">
            <Table aria-label="AtomGit 账号列表" removeWrapper classNames={{ th: 'bg-zinc-50 text-xs text-zinc-500', td: 'py-4 text-sm' }}>
              <TableHeader><TableColumn>账号</TableColumn><TableColumn>订阅</TableColumn><TableColumn>当前窗口</TableColumn><TableColumn>可用模型</TableColumn><TableColumn>代理用量</TableColumn><TableColumn>最近同步</TableColumn><TableColumn>状态</TableColumn><TableColumn align="end">操作</TableColumn></TableHeader>
              <TableBody items={filtered} emptyContent={<EmptyState icon={Users} title="尚未连接账号" description="连接 AtomGit 后将自动领取或同步 Coding Plan" />}>
                {(account) => {
                  const usage = accountUsage(account);
                  const status = statusMeta[account.status] || statusMeta.error;
                  return (
                    <TableRow key={account.id}>
                      <TableCell><div className="flex items-center gap-3">{account.user.avatar_url ? <img alt="" className="h-8 w-8 rounded-full bg-zinc-100 object-cover" src={account.user.avatar_url} /> : <span className="flex h-8 w-8 items-center justify-center rounded-full bg-zinc-100 text-xs font-semibold text-zinc-600">{account.name.slice(0, 2).toUpperCase()}</span>}<div><p className="font-medium text-zinc-900">{account.name}</p><p className="mt-0.5 text-xs text-zinc-400">@{account.user.username}</p></div></div></TableCell>
                      <TableCell><div><p className="font-medium text-zinc-800">{account.plan.codingplan_free?.plan_name || '未领取'}</p><p className="mt-0.5 text-xs text-zinc-400">{account.plan.codingplan_free ? `剩余 ${account.plan.codingplan_free.remaining_days} 天` : '—'}</p></div></TableCell>
                      <TableCell><div className="min-w-40"><div className="mb-1.5 flex items-center justify-between gap-3 text-xs"><span className="text-zinc-500">{usage.label}</span><span className={usage.percent >= 90 ? 'text-red-600' : 'text-zinc-500'}>{usage.percent.toFixed(0)}%</span></div><Progress aria-label="额度使用率" color={usage.percent >= 90 ? 'danger' : usage.percent >= 70 ? 'warning' : 'primary'} size="sm" value={Math.min(usage.percent, 100)} /><p className="mt-1 text-[11px] text-zinc-400">{usage.reset || '等待窗口刷新'}</p></div></TableCell>
                      <TableCell><span className="font-medium text-zinc-700">{account.models.length}</span></TableCell>
                      <TableCell><div><p className="font-medium text-zinc-800">{formatTokens(account.input_tokens + account.output_tokens)}</p><p className="mt-0.5 text-xs text-zinc-400">{account.request_count.toLocaleString()} 次请求</p></div></TableCell>
                      <TableCell><span className="whitespace-nowrap text-zinc-500">{formatDateTime(account.last_sync_at)}</span></TableCell>
                      <TableCell><Tooltip content={account.last_error || status.label}><Chip color={status.color} radius="sm" size="sm" variant="flat">{status.label}</Chip></Tooltip></TableCell>
                      <TableCell><div className="flex justify-end gap-1"><Tooltip content="同步额度与模型"><Button isIconOnly aria-label="同步账号" isLoading={busy === `sync:${account.id}`} radius="sm" size="sm" variant="light" onPress={() => void syncAccount(account)}><RefreshCw size={16} /></Button></Tooltip><Tooltip content={account.enabled ? '暂停调度' : '恢复调度'}><Button isIconOnly aria-label={account.enabled ? '暂停账号' : '恢复账号'} isLoading={busy === `toggle:${account.id}`} radius="sm" size="sm" variant="light" onPress={() => void toggleAccount(account)}>{account.enabled ? <Pause size={16} /> : <Play size={16} />}</Button></Tooltip><Tooltip color="danger" content="删除账号"><Button isIconOnly aria-label="删除账号" color="danger" radius="sm" size="sm" variant="light" onPress={() => { setDeleting(account); deleteModal.onOpen(); }}><Trash2 size={16} /></Button></Tooltip></div></TableCell>
                    </TableRow>
                  );
                }}
              </TableBody>
            </Table>
          </div>
        )}
        <div className="border-t border-zinc-100 px-5 py-3 text-xs text-zinc-400">共 {filtered.length} 个账号</div>
      </section>

      <Modal isOpen={oauthModal.isOpen} radius="sm" onOpenChange={oauthModal.onOpenChange}>
        <ModalContent>{(onClose) => <><ModalHeader>连接 AtomGit</ModalHeader><ModalBody>{oauth?.status === 'complete' ? <div className="flex items-start gap-3 rounded-md border border-emerald-200 bg-emerald-50 p-4 text-sm text-emerald-800"><CheckCircle2 className="shrink-0" size={19} /><span>授权完成，账号权益与模型已同步。</span></div> : oauth?.status === 'expired' ? <div className="flex items-start gap-3 rounded-md border border-red-200 bg-red-50 p-4 text-sm text-red-700"><AlertCircle className="shrink-0" size={19} /><span>授权会话已过期，请重新发起连接。</span></div> : <div className="space-y-4"><div className="flex items-center gap-3 rounded-md border border-blue-200 bg-blue-50 p-4 text-sm text-blue-800"><span className="h-5 w-5 shrink-0 animate-spin rounded-full border-2 border-blue-200 border-t-blue-600" /><span>授权链接已生成，请复制链接或前往 AtomGit 完成授权。</span></div><div className="flex items-center gap-2"><Input isReadOnly aria-label="AtomGit OAuth 链接" classNames={{ input: 'font-mono text-xs' }} radius="sm" value={oauth?.login_url || ''} /><Tooltip content={oauthCopied ? '已复制' : '复制 OAuth 链接'}><Button isIconOnly aria-label="复制 OAuth 链接" color={oauthCopied ? 'success' : 'primary'} radius="sm" variant="flat" onPress={() => void copyOAuthURL()}>{oauthCopied ? <Check size={17} /> : <Copy size={17} />}</Button></Tooltip></div><Button as="a" color="primary" endContent={<ExternalLink size={16} />} href={oauth?.login_url} radius="sm" rel="noreferrer" target="_blank">前往 AtomGit 授权</Button></div>}</ModalBody><ModalFooter><Button radius="sm" variant="light" onPress={onClose}>{oauth?.status === 'complete' ? '完成' : '关闭'}</Button></ModalFooter></>}</ModalContent>
      </Modal>

      <Modal isOpen={deleteModal.isOpen} radius="sm" size="sm" onOpenChange={deleteModal.onOpenChange}>
        <ModalContent>{(onClose) => <><ModalHeader>删除账号</ModalHeader><ModalBody><p className="text-sm leading-6 text-zinc-600">删除“{deleting?.name}”后，其 OAuth 凭据和模型路由将立即移除。</p></ModalBody><ModalFooter><Button radius="sm" variant="light" onPress={onClose}>取消</Button><Button color="danger" isLoading={busy === `delete:${deleting?.id}`} radius="sm" onPress={() => void removeAccount()}>确认删除</Button></ModalFooter></>}</ModalContent>
      </Modal>
    </PageShell>
  );
}
