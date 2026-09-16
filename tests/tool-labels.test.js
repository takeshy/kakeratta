import assert from "node:assert/strict";
import test from "node:test";
import { capability, toolLabel, uniqueTools } from "../frontend/src/tool_labels.js";

test("a qualified tool is named by the capability it shares with the built-in one", () => {
  assert.equal(capability("place_search"), "place_search");
  assert.equal(capability("geo.place_search"), "place_search");
  assert.equal(toolLabel("geo.place_search"), "お店や場所を探す");
  assert.equal(toolLabel("route"), "行き方を調べる");
  assert.equal(toolLabel("geo.commute"), "geo.commute");
});

test("a server twin is folded into the built-in name beside it", () => {
  assert.deepEqual(
    uniqueTools(["web_search", "place_search", "route", "schedule", "geo.place_search", "geo.route"]),
    ["web_search", "place_search", "route", "schedule"],
  );
  // A tool only a server offers keeps its qualified name.
  assert.deepEqual(uniqueTools(["geo.place_search"]), ["geo.place_search"]);
  // Two servers offering the same capability stay apart, since neither is built in.
  assert.deepEqual(uniqueTools(["a.place_search", "b.place_search"]), ["a.place_search", "b.place_search"]);
  assert.deepEqual(uniqueTools([]), []);
});
