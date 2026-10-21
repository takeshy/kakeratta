// Render all gallery layouts in one browser and reject clipped layouts.
import { spawn } from 'node:child_process';
import { readFile, writeFile, mkdir, mkdtemp, rm } from 'node:fs/promises';
import { dirname, join, resolve } from 'node:path';
import { fileURLToPath, pathToFileURL } from 'node:url';
const here = dirname(fileURLToPath(import.meta.url));
const scratch = resolve(here, '../../../tmp/hackathon-gallery');
await mkdir(scratch, { recursive: true });
const profile = await mkdtemp(join(scratch, 'render-profile-'));
const browser = spawn(process.env.CHROMIUM || 'chromium', ['--headless', '--no-sandbox', '--disable-gpu', '--disable-dev-shm-usage', '--hide-scrollbars', '--no-first-run', '--no-default-browser-check', '--remote-debugging-port=0', `--user-data-dir=${profile}`, 'about:blank'], { stdio: 'ignore' });
let launchError;
browser.on('error', error => { launchError = error; });
const ended = new Promise(resolve => browser.once('exit', resolve));
const pause = ms => new Promise(resolve => setTimeout(resolve, ms));
let socket;
try {
  let port;
  for (let i = 0; i < 150; i++) {
    if (launchError) throw launchError;
    try { port = Number((await readFile(join(profile, 'DevToolsActivePort'), 'utf8')).split('\n')[0]); break; } catch { await pause(100); }
  }
  if (!port) throw Error('Chromium did not start');
  const tabs = await (await fetch(`http://127.0.0.1:${port}/json`)).json();
  socket = new WebSocket(tabs.find(t => t.type === 'page').webSocketDebuggerUrl);
  await new Promise((resolve, reject) => { socket.onopen = resolve; socket.onerror = reject; });
  let seq = 0;
  const pending = new Map();
  socket.onmessage = event => {
    const result = JSON.parse(event.data), item = pending.get(result.id);
    if (!item) return;
    pending.delete(result.id); clearTimeout(item.timer);
    if (result.error) item.reject(Error(result.error.message)); else item.resolve(result.result);
  };
  const cdp = (method, params = {}) => new Promise((resolve, reject) => {
    const id = ++seq;
    const timer = setTimeout(() => { pending.delete(id); reject(Error(`${method} timed out`)); }, 30000);
    pending.set(id, { resolve, reject, timer });
    socket.send(JSON.stringify({ id, method, params }));
  });
  const evaluate = async expression => {
    const result = await cdp('Runtime.evaluate', { expression, awaitPromise: true, returnByValue: true });
    if (result.exceptionDetails) throw Error(result.exceptionDetails.exception?.description || result.exceptionDetails.text);
    return result.result.value;
  };
  await cdp('Page.enable');
  await cdp('Emulation.setDeviceMetricsOverride', { width: 1800, height: 1200, deviceScaleFactor: 1, mobile: false });
  const names = (await readFile(join(scratch, 'layouts.txt'), 'utf8')).trim().split('\n');
  const selected = process.env.GALLERY_ONLY?.split(',');
  for (const name of names) {
    if (selected && !selected.includes(name.split('-')[0])) continue;
    const url = pathToFileURL(join(scratch, name + '.html')).href;
    await cdp('Page.navigate', { url });
    let ready = false;
    for (let i = 0; i < 100; i++) {
      if (await evaluate(`location.href === ${JSON.stringify(url)} && document.readyState === 'complete'`)) { ready = true; break; }
      await pause(50);
    }
    if (!ready) throw Error(`${name}: page did not load`);
    await evaluate(`(async () => { await document.fonts.ready; await Promise.all([...document.images].map(img => img.decode())); return true; })()`);
    const errors = await evaluate(`(() => {
      const errors = [];
      const elements = [...document.querySelectorAll('.placed,.hero-copy,header,.bottom-line')];
      const footer = document.querySelector('footer').getBoundingClientRect();
      const rects = elements.map(el => ({ el, rect: el.getBoundingClientRect() }));
      for (const { el, rect } of rects) {
        const label = el.textContent.trim().slice(0, 55);
        if (rect.left < 60 || rect.right > 1740 || rect.top < 120 || rect.bottom > footer.top - 6) errors.push('Outside content area: ' + label + ' ' + JSON.stringify({ x: rect.x, y: rect.y, w: rect.width, h: rect.height }));
      }
      for (let i = 0; i < rects.length; i++) for (let j = i + 1; j < rects.length; j++) {
        const a = rects[i], b = rects[j];
        if (Math.min(a.rect.right,b.rect.right) - Math.max(a.rect.left,b.rect.left) > 3 && Math.min(a.rect.bottom,b.rect.bottom) - Math.max(a.rect.top,b.rect.top) > 3) errors.push('Overlap: ' + a.el.textContent.trim().slice(0,35) + ' / ' + b.el.textContent.trim().slice(0,35));
      }
      return errors;
    })()`);
    if (errors.length) throw Error(`${name}:\n${errors.join('\n')}`);
    const screenshot = await cdp('Page.captureScreenshot', { format: 'png', clip: { x: 0, y: 0, width: 1800, height: 1200, scale: 1 }, captureBeyondViewport: false });
    await writeFile(join(here, 'images', name + '.png'), Buffer.from(screenshot.data, 'base64'));
    console.log(`${name}: rendered; images decoded; no content overflow or overlap`);
  }
} finally {
  socket?.close(); browser.kill();
  await Promise.race([ended, pause(3000)]);
  await rm(profile, { recursive: true, force: true });
}
