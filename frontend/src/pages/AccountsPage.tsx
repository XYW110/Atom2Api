import { useCallback, useEffect, useMemo, useRef, useState } from 'react';
import { AlertCircle, ArrowLeft, Check, CheckCircle2, Copy, ExternalLink, Eye, Gift, History, Pause, Pencil, Play, Plus, RefreshCw, Search, Trash2, Users } from 'lucide-react';
import { Button, Chip, Input, Modal, ModalBody, ModalContent, ModalFooter, ModalHeader, Progress, Skeleton, Switch, Table, TableBody, TableCell, TableColumn, TableHeader, TableRow, Textarea, Tooltip, useDisclosure } from '@heroui/react';
import { copyText } from '../clipboard';
import { EmptyState, PageShell } from '../components/PageShell';
import { useToast } from '../components/Toast';
import { apiFetch, type Account, type PlanClaimLog, type PlanClaimResult, errorMessage, formatDateTime, formatTokens, jsonRequest } from '../api';

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

const RESET_DATA_RELOAD_DELAY_MS = 5_000;
const RESET_DATA_RELOAD_RETRY_MS = 5_000;

function accountUsage(account: Account) {
  const visible = (account.plan.rate_limit_windows || []).filter((window) => window.show_enable === 1).sort((a, b) => a.window_size_seconds - b.window_size_seconds)[0];
  if (visible) return { percent: visible.usage_percent, label: `${visible.calls_used.toLocaleString()} / ${visible.call_limit.toLocaleString()} 次`, resetAt: visible.reset_at, secondsUntilReset: visible.seconds_until_reset };
  const current = account.plan.current_usage;
  if (current) return { percent: current.usage_percent, label: `${formatTokens(current.window_tokens_used)} / ${formatTokens(current.window_token_limit)} tokens`, resetAt: current.reset_at, secondsUntilReset: current.seconds_until_reset };
  return { percent: 0, label: '等待额度数据', resetAt: '', secondsUntilReset: 0 };
}

function parseResetTime(value: string) {
  const localMatch = /^(\d{4})[/-](\d{1,2})[/-](\d{1,2})[ T](\d{1,2}):(\d{1,2})(?::(\d{1,2}))?$/.exec(value.trim());
  if (localMatch) {
    const [, year, month, day, hour, minute, second = '0'] = localMatch;
    const date = new Date(Number(year), Number(month) - 1, Number(day), Number(hour), Number(minute), Number(second));
    return Number.isNaN(date.getTime()) ? null : date;
  }
  const date = new Date(value);
  return Number.isNaN(date.getTime()) ? null : date;
}

function formatResetTime(value: string) {
  const date = parseResetTime(value);
  if (!date) return '';
  const hour = String(date.getHours()).padStart(2, '0');
  const minute = String(date.getMinutes()).padStart(2, '0');
  return `${date.getFullYear()}年${date.getMonth() + 1}月${date.getDate()}日${hour}点${minute}分`;
}

function resetTargetTime(resetAt: string, secondsUntilReset: number, snapshotAt: number) {
  return parseResetTime(resetAt)?.getTime() ?? snapshotAt + Math.max(0, secondsUntilReset) * 1000;
}

function formatResetCountdown(resetAt: string, secondsUntilReset: number, snapshotAt: number, now: number) {
  const target = resetTargetTime(resetAt, secondsUntilReset, snapshotAt);
  const remaining = Math.max(0, Math.ceil((target - now) / 1000));
  const hours = Math.floor(remaining / 3600);
  const minutes = Math.floor((remaining % 3600) / 60);
  const seconds = remaining % 60;
  return `${String(hours).padStart(2, '0')}:${String(minutes).padStart(2, '0')}:${String(seconds).padStart(2, '0')}`;
}

function ResetCountdown({ resetAt, secondsUntilReset, snapshotAt }: { resetAt: string; secondsUntilReset: number; snapshotAt: number }) {
  const [now, setNow] = useState(() => Date.now());

  useEffect(() => {
    const interval = window.setInterval(() => setNow(Date.now()), 1000);
    return () => window.clearInterval(interval);
  }, []);

  return <span className="font-mono tabular-nums">距离重置{formatResetCountdown(resetAt, secondsUntilReset, snapshotAt, now)}</span>;
}

