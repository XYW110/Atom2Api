import { useCallback, useEffect, useMemo, useState } from 'react';
import { Activity, AlertCircle, ArrowDownToLine, ArrowUpFromLine, Clock3, Gauge, KeyRound, RefreshCw, Timer, Users } from 'lucide-react';
import { Button, ButtonGroup, Chip, Skeleton, Table, TableBody, TableCell, TableColumn, TableHeader, TableRow } from '@heroui/react';
import { PageShell } from '../components/PageShell';
import { useToast } from '../components/Toast';
import { apiFetch, type DashboardResponse, errorMessage, formatDateTime, formatTokens } from '../api';

const emptyDashboard: DashboardResponse = {
  range: '24h',
  summary: { requests: 0, rpm: 0, input_tokens: 0, output_tokens: 0, total_tokens: 0, success_rate: 0, average_latency_ms: 0, active_accounts: 0, total_accounts: 0, active_keys: 0 },
  trend: [], model_distribution: [], recent_requests: [],
};

function trendLabel(start: string, range: string) {
  const date = new Date(start);
  if (Number.isNaN(date.getTime())) return '';
  const pad = (value: number) => value.toString().padStart(2, '0');
  return range === '24h'
    ? `${pad(date.getHours())}:${pad(date.getMinutes())}`
    : `${pad(date.getMonth() + 1)}-${pad(date.getDate())}`;
}

function recentLatency(request: NonNullable<DashboardResponse['recent_requests']>[number]) {
  if (!request.streaming) return `首字 不适用 · 耗时 ${request.latency_ms.toLocaleString()} ms`;
  const firstToken = request.first_token_latency_ms === undefined ? '未记录' : `${request.first_token_latency_ms.toLocaleString()} ms`;
  const completion = request.completion_latency_ms === undefined ? '未记录' : `${request.completion_latency_ms.toLocaleString()} ms`;
  return `首字 ${firstToken} · 完成 ${completion}`;
}

