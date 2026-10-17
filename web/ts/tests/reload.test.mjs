import assert from "node:assert/strict";
import test from "node:test";

import { onReload, requestReload } from "../../static/reload.js";

test("a reload request refetches every listening resource and waits for all of them", async () => {
  const target = new EventTarget();
  const finished = [];
  let release;
  const slow = new Promise((resolve) => (release = resolve));
  onReload(target, async () => {
    await slow;
    finished.push("slow");
  });
  onReload(target, async () => {
    finished.push("fast");
  });

  let done = false;
  const request = requestReload(target).then(() => (done = true));
  await Promise.resolve();
  assert.deepEqual(finished, ["fast"]);
  assert.equal(done, false);

  release();
  await request;
  assert.deepEqual(finished, ["fast", "slow"]);
});

test("a failed resource does not fail the request and a removed listener is not called", async () => {
  const target = new EventTarget();
  let calls = 0;
  onReload(target, async () => {
    throw new Error("unavailable");
  });
  const stop = onReload(target, async () => calls++);
  stop();

  await requestReload(target);
  assert.equal(calls, 0);
});
