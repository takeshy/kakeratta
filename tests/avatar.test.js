import test from "node:test";
import assert from "node:assert/strict";
import { avatarNode, avatarSource, compressAvatar, createAvatarField, userProfile } from "../frontend/src/avatar.js";
import { t as translate } from "../frontend/src/i18n.js";

const photo = "data:image/jpeg;base64,YWJj";
class Element {
  children = []; listeners = {}; value = ""; textContent = "";
  constructor(tag) { this.tag = tag; }
  append(...children) { for (const child of children) { child.parent = this; this.children.push(child); } }
  get firstChild() { return this.children[0]; }
  remove() { if (this.parent) this.parent.children.splice(this.parent.children.indexOf(this), 1); }
  setAttribute(key, value) { this[key] = value === "" ? true : value; }
  addEventListener(name, listener) { this.listeners[name] = listener; }
}
function dom(t) {
  globalThis.Node = Element;
  globalThis.document = { createElement: tag => new Element(tag), createTextNode: text => Object.assign(new Element("text"), { textContent: text }) };
  t.after(() => { delete globalThis.Node; delete globalThis.document; });
}
const walk = node => [node, ...node.children.flatMap(walk)];

test("profile uses defaults and avatar rendering accepts only bounded embedded JPEGs", t => {
  dom(t);
  assert.equal(userProfile({}).name, translate("あなた"));
  assert.deepEqual(userProfile({ profile: { name: "佐藤", icon: "🌸", avatar: photo } }), { name: "佐藤", icon: "🌸", avatar: photo });
  assert.equal(avatarNode({ icon: "🌸" }).textContent, "🌸");
  assert.equal(avatarNode({ avatar: photo }).children[0].src, photo);
  for (const value of ["https://example.com/a.jpg", "data:image/svg+xml;base64,PHN2Zy8+", "data:image/jpeg;base64,!", photo + "A".repeat(50000)]) assert.equal(avatarSource(value), "");
});

test("portrait photo is cropped square, reduced to 128px and recompressed to the size limit", async t => {
  const draws = [], qualities = [], revoked = [];
  globalThis.Image = class { naturalWidth = 200; naturalHeight = 400; async decode() {} };
  globalThis.document = { createElement: tag => {
    assert.equal(tag, "canvas");
    const canvas = { getContext: () => ({ fillRect() {}, drawImage: (...args) => draws.push(args.slice(1)) }), toDataURL: (type, quality) => {
      assert.equal(canvas.width, 128); assert.equal(canvas.height, 128); assert.equal(type, "image/jpeg");
      qualities.push(quality);
      return qualities.length === 1 ? "data:image/jpeg;base64," + "A".repeat(50000) : photo;
    } };
    return canvas;
  } };
  t.mock.method(URL, "createObjectURL", () => "blob:photo");
  t.mock.method(URL, "revokeObjectURL", value => revoked.push(value));
  t.after(() => { delete globalThis.Image; delete globalThis.document; });
  assert.equal(await compressAvatar({ size: 2000000 }), photo);
  assert.deepEqual(draws, [[0, 100, 200, 200, 0, 0, 128, 128]]);
  assert.deepEqual(qualities, [0.82, 0.65]);
  assert.deepEqual(revoked, ["blob:photo"]);
  await assert.rejects(compressAvatar({ size: 21 * 1024 * 1024 }));
  assert.equal(URL.createObjectURL.mock.callCount(), 1, "oversized input is refused before decoding");
});

test("photo selection saves the compressed result, and a failed removal keeps the saved image", async t => {
  dom(t);
  globalThis.Image = class { naturalWidth = 300; naturalHeight = 300; async decode() {} };
  t.after(() => { delete globalThis.Image; });
  const create = document.createElement;
  document.createElement = tag => tag === "canvas" ? { getContext: () => ({ fillRect() {}, drawImage() {} }), toDataURL: () => photo } : create(tag);
  t.mock.method(URL, "createObjectURL", () => "blob:photo");
  t.mock.method(URL, "revokeObjectURL", () => {});
  const saved = [];
  let fail = false;
  const field = createAvatarField({ icon: "🌸", onChange: next => { if (fail) return false; saved.push(next); } });
  const file = walk(field.element).find(node => node.type === "file");
  file.files = [{ size: 5000000 }];
  await file.listeners.change();
  assert.deepEqual(saved, [{ icon: "🌸", avatar: photo }]);
  assert.equal(field.value().avatar, photo);
  fail = true;
  await walk(field.element).find(node => node.textContent === translate("写真を外す")).listeners.click();
  assert.equal(field.value().avatar, photo);
  assert.equal(field.busy(), false);
  assert.equal(field.input.disabled, false);
  fail = false;
  field.input.value = "🙂";
  await field.input.listeners.change();
  assert.deepEqual(field.value(), { icon: "🙂", avatar: "" });
});
