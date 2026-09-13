import test from "node:test";
import assert from "node:assert/strict";
import { createTimelinePages } from "../frontend/src/timeline-pages.js";

const deferred = () => { let resolve, reject; const promise = new Promise((a,b) => { resolve=a; reject=b; }); return {promise,resolve,reject}; };

test("loads 20 posts at a time, merges threads, and stops at the end", async () => {
  const queries = [], changes = [], states = [];
  const pager = createTimelinePages({ fetchPage: async (q) => {
    queries.push(q);
    return q.before ? {entries:[{id:"old"}]} : {entries:[{id:"recent"},{id:"reply",root:"recent"}],next:"cursor"};
  }, onChange: e => changes.push(e), onState: s => states.push(s) });
  await pager.refresh();
  await pager.more();
  await pager.more();
  assert.deepEqual(queries,[{limit:20,before:null},{limit:20,before:"cursor"}]);
  assert.deepEqual(changes.at(-1).map(e=>e.id),["recent","reply","old"]);
  assert.equal(states.at(-1).hasMore,false);
});

test("in-flight live replies and deletions win over HTTP snapshots", async () => {
  const response = deferred();
  let latest;
  let calls=0;
  const pager=createTimelinePages({fetchPage:()=>{calls++;return response.promise;},onChange:e=>latest=e});
  const loading=pager.refresh();
  await Promise.resolve();
  const duplicate=pager.refresh();
  pager.upsert({id:"post",deleted:true});
  pager.upsert({id:"reply",root:"post"});
  response.resolve({entries:[{id:"post",deleted:false}],next:"older"});
  await Promise.all([loading,duplicate]);
  assert.equal(calls,1);
  assert.equal(latest.find(e=>e.id==="post").deleted,true);
  assert.ok(latest.find(e=>e.id==="reply"));
});

test("a failed next page can be retried without losing entries or advancing the cursor",async()=>{
  let latest,state,fail=true;
  const queries=[];
  const pager=createTimelinePages({fetchPage:async q=>{
    queries.push(q.before);
    if(!q.before)return {entries:[{id:"one"}],next:"older"};
    if(fail)throw Error("offline");
    return {entries:[{id:"two"}]};
  },onChange:e=>latest=e,onState:s=>state=s});
  await pager.refresh();
  await pager.more();
  assert.equal(state.error.message,"offline");
  assert.deepEqual(latest,[{id:"one"}]);
  fail=false;
  await pager.more();
  assert.deepEqual(queries,[null,"older","older"]);
  assert.equal(latest.length,2);
});

test("reconnect refreshes only the pages already loaded",async()=>{
  const queries=[];
  const pager=createTimelinePages({fetchPage:async q=>{
    queries.push(q.before);
    return q.before?{entries:[{id:"two"}],next:"even-older"}:{entries:[{id:"one"}],next:"older"};
  },onChange:()=>{}});
  await pager.refresh();
  await pager.more();
  await pager.refresh();
  assert.deepEqual(queries,[null,"older",null,"older"]);
});
