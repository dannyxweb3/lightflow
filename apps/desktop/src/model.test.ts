import { describe, it, expect, vi, afterEach } from 'vitest';
import { PreviewClient } from './preview';
import { newest } from './model';

afterEach(() => vi.useRealTimers());
describe('connection contract', () => {
  it('disconnect cancels every pending browser transition', async () => {
    vi.useFakeTimers();
    const client = new PreviewClient();
    await client.request({ method: 'connect', params: { country_code: 'JP' } });
    await client.request({ method: 'disconnect', params: {} });
    await vi.advanceTimersByTimeAsync(3000);
    const state = await client.request({ method: 'get_snapshot', params: {} });
    expect(state.state).toBe('disconnected');
    expect(state.country_code).toBe('');
  });
  it('rejects a connection in direct mode', async () => {
    const client = new PreviewClient();
    await client.request({ method: 'update_settings', params: { mode: 'direct', transport_preference: 'auto', allow_lan: true } });
    await expect(client.request({ method: 'connect', params: { country_code: '' } })).rejects.toThrow('DIRECT_MODE');
  });
  it('stale polls cannot overwrite a newer disconnect snapshot', async () => {
    vi.useFakeTimers();
    const client = new PreviewClient();
    const old = await client.request({ method: 'connect', params: { country_code: 'JP' } });
    const current = await client.request({ method: 'disconnect', params: {} });
    expect(newest(current, old)).toEqual(current);
  });
  it('all preview connections remain explicitly simulated', async () => {
    vi.useFakeTimers();
    const client = new PreviewClient();
    await client.request({ method: 'connect', params: { country_code: '' } });
    await vi.advanceTimersByTimeAsync(2000);
    const state = await client.request({ method: 'get_snapshot', params: {} });
    expect(state.state).toBe('connected');
    expect(state.simulation).toBe(true);
  });
  it('accepts the lower sequence of a restarted daemon instance', async () => {
    const client = new PreviewClient();
    const previous = await client.request({ method: 'get_snapshot', params: {} });
    const incoming = { ...previous, instance_id: 'new-instance', sequence: 0 };
    expect(newest({ ...previous, sequence: 100 }, incoming)).toEqual(incoming);
  });
});
