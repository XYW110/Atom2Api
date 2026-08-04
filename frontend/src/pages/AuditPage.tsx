import { type ReactNode, useCallback, useEffect, useMemo, useState } from 'react';
import {
  AlertCircle,
  ArrowDownToLine,
  ArrowUpFromLine,
  Check,
  Clipboard,
  Clock3,
  FileJson,
  RefreshCw,
  Search,
} from 'lucide-react';
import {
  Button,
  ButtonGroup,
  Chip,
  Input,
  Modal,
  ModalBody,
  ModalContent,
  ModalHeader,
  Pagination,
  Spinner,
  Tab,
  Table,
  TableBody,
  TableCell,
  TableColumn,
  TableHeader,
  TableRow,
  Tabs,
  Tooltip,
} from '@heroui/react';
import { apiFetch, type AuditListResponse, type AuditRecordDetail, type AuditRecordSummary, type AuditRequestAttempt, errorMessage, formatTokens } from '../api';
import { copyText } from '../clipboard';
import { EmptyState, PageShell } from '../components/PageShell';
import { useToast } from '../components/Toast';

const PAGE_SIZE = 20;
const emptyResult: AuditListResponse = { items: [], total: 0, page: 1, page_size: PAGE_SIZE, pages: 0 };

function statusColor(status: number): 'success' | 'warning' | 'danger' | 'default' {
  if (status >= 200 && status < 300) return 'success';
  if (status >= 300 && status < 400) return 'warning';
  if (status >= 400) return 'danger';
  return 'default';
}

function methodClass(method: string) {
  switch (method.toUpperCase()) {
    case 'GET': return 'bg-emerald-50 text-emerald-700 ring-emerald-200';
    case 'PUT':
    case 'PATCH': return 'bg-amber-50 text-amber-700 ring-amber-200';
    case 'DELETE': return 'bg-red-50 text-red-700 ring-red-200';
    default: return 'bg-blue-50 text-blue-700 ring-blue-200';
  }
}

function formatLatency(milliseconds: number) {
  if (milliseconds < 1000) return `${milliseconds.toLocaleString()} ms`;
  return `${(milliseconds / 1000).toFixed(milliseconds < 10_000 ? 2 : 1)} s`;
}

function LatencyBreakdown({ record }: { record: Pick<AuditRecordSummary, 'streaming' | 'latency_ms' | 'first_token_latency_ms' | 'completion_latency_ms'> }) {
  if (!record.streaming) {
    return <div className="min-w-28 whitespace-nowrap text-[11px] tabular-nums text-zinc-500"><p><span className="text-zinc-400">首字</span> 不适用</p><p className="mt-1 flex items-center gap-1"><Clock3 size={11} className="text-zinc-400" /><span className="text-zinc-400">耗时</span> {formatLatency(record.latency_ms)}</p></div>;
  }
  const firstToken = record.first_token_latency_ms === undefined ? '未记录' : formatLatency(record.first_token_latency_ms);
  const completion = record.completion_latency_ms === undefined ? '未记录' : formatLatency(record.completion_latency_ms);
  return (
    <Tooltip content={`请求总耗时 ${formatLatency(record.latency_ms)}`}>
      <div className="min-w-28 whitespace-nowrap text-[11px] tabular-nums text-zinc-500">
        <p><span className="text-zinc-400">首字</span> {firstToken}</p>
        <p className="mt-1"><span className="text-zinc-400">完成</span> {completion}</p>
      </div>
    </Tooltip>
  );
}

function formatRequestTime(value: string) {
  const date = new Date(value);
  if (Number.isNaN(date.getTime())) return value;
  return new Intl.DateTimeFormat('zh-CN', {
    year: 'numeric', month: '2-digit', day: '2-digit',
    hour: '2-digit', minute: '2-digit', second: '2-digit', hour12: false,
  }).format(date);
}

function readableBody(body?: string) {
  if (!body) return '';
  try {
    return JSON.stringify(JSON.parse(body), null, 2);
  } catch {
    return body;
  }
}

function formattedHeaders(headers?: Record<string, string[]>) {
  if (!headers || Object.keys(headers).length === 0) return '';
  return Object.entries(headers)
    .flatMap(([key, values]) => values.length > 0 ? values.map((value) => `${key}: ${value}`) : [`${key}:`])
    .join('\n');
}

