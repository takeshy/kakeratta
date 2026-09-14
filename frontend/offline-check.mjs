import http from 'node:http';
import {readFile,writeFile} from 'node:fs/promises';
import {spawn} from 'node:child_process';
import {fileURLToPath} from 'node:url';
import {homedir} from 'node:os';
import assert from 'node:assert/strict';
// End-to-end offline check against a local fixture API; never calls production.
const root=fileURLToPath(new URL('./dist',import.meta.url));
const captures=fileURLToPath(new URL('../docs/captures/',import.meta.url));
const entries=[{id:'sample',root:'sample',kind:'post',speaker:'user',text:'今日の散歩で、気になる喫茶店を見つけた。',at:new Date().toISOString()}];
let submitted=0;
const bodies=[];
const server=http.createServer(async(req,res)=>{
 const url=new URL(req.url,'http://localhost');
 const json=data=>{res.setHeader('Content-Type','application/json');res.end(JSON.stringify(data));};
 if(url.pathname==='/api/auth/config')return json({enabled:false});
 if(url.pathname==='/api/auth/identity')return json({enabled:false,key:'browser-test'});
 if(url.pathname==='/api/timeline')return json({entries});
 if(url.pathname==='/api/settings')return json({baseUrl:'https://example.com',lowModel:'sample',apiKey:'sample',active:[],speech:{}});
 if(url.pathname==='/api/personas')return json({personas:[{id:'tabe',name:'tabe',icon:'🍚',active:true,tier:'low',tools:[]}],tools:[]});
 if(url.pathname==='/api/social/bluesky/connection')return json({available:false});
 if(url.pathname==='/api/events'){res.writeHead(200,{'Content-Type':'text/event-stream'});res.write(': heartbeat\n\n');return;}
 if(url.pathname==='/api/post'){
  let text='';for await(const c of req)text+=c;
  const body=JSON.parse(text);bodies.push(body);let entry=entries.find(e=>e.id===body.clientId);
  if(!entry){submitted++;entry={id:body.clientId,root:body.clientId,kind:'post',speaker:'user',text:body.text,at:body.clientAt};entries.push(entry);}
  return json(entry);
 }
 try {const path=url.pathname==='/'?'/index.html':url.pathname;const data=await readFile(root+path);res.setHeader('Content-Type',path.endsWith('.js')?'text/javascript':path.endsWith('.css')?'text/css':path.endsWith('.png')?'image/png':path.endsWith('.svg')?'image/svg+xml':'text/html');res.end(data);}catch{res.writeHead(404);res.end();}
});
await new Promise(r=>server.listen(19222,'127.0.0.1',r));
const browser=spawn(process.env.CHROMIUM || 'chromium',['--headless','--no-sandbox','--disable-gpu','--no-proxy-server','--remote-debugging-port=19223',`--user-data-dir=${homedir()}/.cache/kakeratta-offline-test-${Date.now()}`,'about:blank'],{stdio:'ignore'});
const pause=ms=>new Promise(r=>setTimeout(r,ms));
let socket;
try{
 let tabs;for(let i=0;i<100;i++){try{tabs=await(await fetch('http://127.0.0.1:19223/json')).json();break;}catch{await pause(100);}}
 socket=new WebSocket(tabs.find(t=>t.type==='page').webSocketDebuggerUrl);await new Promise(r=>socket.addEventListener('open',r,{once:true}));
 let seq=0;const pending=new Map(),errors=[];
 socket.onmessage=e=>{const m=JSON.parse(e.data);if(m.id){const p=pending.get(m.id);pending.delete(m.id);m.error?p.reject(Error(JSON.stringify(m.error))):p.resolve(m.result);}if(m.method==='Runtime.exceptionThrown')errors.push(m.params.exceptionDetails.text+': '+m.params.exceptionDetails.exception?.description);};
 const cdp=(method,params={})=>new Promise((resolve,reject)=>{const id=++seq;pending.set(id,{resolve,reject});socket.send(JSON.stringify({id,method,params}));});
 const evaluate=async expression=>{const r=await cdp('Runtime.evaluate',{expression,awaitPromise:true,returnByValue:true});if(r.exceptionDetails)throw Error(JSON.stringify(r.exceptionDetails));return r.result.value;};
 const until=async expression=>{for(let i=0;i<100;i++){if(await evaluate(expression))return;await pause(100);}throw Error('Timed out: '+expression+'\n'+await evaluate('document.body.innerText')+'\n'+errors.join('\n'));};
 await cdp('Page.addScriptToEvaluateOnNewDocument',{source:"if(!localStorage.getItem('kakeratta.language')) localStorage.setItem('kakeratta.language','ja');"});
 await cdp('Runtime.enable');await cdp('Network.enable');await cdp('Page.enable');
 await cdp('Emulation.setDeviceMetricsOverride',{width:760,height:1000,deviceScaleFactor:1,mobile:false});
 await cdp('Page.navigate',{url:'http://127.0.0.1:19222/'});
 await until(`document.querySelector('.said')?.textContent.includes('喫茶店')`);
 await until('!!navigator.serviceWorker.controller');
 await evaluate(`document.querySelector('#sheet .close')?.click()`);
 await cdp('Network.emulateNetworkConditions',{offline:true,latency:0,downloadThroughput:0,uploadThroughput:0});
 await cdp('Network.overrideNetworkState',{offline:true,latency:0,downloadThroughput:0,uploadThroughput:0});
 await cdp('Page.reload');
 await until(`document.querySelector('.said')?.textContent.includes('喫茶店')`);
 await evaluate(`(()=>{const input=document.querySelector('.compose-text');input.value='帰り道に寄ってみたい。電波がなくてもメモできた。';input.dispatchEvent(new Event('input'));document.querySelector('.compose').dispatchEvent(new Event('submit',{cancelable:true}));})()`);
 await until(`document.body.innerText.includes('送信待ち 1件')`);
 assert.equal(submitted,0);
 await cdp('Page.reload');await until(`document.body.innerText.includes('送信待ち 1件') && document.body.innerText.includes('電波がなくても')`);
 await writeFile(`${captures}ja/offline.png`,Buffer.from((await cdp('Page.captureScreenshot',{format:'png'})).data,'base64'));
 await evaluate(`localStorage.setItem('kakeratta.language','en')`);await cdp('Page.reload');await until(`document.body.innerText.includes('1 waiting to send')`);
 await writeFile(`${captures}en/offline.png`,Buffer.from((await cdp('Page.captureScreenshot',{format:'png'})).data,'base64'));
 await cdp('Network.emulateNetworkConditions',{offline:false,latency:0,downloadThroughput:-1,uploadThroughput:-1});
 await cdp('Network.overrideNetworkState',{offline:false,latency:0,downloadThroughput:-1,uploadThroughput:-1});
 await until(`!document.body.innerText.includes('waiting to send') && document.body.innerText.includes('電波がなくても')`);
 assert.equal(submitted,1);
 await cdp('Page.reload');await until(`document.body.innerText.includes('電波がなくても')`);assert.equal(submitted,1);
 // Android share-target delivery must also survive without networking.
 await cdp('Network.emulateNetworkConditions',{offline:true,latency:0,downloadThroughput:0,uploadThroughput:0});
 await cdp('Network.overrideNetworkState',{offline:true,latency:0,downloadThroughput:0,uploadThroughput:0});
 await evaluate(`(async()=>{
   const form=new FormData();
   form.set('text','Offline shared image');
   const png=Uint8Array.from(atob('iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mP8/x8AAwMCAO+jRZkAAAAASUVORK5CYII='),c=>c.charCodeAt(0));
   form.set('image',new Blob([png],{type:'image/png'}),'sample.png');
   await fetch('/api/capture',{method:'POST',body:form});
   const {outbox}=await import('/src/api.js');
   return (await outbox.entries()).length;
 })()`);
 await cdp('Page.reload');await until(`document.body.innerText.includes('Offline shared image') && document.querySelector('.local-shot')?.src.startsWith('data:image/png;base64,')`);
 assert.equal(submitted,1);
 await cdp('Network.emulateNetworkConditions',{offline:false,latency:0,downloadThroughput:-1,uploadThroughput:-1});
 await cdp('Network.overrideNetworkState',{offline:false,latency:0,downloadThroughput:-1,uploadThroughput:-1});
 await until(`!document.body.innerText.includes('waiting to send') && document.body.innerText.includes('Offline shared image')`);
 assert.equal(submitted,2);
 assert.ok(bodies.at(-1).image.startsWith('data:image/png;base64,'));
 assert.deepEqual(errors,[]);
 console.log('PASS: cached boot offline, durable post after reload, reconnect sends once, offline image share survives reload, no browser exceptions');
}finally{socket?.close();browser.kill();server.closeAllConnections();server.close();}
