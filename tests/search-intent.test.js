import test from 'node:test';
import assert from 'node:assert/strict';
import { initialSearchMode } from '../frontend/src/search-intent.js';

test('short names and keyword lists use keyword search without an AI decision',()=>{
  for(const query of ['サザエ','さざえ','ｻｻﾞｴ','ピノ・ノワール','赤ワイン 軽い','サザエのつぼ焼き','シイタケ','Pinot Noir','Qwen3-Embedding-8B']) assert.equal(initialSearchMode(query),'keyword',query);
});
test('questions, requests, and clear sentences start with meaning search',()=>{
  for(const query of ['サザエ？','サザエ?','サザエについて教えて','おすすめをおしえて','ワインを探して','あの店をさがして','どこで食べた','どんな味だった','昨日食べた貝','軽くて飲みやすかった赤ワイン','What wine did I enjoy','find a light red wine','A light red wine I enjoyed']) assert.equal(initialSearchMode(query),'hybrid',query);
});