function isObject(value: unknown): value is Record<string, unknown> {
  return typeof value === 'object' && value !== null && !Array.isArray(value);
}

function contentText(value: unknown): string {
  if (typeof value === 'string') return value;
  if (Array.isArray(value)) return value.map(contentText).join('');
  if (!isObject(value)) return '';
  if (typeof value.text === 'string') return value.text;
  return contentText(value.content);
}

function completedResponseText(payload: Record<string, unknown>): string {
  const response = isObject(payload.response) ? payload.response : payload;
  if (typeof response.output_text === 'string') return response.output_text;
  if (!Array.isArray(response.output)) return '';
  return response.output.map((item) => {
    if (!isObject(item) || !Array.isArray(item.content)) return '';
    return item.content.map(contentText).join('');
  }).join('');
}

function streamedResponseText(body?: string): string {
  if (!body) return '';
  const deltas: string[] = [];
  const completed: string[] = [];

  for (const line of body.split(/\r?\n/)) {
    const match = /^data:\s?(.*)$/.exec(line.trim());
    if (!match || !match[1] || match[1] === '[DONE]') continue;
    try {
      const payload: unknown = JSON.parse(match[1]);
      if (!isObject(payload)) continue;
      if (payload.type === 'response.output_text.delta' && typeof payload.delta === 'string') {
        deltas.push(payload.delta);
        continue;
      }
      if (Array.isArray(payload.choices)) {
        for (const choice of payload.choices) {
          if (!isObject(choice) || !isObject(choice.delta)) continue;
          const text = contentText(choice.delta.content);
          if (text) deltas.push(text);
        }
        continue;
      }
      const text = completedResponseText(payload);
      if (text) completed.push(text);
    } catch {
      // Ignore keep-alives and non-JSON SSE events; raw mode still exposes them.
    }
  }

  return deltas.join('') || completed[completed.length - 1] || '';
}

function TextViewer({ content, copyContent, emptyText, copyLabel, controls }: { content?: string; copyContent?: string; emptyText: string; copyLabel: string; controls?: ReactNode }) {
  const [copied, setCopied] = useState(false);
  const { showToast } = useToast();

  const copy = async () => {
    const value = copyContent ?? content;
    if (!value) return;
    try {
      await copyText(value);
      setCopied(true);
      showToast('success', '复制成功', `${copyLabel.replace('复制', '')}已复制到剪贴板`);
      window.setTimeout(() => setCopied(false), 1600);
    } catch (copyError) {
      showToast('error', '复制失败', errorMessage(copyError, '请手动选择并复制内容'));
    }
  };

  if (!content) {
    return (
      <div className="flex min-h-72 flex-col items-center justify-center border-y border-zinc-200 bg-zinc-50 px-6 text-center">
        <FileJson size={24} className="text-zinc-300" />
        <p className="mt-3 text-sm text-zinc-500">{emptyText}</p>
      </div>
    );
  }

  return (
    <div className="overflow-hidden border-y border-zinc-800 bg-zinc-950">
      <div className="flex h-11 items-center justify-between border-b border-white/10 px-4">
        <span className="text-xs font-medium text-zinc-400">{content.length.toLocaleString()} 字符</span>
        <div className="flex items-center gap-2">
          {controls}
          <Tooltip content={copied ? '已复制' : copyLabel}>
            <Button isIconOnly aria-label={copyLabel} className="text-zinc-400 data-[hover=true]:bg-white/10 data-[hover=true]:text-white" radius="sm" size="sm" variant="light" onPress={() => void copy()}>
              {copied ? <Check size={16} /> : <Clipboard size={16} />}
            </Button>
          </Tooltip>
        </div>
      </div>
      <pre className="max-h-[52vh] min-h-72 overflow-auto whitespace-pre-wrap break-words p-4 font-mono text-xs leading-6 text-zinc-200">{content}</pre>
    </div>
  );
}

function BodyViewer({ body, emptyText, copyLabel }: { body?: string; emptyText: string; copyLabel: string }) {
  const formatted = useMemo(() => readableBody(body), [body]);
  return <TextViewer content={formatted} copyContent={body} copyLabel={copyLabel} emptyText={emptyText} />;
}

