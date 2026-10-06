import assert from "node:assert/strict";
import test from "node:test";
import { capability, toolLabel, uniqueTools } from "../frontend/src/tool_labels.js";

test("a qualified tool is named by the capability it shares with the built-in one", () => {
  assert.equal(capability("place_search"), "place_search");
  assert.equal(capability("geo.place_search"), "place_search");
  assert.equal(toolLabel("geo.place_search"), "お店や場所を探す");
  assert.equal(toolLabel("weather_forecast"), "天気予報を調べる");
  assert.equal(toolLabel("geo.commute"), "geo.commute");
});

test("a server twin is folded into the built-in name beside it", () => {
  assert.deepEqual(
    uniqueTools(["web_search", "place_search", "schedule", "geo.place_search"]),
    ["web_search", "place_search", "schedule"],
  );
  // A tool only a server offers keeps its qualified name.
  assert.deepEqual(uniqueTools(["geo.place_search"]), ["geo.place_search"]);
  // Two servers offering the same capability stay apart, since neither is built in.
  assert.deepEqual(uniqueTools(["a.place_search", "b.place_search"]), ["a.place_search", "b.place_search"]);
  assert.deepEqual(uniqueTools(["route", "place_search"]), ["place_search"]);
  assert.deepEqual(uniqueTools([]), []);
});
