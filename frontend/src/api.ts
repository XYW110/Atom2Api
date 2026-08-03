export interface UserInfo {
  id: string;
  username: string;
  name?: string;
  email?: string;
  avatar_url?: string;
}

export interface PlanInfo {
  plan_name: string;
  status: number;
  claimed_at: string;
  expires_at: string;
  remaining_days: number;
  total_days: number;
}

export interface RateLimitWindow {
  rule_index: number;
  show_enable: number;
  window_size_seconds: number;
  window_hours: number;
  call_limit: number;
  calls_used: number;
  usage_percent: number;
  quota_exhausted: boolean;
  reset_at: string;
  reset_at_display: string;
  seconds_until_reset: number;
  usage_status_desc: string;
}

export interface CurrentUsage {
  window_token_limit: number;
  window_tokens_used: number;
  usage_percent: number;
  window_hours: number;
  reset_at: string;
  reset_at_display: string;
  seconds_until_reset: number;
  usage_status_desc: string;
}

export interface CodingPlanStatus {
  codingplan_free?: PlanInfo;
  current_usage?: CurrentUsage;
  expires_at?: string;
  window_quota_exhausted: boolean;
  window_quota_hint?: string;
  rate_limit_windows?: RateLimitWindow[];
}

export interface ProviderUsageRow {
  date: string;
  total_counts: number;
  total_tokens: number;
  model_counts: Record<string, number>;
  model_tokens: Record<string, number>;
}

export interface ProviderUsage {
  days: number;
  start_date: string;
  end_date: string;
  rows: ProviderUsageRow[];
  total_counts: number;
  total_tokens: number;
}

export interface PlanClaimSchedule {
  enabled: boolean;
  cron: string;
}

export interface VersionInfo {
  current_version: string;
  latest_version?: string;
  update_available: boolean;
  release_url?: string;
  release_notes?: string;
  published_at?: string;
  checked_at: string;
  check_error?: string;
}

export interface CodingPlanClaimAttempt {
  plan_type: 'Max' | 'Pro' | 'Lite';
  http_status: number;
  response: string;
  success: boolean;
  duplicate: boolean;
  message?: string;
}

export interface PlanClaimLog {
  id: string;
  account_id: string;
  account_name: string;
  trigger: 'manual' | 'scheduled';
  cron?: string;
  status: 'running' | 'success' | 'failed';
  plan_name?: string;
  message?: string;
  attempts?: CodingPlanClaimAttempt[];
  started_at: string;
  finished_at?: string;
}

export interface PlanClaimResult {
  account: Account;
  log: PlanClaimLog;
}

export interface Account {
  id: string;
  name: string;
  note: string;
  status: 'active' | 'paused' | 'error' | 'syncing';
  enabled: boolean;
  user: UserInfo;
  plan: CodingPlanStatus;
  models: Array<{ display_model_name: string; plan_available: boolean }>;
  provider_usage?: ProviderUsage;
  plan_claim_schedule: PlanClaimSchedule;
  request_count: number;
  input_tokens: number;
  output_tokens: number;
  created_at: string;
  updated_at: string;
  last_sync_at?: string;
  last_used_at?: string;
  last_error?: string;
  token_expires_at?: string;
}

export interface APIKeyRecord {
  id: string;
  name: string;
  prefix: string;
  enabled: boolean;
  allowed_models?: string[];
  rpm_limit: number;
  concurrency_limit: number;
  created_at: string;
  expires_at?: string;
  last_used_at?: string;
  request_count: number;
  input_tokens: number;
  output_tokens: number;
}

export interface ModelRecord {
  id: string;
  alias: string;
  upstream: string;
  provider_type: string;
  base_url: string;
  context_window: number;
  enabled: boolean;
  account_count: number;
  accounts: string[];
  plans: string[];
  manual: boolean;
  responses_chat_compat: boolean;
}

export interface ProtocolProbeResult {
  available: boolean;
  status: number;
  latency_ms: number;
  error?: string;
}

export interface AccountProtocolProbeResult {
  account_id: string;
  account_name: string;
  streaming: boolean;
  results: Array<{
    model: string;
    chat: ProtocolProbeResult;
    responses: ProtocolProbeResult;
  }>;
}

