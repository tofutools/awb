import assert from "node:assert/strict";
import test from "node:test";

import { inspectorPopoverPosition, inspectorStatusAction } from "../../static/inspector.js";

test("the native status control dispatches domain transitions", () => {
  assert.equal(inspectorStatusAction("open", "open"), "none");
  assert.equal(inspectorStatusAction("open", "in_progress"), "claim");
  assert.equal(inspectorStatusAction("in_progress", "open"), "release");
  assert.equal(inspectorStatusAction("in_progress", "closed"), "close");
  assert.equal(inspectorStatusAction("closed", "open"), "reopen");
  assert.equal(inspectorStatusAction("closed", "in_progress"), "claim");
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
