// Reproducible documentation captures: shipped UI, local sample API responses.
// No Google credentials, Google calls, or real Timeline writes are used.
// From the repository root: node tools/google-bridge/captures.mjs
import assert from 'node:assert/strict';
import { spawn } from 'node:child_process';
import { createServer } from 'node:http';
import { mkdir, mkdtemp, readFile, writeFile, rm } from 'node:fs/promises';
import { join, resolve, extname } from 'node:path';
import { fileURLToPath } from 'node:url';

const root = fileURLToPath(new URL('../../', import.meta.url));
const web = join(root, 'tools/google-bridge/internal/server/web');
const out = join(root, 'docs/captures/google-bridge');
const stamp = '2026-10-20T09:00:00+09:00';
let mailSynced = false, calendarSynced = false;
const mail = {
  messageId: 'sample-reservation', subject: 'ご予約の確認：10月21日 19:00',
  from: 'レストラン はな <booking@example.com>', receivedAt: stamp, importedAt: stamp,
  classification: { importance: 90, urgency: 20, reason: '明日の予約日時と人数を確認できるメール' },
  summary: '10月21日（水）19:00、レストラン はなを2名で予約。\n予約の変更は前日までに連絡してください。',
  sourceUrl: 'https://mail.google.com/mail/u/0/#inbox/sample-reservation',
};
const event = {
  summary: '明日の出張の準備',
  body: '10月21日 08:30〜09:00\n場所: 自宅\n持ち物：充電器、社員証、予約の控え。',
  sourceUrl: 'https://calendar.google.com/calendar/event?eid=sample-preparation',
};
const mailState = () => ({ settings: { enabled: true, llmEnabled: true, threshold: 50, bodyChars: 2000, retentionDays: 7, lookbackDays: 1, includeLabels: ['INBOX'], priorityLabels: ['IMPORTANT', 'STARRED'], prioritySenders: [], excludedSenders: [], excludedTypes: [] }, messages: mailSynced ? [mail] : [], lastSyncedAt: mailSynced ? stamp : null });
const calendarState = () => ({ settings: { enabled: true, llmEnabled: true, calendarId: 'primary', timeZone: 'Asia/Tokyo', bodyChars: 2000, excludedEvents: [] }, events: calendarSynced ? [event] : [], lastSyncedAt: calendarSynced ? stamp : null });
const unexpected = [];
const server = createServer(async (req, res) => {
  const path = new URL(req.url, 'http://localhost').pathname;
  if (path.startsWith('/api/')) {
    res.setHeader('Content-Type', 'application/json');
    let result;
    if (req.method === 'GET' && path === '/api/session') result = {};
    else if (req.method === 'GET' && path === '/api/google/connection') result = { available: true, redirectUri: 'http://127.0.0.1:8094/api/google/callback', services: ['gmail','calendar'].map(service => ({ service, connected: true, connectionId: `sample-${service}`, status: 'connected', email: 'owner@example.com', scopes: [], lastVerifiedAt: stamp })) };
    else if (req.method === 'GET' && path === '/api/mcp/credential') result = { configured: false, loopback: true, url: 'http://127.0.0.1:8094/mcp' };
    else if (req.method === 'GET' && path === '/api/google/inbox') result = mailState();
    else if (req.method === 'GET' && path === '/api/google/calendar-inbox') result = calendarState();
    else if (req.method === 'POST' && path === '/api/google/inbox/sync') { mailSynced = true; result = { processed: 1, importedIds: [mail.messageId], hasMore: false }; }
    else if (req.method === 'POST' && path === '/api/google/calendar-inbox/sync') { calendarSynced = true; result = {}; }
    else { unexpected.push(`${req.method} ${path}`); res.statusCode = 404; result = { error: 'No capture fixture for this request' }; }
    res.end(JSON.stringify(result)); return;
  }
  // Also serve the manual and its assets for browser validation with the same run.
  let file;
  if (path === '/manual/') file = join(root, 'frontend/manual/index.html');
  else if (path.startsWith('/site/')) file = resolve(root, 'frontend', '.' + path);
  else if (path.startsWith('/captures/')) file = resolve(root, 'docs', '.' + path);
  else file = resolve(web, '.' + (path === '/' ? '/index.html' : path));
  if (![web, join(root, 'frontend'), join(root, 'docs/captures')].some(base => file.startsWith(base + '/'))) { res.writeHead(404).end(); return; }
  try { res.setHeader('Content-Type', ({ '.html': 'text/html', '.js': 'text/javascript', '.css': 'text/css', '.png': 'image/png' })[extname(file)] || 'application/octet-stream'); res.end(await readFile(file)); }
  catch { res.writeHead(404).end(); }
});
await new Promise(resolve => server.listen(0, '127.0.0.1', resolve));
const origin = `http://127.0.0.1:${server.address().port}`;
const scratch = process.env.CAPTURE_SCRATCH || join(root, 'tmp/google-bridge-captures');
await mkdir(scratch, { recursive: true });
await mkdir(out, { recursive: true });
const profile = await mkdtemp(join(scratch, 'profile-'));
const browser = spawn(process.env.CHROMIUM || 'chromium', ['--headless', '--no-sandbox', '--disable-gpu', '--no-proxy-server', '--remote-debugging-port=0', `--user-data-dir=${profile}`, 'about:blank'], { stdio: 'ignore' });
let launchError;
browser.on('error', error => { launchError = error; });
const exited = new Promise(resolve => browser.once('exit', resolve));
const pause = ms => new Promise(resolve => setTimeout(resolve, ms));
let socket;
try {
  let port;
  for (let i = 0; i < 100; i++) {
    if (launchError) throw launchError;
    try { port = Number((await readFile(join(profile, 'DevToolsActivePort'), 'utf8')).split('\n')[0]); break; } catch { await pause(100); }
  }
  assert.ok(port, 'Chromium did not start');
  const tabs = await (await fetch(`http://127.0.0.1:${port}/json`)).json();
  socket = new WebSocket(tabs.find(t => t.type === 'page').webSocketDebuggerUrl);
  await new Promise((resolve, reject) => { socket.onopen = resolve; socket.onerror = reject; });
  let seq = 0;
  const pending = new Map();
  socket.onmessage = e => {
    const m = JSON.parse(e.data), p = pending.get(m.id);
    if (!p) return;
    pending.delete(m.id); clearTimeout(p.timer);
    if (m.error) p.reject(Error(m.error.message)); else p.resolve(m.result);
  };
  const cdp = (method, params = {}) => new Promise((resolve, reject) => {
    const id = ++seq;
    const timer = setTimeout(() => { pending.delete(id); reject(Error(`Timed out: ${method}`)); }, 15000);
    pending.set(id, { resolve, reject, timer });
    socket.send(JSON.stringify({ id, method, params }));
  });
  const evaluate = async expression => {
    const r = await cdp('Runtime.evaluate', { expression, awaitPromise: true, returnByValue: true });
    if (r.exceptionDetails) throw Error(r.exceptionDetails.exception?.description || r.exceptionDetails.text);
    return r.result.value;
  };
  const waitFor = async expression => {
    for (let i = 0; i < 100; i++) { if (await evaluate(expression)) return; await pause(100); }
    throw Error(`Not ready: ${expression}`);
  };
  await cdp('Page.enable');
  await cdp('Emulation.setTimezoneOverride', { timezoneId: 'Asia/Tokyo' });
  await cdp('Emulation.setLocaleOverride', { locale: 'ja-JP' });
  await cdp('Emulation.setDeviceMetricsOverride', { width: 1000, height: 1400, deviceScaleFactor: 2, mobile: false });
  await cdp('Page.navigate', { url: origin });
  await waitFor(`document.querySelector('#inbox button.primary') && document.querySelector('#calendar-inbox button.primary')`);
  await evaluate(`[...document.querySelectorAll('#inbox button')].find(b => b.textContent === '今すぐ差分を取得').click()`);
  await waitFor(`document.querySelector('#inbox .mail-body') && document.querySelector('#inbox').textContent.includes('Hermes配送待ち')`);
  await evaluate(`[...document.querySelectorAll('#calendar-inbox button')].find(b => b.textContent === '今日・明日の予定を今すぐ確認').click()`);
  await waitFor(`document.querySelector('#calendar-inbox .mail-body') && document.querySelector('#calendar-inbox').textContent.includes('Hermes配送待ち')`);
  await evaluate('document.fonts.ready');
  for (const [name, selector] of [['gmail', '#inbox'], ['calendar', '#calendar-inbox']]) {
    const clip = await evaluate(`(() => { const r = document.querySelector(${JSON.stringify(selector)}).getBoundingClientRect(); return { x: r.x + scrollX, y: r.y + scrollY, width: Math.ceil(r.width), height: Math.ceil(r.height), scale: 1 }; })()`);
    const capture = await cdp('Page.captureScreenshot', { format: 'png', captureBeyondViewport: true, clip });
    await writeFile(join(out, name + '.png'), Buffer.from(capture.data, 'base64'));
    console.log(`${name}.png: ${clip.width} × ${clip.height} CSS pixels`);
  }
  // Verify that bare Google anchors select the advanced guide and images load,
  // including the narrow viewport used by the manual's phone readers.
  for (const lang of ['ja', 'en']) {
    await cdp('Emulation.setDeviceMetricsOverride', { width: 390, height: 844, deviceScaleFactor: 1, mobile: true });
    await cdp('Page.navigate', { url: `${origin}/manual/?lang=${lang}#${lang}-google` });
    await waitFor(`document.readyState === 'complete' && document.documentElement.dataset.guide === 'advanced' && document.querySelector('#${lang}-google')`);
    const result = await evaluate(`(async () => {
      const heading = document.querySelector('#${lang}-google');
      const images = [...heading.parentElement.querySelectorAll('img[src*="google-bridge"]')];
      await Promise.all(images.map(image => { image.loading = 'eager'; return image.decode(); }));
      return { visible: heading.getBoundingClientRect().height > 0, images: images.length, overflow: document.documentElement.scrollWidth > innerWidth };
    })()`);
    assert.equal(result.visible, true); assert.equal(result.images, 2); assert.equal(result.overflow, false);
    console.log(`Manual ${lang}: Google anchor, 2 screenshots, no horizontal overflow at 390px`);
  }
  assert.deepEqual(unexpected, []);
} finally {
  socket?.close(); browser.kill(); await exited;
  await new Promise(resolve => server.close(resolve));
  await rm(profile, { recursive: true, force: true });
}