function ClaimLogDetails({ log }: { log: PlanClaimLog }) {
  const attempts = log.attempts || [];
  const rawRecords = attempts.map(({ plan_type, http_status, response }) => ({ plan_type, http_status, response }));

  return (
    <div className="space-y-6 px-6 pb-2">
      <dl className="grid gap-4 border-b border-zinc-100 pb-5 sm:grid-cols-4">
        <div><dt className="text-xs text-zinc-400">触发方式</dt><dd className="mt-1 text-sm font-medium text-zinc-800">{log.trigger === 'scheduled' ? '定时' : '手动'}</dd></div>
        <div><dt className="text-xs text-zinc-400">领取结果</dt><dd className="mt-1 text-sm font-medium text-zinc-800">{log.status === 'success' ? '成功' : log.status === 'failed' ? '失败' : '进行中'}</dd></div>
        <div><dt className="text-xs text-zinc-400">最终套餐</dt><dd className="mt-1 text-sm font-medium text-zinc-800">{log.plan_name || '—'}</dd></div>
        <div><dt className="text-xs text-zinc-400">开始时间</dt><dd className="mt-1 whitespace-nowrap text-sm text-zinc-600">{formatDateTime(log.started_at)}</dd></div>
      </dl>

      <section>
        <h3 className="mb-3 text-sm font-semibold text-zinc-900">套餐领取明细</h3>
        {attempts.length > 0 ? (
          <div className="overflow-hidden rounded-md border border-zinc-200">
            <Table aria-label="套餐领取明细" removeWrapper classNames={{ th: 'bg-zinc-50 text-xs text-zinc-500', td: 'py-3 text-sm align-top' }}>
              <TableHeader><TableColumn>套餐</TableColumn><TableColumn>结果</TableColumn><TableColumn>HTTP</TableColumn><TableColumn>消息</TableColumn></TableHeader>
              <TableBody items={attempts}>{(attempt) => {
                const result = attempt.success ? { label: '成功', color: 'success' as const } : attempt.duplicate ? { label: '已领取', color: 'primary' as const } : { label: '失败', color: 'danger' as const };
                return <TableRow key={attempt.plan_type}><TableCell><span className="font-semibold text-zinc-800">{attempt.plan_type}</span></TableCell><TableCell><Chip color={result.color} radius="sm" size="sm" variant="flat">{result.label}</Chip></TableCell><TableCell><span className="font-mono text-xs text-zinc-600">{attempt.http_status || '无响应'}</span></TableCell><TableCell><p className="max-w-xl whitespace-pre-wrap break-words text-xs leading-5 text-zinc-600">{attempt.message || '—'}</p></TableCell></TableRow>;
              }}</TableBody>
            </Table>
          </div>
        ) : <p className="text-sm text-zinc-500">{log.message || '暂无逐档记录'}</p>}
      </section>

      <section>
        <h3 className="mb-3 text-sm font-semibold text-zinc-900">原始记录</h3>
        <pre className="max-h-80 overflow-auto rounded-md bg-zinc-950 p-4 font-mono text-xs leading-5 text-zinc-100">{attempts.length > 0 ? JSON.stringify(rawRecords, null, 2) : '[]'}</pre>
      </section>
    </div>
  );
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
  const [editing, setEditing] = useState<Account | null>(null);
  const [editName, setEditName] = useState('');
  const [editNote, setEditNote] = useState('');
  const [editScheduleEnabled, setEditScheduleEnabled] = useState(true);
  const [editCron, setEditCron] = useState('0 10 * * *');
  const [logAccount, setLogAccount] = useState<Account | null>(null);
  const [claimLogs, setClaimLogs] = useState<PlanClaimLog[]>([]);
  const [selectedClaimLog, setSelectedClaimLog] = useState<PlanClaimLog | null>(null);
  const [logsLoading, setLogsLoading] = useState(false);
  const [snapshotAt, setSnapshotAt] = useState(() => Date.now());
  const [now, setNow] = useState(() => Date.now());
  const oauthErrorNotified = useRef(false);
  const nextResetReloadAt = useRef(0);
  const oauthModal = useDisclosure();
  const deleteModal = useDisclosure();
  const editModal = useDisclosure();
  const logsModal = useDisclosure();
  const { showToast } = useToast();

  const load = useCallback(async () => {
    try {
      const response = await apiFetch<{ data: Account[] }>('/api/accounts');
      const loadedAt = Date.now();
      setAccounts(response.data || []);
      setSnapshotAt(loadedAt);
      setNow(loadedAt);
      setError('');
    } catch (requestError) {
      setError(requestError instanceof Error ? requestError.message : '无法加载账号');
    } finally {
      setLoading(false);
    }
  }, []);

  useEffect(() => { void load(); }, [load]);

  useEffect(() => {
    const interval = window.setInterval(() => setNow(Date.now()), 1000);
    return () => window.clearInterval(interval);
  }, []);

  useEffect(() => {
    const resetDataShouldReload = accounts.some((account) => {
      const usage = accountUsage(account);
      return usage.resetAt && resetTargetTime(usage.resetAt, usage.secondsUntilReset, snapshotAt) + RESET_DATA_RELOAD_DELAY_MS <= now;
    });
    if (!resetDataShouldReload || nextResetReloadAt.current > now) return;
    nextResetReloadAt.current = now + RESET_DATA_RELOAD_RETRY_MS;
    void load();
  }, [accounts, load, now, snapshotAt]);

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
    return accounts.filter((account) => !normalized || account.name.toLowerCase().includes(normalized) || account.user.username.toLowerCase().includes(normalized) || account.id.includes(normalized) || account.note.toLowerCase().includes(normalized));
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
      await copyText(oauth.login_url);
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

  const openEdit = (account: Account) => {
    setEditing(account);
    setEditName(account.name);
    setEditNote(account.note || '');
    setEditScheduleEnabled(account.plan_claim_schedule.enabled);
    setEditCron(account.plan_claim_schedule.cron || '0 10 * * *');
    editModal.onOpen();
  };

  const saveAccount = async () => {
    if (!editing) return;
    const name = editName.trim();
    const cron = editCron.trim();
    if (!name) {
      showToast('error', '无法保存账号', '账号名称不能为空');
      return;
    }
    if (editScheduleEnabled && !cron) {
      showToast('error', '无法保存领取计划', 'CRON 表达式不能为空');
      return;
    }
    setBusy(`edit:${editing.id}`);
    try {
      await apiFetch(`/api/accounts/${editing.id}`, jsonRequest('PATCH', {
        name,
        note: editNote,
        plan_claim_schedule: { enabled: editScheduleEnabled, cron: cron || '0 10 * * *' },
      }));
      editModal.onClose();
      setEditing(null);
      await load();
      showToast('success', '账号已更新', `“${name}”的账号信息已保存`);
    } catch (requestError) {
      showToast('error', '保存账号失败', errorMessage(requestError, '请检查 CRON 表达式'));
    } finally {
      setBusy('');
    }
  };

  const claimAccount = async (account: Account) => {
    setBusy(`claim:${account.id}`);
    try {
      const result = await apiFetch<PlanClaimResult>(`/api/accounts/${account.id}/claim`, jsonRequest('POST'));
      await load();
      showToast('success', 'Coding Plan 领取完成', result.log.plan_name || `“${account.name}”权益与模型已同步`);
    } catch (requestError) {
      await load();
      showToast('error', 'Coding Plan 领取失败', `“${account.name}”：${errorMessage(requestError, '未知错误')}`);
    } finally {
      setBusy('');
    }
  };

  const runBatchAction = async (action: 'claim' | 'sync') => {
    if (accounts.length === 0) return;
    const actionLabel = action === 'claim' ? '领取 Plan' : '同步额度';
    setBusy(`${action}:all`);
    try {
      const results = await Promise.allSettled(accounts.map((account) => apiFetch(
        `/api/accounts/${account.id}/${action}`,
        jsonRequest('POST'),
      )));
      const failures = results.flatMap((result, index) => result.status === 'rejected' ? [{ account: accounts[index], reason: result.reason }] : []);
      await load();
      if (failures.length === 0) {
        showToast('success', `一键${actionLabel}完成`, `${accounts.length} 个账号已全部处理成功`);
        return;
      }
      const failedNames = failures.slice(0, 3).map(({ account }) => `“${account.name}”`).join('、');
      const remaining = failures.length > 3 ? `等 ${failures.length} 个账号` : '';
      const firstError = errorMessage(failures[0].reason, '未知错误');
      showToast('error', `一键${actionLabel}部分失败`, `${accounts.length - failures.length} 个成功，${failures.length} 个失败；${failedNames}${remaining}：${firstError}`);
    } finally {
      setBusy('');
    }
  };

  const openClaimLogs = async (account: Account) => {
    setLogAccount(account);
    setClaimLogs([]);
    setSelectedClaimLog(null);
    setLogsLoading(true);
    logsModal.onOpen();
    try {
      const response = await apiFetch<{ data: PlanClaimLog[] }>(`/api/plan-claims?account_id=${encodeURIComponent(account.id)}&limit=100`);
      setClaimLogs(response.data || []);
    } catch (requestError) {
      showToast('error', '无法加载领取记录', errorMessage(requestError, '请稍后重试'));
    } finally {
      setLogsLoading(false);
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
  const batchBusy = busy === 'claim:all' || busy === 'sync:all';

  return (
    <PageShell title="账号管理" description="AtomGit OAuth 账户、Coding Plan 权益与滚动额度" action={{ label: '连接 AtomGit', icon: Plus, onPress: () => void startOAuth() }}>
      {error ? <div className="flex items-start gap-2 rounded-md border border-red-200 bg-red-50 px-4 py-3 text-sm text-red-700" role="alert"><AlertCircle className="mt-0.5 shrink-0" size={16} />{error}</div> : null}

      <section className="grid gap-4 sm:grid-cols-3">
        <div className="rounded-lg border border-zinc-200 bg-white px-5 py-4"><p className="text-xs text-zinc-500">已连接账号</p><p className="mt-1 text-xl font-semibold text-zinc-900">{accounts.length}</p></div>
        <div className="rounded-lg border border-zinc-200 bg-white px-5 py-4"><p className="text-xs text-zinc-500">当前可调度</p><p className="mt-1 text-xl font-semibold text-emerald-600">{activeCount}</p></div>
        <div className="rounded-lg border border-zinc-200 bg-white px-5 py-4"><p className="text-xs text-zinc-500">Coding Plan 近 60 天 Tokens</p><p className="mt-1 text-xl font-semibold text-zinc-900">{formatTokens(providerTokens)}</p></div>
      </section>

      <section className="overflow-hidden rounded-lg border border-zinc-200 bg-white">
        <div className="flex flex-col gap-3 border-b border-zinc-100 p-4 sm:flex-row sm:items-center sm:justify-between"><Input aria-label="搜索账号" className="w-full sm:max-w-xs" classNames={{ inputWrapper: 'h-10 rounded-md border border-zinc-200 bg-white shadow-none' }} placeholder="搜索名称、用户名、备注或账号 ID" radius="sm" startContent={<Search size={16} className="text-zinc-400" />} value={query} variant="bordered" onValueChange={setQuery} /><div className="grid grid-cols-2 gap-2 sm:flex"><Button color="primary" isDisabled={loading || accounts.length === 0 || (Boolean(busy) && busy !== 'claim:all')} isLoading={busy === 'claim:all'} radius="sm" startContent={busy === 'claim:all' ? null : <Gift size={16} />} variant="flat" onPress={() => void runBatchAction('claim')}>一键领取 Plan</Button><Button isDisabled={loading || accounts.length === 0 || (Boolean(busy) && busy !== 'sync:all')} isLoading={busy === 'sync:all'} radius="sm" startContent={busy === 'sync:all' ? null : <RefreshCw size={16} />} variant="bordered" onPress={() => void runBatchAction('sync')}>一键同步额度</Button></div></div>
        {loading ? <div className="space-y-3 p-5">{Array.from({ length: 4 }).map((_, index) => <Skeleton key={index} className="h-14 w-full rounded-md" />)}</div> : (
          <div className="overflow-x-auto">
            <Table aria-label="AtomGit 账号列表" removeWrapper classNames={{ th: 'bg-zinc-50 text-xs text-zinc-500', td: 'py-4 text-sm' }}>
              <TableHeader><TableColumn>账号</TableColumn><TableColumn>订阅</TableColumn><TableColumn>当前窗口</TableColumn><TableColumn>可用模型</TableColumn><TableColumn>代理用量</TableColumn><TableColumn>最近同步</TableColumn><TableColumn>计划领取</TableColumn><TableColumn>状态</TableColumn><TableColumn align="end">操作</TableColumn></TableHeader>
              <TableBody items={filtered} emptyContent={<EmptyState icon={Users} title="尚未连接账号" description="连接 AtomGit 后将自动领取或同步 Coding Plan" />}>
                {(account) => {
                  const usage = accountUsage(account);
                  const status = statusMeta[account.status] || statusMeta.error;
                  return (
                    <TableRow key={account.id}>
                      <TableCell><div className="flex min-w-52 items-center gap-3">{account.user.avatar_url ? <img alt="" className="h-8 w-8 rounded-full bg-zinc-100 object-cover" src={account.user.avatar_url} /> : <span className="flex h-8 w-8 items-center justify-center rounded-full bg-zinc-100 text-xs font-semibold text-zinc-600">{account.name.slice(0, 2).toUpperCase()}</span>}<div className="min-w-0"><p className="font-medium text-zinc-900">{account.name}</p><p className="mt-0.5 text-xs text-zinc-400">@{account.user.username}</p>{account.note ? <p className="mt-1 max-w-64 truncate text-xs text-zinc-500" title={account.note}>{account.note}</p> : null}</div></div></TableCell>
                      <TableCell><div><p className="font-medium text-zinc-800">{account.plan.codingplan_free?.plan_name || '未领取'}</p><p className="mt-0.5 text-xs text-zinc-400">{account.plan.codingplan_free ? `剩余 ${account.plan.codingplan_free.remaining_days} 天` : '—'}</p></div></TableCell>
                      <TableCell><div className="min-w-[340px]"><div className="mb-1.5 flex items-center justify-between gap-3 text-xs"><span className="text-zinc-500">{usage.label}</span><span className={usage.percent >= 90 ? 'text-red-600' : 'text-zinc-500'}>{usage.percent.toFixed(0)}%</span></div><Progress aria-label="额度使用率" color={usage.percent >= 90 ? 'danger' : usage.percent >= 70 ? 'warning' : 'primary'} size="sm" value={Math.min(usage.percent, 100)} />{usage.resetAt ? <div className="mt-1 flex items-center justify-between gap-4 whitespace-nowrap text-[11px] text-zinc-400"><span>下一次重置时间:{formatResetTime(usage.resetAt)}</span><ResetCountdown resetAt={usage.resetAt} secondsUntilReset={usage.secondsUntilReset} snapshotAt={snapshotAt} /></div> : <p className="mt-1 text-[11px] text-zinc-400">等待窗口刷新</p>}</div></TableCell>
                      <TableCell><span className="font-medium text-zinc-700">{account.models.length}</span></TableCell>
                      <TableCell><div><p className="font-medium text-zinc-800">{formatTokens(account.input_tokens + account.output_tokens)}</p><p className="mt-0.5 text-xs text-zinc-400">{account.request_count.toLocaleString()} 次请求</p></div></TableCell>
                      <TableCell><span className="whitespace-nowrap text-zinc-500">{formatDateTime(account.last_sync_at)}</span></TableCell>
                      <TableCell><div className="min-w-28"><div className="flex items-center gap-2"><span className={`h-1.5 w-1.5 rounded-full ${account.plan_claim_schedule.enabled ? 'bg-emerald-500' : 'bg-zinc-300'}`} /><span className="text-xs text-zinc-600">{account.plan_claim_schedule.enabled ? '已启用' : '已停用'}</span></div><code className="mt-1 block whitespace-nowrap font-mono text-[11px] text-zinc-400">{account.plan_claim_schedule.cron}</code></div></TableCell>
                      <TableCell><Tooltip content={account.last_error || status.label}><Chip color={status.color} radius="sm" size="sm" variant="flat">{status.label}</Chip></Tooltip></TableCell>
                      <TableCell><div className="flex min-w-56 justify-end gap-1"><Tooltip content="编辑账号与领取计划"><Button isIconOnly aria-label="编辑账号" isDisabled={batchBusy} isLoading={busy === `edit:${account.id}`} radius="sm" size="sm" variant="light" onPress={() => openEdit(account)}><Pencil size={16} /></Button></Tooltip><Tooltip content="立即领取 Coding Plan"><Button isIconOnly aria-label="立即领取 Coding Plan" color="primary" isDisabled={batchBusy} isLoading={busy === `claim:${account.id}`} radius="sm" size="sm" variant="light" onPress={() => void claimAccount(account)}><Gift size={16} /></Button></Tooltip><Tooltip content="领取记录"><Button isIconOnly aria-label="查看领取记录" radius="sm" size="sm" variant="light" onPress={() => void openClaimLogs(account)}><History size={16} /></Button></Tooltip><Tooltip content="同步额度与模型"><Button isIconOnly aria-label="同步账号" isDisabled={batchBusy} isLoading={busy === `sync:${account.id}`} radius="sm" size="sm" variant="light" onPress={() => void syncAccount(account)}><RefreshCw size={16} /></Button></Tooltip><Tooltip content={account.enabled ? '暂停调度' : '恢复调度'}><Button isIconOnly aria-label={account.enabled ? '暂停账号' : '恢复账号'} isDisabled={batchBusy} isLoading={busy === `toggle:${account.id}`} radius="sm" size="sm" variant="light" onPress={() => void toggleAccount(account)}>{account.enabled ? <Pause size={16} /> : <Play size={16} />}</Button></Tooltip><Tooltip color="danger" content="删除账号"><Button isIconOnly aria-label="删除账号" color="danger" isDisabled={batchBusy} radius="sm" size="sm" variant="light" onPress={() => { setDeleting(account); deleteModal.onOpen(); }}><Trash2 size={16} /></Button></Tooltip></div></TableCell>
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

      <Modal isOpen={editModal.isOpen} radius="sm" size="lg" onOpenChange={editModal.onOpenChange}>
        <ModalContent>{(onClose) => <><ModalHeader>编辑账号</ModalHeader><ModalBody><div className="space-y-5"><Input isRequired label="账号名称" labelPlacement="outside" radius="sm" value={editName} onValueChange={setEditName} /><Textarea label="备注" labelPlacement="outside" minRows={3} placeholder="添加账号用途、归属等备注" radius="sm" value={editNote} onValueChange={setEditNote} /><div className="flex items-center justify-between gap-4 rounded-md border border-zinc-200 px-4 py-3"><div><p className="text-sm font-medium text-zinc-800">定时领取 Coding Plan</p><code className="mt-1 block font-mono text-xs text-zinc-400">{editCron || '0 10 * * *'}</code></div><Switch aria-label="定时领取 Coding Plan" isSelected={editScheduleEnabled} size="sm" onValueChange={setEditScheduleEnabled} /></div><Input isRequired={editScheduleEnabled} isDisabled={!editScheduleEnabled} label="CRON 表达式" labelPlacement="outside" placeholder="0 10 * * *" radius="sm" value={editCron} onValueChange={setEditCron} /></div></ModalBody><ModalFooter><Button radius="sm" variant="light" onPress={onClose}>取消</Button><Button color="primary" isLoading={busy === `edit:${editing?.id}`} radius="sm" onPress={() => void saveAccount()}>保存</Button></ModalFooter></>}</ModalContent>
      </Modal>

      <Modal isOpen={logsModal.isOpen} radius="sm" scrollBehavior="inside" size="4xl" onOpenChange={logsModal.onOpenChange}>
        <ModalContent>{(onClose) => <><ModalHeader className="flex items-center gap-2">{selectedClaimLog ? <Tooltip content="返回领取记录"><Button isIconOnly aria-label="返回领取记录" radius="sm" size="sm" variant="light" onPress={() => setSelectedClaimLog(null)}><ArrowLeft size={17} /></Button></Tooltip> : null}<span>{selectedClaimLog ? '领取详情' : logAccount ? `“${logAccount.name}”领取记录` : '领取记录'}</span></ModalHeader><ModalBody className="px-0">{selectedClaimLog ? <ClaimLogDetails log={selectedClaimLog} /> : logsLoading ? <div className="space-y-3 px-6 py-2">{Array.from({ length: 4 }).map((_, index) => <Skeleton key={index} className="h-12 w-full rounded-md" />)}</div> : <Table aria-label="Coding Plan 领取记录" removeWrapper classNames={{ th: 'bg-zinc-50 text-xs text-zinc-500', td: 'py-3 text-sm' }}><TableHeader><TableColumn>触发方式</TableColumn><TableColumn>结果</TableColumn><TableColumn>套餐</TableColumn><TableColumn>开始时间</TableColumn><TableColumn>消息</TableColumn><TableColumn align="end">详情</TableColumn></TableHeader><TableBody items={claimLogs} emptyContent="暂无领取记录">{(claimLog) => <TableRow key={claimLog.id}><TableCell><Chip color={claimLog.trigger === 'scheduled' ? 'primary' : 'default'} radius="sm" size="sm" variant="flat">{claimLog.trigger === 'scheduled' ? '定时' : '手动'}</Chip></TableCell><TableCell><Chip color={claimLog.status === 'success' ? 'success' : claimLog.status === 'failed' ? 'danger' : 'warning'} radius="sm" size="sm" variant="flat">{claimLog.status === 'success' ? '成功' : claimLog.status === 'failed' ? '失败' : '进行中'}</Chip></TableCell><TableCell><span className="whitespace-nowrap text-zinc-700">{claimLog.plan_name || '—'}</span></TableCell><TableCell><span className="whitespace-nowrap text-zinc-500">{formatDateTime(claimLog.started_at)}</span></TableCell><TableCell><p className="max-w-72 truncate text-xs text-zinc-500" title={claimLog.message}>{claimLog.message || '—'}</p></TableCell><TableCell><Tooltip content="查看领取详情"><Button isIconOnly aria-label="查看领取详情" radius="sm" size="sm" variant="light" onPress={() => setSelectedClaimLog(claimLog)}><Eye size={16} /></Button></Tooltip></TableCell></TableRow>}</TableBody></Table>}</ModalBody><ModalFooter>{selectedClaimLog ? <Button radius="sm" variant="light" onPress={() => setSelectedClaimLog(null)}>返回</Button> : null}<Button radius="sm" variant="light" onPress={onClose}>关闭</Button></ModalFooter></>}</ModalContent>
      </Modal>

      <Modal isOpen={deleteModal.isOpen} radius="sm" size="sm" onOpenChange={deleteModal.onOpenChange}>
        <ModalContent>{(onClose) => <><ModalHeader>删除账号</ModalHeader><ModalBody><p className="text-sm leading-6 text-zinc-600">删除“{deleting?.name}”后，其 OAuth 凭据和模型路由将立即移除。</p></ModalBody><ModalFooter><Button radius="sm" variant="light" onPress={onClose}>取消</Button><Button color="danger" isLoading={busy === `delete:${deleting?.id}`} radius="sm" onPress={() => void removeAccount()}>确认删除</Button></ModalFooter></>}</ModalContent>
      </Modal>
    </PageShell>
  );
}