function HeaderViewer({ headers, emptyText, copyLabel }: { headers?: Record<string, string[]>; emptyText: string; copyLabel: string }) {
  const formatted = useMemo(() => formattedHeaders(headers), [headers]);
  return <TextViewer content={formatted} copyLabel={copyLabel} emptyText={emptyText} />;
}

function StreamBodyViewer({ body, emptyText, copyLabel }: { body?: string; emptyText: string; copyLabel: string }) {
  const [raw, setRaw] = useState(false);
  const formatted = useMemo(() => streamedResponseText(body), [body]);
  useEffect(() => setRaw(false), [body]);
  const content = raw ? body : (formatted || body);
  const controls = (
    <ButtonGroup aria-label="响应内容显示方式" radius="sm" size="sm" variant="flat">
      <Button color={!raw ? 'primary' : 'default'} variant={!raw ? 'solid' : 'flat'} onPress={() => setRaw(false)}>格式化</Button>
      <Button color={raw ? 'primary' : 'default'} variant={raw ? 'solid' : 'flat'} onPress={() => setRaw(true)}>原始</Button>
    </ButtonGroup>
  );
  return <TextViewer content={content} copyLabel={copyLabel} controls={controls} emptyText={emptyText} />;
}

function AttemptViewer({ attempts, streaming }: { attempts: AuditRequestAttempt[]; streaming: boolean }) {
  const [selected, setSelected] = useState(0);
  useEffect(() => setSelected(0), [attempts]);
  const attempt = attempts[Math.min(selected, attempts.length - 1)];
  if (!attempt) return null;

  return (
    <div className="border-t border-zinc-200">
      <div className="flex flex-wrap gap-2 border-b border-zinc-200 bg-zinc-50 px-5 py-3">
        {attempts.map((item, index) => (
          <Button key={item.attempt} color={selected === index ? 'primary' : 'default'} radius="sm" size="sm" variant={selected === index ? 'solid' : 'bordered'} onPress={() => setSelected(index)}>
            {item.attempt === 1 ? '首次请求' : `第 ${item.attempt - 1} 次重试`} · {item.status || '无响应'}
          </Button>
        ))}
      </div>
      <div className="flex flex-wrap items-center gap-x-4 gap-y-2 border-b border-zinc-200 px-5 py-3 text-xs text-zinc-500">
        <Chip color={statusColor(attempt.status || 0)} radius="sm" size="sm" variant="flat">{attempt.status || '无状态码'}</Chip>
        <span>耗时 {formatLatency(attempt.latency_ms)}</span>
        {attempt.error ? <span className="break-all text-red-600">{attempt.error}</span> : null}
      </div>
      <Tabs aria-label="单次请求响应详情" classNames={{ base: 'px-5 pt-3', panel: 'p-0 pt-3' }} color="primary" variant="underlined">
        <Tab key="attempt-response" title="响应内容">{streaming ? <StreamBodyViewer body={attempt.response_body} copyLabel="复制响应内容" emptyText="本次请求没有响应正文" /> : <BodyViewer body={attempt.response_body} copyLabel="复制响应内容" emptyText="本次请求没有响应正文" />}</Tab>
        <Tab key="attempt-headers" title="响应 Header"><HeaderViewer headers={attempt.response_headers} copyLabel="复制响应 Header" emptyText="本次请求没有响应 Header" /></Tab>
      </Tabs>
    </div>
  );
}

