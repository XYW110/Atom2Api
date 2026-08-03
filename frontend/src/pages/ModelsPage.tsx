import { useCallback, useEffect, useMemo, useState, type FormEvent } from 'react';
import { AlertCircle, Boxes, Braces, Pencil, Plus, Search, Trash2 } from 'lucide-react';
import { Button, Chip, Input, Modal, ModalBody, ModalContent, ModalFooter, ModalHeader, Skeleton, Switch, Table, TableBody, TableCell, TableColumn, TableHeader, TableRow, Tooltip, useDisclosure } from '@heroui/react';
import { EmptyState, PageShell, StatusDot } from '../components/PageShell';
import { useToast } from '../components/Toast';
import { apiFetch, type ModelRecord, errorMessage, jsonRequest } from '../api';

function contextLabel(tokens: number) {
  if (tokens >= 1_000_000) return `${(tokens / 1_000_000).toFixed(tokens % 1_000_000 ? 1 : 0)}M`;
  if (tokens >= 1000) return `${Math.round(tokens / 1000)}K`;
  return String(tokens || '—');
}

export default function ModelsPage() {
  const [models, setModels] = useState<ModelRecord[]>([]);
  const [query, setQuery] = useState('');
  const [loading, setLoading] = useState(true);
  const [busy, setBusy] = useState('');
  const [error, setError] = useState('');
  const [editing, setEditing] = useState<ModelRecord | null>(null);
  const [alias, setAlias] = useState('');
  const [newUpstream, setNewUpstream] = useState('');
  const [newAlias, setNewAlias] = useState('');
  const [deleting, setDeleting] = useState<ModelRecord | null>(null);
  const editor = useDisclosure();
  const creator = useDisclosure();
  const deleteConfirm = useDisclosure();
  const { showToast } = useToast();

  const load = useCallback(async () => {
    try {
      const response = await apiFetch<{ data: ModelRecord[] }>('/api/models');
      setModels(response.data || []);
      setError('');
    } catch (requestError) {
      setError(requestError instanceof Error ? requestError.message : '无法加载模型');
    } finally {
      setLoading(false);
    }
  }, []);

  useEffect(() => { void load(); }, [load]);

  const filtered = useMemo(() => {
    const normalized = query.trim().toLowerCase();
    return models.filter((model) => !normalized || model.alias.toLowerCase().includes(normalized) || model.upstream.toLowerCase().includes(normalized));
  }, [models, query]);

  const saveSetting = async (model: ModelRecord, values: { alias: string; enabled: boolean; responsesChatCompat: boolean }) => {
    setBusy(model.upstream);
    try {
      await apiFetch('/api/models/settings', jsonRequest('PUT', {
        upstream: model.upstream,
        alias: values.alias,
        enabled: values.enabled,
        responses_chat_compat: values.responsesChatCompat,
      }));
      await load();
      if (values.alias !== model.alias) {
        showToast('success', '模型别名已更新', `“${model.alias}”已更名为“${values.alias}”`);
      } else if (values.responsesChatCompat !== model.responses_chat_compat) {
        showToast('success', values.responsesChatCompat ? 'Responses 转换已开启' : 'Responses 转换已关闭', `“${model.alias}”实验性兼容设置已更新`);
      } else {
        showToast('success', values.enabled ? '模型已启用' : '模型已停用', `“${model.alias}”状态已更新`);
      }
      return true;
    } catch (requestError) {
      showToast('error', '更新模型失败', errorMessage(requestError, '请稍后重试'));
      return false;
    } finally {
      setBusy('');
    }
  };

  const openEdit = (model: ModelRecord) => {
    setEditing(model);
    setAlias(model.alias);
    editor.onOpen();
  };

  const submitAlias = async (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault();
    if (!editing || !alias.trim()) {
      showToast('error', '保存别名失败', '模型别名不能为空');
      return;
    }
    if (await saveSetting(editing, { alias: alias.trim(), enabled: editing.enabled, responsesChatCompat: editing.responses_chat_compat })) editor.onClose();
  };

  const openCreate = () => {
    setNewUpstream('');
    setNewAlias('');
    creator.onOpen();
  };

  const submitCreate = async (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault();
    const upstream = newUpstream.trim();
    if (!upstream) {
      showToast('error', '添加模型失败', '上游模型名称不能为空');
      return;
    }
    setBusy('__create__');
    try {
      await apiFetch('/api/models', jsonRequest('POST', { upstream, alias: newAlias.trim() }));
      await load();
      creator.onClose();
      showToast('success', '模型已添加', `“${newAlias.trim() || upstream}”现在可以对外使用`);
    } catch (requestError) {
      showToast('error', '添加模型失败', errorMessage(requestError, '请稍后重试'));
    } finally {
      setBusy('');
    }
  };

  const openDelete = (model: ModelRecord) => {
    setDeleting(model);
    deleteConfirm.onOpen();
  };

  const deleteModel = async () => {
    if (!deleting) return;
    setBusy(deleting.upstream);
    try {
      await apiFetch('/api/models/settings', jsonRequest('DELETE', { upstream: deleting.upstream }));
      await load();
      deleteConfirm.onClose();
      showToast('success', '模型已删除', `“${deleting.alias}”已从可用模型中移除`);
      setDeleting(null);
    } catch (requestError) {
      showToast('error', '删除模型失败', errorMessage(requestError, '请稍后重试'));
    } finally {
      setBusy('');
    }
  };

  const enabledCount = models.filter((model) => model.enabled).length;
  const compatCount = models.filter((model) => model.responses_chat_compat).length;

  return (
    <PageShell title="模型管理" description="Coding Plan 模型目录与对外 OpenAI 模型别名">
      {error ? <div className="flex items-start gap-2 rounded-md border border-red-200 bg-red-50 px-4 py-3 text-sm text-red-700" role="alert"><AlertCircle className="mt-0.5 shrink-0" size={16} />{error}</div> : null}
      <section className="grid gap-4 sm:grid-cols-3">
        <div className="rounded-lg border border-zinc-200 bg-white px-4 py-3.5"><p className="text-xs text-zinc-500">模型总数</p><p className="mt-1 text-xl font-semibold text-zinc-900">{models.length}</p></div>
        <div className="rounded-lg border border-zinc-200 bg-white px-4 py-3.5"><p className="text-xs text-zinc-500">当前启用</p><p className="mt-1 text-xl font-semibold text-zinc-900">{enabledCount}</p></div>
        <div className="flex items-center gap-3 rounded-lg border border-zinc-200 bg-white px-4 py-3.5"><span className="flex h-9 w-9 items-center justify-center rounded-lg bg-zinc-100 text-zinc-600"><Braces size={18} /></span><div className="min-w-0"><p className="text-xs text-zinc-500">Responses 转 Chat</p><p className="mt-1 text-xl font-semibold text-zinc-900">{compatCount}</p></div></div>
      </section>

      <section className="overflow-hidden rounded-lg border border-zinc-200 bg-white">
        <div className="flex flex-col gap-3 border-b border-zinc-100 p-4 sm:flex-row sm:items-center sm:justify-between"><Input aria-label="搜索模型" className="w-full sm:max-w-xs" classNames={{ inputWrapper: 'h-10 rounded-md border border-zinc-200 bg-white shadow-none' }} placeholder="搜索对外别名或上游模型" radius="sm" startContent={<Search size={16} className="text-zinc-400" />} value={query} variant="bordered" onValueChange={setQuery} /><Button color="primary" radius="sm" startContent={<Plus size={16} />} onPress={openCreate}>添加模型</Button></div>
        {loading ? <div className="space-y-3 p-5">{Array.from({ length: 4 }).map((_, index) => <Skeleton key={index} className="h-14 w-full rounded-md" />)}</div> : (
          <div className="overflow-x-auto"><Table aria-label="模型目录" removeWrapper classNames={{ th: 'bg-zinc-50 text-xs text-zinc-500', td: 'py-4 text-sm' }}>
            <TableHeader><TableColumn>对外模型</TableColumn><TableColumn>上游模型</TableColumn><TableColumn>类型</TableColumn><TableColumn>Responses 转换</TableColumn><TableColumn>上下文</TableColumn><TableColumn>可用账号</TableColumn><TableColumn>订阅</TableColumn><TableColumn>状态</TableColumn><TableColumn align="end">操作</TableColumn></TableHeader>
            <TableBody items={filtered} emptyContent={<EmptyState icon={Boxes} title="尚无可用模型" description="同步 Coding Plan 账号或手动添加模型" />}>
              {(model) => <TableRow key={model.upstream}><TableCell><code className="font-mono text-xs font-semibold text-zinc-900">{model.alias}</code></TableCell><TableCell><div className="max-w-72"><code className="block truncate font-mono text-xs text-zinc-600" title={model.upstream}>{model.upstream}</code><p className="mt-0.5 truncate text-[11px] text-zinc-400" title={model.base_url}>{model.base_url}</p></div></TableCell><TableCell><div className="flex items-center gap-1"><Chip radius="sm" size="sm" variant="flat">{model.provider_type || 'openai'}</Chip>{model.manual ? <Chip color="warning" radius="sm" size="sm" variant="flat">手动</Chip> : null}</div></TableCell><TableCell><Tooltip content="实验性功能：上游不支持原生 Responses API 时，将请求转换到 Chat Completions"><div className="flex min-w-24 items-center gap-2"><Switch aria-label={`${model.alias} Responses 转 Chat`} isDisabled={busy === model.upstream} isSelected={model.responses_chat_compat} size="sm" onValueChange={(responsesChatCompat) => void saveSetting(model, { alias: model.alias, enabled: model.enabled, responsesChatCompat })} /><Chip color="warning" radius="sm" size="sm" variant="flat">实验</Chip></div></Tooltip></TableCell><TableCell>{contextLabel(model.context_window)}</TableCell><TableCell><span className="font-medium text-zinc-800">{model.account_count}</span></TableCell><TableCell><div className="flex max-w-48 flex-wrap gap-1">{model.plans.length ? model.plans.map((plan) => <Chip key={plan} color="primary" radius="sm" size="sm" variant="flat">{plan}</Chip>) : '—'}</div></TableCell><TableCell><div className="flex min-w-20 items-center gap-2"><StatusDot tone={model.enabled ? 'success' : 'neutral'} /><Switch aria-label={`${model.alias} 启用状态`} isDisabled={busy === model.upstream} isSelected={model.enabled} size="sm" onValueChange={(enabled) => void saveSetting(model, { alias: model.alias, enabled, responsesChatCompat: model.responses_chat_compat })} /></div></TableCell><TableCell><div className="flex justify-end"><Tooltip content="编辑对外别名"><Button isIconOnly aria-label="编辑模型别名" radius="sm" size="sm" variant="light" onPress={() => openEdit(model)}><Pencil size={16} /></Button></Tooltip>{model.manual ? <Tooltip color="danger" content="删除手动模型"><Button isIconOnly aria-label="删除手动模型" color="danger" radius="sm" size="sm" variant="light" onPress={() => openDelete(model)}><Trash2 size={16} /></Button></Tooltip> : null}</div></TableCell></TableRow>}
            </TableBody>
          </Table></div>
        )}
        <div className="border-t border-zinc-100 px-5 py-3 text-xs text-zinc-400">共 {filtered.length} 个模型</div>
      </section>

      <Modal isOpen={editor.isOpen} radius="sm" onOpenChange={editor.onOpenChange}>
        <ModalContent>{(onClose) => <form onSubmit={submitAlias}><ModalHeader>编辑模型别名</ModalHeader><ModalBody className="gap-5"><Input isReadOnly label="上游模型" labelPlacement="outside" radius="sm" value={editing?.upstream || ''} classNames={{ input: 'font-mono text-xs' }} /><Input autoFocus isRequired label="对外模型名称" labelPlacement="outside" placeholder="OpenAI 请求中的 model" radius="sm" value={alias} classNames={{ input: 'font-mono text-sm' }} onValueChange={setAlias} /></ModalBody><ModalFooter><Button radius="sm" variant="light" onPress={onClose}>取消</Button><Button color="primary" isLoading={busy === editing?.upstream} radius="sm" type="submit">保存</Button></ModalFooter></form>}</ModalContent>
      </Modal>

      <Modal isOpen={creator.isOpen} radius="sm" onOpenChange={creator.onOpenChange}>
        <ModalContent>{(onClose) => <form onSubmit={submitCreate}><ModalHeader>添加可用模型</ModalHeader><ModalBody className="gap-5"><Input autoFocus isRequired label="上游模型名称" labelPlacement="outside" placeholder="例如 deepseek-v4" radius="sm" value={newUpstream} classNames={{ input: 'font-mono text-sm' }} onValueChange={setNewUpstream} /><Input label="对外模型名称（可选）" labelPlacement="outside" placeholder="默认与上游模型相同" radius="sm" value={newAlias} classNames={{ input: 'font-mono text-sm' }} onValueChange={setNewAlias} /></ModalBody><ModalFooter><Button radius="sm" variant="light" onPress={onClose}>取消</Button><Button color="primary" isLoading={busy === '__create__'} radius="sm" type="submit">添加</Button></ModalFooter></form>}</ModalContent>
      </Modal>

      <Modal isOpen={deleteConfirm.isOpen} radius="sm" onOpenChange={deleteConfirm.onOpenChange}>
        <ModalContent>{(onClose) => <><ModalHeader>删除手动模型</ModalHeader><ModalBody><p className="text-sm text-zinc-600">确定删除 <code className="font-mono font-semibold text-zinc-900">{deleting?.alias}</code>？使用该模型名称的请求将不再可用。</p></ModalBody><ModalFooter><Button radius="sm" variant="light" onPress={onClose}>取消</Button><Button color="danger" isLoading={busy === deleting?.upstream} radius="sm" onPress={() => void deleteModel()}>删除</Button></ModalFooter></>}</ModalContent>
      </Modal>
    </PageShell>
  );
}
