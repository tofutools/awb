import assert from "node:assert/strict";
import test from "node:test";

import {
  deferInspectorPopoverOpen,
  inspectorParent,
  inspectorPopoverPosition,
  inspectorStatusAction,
} from "../../static/inspector.js";

test("the parent field uses only the outgoing parent relation", () => {
  assert.equal(inspectorParent([
    { type: "has-parent", other: "awb-child", direction: "in" },
    { type: "blocked-by", other: "awb-blocker", direction: "out" },
    { type: "has-parent", other: "awb-parent", direction: "out" },
  ]), "awb-parent");
  assert.equal(inspectorParent([]), undefined);
});

test("the native status control dispatches domain transitions", () => {
  assert.equal(inspectorStatusAction("open", "open"), "none");
  assert.equal(inspectorStatusAction("open", "in_progress"), "claim");
  assert.equal(inspectorStatusAction("in_progress", "open"), "release");
  assert.equal(inspectorStatusAction("in_progress", "closed"), "close");
  assert.equal(inspectorStatusAction("closed", "open"), "reopen");
  assert.equal(inspectorStatusAction("closed", "in_progress"), "claim");
});

test("the close editor waits for the native select activation to finish", async () => {
  let opened = false;
  deferInspectorPopoverOpen(() => {
    opened = true;
  });
  assert.equal(opened, false);
  await new Promise((resolve) => setTimeout(resolve));
  assert.equal(opened, true);
});

test("an inspector popover stays in the viewport and flips above its trigger", () => {
  assert.deepEqual(inspectorPopoverPosition(
    { top: 40, right: 300, bottom: 70, width: 100, height: 30 },
    { width: 220, height: 140 },
    { width: 320, height: 500 },
  ), { left: 80, top: 76 });
  assert.deepEqual(inspectorPopoverPosition(
    { top: 420, right: 390, bottom: 450, width: 100, height: 30 },
    { width: 250, height: 160 },
    { width: 400, height: 480 },
  ), { left: 140, top: 254 });
  assert.deepEqual(inspectorPopoverPosition(
    { top: 20, right: 120, bottom: 50, width: 100, height: 30 },
    { width: 300, height: 100 },
    { width: 320, height: 300 },
  ), { left: 8, top: 56 });
  assert.deepEqual(inspectorPopoverPosition(
    { top: 180, right: 560, bottom: 210, width: 100, height: 30 },
    { width: 240, height: 180 },
    { left: 200, top: 100, width: 360, height: 320 },
  ), { left: 312, top: 216 });
});