export default function AuditPage() {
  const [data, setData] = useState<AuditListResponse>(emptyResult);
  const [page, setPage] = useState(1);
  const [query, setQuery] = useState('');
  const [debouncedQuery, setDebouncedQuery] = useState('');
  const [status, setStatus] = useState('');
  const [method, setMethod] = useState('');
  const [loading, setLoading] = useState(true);
  const [refreshing, setRefreshing] = useState(false);
  const [error, setError] = useState('');
  const [selectedId, setSelectedId] = useState<string | null>(null);
  const [detail, setDetail] = useState<AuditRecordDetail | null>(null);
  const [detailLoading, setDetailLoading] = useState(false);
  const [detailError, setDetailError] = useState('');
  const { showToast } = useToast();

  useEffect(() => {
    const timer = window.setTimeout(() => {
      setDebouncedQuery(query.trim());
      setPage(1);
    }, 300);
    return () => window.clearTimeout(timer);
  }, [query]);

  const load = useCallback(async (silent = false, notify = false) => {
    if (silent) setRefreshing(true); else setLoading(true);
    const params = new URLSearchParams({ page: String(page), page_size: String(PAGE_SIZE) });
    if (debouncedQuery) params.set('q', debouncedQuery);
    if (status) params.set('status', status);
    if (method) params.set('method', method);
    try {
      setData(await apiFetch<AuditListResponse>(`/api/audit?${params.toString()}`));
      setError('');
      if (notify) showToast('success', '刷新成功', '审计记录已更新');
    } catch (requestError) {
      const message = errorMessage(requestError, '无法加载审计记录');
      setError(message);
      if (notify) showToast('error', '刷新失败', message);
    } finally {
      setLoading(false);
      setRefreshing(false);
    }
  }, [debouncedQuery, method, page, showToast, status]);

  useEffect(() => { void load(); }, [load]);

  const openDetail = async (record: AuditRecordSummary) => {
    setSelectedId(record.id);
    setDetail(null);
    setDetailError('');
    setDetailLoading(true);
    try {
      setDetail(await apiFetch<AuditRecordDetail>(`/api/audit/${encodeURIComponent(record.id)}`));
    } catch (requestError) {
      setDetailError(requestError instanceof Error ? requestError.message : '无法加载请求详情');
    } finally {
      setDetailLoading(false);
    }
  };

  const closeDetail = () => {
    setSelectedId(null);
    setDetail(null);
    setDetailError('');
  };

  return (
    <PageShell title="请求审计" description="代理流量的请求、响应与执行指标记录">
      {error ? <div className="flex items-start gap-2 rounded-md border border-red-200 bg-red-50 px-4 py-3 text-sm text-red-700" role="alert"><AlertCircle className="mt-0.5 shrink-0" size={16} />{error}</div> : null}

      <section className="overflow-hidden rounded-lg border border-zinc-200 bg-white">
        <div className="flex flex-col gap-3 border-b border-zinc-200 p-4 lg:flex-row lg:items-center">
          <Input
            aria-label="搜索请求"
            className="w-full lg:max-w-sm"
            classNames={{ inputWrapper: 'h-10 rounded-md border border-zinc-200 bg-white shadow-none' }}
            placeholder="搜索请求 ID、路径、模型或账号"
            radius="sm"
            startContent={<Search size={16} className="text-zinc-400" />}
            value={query}
            variant="bordered"
            onValueChange={setQuery}
          />
          <div className="grid grid-cols-2 gap-3 sm:flex">
            <select aria-label="筛选请求方式" className="h-10 min-w-32 rounded-md border border-zinc-200 bg-white px-3 text-sm text-zinc-700 outline-none transition focus:border-blue-500 focus:ring-2 focus:ring-blue-500/20" value={method} onChange={(event) => { setMethod(event.target.value); setPage(1); }}>
              <option value="">全部方式</option>
              <option value="POST">POST</option>
              <option value="GET">GET</option>
              <option value="PUT">PUT</option>
              <option value="PATCH">PATCH</option>
              <option value="DELETE">DELETE</option>
            </select>
            <select aria-label="筛选请求状态" className="h-10 min-w-32 rounded-md border border-zinc-200 bg-white px-3 text-sm text-zinc-700 outline-none transition focus:border-blue-500 focus:ring-2 focus:ring-blue-500/20" value={status} onChange={(event) => { setStatus(event.target.value); setPage(1); }}>
              <option value="">全部状态</option>
              <option value="success">成功</option>
              <option value="error">失败</option>
              <option value="200">200</option>
              <option value="400">400</option>
              <option value="401">401</option>
              <option value="429">429</option>
              <option value="500">500</option>
              <option value="502">502</option>
            </select>
          </div>
          <Tooltip content="刷新审计记录">
            <Button isIconOnly aria-label="刷新审计记录" className="ml-auto" isLoading={refreshing} radius="sm" variant="bordered" onPress={() => void load(true, true)}><RefreshCw size={16} /></Button>
          </Tooltip>
        </div>

        <div className="overflow-x-auto">
          <Table aria-label="请求审计记录" removeWrapper classNames={{ th: 'h-11 bg-zinc-50 text-xs text-zinc-500', td: 'h-[72px] py-3 text-sm' }}>
            <TableHeader>
              <TableColumn>请求</TableColumn>
              <TableColumn>方式</TableColumn>
              <TableColumn>模型</TableColumn>
              <TableColumn>路由账号</TableColumn>
              <TableColumn>密钥</TableColumn>
              <TableColumn>Tokens</TableColumn>
              <TableColumn>状态码</TableColumn>
              <TableColumn>耗时</TableColumn>
              <TableColumn>请求时间</TableColumn>
            </TableHeader>
            <TableBody
              items={data.items}
              isLoading={loading}
              loadingContent={<Spinner color="primary" label="正在加载审计记录" size="sm" />}
              emptyContent={<EmptyState icon={FileJson} title="暂无请求记录" description="代理收到请求后，审计记录会显示在这里" />}
            >
              {(record) => (
                <TableRow key={record.id}>
                  <TableCell>
                    <div className="min-w-52 max-w-72">
                      <code className="block truncate font-mono text-xs font-medium text-zinc-700" title={record.id}>{record.id}</code>
                      <p className="mt-1 truncate font-mono text-[11px] text-zinc-400" title={record.path}>{record.path}</p>
                    </div>
                  </TableCell>
                  <TableCell><span className={`inline-flex min-w-14 justify-center rounded px-2 py-1 font-mono text-[11px] font-semibold ring-1 ring-inset ${methodClass(record.method)}`}>{record.method}</span></TableCell>
                  <TableCell><div className="min-w-40 max-w-60"><p className="truncate font-medium text-zinc-800" title={record.model}>{record.model || '未知模型'}</p>{record.streaming ? <p className="mt-1 text-[11px] text-blue-600">流式响应</p> : null}</div></TableCell>
                  <TableCell><span className="block min-w-28 max-w-48 truncate text-zinc-600" title={record.account_name || undefined}>{record.account_name || '—'}</span></TableCell>
                  <TableCell><span className="block min-w-28 max-w-48 truncate text-zinc-600" title={record.key_name || undefined}>{record.key_name || '—'}</span></TableCell>
                  <TableCell>
                    <div className="min-w-28">
                      <p className="font-medium tabular-nums text-zinc-800">{formatTokens(record.input_tokens + record.output_tokens)}</p>
                      <div className="mt-1 flex items-center gap-2 text-[11px] tabular-nums text-zinc-400"><span className="flex items-center gap-0.5"><ArrowDownToLine size={11} />{formatTokens(record.input_tokens)}</span><span className="flex items-center gap-0.5"><ArrowUpFromLine size={11} />{formatTokens(record.output_tokens)}</span></div>
                    </div>
                  </TableCell>
                  <TableCell>
                    <Button aria-label={`查看请求 ${record.id} 的完整内容，状态码 ${record.status}，重试 ${record.retry_count} 次`} className="h-auto min-h-8 py-1" color={statusColor(record.status)} radius="sm" size="sm" variant="flat" onPress={() => void openDetail(record)}>
                      <span className="flex flex-col items-center font-mono text-xs font-semibold"><span>{record.status || '—'}</span>{record.retry_count > 0 ? <span className="text-[10px] font-normal">重试 {record.retry_count} 次</span> : null}</span>
                    </Button>
                  </TableCell>
                  <TableCell><LatencyBreakdown record={record} /></TableCell>
                  <TableCell><time className="whitespace-nowrap tabular-nums text-zinc-500" dateTime={record.timestamp}>{formatRequestTime(record.timestamp)}</time></TableCell>
                </TableRow>
              )}
            </TableBody>
          </Table>
        </div>

        <div className="flex min-h-16 flex-col items-center justify-between gap-3 border-t border-zinc-200 px-4 py-3 text-xs text-zinc-500 sm:flex-row">
          <span>共 {data.total.toLocaleString()} 条记录 · 第 {data.total ? data.page : 0} / {data.pages} 页</span>
          <Pagination aria-label="审计记录分页" isCompact showControls page={page} total={Math.max(data.pages, 1)} onChange={setPage} />
        </div>
      </section>

      <Modal backdrop="blur" isOpen={selectedId !== null} radius="sm" scrollBehavior="inside" size="5xl" onClose={closeDetail}>
        <ModalContent>
          <ModalHeader className="flex flex-col gap-1 border-b border-zinc-200 px-5 py-4 sm:px-6">
            <span className="text-base font-semibold text-zinc-900">请求详情</span>
            <code className="font-mono text-[11px] font-normal text-zinc-400">{selectedId}</code>
          </ModalHeader>
          <ModalBody className="gap-0 p-0">
            {detailLoading ? <div className="flex min-h-[480px] items-center justify-center"><Spinner label="正在加载完整内容" /></div> : null}
            {detailError ? <div className="m-6 flex items-start gap-2 rounded-md border border-red-200 bg-red-50 px-4 py-3 text-sm text-red-700" role="alert"><AlertCircle className="mt-0.5 shrink-0" size={16} />{detailError}</div> : null}
            {detail ? (
              <>
                <div className="grid gap-px border-b border-zinc-200 bg-zinc-200 sm:grid-cols-2 lg:grid-cols-6">
                  <div className="bg-white px-5 py-3"><p className="text-[11px] text-zinc-400">请求</p><p className="mt-1 truncate font-mono text-xs font-medium text-zinc-800"><span className="mr-2 text-blue-600">{detail.method}</span>{detail.path}</p></div>
                  <div className="bg-white px-5 py-3"><p className="text-[11px] text-zinc-400">模型</p><p className="mt-1 truncate text-xs font-medium text-zinc-800" title={detail.model}>{detail.model || '未知模型'}</p></div>
                  <div className="bg-white px-5 py-3"><p className="text-[11px] text-zinc-400">路由账号</p><p className="mt-1 truncate text-xs font-medium text-zinc-800" title={detail.account_name || undefined}>{detail.account_name || '—'}</p></div>
                  <div className="bg-white px-5 py-3"><p className="text-[11px] text-zinc-400">密钥</p><p className="mt-1 truncate text-xs font-medium text-zinc-800" title={detail.key_name || undefined}>{detail.key_name || '—'}</p></div>
                  <div className="bg-white px-5 py-3"><p className="text-[11px] text-zinc-400">状态与耗时</p><div className="mt-1 flex items-center gap-3"><Chip color={statusColor(detail.status)} radius="sm" size="sm" variant="flat">{detail.status}</Chip><LatencyBreakdown record={detail} /></div>{detail.retry_count > 0 ? <p className="mt-1 text-[11px] font-medium text-amber-600">已重试 {detail.retry_count} 次</p> : null}</div>
                  <div className="bg-white px-5 py-3"><p className="text-[11px] text-zinc-400">请求时间</p><time className="mt-1 block whitespace-nowrap text-xs tabular-nums text-zinc-700" dateTime={detail.timestamp}>{formatRequestTime(detail.timestamp)}</time></div>
                </div>
                {detail.error ? <div className="flex items-start gap-2 border-b border-red-200 bg-red-50 px-5 py-3 text-xs text-red-700"><AlertCircle className="mt-0.5 shrink-0" size={14} /><span className="break-all">{detail.error}</span></div> : null}
                <Tabs aria-label="请求和响应详情" classNames={{ base: 'px-5 pt-3', panel: 'p-0 pt-3' }} color="primary" variant="underlined">
                  <Tab key="request" title="请求内容"><BodyViewer body={detail.request_body} copyLabel="复制请求内容" emptyText="此历史记录未保存请求正文" /></Tab>
                  <Tab key="request-headers" title="请求 Header"><HeaderViewer headers={detail.request_headers} copyLabel="复制请求 Header" emptyText="此历史记录未保存请求 Header" /></Tab>
                  {detail.attempts && detail.attempts.length > 0 ? <Tab key="attempts" title={`响应明细 (${detail.attempts.length})`}><AttemptViewer attempts={detail.attempts} streaming={detail.streaming} /></Tab> : <Tab key="response" title="响应内容">{detail.streaming ? <StreamBodyViewer body={detail.response_body} copyLabel="复制响应内容" emptyText="此历史记录未保存响应正文" /> : <BodyViewer body={detail.response_body} copyLabel="复制响应内容" emptyText="此历史记录未保存响应正文" />}</Tab>}
                  {detail.attempts && detail.attempts.length > 0 ? null : <Tab key="response-headers" title="响应 Header"><HeaderViewer headers={detail.response_headers} copyLabel="复制响应 Header" emptyText="此历史记录未保存响应 Header" /></Tab>}
                </Tabs>
              </>
            ) : null}
          </ModalBody>
        </ModalContent>
      </Modal>
    </PageShell>
  );
}
