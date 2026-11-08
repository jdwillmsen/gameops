import test from "node:test";
import assert from "node:assert/strict";
import { GUIDE_PAGES, PEN_NOTES, STATION_NOTES } from "./guide.js";
import { PEN_SPECIES } from "./logic.js";

test("every guide page has a unique id, a title and a body", () => {
  const ids = GUIDE_PAGES.map((p) => p.id);
  assert.equal(new Set(ids).size, ids.length);
  for (const p of GUIDE_PAGES) {
    assert.ok(p.title.length > 0 && p.title.length <= 40, p.id);
    assert.ok(p.body.length > 50, p.id);
  }
});

test("every pen species has a note naming its taming food", () => {
  for (const p of PEN_SPECIES) {
    assert.ok(PEN_NOTES[p.id], p.id);
    assert.ok(PEN_NOTES[p.id].toLowerCase().includes(p.food.replace("_", " ")), `${p.id} note must name ${p.food}`);
  }
});

test("station notes exist for the non-pen lecterns", () => {
  for (const k of ["safety", "arena", "supply", "breeding", "armor"]) assert.ok(STATION_NOTES[k], k);
});
