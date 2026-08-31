import assert from "node:assert/strict";
import test from "node:test";

import {
  CommandRegistry,
  filterCommands,
  isPaletteShortcut,
  navigationResultCommands,
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

test("navigation results become keyboard commands with stable routes", () => {
  const navigated = [];
  const commands = navigationResultCommands({
    issues: [{ id: "awb-a1b2c3", project: "awb", title: "Palette" }],
    projects: [{ key: "client-ui", name: "Client UI" }],
    users: [{ name: "mikael", full_name: "Mikael Ståldal" }],
  }, (href) => navigated.push(href));
  assert.deepEqual(commands.map(({ group }) => group), ["Issues", "Projects", "Users"]);
  assert.equal(commands[2].label, "Mikael Ståldal");
  assert.equal(commands[2].hint, "@mikael");
  commands.forEach(({ run }) => run());
  assert.deepEqual(navigated, ["#/issues/awb-a1b2c3", "#/projects/client-ui", "#/users?user=mikael"]);
});
