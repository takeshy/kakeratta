import test from 'node:test';
import assert from 'node:assert/strict';
import { createOutbox } from '../frontend/src/outbox.js';
import { recentEntries } from '../frontend/src/local-store.js';
import { setLanguage } from '../frontend/src/i18n.js';
function setup(send) {
  const db = new Map(); let user = 'alice', online = false;
  const record = async (key, value, owner = user) => { const k = `${owner}:${key}`; if (value === undefined) return structuredClone(db.get(k)); if (value === null) db.delete(k); else db.set(k, structuredClone(value)); };
  const options = { send, verify: async () => {}, record, list: async prefix => [...db].filter(([k]) => k.startsWith(`${user}:${prefix}`)).map(([,v]) => structuredClone(v)), identity: () => ({ key: user }), online: () => online };
  return { box: createOutbox(options), restart: () => createOutbox(options), db, user: v => { user=v; }, online: v => { online=v; }, options };
}
test('offline text and image survive restart; parent is sent before its reply', async () => {
  const sent=[];
  const s=setup(async (path,body) => { sent.push({path,body}); return {id:body.clientId}; });
  const root=await s.box.enqueue('/api/post',{text:'note',image:'data:image/png;base64,YQ=='});
  await s.box.enqueue('/api/reply',{text:'reply',replyTo:root.id},root);
  assert.equal(sent.length,0);
  s.online(true); await s.restart().flush();
  assert.equal(sent.length,2);
  assert.equal(sent[1].body.replyTo,sent[0].body.clientId);
  assert.equal(sent[0].body.image,'data:image/png;base64,YQ==');
  assert.equal((await s.box.entries()).length,0);
});
test('display language is captured when queued and survives a language change before delivery', async t => {
  t.after(() => setLanguage('en'));
  const sent = [];
  const s = setup(async (path, body) => { sent.push(body); return { id: body.clientId }; });
  setLanguage('ja');
  const entry = await s.box.enqueue('/api/post', { text: 'infographic please' });
  assert.equal(entry.language, 'ja');
  setLanguage('en');
  await s.box.enqueue('/api/reply', { text: 'next', replyTo: entry.id }, entry);
  s.online(true);
  await s.restart().flush();
  assert.deepEqual(sent.map(body => body.language), ['ja', 'en']);
});
test('lost response retries the identical request ID and body; another account cannot send it',async()=>{
 const sent=[];let fail=true;
 const s=setup(async(path,body)=>{sent.push(body);if(fail)throw Error('lost response');return {id:body.clientId};});
 await s.box.enqueue('/api/post',{text:'once'});s.online(true);await s.box.flush();
 s.user('bob');await s.box.flush();assert.equal(sent.length,1);
 s.user('alice');fail=false;await s.restart().flush();assert.deepEqual(sent[0],sent[1]);
});
test('storage failure retains the composer input by rejecting enqueue; verification failure never sends',async()=>{
 let calls=0;const s=setup(async()=>{calls++;});
 const broken=createOutbox({...s.options,record:async()=>{throw Error('quota');}});
 await assert.rejects(broken.enqueue('/api/post',{text:'keep'}),/quota/);
 await s.box.enqueue('/api/post',{text:'keep'});s.online(true);
 await createOutbox({...s.options,verify:async()=>{throw Error('401');}}).flush();assert.equal(calls,0);
});
test('validation failure remains visible and does not auto retry',async()=>{
 let calls=0;const s=setup(async()=>{calls++;throw Object.assign(Error('too large'),{status:400});});
 await s.box.enqueue('/api/post',{text:'note'});s.online(true);await s.box.flush();await s.box.flush();
 assert.equal(calls,1);assert.equal((await s.box.entries())[0].entry.syncError,'too large');
});
test('cache limits roots while retaining their replies and dropping deleted threads',()=>{
 const entries=[{id:'a',kind:'post',at:'2020-01-01'},{id:'b',kind:'post',at:'2021-01-01'},{id:'r',kind:'reply',root:'b'},{id:'c',kind:'post',deleted:true,at:'2022-01-01'}];
 assert.deepEqual(recentEntries(entries,1).map(e=>e.id),['b','r']);
});

test('the default snapshot contains 20 roots, without counting their replies',()=>{
 const entries=Array.from({length:25},(_,i)=>({id:String(i),kind:'post',at:new Date(2020,0,i+1).toISOString()}));
 entries.push({id:'reply',kind:'reply',root:'24'});
 const snapshot=recentEntries(entries);
 assert.equal(snapshot.filter(e=>e.kind==='post').length,20);
 assert.ok(snapshot.some(e=>e.id==='reply'));
});
test('two tabs flushing one outbox send each queued post once',async()=>{
 const sent=[];
 const s=setup(async(path,body)=>{sent.push(body.clientId);await new Promise(r=>setTimeout(r,5));return {id:body.clientId};});
 let held=Promise.resolve();
 const lock=(name,work)=>{const run=held.then(work);held=run.catch(()=>{});return run;};
 await s.box.enqueue('/api/post',{text:'one'});await s.box.enqueue('/api/post',{text:'two'});
 s.online(true);
 const tabA=createOutbox({...s.options,lock}),tabB=createOutbox({...s.options,lock});
 await Promise.all([tabA.flush(),tabB.flush()]);
 assert.equal(sent.length,2);assert.equal(new Set(sent).size,2);
});

test('unreviewed Gmail is excluded from persistent device timeline snapshots',()=>{
 const pending={id:'pending-mail',kind:'post',text:'private pending mail',import:{type:'gmail',status:'pending'},at:'2026-10-19'};
 const accepted={id:'accepted-mail',kind:'post',import:{type:'gmail',status:'accepted'},at:'2026-10-18'};
 assert.deepEqual(recentEntries([pending,accepted]).map(e=>e.id),['accepted-mail']);
});
