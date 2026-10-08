import { test, expect } from '@playwright/test';

test('country selection, connection and network settings work in explicit preview', async ({ page }) => {
  const errors: string[] = [];
  page.on('pageerror', error => errors.push(error.message));
  await page.goto('/?preview=1');
  await expect(page.getByText('浏览器预览', { exact: true })).toBeVisible();
  await expect(page.getByRole('button', { name: '快速连接', exact: true })).toBeEnabled();
  await page.screenshot({ path: 'test-results/home.png', fullPage: true });
  await page.getByRole('button', { name: '国家与地区', exact: true }).click();
  await page.getByRole('textbox', { name: '搜索国家' }).fill('日本');
  await expect(page.locator('.country-card')).toHaveCount(1);
  await page.locator('.country-card').click();
  await page.getByRole('button', { name: '返回连接' }).click();
  await expect(page.locator('.destination')).toContainText('日本');
  await page.getByRole('button', { name: '快速连接', exact: true }).click();
  await expect(page.getByRole('button', { name: '断开演示连接' })).toBeVisible();
  await expect(page.getByText('演示模式 · 网络未受保护', { exact: true })).toBeVisible();
  await expect(page.getByRole('button', { name: '全局模式', exact: true })).toBeDisabled();
  await page.getByRole('button', { name: '断开演示连接' }).click();
  await page.getByRole('button', { name: '直连模式', exact: true }).click();
  await expect(page.getByRole('button', { name: '快速连接', exact: true })).toBeDisabled();
  await page.getByRole('button', { name: '智能模式', exact: true }).click();
  await page.getByRole('button', { name: '设置', exact: true }).click();
  await page.getByRole('combobox', { name: '连接方式' }).selectOption('tcp');
  await expect(page.getByRole('combobox', { name: '连接方式' })).toHaveValue('tcp');
  await page.getByRole('switch', { name: '允许局域网访问' }).click();
  await expect(page.getByRole('switch', { name: '允许局域网访问' })).toHaveAttribute('aria-checked', 'false');
  expect(errors).toEqual([]);
});

test('normal browser entry never silently falls back to a simulated connection', async ({ page }) => {
  await page.goto('/');
  await expect(page.getByRole('alert')).toContainText('请在桌面应用中使用客户端');
  await expect(page.getByRole('button', { name: '快速连接', exact: true })).toBeDisabled();
});

test('cancelling a connection never revives it', async ({ page }) => {
  await page.goto('/?preview=1');
  await page.getByRole('button', { name: '快速连接', exact: true }).click();
  await page.getByRole('button', { name: '取消连接' }).click();
  await expect(page.getByText('尚未连接', { exact: true })).toBeVisible();
  // Wait through the entire cancelled attempt by asserting continuously rather
  // than accepting only the immediate disconnected render.
  await expect.poll(async () => {
    await new Promise(resolve => setTimeout(resolve, 2200));
    return page.locator('.status-tag').textContent();
  }).toContain('尚未连接');
});
