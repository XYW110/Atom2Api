import { useCallback, useEffect, useState, type FormEvent } from 'react';
import { AlertCircle, Bug, Check, KeyRound, Network, RefreshCw, Save, Settings2, ShieldAlert } from 'lucide-react';
import { Button, Chip, Input, Modal, ModalBody, ModalContent, ModalFooter, ModalHeader, Skeleton, Switch, Tooltip, useDisclosure } from '@heroui/react';
import { PageShell, StatusDot } from '../components/PageShell';
import { useToast } from '../components/Toast';
import { apiFetch, type SettingsResponse, type UserAgentCheckResponse, errorMessage, jsonRequest } from '../api';

type SettingsForm = Pick<SettingsResponse, 'user_agent' | 'platform_base_url' | 'codingplan_api_url' | 'gateway_url' | 'signer_url' | 'audit_debug_enabled' | 'request_timeout_seconds'>;

const emptyForm: SettingsForm = {
  user_agent: '', platform_base_url: '', codingplan_api_url: '', gateway_url: '', signer_url: '', audit_debug_enabled: false, request_timeout_seconds: 120,
};

export default function SettingsPage() {
  const [settings, setSettings] = useState<SettingsResponse | null>(null);
  const [form, setForm] = useState<SettingsForm>(emptyForm);
  const [adminPassword, setAdminPassword] = useState('');
  const [signerToken, setSignerToken] = useState('');
  const [loading, setLoading] = useState(true);
  const [saving, setSaving] = useState(false);
  const [checkingUserAgent, setCheckingUserAgent] = useState(false);
  const [replacingUserAgent, setReplacingUserAgent] = useState(false);
  const [userAgentCandidate, setUserAgentCandidate] = useState<UserAgentCheckResponse | null>(null);
  const [saved, setSaved] = useState(false);
  const [error, setError] = useState('');
  const userAgentConfirmation = useDisclosure();
  const { showToast } = useToast();

  const load = useCallback(async () => {
    try {
      const response = await apiFetch<SettingsResponse>('/api/settings');
      setSettings(response);
      setForm({
        user_agent: response.user_agent,
        platform_base_url: response.platform_base_url,
        codingplan_api_url: response.codingplan_api_url,
        gateway_url: response.gateway_url,
        signer_url: response.signer_url,
        audit_debug_enabled: response.audit_debug_enabled,
        request_timeout_seconds: response.request_timeout_seconds,
      });
      setError('');
    } catch (requestError) {
      setError(requestError instanceof Error ? requestError.message : '无法加载设置');
    } finally {
      setLoading(false);
    }
  }, []);

  useEffect(() => { void load(); }, [load]);

  const update = <K extends keyof SettingsForm>(key: K, value: SettingsForm[K]) => {
    setForm((current) => ({ ...current, [key]: value }));
    setSaved(false);
    setError('');
  };

  const save = async (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault();
    if (!form.user_agent.trim() || !form.platform_base_url.trim() || !form.codingplan_api_url.trim() || !form.gateway_url.trim()) {
      setError('必填设置不能为空');
      showToast('error', '保存设置失败', '必填设置不能为空');
      return;
    }
    setSaving(true);
    setSaved(false);
    try {
      const payload: Record<string, unknown> = { ...form };
      if (adminPassword) payload.admin_password = adminPassword;
      if (signerToken) payload.signer_token = signerToken;
      const response = await apiFetch<SettingsResponse>('/api/settings', jsonRequest('PUT', payload));
      setSettings(response);
      setAdminPassword('');
      setSignerToken('');
      setSaved(true);
      setError('');
      showToast('success', '设置保存成功', '新配置已立即生效');
    } catch (requestError) {
      showToast('error', '保存设置失败', errorMessage(requestError, '请检查配置后重试'));
    } finally {
      setSaving(false);
    }
  };

  const checkUserAgent = async () => {
    setCheckingUserAgent(true);
    try {
      const result = await apiFetch<UserAgentCheckResponse>('/api/settings/user-agent/check', { method: 'POST' });
      if (form.user_agent.trim() === result.user_agent) {
        showToast('success', 'User-Agent 已是最新', result.user_agent);
        return;
      }
      setUserAgentCandidate(result);
      userAgentConfirmation.onOpen();
    } catch (requestError) {
      showToast('error', '检查 User-Agent 失败', errorMessage(requestError, '无法读取 AtomCode 版本'));
    } finally {
      setCheckingUserAgent(false);
    }
  };

  const replaceUserAgent = async () => {
    if (!userAgentCandidate) return;
    setReplacingUserAgent(true);
    try {
      const response = await apiFetch<SettingsResponse>('/api/settings', jsonRequest('PUT', { user_agent: userAgentCandidate.user_agent }));
      setSettings(response);
      setForm((current) => ({ ...current, user_agent: response.user_agent }));
      setSaved(false);
      setError('');
      userAgentConfirmation.onClose();
      showToast('success', 'User-Agent 已替换', response.user_agent);
    } catch (requestError) {
      showToast('error', '替换 User-Agent 失败', errorMessage(requestError, '请稍后重试'));
    } finally {
      setReplacingUserAgent(false);
    }
  };

  const isDirty = Boolean(settings) && (
    form.user_agent !== settings?.user_agent || form.platform_base_url !== settings?.platform_base_url ||
    form.codingplan_api_url !== settings?.codingplan_api_url || form.gateway_url !== settings?.gateway_url ||
    form.signer_url !== settings?.signer_url || form.audit_debug_enabled !== settings?.audit_debug_enabled ||
    form.request_timeout_seconds !== settings?.request_timeout_seconds ||
    Boolean(adminPassword) || Boolean(signerToken)
  );

  return (
    <PageShell title="系统设置" description="认证服务、Coding Plan 网关与请求参数">
      {settings?.default_password ? <div className="flex items-start gap-3 rounded-md border border-amber-200 bg-amber-50 px-4 py-3 text-sm text-amber-800" role="alert"><ShieldAlert className="mt-0.5 shrink-0" size={17} /><span>当前仍使用默认管理密码，请在下方设置新密码。</span></div> : null}
      {error ? <div className="flex items-start gap-2 rounded-md border border-red-200 bg-red-50 px-4 py-3 text-sm text-red-700" role="alert"><AlertCircle className="mt-0.5 shrink-0" size={16} />{error}</div> : null}

      <form className="space-y-5" onSubmit={save}>
        <section className="overflow-hidden rounded-lg border border-zinc-200 bg-white">
          <div className="flex items-center justify-between border-b border-zinc-100 px-5 py-4 sm:px-6"><div className="flex items-center gap-3"><span className="flex h-9 w-9 items-center justify-center rounded-lg bg-blue-50 text-blue-600"><Network size={18} /></span><div><h2 className="text-sm font-semibold text-zinc-900">服务端点</h2><p className="mt-0.5 text-xs text-zinc-500">OAuth、权益和模型网关</p></div></div><Chip className="border-zinc-200 bg-white text-zinc-600" radius="sm" size="sm" startContent={<StatusDot tone={error ? 'danger' : 'success'} />} variant="bordered">{error ? '配置异常' : '已加载'}</Chip></div>
          {loading ? <div className="grid gap-5 px-5 py-6 sm:grid-cols-2 sm:px-6">{Array.from({ length: 4 }).map((_, index) => <Skeleton key={index} className="h-14 w-full rounded-md" />)}</div> : <div className="grid gap-5 px-5 py-6 sm:grid-cols-2 sm:px-6"><Input isRequired label="Platform OAuth" labelPlacement="outside" radius="sm" value={form.platform_base_url} onValueChange={(value) => update('platform_base_url', value)} /><Input isRequired label="Coding Plan API" labelPlacement="outside" radius="sm" value={form.codingplan_api_url} onValueChange={(value) => update('codingplan_api_url', value)} /><Input isRequired label="LLM Gateway" labelPlacement="outside" radius="sm" value={form.gateway_url} onValueChange={(value) => update('gateway_url', value)} /><Input label="请求签名服务" labelPlacement="outside" placeholder="https://signer.example.com/v1/sign" radius="sm" value={form.signer_url} onValueChange={(value) => update('signer_url', value)} /></div>}
        </section>

        <section className="overflow-hidden rounded-lg border border-zinc-200 bg-white">
          <div className="flex items-center gap-3 border-b border-zinc-100 px-5 py-4 sm:px-6"><span className="flex h-9 w-9 items-center justify-center rounded-lg bg-zinc-100 text-zinc-600"><Settings2 size={18} /></span><div><h2 className="text-sm font-semibold text-zinc-900">请求参数</h2><p className="mt-0.5 text-xs text-zinc-500">上游客户端标识与超时</p></div></div>
          <div className="grid gap-5 px-5 py-6 sm:grid-cols-[minmax(0,1fr)_220px] sm:px-6"><Input isRequired label="User-Agent" labelPlacement="outside" radius="sm" value={form.user_agent} classNames={{ input: 'font-mono text-sm' }} endContent={<Tooltip content="检查 AtomCode 最新版本"><Button isIconOnly aria-label="检查 AtomCode User-Agent" isDisabled={loading} isLoading={checkingUserAgent} radius="sm" size="sm" type="button" variant="light" onPress={() => void checkUserAgent()}>{checkingUserAgent ? null : <RefreshCw size={16} />}</Button></Tooltip>} onValueChange={(value) => update('user_agent', value)} /><Input isRequired label="请求超时（秒）" labelPlacement="outside" max={600} min={5} radius="sm" type="number" value={String(form.request_timeout_seconds)} onValueChange={(value) => update('request_timeout_seconds', Number(value) || 0)} /></div>
        </section>

        <section className="overflow-hidden rounded-lg border border-zinc-200 bg-white">
          <div className="flex items-center gap-3 border-b border-zinc-100 px-5 py-4 sm:px-6"><span className="flex h-9 w-9 items-center justify-center rounded-lg bg-violet-50 text-violet-700"><Bug size={18} /></span><div><h2 className="text-sm font-semibold text-zinc-900">请求审计</h2><p className="mt-0.5 text-xs text-zinc-500">详细内容记录策略</p></div></div>
          <div className="flex flex-col gap-4 px-5 py-5 sm:flex-row sm:items-center sm:justify-between sm:px-6">
            <div><p className="text-sm font-medium text-zinc-800">审计调试模式</p><p className="mt-1 text-xs leading-5 text-amber-700">完整内容可能包含提示词、代码和其他敏感数据</p></div>
            <Switch aria-label="切换审计调试模式" color="primary" isSelected={form.audit_debug_enabled} onValueChange={(selected) => update('audit_debug_enabled', selected)}><span className="text-sm text-zinc-600">{form.audit_debug_enabled ? '已开启' : '已关闭'}</span></Switch>
          </div>
        </section>

        <section className="overflow-hidden rounded-lg border border-zinc-200 bg-white">
          <div className="flex items-center gap-3 border-b border-zinc-100 px-5 py-4 sm:px-6"><span className="flex h-9 w-9 items-center justify-center rounded-lg bg-amber-50 text-amber-700"><KeyRound size={18} /></span><div><h2 className="text-sm font-semibold text-zinc-900">敏感凭据</h2><p className="mt-0.5 text-xs text-zinc-500">留空表示保持当前值</p></div></div>
          <div className="grid gap-5 px-5 py-6 sm:grid-cols-2 sm:px-6"><Input label="新管理密码" labelPlacement="outside" placeholder="至少使用高强度随机密码" radius="sm" type="password" value={adminPassword} onValueChange={(value) => { setAdminPassword(value); setSaved(false); }} /><Input label="签名服务 Token" labelPlacement="outside" placeholder={settings?.signer_configured ? '已配置' : '未配置'} radius="sm" type="password" value={signerToken} onValueChange={(value) => { setSignerToken(value); setSaved(false); }} /></div>
        </section>

        <div className="flex flex-col-reverse gap-3 sm:flex-row sm:items-center sm:justify-between"><span className="text-xs text-zinc-400">配置文件：{settings?.data_path ? 'config.json' : '—'}</span><div className="flex items-center justify-end gap-3">{saved ? <span className="flex items-center gap-1.5 text-sm text-emerald-700" role="status"><Check size={16} />已保存</span> : null}<Button color="primary" isDisabled={!isDirty} isLoading={saving} radius="sm" startContent={saving ? null : <Save size={16} />} type="submit">保存设置</Button></div></div>
      </form>

      <Modal isOpen={userAgentConfirmation.isOpen} radius="sm" size="sm" onOpenChange={userAgentConfirmation.onOpenChange}>
        <ModalContent>{(onClose) => <><ModalHeader>更新 User-Agent</ModalHeader><ModalBody><p className="text-sm leading-6 text-zinc-600">AtomCode 当前版本为 <code className="font-mono font-semibold text-zinc-900">{userAgentCandidate?.version}</code>，是否将 User-Agent 从 <code className="break-all font-mono text-zinc-700">{form.user_agent}</code> 替换为 <code className="break-all font-mono font-semibold text-zinc-900">{userAgentCandidate?.user_agent}</code>？</p></ModalBody><ModalFooter><Button radius="sm" variant="light" onPress={onClose}>保留当前值</Button><Button color="primary" isLoading={replacingUserAgent} radius="sm" onPress={() => void replaceUserAgent()}>替换</Button></ModalFooter></>}</ModalContent>
      </Modal>
    </PageShell>
  );
}
