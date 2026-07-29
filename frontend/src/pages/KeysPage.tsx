import { useCallback, useEffect, useMemo, useState, type FormEvent } from 'react';
import { AlertCircle, Check, Copy, KeyRound, Plus, Search, ShieldOff, Trash2 } from 'lucide-react';
import { Button, Checkbox, Chip, Input, Modal, ModalBody, ModalContent, ModalFooter, ModalHeader, Skeleton, Table, TableBody, TableCell, TableColumn, TableHeader, TableRow, Tooltip, useDisclosure } from '@heroui/react';
import { EmptyState, PageShell } from '../components/PageShell';
import { useToast } from '../components/Toast';
import { apiFetch, type APIKeyRecord, type ModelRecord, errorMessage, formatDateTime, formatTokens, jsonRequest } from '../api';

export default function KeysPage() {
  const [keys, setKeys] = useState<APIKeyRecord[]>([]);
  const [models, setModels] = useState<ModelRecord[]>([]);
  const [query, setQuery] = useState('');
  const [loading, setLoading] = useState(true);
  const [busy, setBusy] = useState('');
  const [error, setError] = useState('');
  const [name, setName] = useState('');
  const [allowedModels, setAllowedModels] = useState<string[]>([]);
  const [secret, setSecret] = useState('');
  const [copied, setCopied] = useState(false);
  const [deleting, setDeleting] = useState<APIKeyRecord | null>(null);
  const creator = useDisclosure();
  const confirmation = useDisclosure();
  const { showToast } = useToast();

  const load = useCallback(async () => {
    try {
      const [keyResponse, modelResponse] = await Promise.all([
        apiFetch<{ data: APIKeyRecord[] }>('/api/keys'),
        apiFetch<{ data: ModelRecord[] }>('/api/models'),
      ]);
      setKeys(keyResponse.data || []);
      setModels((modelResponse.data || []).filter((model) => model.enabled));
      setError('');
    } catch (requestError) {
      setError(requestError instanceof Error ? requestError.message : '无法加载密钥');
    } finally {
      setLoading(false);
    }
  }, []);

  useEffect(() => { void load(); }, [load]);

  const filtered = useMemo(() => {
    const normalized = query.trim().toLowerCase();
    return keys.filter((key) => !normalized || key.name.toLowerCase().includes(normalized) || key.prefix.toLowerCase().includes(normalized));
  }, [keys, query]);

  const openCreate = () => {
    setName('');
    setAllowedModels([]);
    setSecret('');
    setCopied(false);
    creator.onOpen();
  };

  const createKey = async (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault();
    if (!name.trim()) {
      showToast('error', '创建密钥失败', '请输入密钥名称');
      return;
    }
    setBusy('create');
    try {
      const response = await apiFetch<{ key: APIKeyRecord; secret: string }>('/api/keys', jsonRequest('POST', { name: name.trim(), allowed_models: allowedModels }));
      setSecret(response.secret);
      await load();
      showToast('success', '密钥创建成功', `“${response.key.name}”已创建，请及时复制密钥`);
    } catch (requestError) {
      showToast('error', '创建密钥失败', errorMessage(requestError, '请稍后重试'));
    } finally {
      setBusy('');
    }
  };

  const toggleKey = async (key: APIKeyRecord) => {
    setBusy(`toggle:${key.id}`);
    try {
      await apiFetch(`/api/keys/${key.id}`, jsonRequest('PATCH', { enabled: !key.enabled }));
      await load();
      showToast('success', key.enabled ? '密钥已撤销' : '密钥已恢复', `“${key.name}”状态已更新`);
    } catch (requestError) {
      showToast('error', key.enabled ? '撤销密钥失败' : '恢复密钥失败', errorMessage(requestError, '请稍后重试'));
    } finally {
      setBusy('');
    }
  };

  const deleteKey = async () => {
    if (!deleting) return;
    const keyName = deleting.name;
    setBusy(`delete:${deleting.id}`);
    try {
      await apiFetch(`/api/keys/${deleting.id}`, { method: 'DELETE' });
      confirmation.onClose();
      setDeleting(null);
      await load();
      showToast('success', '密钥已删除', `“${keyName}”已永久删除`);
    } catch (requestError) {
      showToast('error', '删除密钥失败', errorMessage(requestError, '请稍后重试'));
    } finally {
      setBusy('');
    }
  };

  const copySecret = async () => {
    try {
      await navigator.clipboard.writeText(secret);
      setCopied(true);
      showToast('success', '复制成功', 'API 密钥已复制到剪贴板');
    } catch (copyError) {
      showToast('error', '复制失败', errorMessage(copyError, '请手动选择并复制密钥'));
    }
  };

  const activeCount = keys.filter((key) => key.enabled && (!key.expires_at || new Date(key.expires_at) > new Date())).length;

  return (
    <PageShell title="密钥管理" description="签发和撤销外部 OpenAI 兼容访问凭据" action={{ label: '创建密钥', icon: Plus, onPress: openCreate }}>
      {error ? <div className="flex items-start gap-2 rounded-md border border-red-200 bg-red-50 px-4 py-3 text-sm text-red-700" role="alert"><AlertCircle className="mt-0.5 shrink-0" size={16} />{error}</div> : null}
      <section className="grid gap-4 sm:grid-cols-3">
        <div className="rounded-lg border border-zinc-200 bg-white px-5 py-4"><p className="text-xs text-zinc-500">密钥总数</p><p className="mt-1 text-xl font-semibold text-zinc-900">{keys.length}</p></div>
        <div className="rounded-lg border border-zinc-200 bg-white px-5 py-4"><p className="text-xs text-zinc-500">当前有效</p><p className="mt-1 text-xl font-semibold text-emerald-600">{activeCount}</p></div>
        <div className="rounded-lg border border-zinc-200 bg-white px-5 py-4"><p className="text-xs text-zinc-500">累计请求</p><p className="mt-1 text-xl font-semibold text-zinc-900">{formatTokens(keys.reduce((sum, key) => sum + key.request_count, 0))}</p></div>
      </section>

      <section className="overflow-hidden rounded-lg border border-zinc-200 bg-white">
        <div className="border-b border-zinc-100 p-4"><Input aria-label="搜索密钥" className="w-full sm:max-w-xs" classNames={{ inputWrapper: 'h-10 rounded-md border border-zinc-200 bg-white shadow-none' }} placeholder="搜索名称或前缀" radius="sm" startContent={<Search size={16} className="text-zinc-400" />} value={query} variant="bordered" onValueChange={setQuery} /></div>
        {loading ? <div className="space-y-3 p-5">{Array.from({ length: 4 }).map((_, index) => <Skeleton key={index} className="h-14 w-full rounded-md" />)}</div> : (
          <div className="overflow-x-auto"><Table aria-label="API 密钥列表" removeWrapper classNames={{ th: 'bg-zinc-50 text-xs text-zinc-500', td: 'py-4 text-sm' }}>
            <TableHeader><TableColumn>名称</TableColumn><TableColumn>密钥前缀</TableColumn><TableColumn>模型权限</TableColumn><TableColumn>请求 / Tokens</TableColumn><TableColumn>创建时间</TableColumn><TableColumn>最近使用</TableColumn><TableColumn>状态</TableColumn><TableColumn align="end">操作</TableColumn></TableHeader>
            <TableBody items={filtered} emptyContent={<EmptyState icon={KeyRound} title="尚未创建密钥" description="创建后可用于 OpenAI SDK 的 Bearer 认证" />}>
              {(key) => {
                const expired = Boolean(key.expires_at && new Date(key.expires_at) < new Date());
                return <TableRow key={key.id}><TableCell><p className="font-medium text-zinc-900">{key.name}</p><p className="mt-0.5 text-xs text-zinc-400">{key.id}</p></TableCell><TableCell><code className="font-mono text-xs text-zinc-600">{key.prefix}</code></TableCell><TableCell><span className="text-zinc-600">{key.allowed_models?.length ? `${key.allowed_models.length} 个模型` : '全部模型'}</span></TableCell><TableCell><p className="font-medium text-zinc-800">{formatTokens(key.request_count)} 次</p><p className="mt-0.5 text-xs text-zinc-400">{formatTokens(key.input_tokens + key.output_tokens)} tokens</p></TableCell><TableCell><span className="whitespace-nowrap text-zinc-500">{formatDateTime(key.created_at)}</span></TableCell><TableCell><span className="whitespace-nowrap text-zinc-500">{formatDateTime(key.last_used_at)}</span></TableCell><TableCell><Chip color={expired ? 'danger' : key.enabled ? 'success' : 'default'} radius="sm" size="sm" variant="flat">{expired ? '已过期' : key.enabled ? '有效' : '已撤销'}</Chip></TableCell><TableCell><div className="flex justify-end gap-1"><Tooltip content={key.enabled ? '撤销密钥' : '恢复密钥'}><Button isIconOnly aria-label={key.enabled ? '撤销密钥' : '恢复密钥'} color={key.enabled ? 'warning' : 'success'} isLoading={busy === `toggle:${key.id}`} radius="sm" size="sm" variant="light" onPress={() => void toggleKey(key)}>{key.enabled ? <ShieldOff size={16} /> : <Check size={16} />}</Button></Tooltip><Tooltip color="danger" content="删除密钥"><Button isIconOnly aria-label="删除密钥" color="danger" radius="sm" size="sm" variant="light" onPress={() => { setDeleting(key); confirmation.onOpen(); }}><Trash2 size={16} /></Button></Tooltip></div></TableCell></TableRow>;
              }}
            </TableBody>
          </Table></div>
        )}
        <div className="border-t border-zinc-100 px-5 py-3 text-xs text-zinc-400">共 {filtered.length} 个密钥</div>
      </section>

      <Modal isOpen={creator.isOpen} radius="sm" scrollBehavior="inside" onOpenChange={creator.onOpenChange}>
        <ModalContent>{(onClose) => secret ? <><ModalHeader>密钥已创建</ModalHeader><ModalBody><div className="rounded-md border border-amber-200 bg-amber-50 px-4 py-3 text-sm text-amber-800">密钥仅在本次创建后显示。</div><div className="flex items-center gap-2"><Input isReadOnly aria-label="新 API 密钥" classNames={{ input: 'font-mono text-xs' }} radius="sm" value={secret} /><Button isIconOnly aria-label="复制密钥" color={copied ? 'success' : 'primary'} radius="sm" variant="flat" onPress={() => void copySecret()}>{copied ? <Check size={17} /> : <Copy size={17} />}</Button></div></ModalBody><ModalFooter><Button color="primary" radius="sm" onPress={onClose}>完成</Button></ModalFooter></> : <form onSubmit={createKey}><ModalHeader>创建 API 密钥</ModalHeader><ModalBody className="gap-5"><Input autoFocus isRequired label="名称" labelPlacement="outside" placeholder="例如：生产服务" radius="sm" value={name} onValueChange={setName} /><div><p className="mb-3 text-sm font-medium text-zinc-700">可用模型</p><div className="max-h-52 space-y-2 overflow-y-auto rounded-md border border-zinc-200 p-3">{models.map((model) => <Checkbox key={model.upstream} isSelected={allowedModels.includes(model.alias)} size="sm" onValueChange={(selected) => setAllowedModels((current) => selected ? [...current, model.alias] : current.filter((item) => item !== model.alias))}><span className="font-mono text-xs">{model.alias}</span></Checkbox>)}{!models.length ? <p className="py-3 text-center text-sm text-zinc-400">暂无已启用模型；留空代表允许全部未来模型</p> : null}</div><p className="mt-2 text-xs text-zinc-400">未选择时允许访问全部已启用模型</p></div></ModalBody><ModalFooter><Button radius="sm" variant="light" onPress={onClose}>取消</Button><Button color="primary" isLoading={busy === 'create'} radius="sm" type="submit">创建</Button></ModalFooter></form>}</ModalContent>
      </Modal>

      <Modal isOpen={confirmation.isOpen} radius="sm" size="sm" onOpenChange={confirmation.onOpenChange}>
        <ModalContent>{(onClose) => <><ModalHeader>删除密钥</ModalHeader><ModalBody><p className="text-sm leading-6 text-zinc-600">确认永久删除“{deleting?.name}”？使用该密钥的客户端将立即失去访问权限。</p></ModalBody><ModalFooter><Button radius="sm" variant="light" onPress={onClose}>取消</Button><Button color="danger" isLoading={busy === `delete:${deleting?.id}`} radius="sm" onPress={() => void deleteKey()}>确认删除</Button></ModalFooter></>}</ModalContent>
      </Modal>
    </PageShell>
  );
}
