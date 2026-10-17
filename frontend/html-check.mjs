// Browser regression checks for generated HTML. Uses only a local fixture
// server and the shipped modules; no account, provider or publication calls.
import assert from "node:assert/strict";
import http from "node:http";
import { spawn } from "node:child_process";
import { mkdir, mkdtemp, readFile, rm } from "node:fs/promises";
import { homedir } from "node:os";
import { join } from "node:path";

const root = new URL("./src/", import.meta.url);
let probes = 0;
const server = http.createServer(async (req, res) => {
  const path = new URL(req.url, "http://localhost").pathname;
  if (path === "/probe") { probes++; res.end("unexpected request"); return; }
  if (path === "/") { res.setHeader("Content-Type", "text/html"); res.end("<!doctype html><html><body></body></html>"); return; }
  if (!/^\/src\/[a-z0-9_-]+\.js$/.test(path)) { res.writeHead(404); res.end(); return; }
  try {
    res.setHeader("Content-Type", "text/javascript");
    res.end(await readFile(new URL(path.slice(5), root)));
  } catch { res.writeHead(404); res.end(); }
});
await new Promise(resolve => server.listen(0, "127.0.0.1", resolve));
const origin = `http://127.0.0.1:${server.address().port}`;
// Snap Chromium requires a profile under the home directory.
const scratch = process.env.HTML_CHECK_SCRATCH || join(homedir(), ".cache", "kakeratta-html-check");
await mkdir(scratch, { recursive: true });
const profile = await mkdtemp(join(scratch, "profile-"));
const browser = spawn(process.env.CHROMIUM || "chromium", [
  "--headless", "--no-sandbox", "--disable-gpu", "--no-proxy-server",
  "--remote-debugging-port=0", `--user-data-dir=${profile}`, "about:blank",
], { stdio: "ignore" });
let launchError;
browser.on("error", error => { launchError = error; });
const exited = new Promise(resolve => browser.once("exit", resolve));
const pause = ms => new Promise(resolve => setTimeout(resolve, ms));
let socket;
try {
  let port;
  for (let i = 0; i < 100; i++) {
    if (launchError) throw launchError;
    try { port = Number((await readFile(join(profile, "DevToolsActivePort"), "utf8")).split("\n")[0]); break; }
    catch { await pause(100); }
  }
  assert.ok(port, "Chromium did not start; set CHROMIUM to its executable");
  const tabs = await (await fetch(`http://127.0.0.1:${port}/json`)).json();
  socket = new WebSocket(tabs.find(tab => tab.type === "page").webSocketDebuggerUrl);
  await new Promise((resolve, reject) => { socket.addEventListener("open", resolve, { once: true }); socket.addEventListener("error", reject, { once: true }); });
  let sequence = 0;
  const pending = new Map();
  socket.onmessage = event => {
    const message = JSON.parse(event.data);
    if (!message.id) return;
    const call = pending.get(message.id);
    if (!call) return;
    pending.delete(message.id);
    clearTimeout(call.timer);
    if (message.error) call.reject(Error(JSON.stringify(message.error))); else call.resolve(message.result);
  };
  const cdp = (method, params = {}) => new Promise((resolve, reject) => {
    const id = ++sequence;
    const timer = setTimeout(() => { pending.delete(id); reject(Error(`Timed out: ${method}`)); }, 30000);
    pending.set(id, { resolve, reject, timer });
    socket.send(JSON.stringify({ id, method, params }));
  });
  const evaluate = async expression => {
    const result = await cdp("Runtime.evaluate", { expression, awaitPromise: true, returnByValue: true });
    if (result.exceptionDetails) throw Error(result.exceptionDetails.exception?.description || result.exceptionDetails.text);
    return result.result.value;
  };
  await cdp("Page.enable");
  await cdp("Page.navigate", { url: origin });
  for (let i = 0; i < 100 && !await evaluate(`location.origin === ${JSON.stringify(origin)} && document.readyState === "complete"`); i++) await pause(50);
  const result = await evaluate(`(async () => {
    const { renderHtmlDocument } = await import('/src/html-document.js');
    const { renderHtmlImages } = await import('/src/html-image.js');
    const hostile = '<script>parent.__htmlEscaped = true;fetch("/probe")<\/script><img src="/probe"><style>body{background-image:url("/probe")}</style>';
    const source = '<html><head><style>html,body{margin:0} .art{height:2000px;background:linear-gradient(red,blue)}</style></head><body>' + hostile + '<div class="art">diagram</div></body></html>';
    const frame = renderHtmlDocument(source);
    await new Promise(resolve => { frame.onload = resolve; document.body.append(frame); });
    const isolated = frame.contentDocument === null;
    frame.remove();
    const capture = await renderHtmlImages(source);
    const pages = [];
    for (const src of capture.images) {
      const image = new Image(); image.src = src; await image.decode();
      const canvas = document.createElement('canvas'); canvas.width = image.width; canvas.height = image.height;
      const ctx = canvas.getContext('2d'); ctx.drawImage(image, 0, 0);
      pages.push({ width: image.width, height: image.height, pixel: [...ctx.getImageData(20, image.height - 20, 1, 1).data] });
    }
    const rejected = [];
    for (const style of ['height:14401px', 'width:2401px;height:20px']) {
      try { await renderHtmlImages('<html><body style="margin:0"><div style="' + style + '">too big</div></body></html>'); rejected.push(false); }
      catch { rejected.push(true); }
    }
    return { isolated, escaped: !!window.__htmlEscaped, pages, rejected, hash: capture.sourceHash, framesLeft: document.querySelectorAll('iframe').length };
  })()`);
  assert.equal(result.isolated, true, "timeline frame must have an opaque origin");
  assert.equal(result.escaped, false, "generated scripts ran");
  assert.equal(probes, 0, "generated HTML fetched a forbidden subresource");
  assert.equal(result.pages.length, 2);
  assert.equal(result.pages[0].width, 1440);
  assert.equal(result.pages[0].height, 2880);
  assert.equal(result.pages[1].width, 1440);
  assert.ok(result.pages[1].height > 1000 && result.pages[1].height < 1300);
  assert.ok(result.pages[1].pixel[2] > 200 && result.pages[1].pixel[0] < 50, "last page lost the diagram's blue lower edge");
  assert.match(result.hash, /^[a-f0-9]{64}$/);
  assert.deepEqual(result.rejected, [true, true]);
  assert.equal(result.framesLeft, 0);
  const sharing = await evaluate(`(async () => {
    const { api } = await import('/src/api.js');
    const { openSocial } = await import('/src/social.js');
    api.thread = async () => ({entries:[{id:'root',kind:'post',speaker:'user',text:'A simple fragment'}, {id:'answer',root:'root',replyTo:'root',speaker:'tabe',text:'## A useful answer\\n- One detail'}]});
    api.blueskyConnection = async () => ({available:true,connected:true,did:'did:plc:fixture'});
    let request, calls=0;
    api.previewSocial = async body => ({digest:'fixture',account:'did:plc:fixture',posts:body.entryIds.map(id=>({entryId:id,text:body.formats[id]==='image'?'[Image]':'A simple fragment',image:body.htmlImages[id]?.images[0]}))});
    api.publishSocial = async body => {request=body;calls++;return {posts:[]};};
    await openSocial('root', id=>({name:id}));
    const waitFor = async predicate => {for(let i=0;i<200;i++){if(predicate())return;await new Promise(resolve=>setTimeout(resolve,25));}throw Error('Sharing did not settle');};
    const dialog=document.querySelector('.social-dialog');
    const publish=dialog.querySelector('.social-publish');
    await waitFor(()=>!publish.disabled);
    const formats=dialog.querySelector('.social-formats');
    const imageButton=[...formats.querySelectorAll('button')][1];
    imageButton.click();
    await waitFor(()=>!publish.disabled);
    const image=dialog.querySelector('.social-entry-preview img');
    if(!image)throw Error('No inline image preview');
    await image.decode();
    const width=image.naturalWidth;
    publish.click();await waitFor(()=>calls===1 && publish.disabled);
    const result={width,calls,format:request.formats.root,hash:request.htmlImages.root.sourceHash,previewButton:[...dialog.querySelectorAll('button')].some(button=>button.textContent==='プレビュー')};
    dialog.close();
    return result;
  })()`);
  assert.equal(sharing.width, 1440);
  assert.equal(sharing.format, "image");
  assert.match(sharing.hash, /^[a-f0-9]{64}$/);
  assert.equal(sharing.calls, 1);
  assert.equal(sharing.previewButton, false);
  console.log("PASS: automatic inline social preview, text/image switch, real snapshot capture, one-click publication");
  console.log("PASS: isolated HTML, blocked scripts/resources, two captured pages with visible lower edge, size limits, frame cleanup");
} finally {
  socket?.close();
  browser.kill();
  await Promise.race([exited, pause(3000)]);
  server.closeAllConnections();
  server.close();
  await rm(profile, { recursive: true, force: true });
}
