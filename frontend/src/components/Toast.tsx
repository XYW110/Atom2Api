import { createContext, useCallback, useContext, useEffect, useMemo, useRef, useState, type ReactNode } from 'react';
import { AlertCircle, CheckCircle2, X } from 'lucide-react';

type ToastType = 'success' | 'error';

interface ToastItem {
  id: number;
  type: ToastType;
  title: string;
  description?: string;
}

interface ToastContextValue {
  showToast: (type: ToastType, title: string, description?: string) => void;
}

const ToastContext = createContext<ToastContextValue | null>(null);

export function ToastProvider({ children }: { children: ReactNode }) {
  const [toasts, setToasts] = useState<ToastItem[]>([]);
  const nextID = useRef(0);
  const timers = useRef(new Map<number, number>());

  const dismiss = useCallback((id: number) => {
    const timer = timers.current.get(id);
    if (timer !== undefined) window.clearTimeout(timer);
    timers.current.delete(id);
    setToasts((current) => current.filter((toast) => toast.id !== id));
  }, []);

  const showToast = useCallback((type: ToastType, title: string, description?: string) => {
    const id = ++nextID.current;
    setToasts((current) => [...current.slice(-3), { id, type, title, description }]);
    const timer = window.setTimeout(() => {
      timers.current.delete(id);
      setToasts((current) => current.filter((toast) => toast.id !== id));
    }, 5000);
    timers.current.set(id, timer);
  }, []);

  useEffect(() => () => {
    timers.current.forEach((timer) => window.clearTimeout(timer));
    timers.current.clear();
  }, []);

  const value = useMemo(() => ({ showToast }), [showToast]);

  return (
    <ToastContext.Provider value={value}>
      {children}
      <div aria-label="操作通知" className="pointer-events-none fixed inset-x-4 top-4 z-[100] flex flex-col items-end gap-2 sm:inset-x-auto sm:right-6 sm:top-6 sm:w-96">
        {toasts.map((toast) => {
          const success = toast.type === 'success';
          return (
            <div
              key={toast.id}
              aria-atomic="true"
              className={`pointer-events-auto flex w-full items-start gap-3 rounded-md border bg-white px-4 py-3 shadow-lg ${success ? 'border-emerald-200' : 'border-red-200'}`}
              role={success ? 'status' : 'alert'}
            >
              {success ? <CheckCircle2 className="mt-0.5 shrink-0 text-emerald-600" size={18} /> : <AlertCircle className="mt-0.5 shrink-0 text-red-600" size={18} />}
              <div className="min-w-0 flex-1">
                <p className={`text-sm font-semibold ${success ? 'text-emerald-800' : 'text-red-800'}`}>{toast.title}</p>
                {toast.description ? <p className="mt-0.5 break-words text-xs leading-5 text-zinc-600">{toast.description}</p> : null}
              </div>
              <button aria-label="关闭通知" className="-mr-1 -mt-1 flex h-7 w-7 shrink-0 items-center justify-center rounded text-zinc-400 hover:bg-zinc-100 hover:text-zinc-700 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-blue-500" type="button" onClick={() => dismiss(toast.id)}><X size={15} /></button>
            </div>
          );
        })}
      </div>
    </ToastContext.Provider>
  );
}

export function useToast() {
  const context = useContext(ToastContext);
  if (!context) throw new Error('useToast must be used within ToastProvider');
  return context;
}

