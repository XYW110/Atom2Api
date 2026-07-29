import { useEffect, useState, type FormEvent } from 'react';
import { useLocation, useNavigate } from 'react-router-dom';
import { Atom, Check, Eye, EyeOff, LockKeyhole, ShieldCheck } from 'lucide-react';
import { Button, Input } from '@heroui/react';
import { useToast } from '../components/Toast';
import { apiFetch, errorMessage, jsonRequest } from '../api';

export default function LoginPage() {
  const navigate = useNavigate();
  const location = useLocation();
  const [password, setPassword] = useState('');
  const [showPassword, setShowPassword] = useState(false);
  const [error, setError] = useState('');
  const [isLoading, setIsLoading] = useState(false);
  const { showToast } = useToast();

  useEffect(() => {
    void apiFetch<{ authenticated: boolean }>('/api/auth/status').then((status) => {
      if (status.authenticated) navigate('/', { replace: true });
    }).catch(() => undefined);
  }, [navigate]);

  const handleSubmit = async (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault();
    if (!password.trim()) {
      setError('请输入管理密码');
      showToast('error', '登录失败', '请输入管理密码');
      return;
    }
    setError('');
    setIsLoading(true);
    try {
      await apiFetch('/api/auth/login', jsonRequest('POST', { password }));
      showToast('success', '登录成功', '欢迎进入 Atom2Api 管理后台');
      const from = (location.state as { from?: string } | null)?.from;
      navigate(from && from !== '/login' ? from : '/', { replace: true });
    } catch (requestError) {
      const message = errorMessage(requestError, '请检查管理密码');
      setError(message);
      showToast('error', '登录失败', message);
    } finally {
      setIsLoading(false);
    }
  };

  return (
    <div className="min-h-screen bg-white lg:grid lg:grid-cols-[minmax(380px,0.9fr)_minmax(520px,1.1fr)]">
      <section className="relative hidden min-h-screen overflow-hidden bg-zinc-950 px-10 py-9 lg:flex lg:flex-col lg:justify-between xl:px-16 xl:py-12">
        <div className="login-grid absolute inset-0 opacity-30" />
        <div className="relative flex items-center gap-3">
          <div className="flex h-10 w-10 items-center justify-center rounded-lg bg-blue-500 text-white"><Atom size={23} /></div>
          <div><p className="font-semibold text-white">Atom2Api</p><p className="text-xs text-zinc-500">OpenAI Compatible Gateway</p></div>
        </div>
        <div className="relative max-w-lg">
          <div className="mb-8 flex items-center gap-3"><span className="h-px w-10 bg-blue-500" /><span className="text-xs font-semibold uppercase text-blue-400">Gateway Console</span></div>
          <h1 className="text-4xl font-semibold leading-tight text-white xl:text-5xl">Coding Plan<br />统一访问入口</h1>
          <p className="mt-5 max-w-md text-base leading-7 text-zinc-400">管理 AtomGit 账号、滚动额度、模型路由和 OpenAI 兼容密钥。</p>
          <div className="mt-10 grid max-w-md grid-cols-2 gap-px overflow-hidden rounded-lg border border-white/10 bg-white/10">
            {['OAuth 账户', '额度同步', '流式代理', 'Token 统计'].map((label) => (
              <div key={label} className="bg-zinc-950/90 px-5 py-4"><Check size={17} className="text-emerald-400" /><p className="mt-2 text-sm font-medium text-zinc-200">{label}</p></div>
            ))}
          </div>
        </div>
        <div className="relative flex items-center gap-2 text-xs text-zinc-500"><ShieldCheck size={15} className="text-emerald-500" />HttpOnly 管理会话</div>
      </section>

      <section className="flex min-h-screen items-center justify-center px-5 py-10 sm:px-8">
        <div className="w-full max-w-sm">
          <div className="mb-10 flex items-center gap-3 lg:hidden"><div className="flex h-10 w-10 items-center justify-center rounded-lg bg-zinc-950 text-white"><Atom size={23} /></div><div><p className="font-semibold text-zinc-950">Atom2Api</p><p className="text-xs text-zinc-500">OpenAI Gateway</p></div></div>
          <div className="mb-8"><div className="mb-5 flex h-11 w-11 items-center justify-center rounded-lg bg-blue-50 text-blue-600"><LockKeyhole size={21} /></div><h2 className="text-2xl font-semibold text-zinc-950">登录管理控制台</h2><p className="mt-2 text-sm text-zinc-500">请输入 config.json 中的管理密码</p></div>
          <form className="space-y-5" onSubmit={handleSubmit}>
            <Input
              autoFocus isRequired errorMessage={error} isInvalid={Boolean(error)} label="管理密码" labelPlacement="outside"
              placeholder="输入密码" radius="sm" type={showPassword ? 'text' : 'password'} value={password}
              classNames={{ label: 'text-sm font-medium text-zinc-700', inputWrapper: 'h-11 rounded-md border border-zinc-200 bg-white shadow-none data-[hover=true]:border-zinc-300 group-data-[focus=true]:border-blue-500' }}
              endContent={<button aria-label={showPassword ? '隐藏密码' : '显示密码'} className="text-zinc-400 hover:text-zinc-700 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-blue-500" type="button" onClick={() => setShowPassword((value) => !value)}>{showPassword ? <EyeOff size={18} /> : <Eye size={18} />}</button>}
              onValueChange={(value) => { setPassword(value); setError(''); }}
            />
            <Button fullWidth color="primary" isLoading={isLoading} radius="sm" size="lg" type="submit">{isLoading ? '正在验证' : '登录'}</Button>
          </form>
          <div className="mt-8 flex items-center gap-2 border-t border-zinc-100 pt-6 text-xs text-zinc-400"><Check size={14} className="text-emerald-500" />会话有效期 12 小时</div>
        </div>
      </section>
    </div>
  );
}
