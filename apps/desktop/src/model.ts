export type NetworkMode = 'smart' | 'global' | 'direct';
export type Transport = 'auto' | 'quic' | 'tcp';
export type ConnectionState = 'disconnected' | 'authorizing' | 'preparing' | 'connecting' | 'verifying' | 'connected';
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
}
export type Command =
  | { method: 'get_snapshot'; params: Record<string, never> }
  | { method: 'connect'; params: { country_code: string } }
  | { method: 'disconnect'; params: Record<string, never> }
  | { method: 'update_settings'; params: Settings };
export const stateLabels: Record<ConnectionState, string> = {
  disconnected: '尚未连接', authorizing: '正在检查连接权限', preparing: '正在准备连接',
  connecting: '正在建立连接', verifying: '正在验证连接', connected: '演示已连接',
};
export function isBusy(state?: ConnectionState) { return !!state && state !== 'disconnected' && state !== 'connected'; }
export function newest(current: Snapshot | undefined, incoming: Snapshot): Snapshot {
  return current && current.instance_id === incoming.instance_id && current.sequence > incoming.sequence ? current : incoming;
}
export const errorMessages: Record<string, string> = {
  DAEMON_UNAVAILABLE: '本地网络服务尚未启动，请先运行开发启动脚本。',
  DAEMON_TIMEOUT: '本地服务响应超时，请检查服务后重试。',
  BROWSER_PREVIEW_DISABLED: '请在桌面应用中使用客户端，或通过 ?preview=1 打开浏览器演示。',
  DIRECT_MODE: '直连模式下不建立 VPN，请先切换到智能或全局模式。',
  CONNECTION_BUSY: '已有连接操作正在进行，请先断开。',
  DISCONNECT_BEFORE_SETTINGS: '请先断开连接，再修改网络设置。',
  NETWORK_ADAPTER_NOT_READY: '真实网络适配尚未完成，当前版本只支持演示。',
  INVALID_COUNTRY: '此国家当前不可用，请重新选择。',
};
export function errorMessage(error: unknown) {
  const code = error instanceof Error ? error.message : String(error);
  return errorMessages[code] ?? '操作未完成，请检查本地服务并重试。';
}
