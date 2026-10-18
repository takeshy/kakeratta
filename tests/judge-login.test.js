import test from "node:test";
import assert from "node:assert/strict";
import { initializeJudgeLanguage, judgeInviteToken, showJudgeLogin } from "../frontend/src/judge-login.js";
import { getLanguage, setLanguage } from "../frontend/src/i18n.js";
import { verifyIdentity } from "../frontend/src/auth.js";

class Element {
  children = []; listeners = {}; textContent = ""; value = "";
  constructor(tag) { this.tag = tag; }
  append(...nodes) { for (const node of nodes) { node.parent = this; this.children.push(node); } }
  get firstChild() { return this.children[0]; }
  remove() { this.parent.children.splice(this.parent.children.indexOf(this), 1); }
  setAttribute(key, value) { this[key] = value; }
  addEventListener(key, fn) { this.listeners[key] = fn; }
}
const walk = node => [node, ...node.children.flatMap(walk)];
const tick = () => new Promise(resolve => setImmediate(resolve));
const deferred = () => { let resolve, reject; const promise = new Promise((a,b) => { resolve=a; reject=b; }); return { promise, resolve, reject }; };
function environment(t) {
  const root = new Element("root"), values = new Map();
  for (const [key,value] of Object.entries({
    Node:Element,
    document:{ querySelector:() => root, createElement:tag => new Element(tag), createTextNode:text => Object.assign(new Element("text"),{textContent:text}) },
    localStorage:{getItem:key => values.get(key) ?? null,setItem:(key,value) => values.set(key,value),removeItem:key => values.delete(key)},
    navigator:{languages:["ja-JP"]},
  })) {
    const previous = Object.getOwnPropertyDescriptor(globalThis,key);
    Object.defineProperty(globalThis,key,{configurable:true,writable:true,value});
    t.after(() => { if (previous) Object.defineProperty(globalThis,key,previous); else delete globalThis[key]; });
  }
  return {root,values};
}

test("a fresh judge browser starts in English and keeps the choice after navigation", t => {
  const {values} = environment(t);
  setLanguage("ja");
  initializeJudgeLanguage("/judge/invitation");
  assert.equal(getLanguage(),"en");
  assert.equal(values.get("kakeratta.language"),"en");
  initializeJudgeLanguage("/judge/invitation","?lang=ja");
  assert.equal(getLanguage(),"ja");
  initializeJudgeLanguage("/judge/invitation");
  assert.equal(getLanguage(),"ja","retain an explicitly saved choice");
  setLanguage("en");
  initializeJudgeLanguage("/");
  assert.equal(getLanguage(),"en");
  assert.equal(judgeInviteToken("/judge/invitation"),"invitation");
});

test("judge registration cannot race another registration or sign-in while the workspace is prepared", async t => {
  const {root} = environment(t);
  setLanguage("en");
  const provider=deferred(),workspace=deferred();
  let registrations=0,logins=0,finished;
  showJudgeLogin({register:() => { registrations++; return provider.promise; },signIn:() => { logins++; },finish:user => { finished=user; return workspace.promise; }});
  const form=root.firstChild;
  const [email,password]=walk(form).filter(n=>n.tag==="input");
  email.value="judge@example.com";password.value="password";
  const [register,login]=walk(form).filter(n=>n.tag==="button");
  form.listeners.submit({preventDefault(){}});
  form.listeners.submit({preventDefault(){}});
  login.listeners.click();
  assert.equal(registrations,1);assert.equal(logins,0);
  assert.equal(register.disabled,true);assert.equal(login.disabled,true);
  const user={uid:"judge"};provider.resolve({user});await tick();
  assert.equal(finished,user);
  assert.ok(walk(form).some(n=>n.textContent==="Preparing your workspace…"));
  assert.equal(walk(form).find(n=>n.role==="alert").textContent,"");
  workspace.resolve();await tick();
  assert.equal(form["aria-busy"],"true");assert.equal(register.disabled,true);
});

test("failed judge setup preserves credentials and permits sign-in to the created account", async t => {
  const {root}=environment(t);
  let fail=true,logins=0;
  showJudgeLogin({register:async()=>({user:{}}),signIn:async()=>{logins++;return{user:{}};},finish:async()=>{if(fail)throw Error("workspace unavailable");}});
  const form=root.firstChild;
  const email=walk(form).find(n=>n.type==="email");email.value="judge@example.com";
  form.listeners.submit({preventDefault(){}});await tick();
  assert.equal(walk(form).find(n=>n.role==="alert").textContent,"workspace unavailable");
  assert.equal(email.value,"judge@example.com");
  const login=walk(form).find(n=>n.type==="button");
  assert.equal(login.disabled,false);
  fail=false;login.listeners.click();await tick();
  assert.equal(logins,1);
  assert.equal(walk(form).find(n=>n.role==="alert").textContent,"");
});

test("intentional judge sign-in switches cached identity before navigation without an Account changed error", async t => {
  const {values}=environment(t);
  values.set("kakeratta.localIdentity",JSON.stringify({key:"previous",enabled:true}));
  let reloads=0;
  const previous=globalThis.location;
  globalThis.location={reload(){reloads++;}};
  t.after(()=>{globalThis.location=previous;});
  t.mock.method(globalThis,"fetch",async()=>new Response(JSON.stringify({key:"judge",enabled:true})));
  const result=await verifyIdentity(true,{reloadOnChange:false});
  assert.equal(result.key,"judge");
  assert.equal(JSON.parse(values.get("kakeratta.localIdentity")).key,"judge");
  assert.equal(reloads,0);
});
