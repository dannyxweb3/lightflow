import { invoke, isTauri } from '@tauri-apps/api/core';
import type { Command, Snapshot } from './model';
import { PreviewClient } from './preview';

const preview = !isTauri() && new URLSearchParams(location.search).get('preview') === '1' ? new PreviewClient() : undefined;
export const browserPreview = !!preview;

export async function request(command: Command): Promise<Snapshot> {
  if (preview) return preview.request(command);
  if (!isTauri()) throw new Error('BROWSER_PREVIEW_DISABLED');
  const requestId = crypto.randomUUID();
  const response = await invoke<{
    api_version: number; request_id: string; result?: Snapshot; error?: { code: string };
  }>('daemon_request', { request: { api_version: 1, request_id: requestId, ...command } });
  if (response.error) throw new Error(response.error.code);
  if (!response.result || response.api_version !== 1 || response.request_id !== requestId) throw new Error('INVALID_DAEMON_RESPONSE');
  return response.result;
}
