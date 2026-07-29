import type { ReactNode } from 'react';
import { Button } from '@heroui/react';
import type { LucideIcon } from 'lucide-react';

interface PageShellProps {
  title: string;
  description: string;
  action?: {
    label: string;
    icon: LucideIcon;
    onPress: () => void;
  };
  children: ReactNode;
}

export function PageShell({ title, description, action, children }: PageShellProps) {
  const ActionIcon = action?.icon;

  return (
    <div className="space-y-6">
      <div className="flex flex-col gap-4 sm:flex-row sm:items-start sm:justify-between">
        <div>
          <h1 className="text-2xl font-semibold text-zinc-950">{title}</h1>
          <p className="mt-1 text-sm text-zinc-500">{description}</p>
        </div>
        {action && ActionIcon ? (
          <Button color="primary" radius="sm" startContent={<ActionIcon size={17} />} onPress={action.onPress}>
            {action.label}
          </Button>
        ) : null}
      </div>
      {children}
    </div>
  );
}

export function EmptyState({ icon: Icon, title, description }: { icon: LucideIcon; title: string; description: string }) {
  return (
    <div className="flex min-h-56 flex-col items-center justify-center px-6 text-center">
      <div className="mb-3 flex h-10 w-10 items-center justify-center rounded-lg bg-zinc-100 text-zinc-500">
        <Icon size={19} />
      </div>
      <p className="text-sm font-semibold text-zinc-800">{title}</p>
      <p className="mt-1 max-w-sm text-sm text-zinc-500">{description}</p>
    </div>
  );
}

export function StatusDot({ tone = 'success' }: { tone?: 'success' | 'warning' | 'danger' | 'neutral' }) {
  const colors = {
    success: 'bg-emerald-500',
    warning: 'bg-amber-500',
    danger: 'bg-red-500',
    neutral: 'bg-zinc-400',
  };

  return <span aria-hidden="true" className={`inline-block h-2 w-2 rounded-full ${colors[tone]}`} />;
}
