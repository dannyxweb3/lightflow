import { test, expect } from '@playwright/test';

// UI contract test only. This mock does not claim to verify VPN forwarding.
test('login clears password, shows subscription, and signs out locally', async ({ page }) => {
  await page.addInitScript(() => {
    Object.defineProperty(globalThis, 'isTauri', { value: true });
    const empty = { authenticated: false, busy: false, email: '', plan: '', expires_at: '', enabled: false, device_count: 0, device_limit: 0, concurrent_limit: 0, checked_at: '' };
    const snapshot = {
      instance_id: 'account-ui-test', sequence: 0, state: 'disconnected', country_code: '', operation_id: '',
      simulation: false, daemon_version: 'test', settings: { mode: 'global', transport_preference: 'quic', allow_lan: true },
      countries: [], account: { ...empty },
    };
    let loginReady = 0;
    Object.defineProperty(window, '__TAURI_INTERNALS__', { value: {
      invoke: async (_command: string, args: { request: { method: string; request_id: string } }) => {
        const request = args.request;
        if (request.method === 'login') {
          snapshot.account.busy = true;
          loginReady = Date.now() + 1200;
        }
        if (loginReady && Date.now() >= loginReady) {
          loginReady = 0;
          snapshot.account = { ...empty, authenticated: true, email: 'ui@example.test', plan: 'standard', enabled: true, expires_at: '2099-01-01T00:00:00Z', device_count: 1, device_limit: 3, concurrent_limit: 2, checked_at: '2026-10-10T12:00:00Z' };
        }
        if (request.method === 'logout') snapshot.account = { ...empty };
        snapshot.sequence++;
        return { api_version: 1, request_id: request.request_id, result: structuredClone(snapshot) };
      },
    } });
  });
  await page.goto('/');
  await page.getByRole('button', { name: '登录后连接', exact: true }).click();
  await page.screenshot({ path: 'test-results/login.png', fullPage: true });
  await page.getByLabel('邮箱', { exact: true }).fill('ui@example.test');
  const password = page.getByLabel('密码', { exact: true });
  await password.fill('test-password-only');
  await page.getByRole('button', { name: '登录', exact: true }).click();
  await expect(password).toHaveValue('');
  await expect(page.getByText('ui@example.test', { exact: true })).toBeVisible();
  await expect(page.getByText('订阅有效', { exact: true })).toBeVisible();
  await expect(page.getByText('1 / 3', { exact: true })).toBeVisible();
  await page.screenshot({ path: 'test-results/account.png', fullPage: true });
  await page.getByRole('button', { name: '退出本机登录', exact: true }).click();
  await expect(page.getByLabel('密码', { exact: true })).toHaveValue('');
  await expect(page.getByText('ui@example.test', { exact: true })).toHaveCount(0);
});