export default function DashboardPage() {
  const [range, setRange] = useState('24h');
  const [data, setData] = useState<DashboardResponse>(emptyDashboard);
  const [loading, setLoading] = useState(true);
  const [refreshing, setRefreshing] = useState(false);
  const [error, setError] = useState('');
  const { showToast } = useToast();

  const load = useCallback(async (silent = false, notify = false) => {
    if (silent) setRefreshing(true); else setLoading(true);
    try {
      setData(await apiFetch<DashboardResponse>(`/api/dashboard?range=${range}`));
      setError('');
      if (notify) showToast('success', '刷新成功', '仪表盘数据已更新');
    } catch (requestError) {
      const message = errorMessage(requestError, '无法加载仪表盘');
      setError(message);
      if (notify) showToast('error', '刷新失败', message);
    } finally {
      setLoading(false);
      setRefreshing(false);
    }
  }, [range, showToast]);

  useEffect(() => {
    void load();
    const interval = window.setInterval(() => void load(true), 15000);
    return () => window.clearInterval(interval);
  }, [load]);

  const maxRequests = useMemo(() => Math.max(...data.trend.map((point) => point.requests), 1), [data.trend]);
  const totalModelRequests = useMemo(() => (data.model_distribution || []).reduce((sum, item) => sum + item.requests, 0), [data.model_distribution]);
  const stats = [
    { label: '请求数', value: formatTokens(data.summary.requests), icon: Activity, tone: 'bg-blue-50 text-blue-600' },
    { label: 'RPM · 10 分钟均值', value: data.summary.rpm.toFixed(1), icon: Timer, tone: 'bg-cyan-50 text-cyan-600' },
    { label: '输入 Tokens', value: formatTokens(data.summary.input_tokens), icon: ArrowDownToLine, tone: 'bg-emerald-50 text-emerald-600' },
    { label: '输出 Tokens', value: formatTokens(data.summary.output_tokens), icon: ArrowUpFromLine, tone: 'bg-violet-50 text-violet-600' },
    { label: '成功率', value: `${data.summary.success_rate.toFixed(2)}%`, icon: Gauge, tone: 'bg-amber-50 text-amber-600' },
  ];

  return (
    <PageShell title="运行概览" description="OpenAI 网关流量、Token 用量与资源状态">
      <div className="flex flex-wrap items-center justify-between gap-3">
        <ButtonGroup radius="sm" size="sm" variant="flat">
          {[['24h', '24 小时'], ['7d', '7 天'], ['30d', '30 天']].map(([value, label]) => (
            <Button key={value} className={range === value ? 'bg-zinc-900 text-white' : ''} onPress={() => setRange(value)}>{label}</Button>
          ))}
        </ButtonGroup>
        <Button isIconOnly aria-label="刷新仪表盘" isLoading={refreshing} radius="sm" size="sm" variant="bordered" onPress={() => void load(true, true)}><RefreshCw size={16} /></Button>
      </div>

      {error ? <div className="flex items-start gap-2 rounded-md border border-red-200 bg-red-50 px-4 py-3 text-sm text-red-700" role="alert"><AlertCircle className="mt-0.5 shrink-0" size={16} />{error}</div> : null}

      <section aria-label="关键指标" className="grid gap-4 sm:grid-cols-2 lg:grid-cols-3 xl:grid-cols-5">
        {stats.map((stat) => {
          const Icon = stat.icon;
          return (
            <div key={stat.label} className="rounded-lg border border-zinc-200 bg-white p-5">
              {loading ? <><Skeleton className="h-4 w-24 rounded" /><Skeleton className="mt-3 h-8 w-32 rounded" /></> : <div className="flex items-start justify-between"><div><p className="text-sm text-zinc-500">{stat.label}</p><p className="mt-2 text-2xl font-semibold text-zinc-950">{stat.value}</p></div><div className={`flex h-9 w-9 items-center justify-center rounded-lg ${stat.tone}`}><Icon size={18} /></div></div>}
            </div>
          );
        })}
      </section>

      <section className="grid gap-4 xl:grid-cols-[minmax(0,1.55fr)_minmax(320px,0.75fr)]">
        <div className="overflow-hidden rounded-lg border border-zinc-200 bg-white p-5 sm:p-6">
          <div className="flex items-start justify-between gap-3"><div><h2 className="text-base font-semibold text-zinc-900">请求趋势</h2><p className="mt-1 text-xs text-zinc-500">成功与失败请求按时间聚合</p></div><div className="flex gap-4 text-xs text-zinc-500"><span className="flex items-center gap-1.5"><span className="h-2 w-2 rounded-sm bg-blue-500" />请求</span><span className="flex items-center gap-1.5"><span className="h-2 w-2 rounded-sm bg-red-400" />失败</span></div></div>
          <div className="mt-6 overflow-x-auto">
            <div className="grid h-56 min-w-[640px] items-end gap-2 border-b border-zinc-200" style={{ gridTemplateColumns: `repeat(${Math.max(data.trend.length, 1)}, minmax(18px, 1fr))` }}>
              {loading ? Array.from({ length: 12 }).map((_, index) => <Skeleton key={index} className="mx-auto w-5 rounded-t" style={{ height: `${40 + (index % 5) * 22}px` }} />) : data.trend.map((point) => (
                <div key={point.start} className="group flex h-full min-w-0 flex-col items-center justify-end gap-1">
                  <div className="relative flex h-[180px] w-full max-w-8 items-end overflow-hidden rounded-t-sm bg-zinc-100" title={`${point.requests} 次请求 · ${point.errors} 次失败`}>
                    <div className="w-full bg-blue-500" style={{ height: `${Math.max((point.requests / maxRequests) * 100, point.requests ? 5 : 0)}%` }} />
                    <div className="absolute bottom-0 left-0 w-full bg-red-400" style={{ height: `${point.requests ? (point.errors / maxRequests) * 100 : 0}%` }} />
                  </div>
                  <span className="pb-2 text-[10px] text-zinc-400">{trendLabel(point.start, range)}</span>
                </div>
              ))}
            </div>
          </div>
        </div>

        <div className="rounded-lg border border-zinc-200 bg-white p-5 sm:p-6">
          <h2 className="text-base font-semibold text-zinc-900">模型调用分布</h2><p className="mt-1 text-xs text-zinc-500">当前时间范围内的请求占比</p>
          <div className="mt-6 space-y-5">
            {(data.model_distribution || []).slice(0, 6).map((model) => {
              const percent = totalModelRequests ? (model.requests / totalModelRequests) * 100 : 0;
              return <div key={model.model}><div className="mb-2 flex items-center justify-between gap-3 text-xs"><span className="truncate font-medium text-zinc-700">{model.model || '未知模型'}</span><span className="shrink-0 text-zinc-400">{formatTokens(model.requests)} · {percent.toFixed(0)}%</span></div><div className="h-1.5 overflow-hidden rounded-sm bg-zinc-100"><div className="h-full rounded-sm bg-zinc-800" style={{ width: `${percent}%` }} /></div></div>;
            })}
            {!loading && !(data.model_distribution || []).length ? <p className="py-16 text-center text-sm text-zinc-400">当前范围暂无调用</p> : null}
          </div>
        </div>
      </section>

      <section className="grid gap-4 md:grid-cols-3">
        <div className="flex items-center gap-4 rounded-lg border border-zinc-200 bg-white px-5 py-4"><div className="flex h-10 w-10 items-center justify-center rounded-lg bg-emerald-50 text-emerald-600"><Users size={19} /></div><div><p className="text-sm font-medium text-zinc-800">可用账号</p><p className="mt-1 text-xs text-zinc-500">{data.summary.active_accounts} / {data.summary.total_accounts} 个</p></div></div>
        <div className="flex items-center gap-4 rounded-lg border border-zinc-200 bg-white px-5 py-4"><div className="flex h-10 w-10 items-center justify-center rounded-lg bg-blue-50 text-blue-600"><KeyRound size={19} /></div><div><p className="text-sm font-medium text-zinc-800">有效密钥</p><p className="mt-1 text-xs text-zinc-500">{data.summary.active_keys} 个</p></div></div>
        <div className="flex items-center gap-4 rounded-lg border border-zinc-200 bg-white px-5 py-4"><div className="flex h-10 w-10 items-center justify-center rounded-lg bg-amber-50 text-amber-600"><Clock3 size={19} /></div><div><p className="text-sm font-medium text-zinc-800">平均延迟</p><p className="mt-1 text-xs text-zinc-500">{data.summary.average_latency_ms.toLocaleString()} ms</p></div></div>
      </section>

      <section className="overflow-hidden rounded-lg border border-zinc-200 bg-white">
        <div className="border-b border-zinc-100 px-5 py-4 sm:px-6"><h2 className="text-base font-semibold text-zinc-900">最近请求</h2><p className="mt-1 text-xs text-zinc-500">最新 20 条网关调用</p></div>
        <div className="overflow-x-auto">
          <Table aria-label="最近请求" removeWrapper classNames={{ th: 'bg-zinc-50 text-xs text-zinc-500', td: 'py-3.5 text-sm' }}>
            <TableHeader><TableColumn>请求 ID</TableColumn><TableColumn>模型</TableColumn><TableColumn>密钥</TableColumn><TableColumn>账号</TableColumn><TableColumn>延迟</TableColumn><TableColumn>Tokens</TableColumn><TableColumn>状态</TableColumn><TableColumn>时间</TableColumn></TableHeader>
            <TableBody items={data.recent_requests || []} emptyContent={loading ? '正在加载' : '暂无请求记录'}>
              {(request) => <TableRow key={request.id}><TableCell><code className="text-xs text-zinc-500">{request.id}</code></TableCell><TableCell><span className="font-medium text-zinc-800">{request.model}</span></TableCell><TableCell>{request.key_name || '—'}</TableCell><TableCell>{request.account_name || '—'}</TableCell><TableCell><span className="whitespace-nowrap text-xs tabular-nums text-zinc-600">{recentLatency(request)}</span></TableCell><TableCell>{formatTokens(request.input_tokens + request.output_tokens)}</TableCell><TableCell><Chip color={request.status >= 200 && request.status < 400 ? 'success' : 'danger'} radius="sm" size="sm" variant="flat">{request.status}</Chip></TableCell><TableCell><span className="whitespace-nowrap text-zinc-500">{formatDateTime(request.timestamp)}</span></TableCell></TableRow>}
            </TableBody>
          </Table>
        </div>
      </section>
    </PageShell>
  );
}