export interface DashboardSummary {
  requests: number;
  input_tokens: number;
  output_tokens: number;
  total_tokens: number;
  success_rate: number;
  average_latency_ms: number;
  active_accounts: number;
  total_accounts: number;
  active_keys: number;
}

export interface DashboardTrend {
  start: string;
  label: string;
  requests: number;
  errors: number;
  input_tokens: number;
  output_tokens: number;
}

export interface DashboardResponse {
  range: string;
  summary: DashboardSummary;
  trend: DashboardTrend[];
  model_distribution: Array<{ model: string; requests: number; input_tokens: number; output_tokens: number }> | null;
  recent_requests: Array<{
    id: string;
    timestamp: string;
    model: string;
    account_name: string;
    key_name: string;
    status: number;
    latency_ms: number;
    first_token_latency_ms?: number;
    completion_latency_ms?: number;
    input_tokens: number;
    output_tokens: number;
    streaming: boolean;
  }> | null;
}

export interface AuditRecordSummary {
  id: string;
  timestamp: string;
  method: string;
  path: string;
  model: string;
  upstream_model: string;
  account_name: string;
  key_name: string;
  status: number;
  latency_ms: number;
  first_token_latency_ms?: number;
  completion_latency_ms?: number;
  input_tokens: number;
  output_tokens: number;
  cached_tokens?: number;
  reasoning_tokens?: number;
  streaming: boolean;
  has_request_body: boolean;
  has_response_body: boolean;
}

export type AuditRecordDetail = Omit<AuditRecordSummary, 'has_request_body' | 'has_response_body'> & {
  account_id?: string;
  api_key_id?: string;
  error?: string;
  request_body?: string;
  response_body?: string;
  request_headers?: Record<string, string[]>;
  response_headers?: Record<string, string[]>;
};

export interface AuditListResponse {
  items: AuditRecordSummary[];
  total: number;
  page: number;
  page_size: number;
  pages: number;
}

export interface SettingsResponse {
  user_agent: string;
  listen_address: string;
  data_path: string;
  platform_base_url: string;
  codingplan_api_url: string;
  gateway_url: string;
  signer_url: string;
  signer_configured: boolean;
  audit_debug_enabled: boolean;
  request_timeout_seconds: number;
  default_password: boolean;
  loaded_at: string;
}

export interface UserAgentCheckResponse {
  version: string;
  user_agent: string;
}

export class APIError extends Error {
  status: number;

  constructor(message: string, status: number) {
    super(message);
    this.status = status;
  }
}

export async function apiFetch<T>(path: string, init: RequestInit = {}): Promise<T> {
  const headers = new Headers(init.headers);
  headers.set('Accept', 'application/json');
  const response = await fetch(path, { ...init, headers, credentials: 'same-origin' });
  if (!response.ok) {
    let message = `请求失败 (${response.status})`;
    try {
      const body = (await response.json()) as { error?: string; message?: string };
      message = body.error || body.message || message;
    } catch {
      // Keep the status-based fallback.
    }
    if (response.status === 401 && path !== '/api/auth/login') {
      window.dispatchEvent(new Event('atom2api:unauthorized'));
    }
    throw new APIError(message, response.status);
  }
  if (response.status === 204) return undefined as T;
  return (await response.json()) as T;
}

export function jsonRequest(method: string, body?: unknown): RequestInit {
  return {
    method,
    headers: { 'Content-Type': 'application/json' },
    body: body === undefined ? undefined : JSON.stringify(body),
  };
}

export function errorMessage(error: unknown, fallback: string): string {
  return error instanceof Error ? error.message : fallback;
}

export function formatTokens(value: number): string {
  return new Intl.NumberFormat('zh-CN', { notation: value >= 10000 ? 'compact' : 'standard', maximumFractionDigits: 1 }).format(value || 0);
}

export function formatDateTime(value?: string): string {
  if (!value) return '尚无记录';
  const date = new Date(value);
  if (Number.isNaN(date.getTime())) return value;
  return new Intl.DateTimeFormat('zh-CN', {
    month: '2-digit', day: '2-digit', hour: '2-digit', minute: '2-digit',
  }).format(date);
}
