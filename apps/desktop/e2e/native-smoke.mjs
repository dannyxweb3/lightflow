import { chromium } from '@playwright/test';
import { mkdir } from 'node:fs/promises';

let browser;
for (let attempt = 0; attempt < 80; attempt++) {
  try { browser = await chromium.connectOverCDP('http://127.0.0.1:19222'); break; }
  catch { await new Promise(resolve => setTimeout(resolve, 100)); }
}
if (!browser) throw new Error('Native WebView2 debugger did not become ready');
try {
  let page;
  for (let attempt = 0; attempt < 80; attempt++) {
    page = browser.contexts().flatMap(context => context.pages()).find(p => p.url().includes('tauri.localhost'));
    if (page) break;
    await new Promise(resolve => setTimeout(resolve, 100));
  }
  if (!page) throw new Error('Embedded Tauri page not found');
  const errors = [];
  page.on('pageerror', error => errors.push(error.message));
  await page.getByText('本地服务就绪', { exact: true }).waitFor();
  await page.getByRole('button', { name: '快速连接', exact: true }).click();
  await page.getByRole('button', { name: '断开演示连接' }).waitFor();
  await page.getByRole('button', { name: '断开演示连接' }).click();
  await page.getByText('尚未连接', { exact: true }).waitFor();
  await mkdir('test-results', { recursive: true });
  await page.screenshot({ path: 'test-results/native-home.png' });
  if (errors.length) throw new Error(errors.join('\n'));
  console.log('PASS: native Tauri → Rust Bridge → Windows Named Pipe → Go daemon → disconnect');
} finally { await browser.close(); }
