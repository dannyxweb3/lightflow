export type NetworkMode = 'smart' | 'global' | 'direct';
export type Transport = 'auto' | 'quic' | 'tcp';
export type ConnectionState = 'disconnected' | 'authorizing' | 'preparing' | 'connecting' | 'verifying' | 'connected' | 'disconnecting';
export interface Country { code: string; name: string; city: string; latency_ms: number }
export interface Settings { mode: NetworkMode; transport_preference: Transport; allow_lan: boolean }
export interface Snapshot {
  instance_id: string;
  sequence: number;
  state: ConnectionState;
  country_code: string;
  operation_id: string;
  connected_at?: string;
  simulation: boolean;
  daemon_version: string;
  settings: Settings;
  countries: Country[];
  last_error?: string;
  account?: Account;
}
export interface Account {
 authenticated: boolean; busy: boolean; email: string; plan: string; expires_at: string;
 enabled: boolean; device_count: number; device_limit: number; concurrent_limit: number; checked_at: string;
}
export type Command =
  | { method: 'get_snapshot'; params: Record<string, never> }
  | { method: 'connect'; params: { country_code: string } }
  | { method: 'disconnect'; params: Record<string, never> }
  | { method: 'login'; params: { email: string; password: string } }
  | { method: 'logout' | 'refresh_account'; params: Record<string, never> }
  | { method: 'update_settings'; params: Settings };
export const stateLabels: Record<ConnectionState, string> = {
  disconnected: '尚未连接', authorizing: '正在检查连接权限', preparing: '正在准备连接',
  connecting: '正在建立连接', verifying: '正在验证连接', connected: '已连接', disconnecting: '正在断开并恢复网络',
};
export function isBusy(state?: ConnectionState) { return !!state && state !== 'disconnected' && state !== 'connected'; }
export function newest(current: Snapshot | undefined, incoming: Snapshot): Snapshot {
  return current && current.instance_id === incoming.instance_id && current.sequence > incoming.sequence ? current : incoming;
}
export const errorMessages: Record<string, string> = {
  ACCOUNT_NOT_AVAILABLE: '登录仅在真实桌面客户端中可用。',
  LOGIN_REQUIRED: '请登录账号；登录状态失效时需要重新登录。',
  INVALID_CREDENTIALS: '邮箱或密码不正确，请重新输入。',
  LOGIN_RATE_LIMITED: '登录尝试过于频繁，请稍后再试。',
  ACCOUNT_REQUEST_FAILED: '账号请求未完成，请检查网络后重试。',
  ACCOUNT_BUSY: '账号操作正在进行，请稍候。',
  SUBSCRIPTION_INACTIVE: '订阅未启用或已到期，请联系管理员。',
  LOGOUT_BEFORE_LOGIN: '请先退出当前账号，再登录其他账号。',
  DISCONNECT_BEFORE_ACCOUNT: '请先断开连接，再刷新账号信息。',
  DEVICE_LIMIT_REACHED: '账号设备数量已达上限，请联系管理员。',
  DEVICE_REVOKED: '本机设备已被撤销，请联系管理员。',
  LOCAL_LOGOUT_FAILED: '本机登录状态清理失败，请重试。',
  DAEMON_UNAVAILABLE: '本地网络服务尚未启动，请先运行开发启动脚本。',
  DAEMON_TIMEOUT: '本地服务响应超时，请检查服务后重试。',
  BROWSER_PREVIEW_DISABLED: '请在桌面应用中使用客户端，或通过 ?preview=1 打开浏览器演示。',
  DIRECT_MODE: '直连模式下不建立 VPN，请先切换到智能或全局模式。',
  CONNECTION_BUSY: '已有连接操作正在进行，请先断开。',
  DISCONNECT_BEFORE_SETTINGS: '请先断开连接，再修改网络设置。',
  NETWORK_ADAPTER_NOT_READY: '真实网络适配尚未完成，当前版本只支持演示。',
  INVALID_COUNTRY: '此国家当前不可用，请重新选择。',
  REAL_CONNECTION_FAILED: '真实连接未成功，已停止代理。请检查网络后重试。',
  PREVIOUS_LEASE_UNRESOLVED: '上次连接尚未确认释放，暂不能新建连接，请检查网络服务。',
  NETWORK_RECOVERY_FAILED: '网络恢复未完成，请停止操作并检查网络服务。',
  SETTING_NOT_AVAILABLE: '当前开发版暂不支持此设置。',
  UNTRUSTED_DAEMON: '本地网络服务身份校验失败。',
};
export function errorMessage(error: unknown) {
  const code = error instanceof Error ? error.message : String(error);
  return errorMessages[code] ?? '操作未完成，请检查本地服务并重试。';
}
