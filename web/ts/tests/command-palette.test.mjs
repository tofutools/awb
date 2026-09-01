import assert from "node:assert/strict";
import test from "node:test";

import {
  CommandRegistry,
  filterCommands,
  isPlainPaletteBoundaryKey,
  isPaletteShortcut,
  navigationResultCommands,
  nextPaletteSelection,
  paletteShortcutHint,
  paletteTrigger,
  visiblePalettePageSize,
} from "../../static/command-palette.js";

const command = (id, label, keywords = "") => ({
  id, label, keywords, hint: "View", group: "Navigation", run() {},
});

test("command providers are extensible and can be unregistered", () => {
  const registry = new CommandRegistry();
  registry.register("core", () => [command("ready", "Go to Ready")]);
  const unregister = registry.register("feature", () => [command("roadmap", "Go to Roadmap")]);
  assert.deepEqual(registry.commands().map(({ id }) => id), ["ready", "roadmap"]);
  unregister();
  assert.deepEqual(registry.commands().map(({ id }) => id), ["ready"]);
});

test("commands match labels, hints, and keywords across every query term", () => {
  const commands = [
    command("ready", "Go to Ready", "board unassigned"),
    command("users", "Go to Users", "people accounts"),
  ];
  assert.deepEqual(filterCommands(commands, "ready board").map(({ id }) => id), ["ready"]);
  assert.deepEqual(filterCommands(commands, "ACCOUNT").map(({ id }) => id), ["users"]);
});

test("Ctrl-K off macOS and Cmd-K on macOS are claimed without accepting other chords", () => {
  const event = (overrides = {}) => ({
    key: "k", ctrlKey: false, metaKey: false, altKey: false, shiftKey: false,
    repeat: false, isComposing: false, ...overrides,
  });
  assert.equal(isPaletteShortcut(event({ ctrlKey: true }), false), true);
  assert.equal(isPaletteShortcut(event({ metaKey: true }), false), false);
  assert.equal(isPaletteShortcut(event({ metaKey: true, key: "K" }), true), true);
  assert.equal(isPaletteShortcut(event({ ctrlKey: true }), true), false);
  assert.equal(isPaletteShortcut(event({ ctrlKey: true, shiftKey: true }), false), false);
  assert.equal(isPaletteShortcut(event({ ctrlKey: true, repeat: true }), false), false);
  assert.equal(isPaletteShortcut(event({ metaKey: true, isComposing: true }), true), false);
});

test("the header trigger advertises the palette shortcut accessibly on each platform", () => {
  assert.equal(paletteTrigger.label, "Commands");
  assert.equal(paletteTrigger.title, "Open command palette (Ctrl/Cmd+K)");
  assert.equal(paletteTrigger.keyShortcuts, "Control+K Meta+K");
  assert.equal(paletteShortcutHint(false), "Ctrl K");
  assert.equal(paletteShortcutHint(true), "⌘K");
});

test("navigation results become keyboard commands with stable routes", () => {
  const navigated = [];
  const commands = navigationResultCommands({
    issues: [{ id: "awb-a1b2c3", workspace: "awb", title: "Palette" }],
    workspaces: [{ key: "client-ui", name: "Client UI" }],
    users: [{ name: "mikael", full_name: "Mikael Ståldal" }],
  }, (href) => navigated.push(href));
  assert.deepEqual(commands.map(({ group }) => group), ["Issues", "Workspaces", "Users"]);
  assert.equal(commands[2].label, "Mikael Ståldal");
  assert.equal(commands[2].hint, "@mikael");
  commands.forEach(({ run }) => run());
  assert.deepEqual(navigated, ["#/issues/awb-a1b2c3", "#/workspaces/client-ui", "#/users?user=mikael"]);
});

test("page and boundary keys clamp across grouped static and dynamic results", () => {
  const commands = [
    command("ready", "Go to Ready"),
    ...navigationResultCommands({
      issues: [
        { id: "awb-a1b2c3", workspace: "awb", title: "Palette" },
        { id: "awb-d4e5f6", workspace: "awb", title: "Keyboard" },
      ],
      workspaces: [{ key: "client-ui", name: "Client UI" }],
      users: [{ name: "mikael", full_name: "Mikael Ståldal" }],
    }, () => {}),
    command("users", "Go to Users"),
  ];
  assert.deepEqual(commands.map(({ group }) => group),
    ["Navigation", "Issues", "Issues", "Workspaces", "Users", "Navigation"]);

  assert.equal(nextPaletteSelection("PageDown", 0, commands.length, 3), 3);
  assert.equal(nextPaletteSelection("PageDown", 3, commands.length, 3), 5);
  assert.equal(nextPaletteSelection("PageDown", 5, commands.length, 3), 5);
  assert.equal(nextPaletteSelection("PageUp", 5, commands.length, 3), 2);
  assert.equal(nextPaletteSelection("PageUp", 2, commands.length, 3), 0);
  assert.equal(nextPaletteSelection("PageUp", 0, commands.length, 3), 0);
  assert.equal(nextPaletteSelection("Home", 4, commands.length, 3), 0);
  assert.equal(nextPaletteSelection("End", 1, commands.length, 3), 5);
});

test("page size comes from option positions in the grouped result viewport", () => {
  // A group heading occupies the gap between the third and fourth options.
  // Dividing the 120px viewport by the 40px row height would overcount it.
  const options = [20, 60, 100, 160, 200].map((offsetTop) => ({ offsetTop }));
  const list = { clientHeight: 120, querySelectorAll: () => options };
  assert.equal(visiblePalettePageSize(list, 0, 1), 2);
  assert.equal(visiblePalettePageSize(list, 4, -1), 2);
  assert.equal(visiblePalettePageSize({ clientHeight: 0, querySelectorAll: () => [] }, 0, 1), 1);
});

test("navigation uses the refreshed backend result count", () => {
  const beforeResponseCount = 2;
  assert.equal(nextPaletteSelection("End", 0, beforeResponseCount, 4), 1);

  // A ready backend response resets selection before appending its issue,
  // workspace and user groups. Subsequent keys use that freshly rendered count.
  const refreshedCount = beforeResponseCount + 3;
  const resetSelection = 0;
  assert.equal(nextPaletteSelection("PageDown", resetSelection, refreshedCount, 4), 4);
  assert.equal(nextPaletteSelection("End", resetSelection, refreshedCount, 4), 4);
});

test("arrow navigation keeps its wrapping behaviour", () => {
  assert.equal(nextPaletteSelection("ArrowUp", 0, 4, 2), 3);
  assert.equal(nextPaletteSelection("ArrowDown", 3, 4, 2), 0);
});

test("Home and End are claimed only without modifiers", () => {
  const event = (key, overrides = {}) => ({
    key, ctrlKey: false, metaKey: false, altKey: false, shiftKey: false, ...overrides,
  });
  assert.equal(isPlainPaletteBoundaryKey(event("Home")), true);
  assert.equal(isPlainPaletteBoundaryKey(event("End")), true);
  assert.equal(isPlainPaletteBoundaryKey(event("Home", { ctrlKey: true })), false);
  assert.equal(isPlainPaletteBoundaryKey(event("End", { metaKey: true })), false);
  assert.equal(isPlainPaletteBoundaryKey(event("Home", { shiftKey: true })), false);
});
