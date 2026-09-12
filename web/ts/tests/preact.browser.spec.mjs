const { expect, test } = await import(
  process.env.PLAYWRIGHT_TEST_MODULE ?? "playwright/test"
);
const baseURL = process.env.AWB_BROWSER_BASE_URL;

test.beforeEach(async ({ page }) => {
  test.skip(
    !baseURL,
    "set AWB_BROWSER_BASE_URL to a disposable awb serve instance",
  );
  await page.setViewportSize({ width: 1440, height: 900 });
});

async function fixture(page, suffix) {
  const key = `p${Date.now().toString(36)}${suffix}`;
  const response = await page.request.post(`${baseURL}/api/workspaces`, {
    data: { key, name: "Preact browser checks" },
  });
  expect(response.ok(), await response.text()).toBe(true);
  return key;
}

for (const width of [1440, 390]) {
  test(`command palette opens without clipping at ${width}px`, async ({ page }) => {
    await page.setViewportSize({ width, height: 900 });
    await page.goto(baseURL);
    const trigger = page.getByRole("button", { name: /^Commands/ });
    await trigger.click();
    const dialog = page.getByRole("dialog", { name: "Commands", exact: true });
    const search = dialog.getByRole("combobox", { name: "Search commands" });
    await expect(search).toBeFocused();

    // Visibility alone does not detect a dialog clipped to a header control's height.
    const assertUnclipped = async () => {
      expect(await dialog.evaluate((node) => node.scrollHeight <= node.clientHeight)).toBe(true);
      await expect(search).toBeInViewport();
      await expect(dialog.getByRole("option").last()).toBeInViewport();
      await expect(dialog.locator("footer")).toBeInViewport();
    };
    await assertUnclipped();
    const options = dialog.getByRole("option");
    const highlight = await options.first().evaluate((node) => getComputedStyle(node).backgroundColor);
    expect(highlight).not.toBe("rgba(0, 0, 0, 0)");
    await page.keyboard.press("ArrowDown");
    await expect(options.nth(1)).toHaveAttribute("aria-selected", "true");
    await expect(options.nth(1)).toHaveCSS("background-color", highlight);
    await page.keyboard.press("Escape");
    await expect(dialog).toHaveCount(0);
    await expect(trigger).toBeFocused();

    await trigger.click();
    await page.mouse.click(2, 2);
    await expect(dialog).toHaveCount(0);
    await expect(trigger).toBeFocused();

    await page.keyboard.press("Control+k");
    await expect(search).toBeFocused();
    await assertUnclipped();
    await search.fill("boards");
    await page.keyboard.press("Enter");
    await expect(dialog).toHaveCount(0);
    await expect(page).toHaveURL(/#\/boards$/);
  });
}

test("command palette keeps search and shortcuts visible while results scroll", async ({ page }) => {
  await page.setViewportSize({ width: 700, height: 400 });
  await page.goto(baseURL);
  await page.getByRole("button", { name: /^Commands/ }).click();
  const dialog = page.getByRole("dialog", { name: "Commands", exact: true });
  const search = dialog.getByRole("combobox", { name: "Search commands" });
  const list = dialog.getByRole("listbox");
  expect(await list.evaluate((node) => node.scrollHeight > node.clientHeight)).toBe(true);
  await page.keyboard.press("End");
  await expect(dialog.getByRole("option").last()).toHaveAttribute("aria-selected", "true");
  await expect(dialog.getByRole("option").last()).toBeInViewport();
  await expect(search).toBeInViewport();
  await expect(dialog.locator("footer")).toBeInViewport();
  expect(await dialog.evaluate((node) => node.scrollHeight <= node.clientHeight)).toBe(true);
});
async function createIssue(page, workspace, title, extra = {}) {
  const response = await page.request.post(`${baseURL}/api/issues`, {
    data: { workspace, title, ...extra },
  });
  expect(response.ok(), await response.text()).toBe(true);
  return response.json();
}

async function closeIssue(page, issue) {
  const current = await page.request.get(`${baseURL}/api/issues/${issue.id}`);
  expect(current.ok(), await current.text()).toBe(true);
  const etag = current.headers().etag;
  expect(etag).toBeTruthy();
  const response = await page.request.post(
    `${baseURL}/api/issues/${issue.id}/close`,
    { data: {}, headers: { "If-Match": etag } },
  );
  expect(response.ok(), await response.text()).toBe(true);
}

test("the root opens Issues first and the status view can show closed work", async ({
  page,
}) => {
  const workspace = await fixture(page, "s");
  const open = await createIssue(page, workspace, "Open status target");
  const closed = await createIssue(page, workspace, "Closed status target");
  await closeIssue(page, closed);

  await page.goto(baseURL);
  await expect(page.getByRole("heading", { name: "Issues", exact: true })).toBeVisible();
  const tabs = page.locator(".app-header nav a");
  await expect(tabs.first()).toHaveText("Issues");
  await expect(tabs.first()).toHaveClass(/active/);

  await page.goto(`${baseURL}/#/issues?workspace=${workspace}`);
  await expect(page.getByRole("link", { name: new RegExp(open.id) })).toBeVisible();
  await expect(page.getByRole("link", { name: new RegExp(closed.id) })).toHaveCount(0);
  await page.getByRole("button", { name: "Configure issue view" }).click();
  const view = page.getByRole("dialog", { name: "Issue view options" });
  await view.getByRole("checkbox", { name: "Closed", exact: true }).check();
  await view.getByRole("button", { name: "Done", exact: true }).click();
  await expect(page.getByRole("link", { name: new RegExp(closed.id) })).toBeVisible();
});

test("direct links, reload and history preserve URL state and mounted filter controls", async ({
  page,
}) => {
  const workspace = await fixture(page, "n");
  const issue = await createIssue(page, workspace, "Navigation target");
  await page.goto(
    `${baseURL}/#/issues?workspace=${workspace}&include-closed=true&sort=-priority`,
  );
  await expect(
    page.getByRole("link", {
      name: `${issue.id} Navigation target`,
      exact: true,
    }),
  ).toBeVisible();
  const filter = page.getByRole("searchbox", { name: "Filter all issues…" });
  await filter.fill("Navigation");
  await expect(page).toHaveURL(/filter=Navigation/);
  const original = await filter.elementHandle();
  await expect(filter).toBeFocused();
  await expect
    .poll(() => page.locator(".filter-count").innerText())
    .toBe("1 issue");
  expect(
    await filter.evaluate((node, original) => node === original, original),
  ).toBe(true);
  await page
    .getByRole("link", { name: `${issue.id} Navigation target`, exact: true })
    .click();
  await expect(
    page.getByRole("heading", { name: "Navigation target", exact: true }),
  ).toBeVisible();
  await page.reload();
  await expect(
    page.getByRole("heading", { name: "Navigation target", exact: true }),
  ).toBeVisible();
  await page.goBack();
  await expect(filter).toHaveValue("Navigation");
  await page
    .getByRole("button", { name: "Configure issue view" })
    .click();
  await expect(
    page.getByRole("checkbox", { name: "Closed", exact: true }),
  ).toBeChecked();
  await page.keyboard.press("Escape");
  await page.goForward();
  await expect(
    page.getByRole("heading", { name: "Navigation target", exact: true }),
  ).toBeVisible();
  await page.getByRole("link", { name: "Workspaces", exact: true }).click();
  await expect(
    page.getByRole("heading", { name: "Workspaces", exact: true }),
  ).toBeVisible();
  await page.keyboard.press("Control+k");
  await expect(
    page.getByRole("dialog", { name: "Commands", exact: true }),
  ).toBeVisible();
  await page.getByRole("combobox", { name: "Search commands" }).fill("boards");
  await page.keyboard.press("Enter");
  await expect(
    page.getByRole("heading", { name: "Boards", exact: true }),
  ).toBeVisible();
  await page.goBack();
  await expect(
    page.getByRole("heading", { name: "Workspaces", exact: true }),
  ).toBeVisible();
});

test("issue creation retains drafts, stages resources and uploads attachments", async ({
  page,
}) => {
  const workspace = await fixture(page, "c");
  const related = await createIssue(page, workspace, "Related target");
  await page.goto(`${baseURL}/#/issues?workspace=${workspace}`);
  await page.getByRole("button", { name: "New issue", exact: true }).click();
  const dialog = page.getByRole("dialog", { name: "New issue", exact: true });
  await dialog.getByLabel("Title", { exact: true }).fill("Draft stays put");
  await dialog
    .getByRole("combobox", { name: "Type", exact: true })
    .selectOption("bug");
  await dialog
    .getByRole("combobox", { name: "Priority", exact: true })
    .selectOption("1");
  await dialog
    .getByRole("combobox", { name: "Label", exact: true })
    .fill("draft");
  await dialog
    .locator(".issue-create-label-editor")
    .getByRole("button", { name: "Add", exact: true })
    .click();
  await expect(dialog.getByLabel("Title", { exact: true })).toHaveValue(
    "Draft stays put",
  );
  await expect(
    dialog.getByRole("combobox", { name: "Type", exact: true }),
  ).toHaveValue("bug");
  await expect(
    dialog.getByRole("combobox", { name: "Priority", exact: true }),
  ).toHaveValue("1");
  await dialog
    .getByRole("button", { name: "+ Add relation", exact: true })
    .click();
  await dialog.getByLabel("Relation type").selectOption("related");
  await dialog.getByLabel("Other issue ID").fill(related.id);
  await dialog
    .getByRole("button", { name: "Add relation", exact: true })
    .click();
  await dialog.getByLabel("Attachment files").setInputFiles({
    name: "evidence.txt",
    mimeType: "text/plain",
    buffer: Buffer.from("browser evidence\n"),
  });
  await dialog
    .getByRole("button", { name: "Create issue", exact: true })
    .click();
  await expect(dialog).toHaveCount(0);
  const link = page.getByRole("link", { name: /Draft stays put/ });
  await expect(link).toBeVisible();
  await link.click();
  await expect(page.locator(".attachment-section")).toContainText(
    "evidence.txt",
  );
  await expect(page.locator(".relation-section")).toContainText(related.id);
  await expect(page.locator(".issue-sidebar")).toContainText("draft");
  await expect(
    page.getByRole("combobox", { name: "Type", exact: true }),
  ).toHaveValue("bug");
  await expect(
    page.getByRole("combobox", { name: "Priority", exact: true }),
  ).toHaveValue("1");
});

test("editing, inspector mutations and comments retain DOM identity and viewing position", async ({
  page,
}) => {
  const errors = [];
  page.on("pageerror", (error) => errors.push(error.message));
  const workspace = await fixture(page, "e");
  const issue = await createIssue(page, workspace, "Stable issue", {
    description: Array.from({ length: 40 }, (_, i) => `Paragraph ${i}.`).join(
      "\n\n",
    ),
  });
  await page.goto(`${baseURL}/#/issues/${issue.id}`);
  await page.getByRole("button", { name: "Edit issue", exact: true }).click();
  const title = page.locator(".issue-edit-form input[name=title]");
  await expect(title).toBeFocused();
  await title.fill("Stable issue edited");
  await expect(page.locator(".cm-content")).toBeVisible();
  const view = await page.locator(".issue-view").elementHandle();
  // A save that fails keeps the editor, its draft and its focus, and reports
  // the failure inside the form.
  const failSave = async (route) => {
    if (route.request().method() !== "PATCH") {
      await route.continue();
      return;
    }
    await route.fulfill({
      status: 500,
      contentType: "application/json",
      body: '{"error":"simulated save failure"}',
    });
  };
  await page.route(`**/api/issues/${issue.id}`, failSave);
  await page.keyboard.press("Control+Enter");
  await expect(page.locator(".issue-edit-form .edit-error")).toHaveText(
    "simulated save failure",
  );
  await expect(title).toHaveValue("Stable issue edited");
  await expect(title).toBeFocused();
  await expect(page.locator("h1")).toHaveText("Stable issue");
  await page.unroute(`**/api/issues/${issue.id}`, failSave);
  await page
    .locator(".issue-edit-form")
    .getByRole("button", { name: "Save changes" })
    .scrollIntoViewIfNeeded();
  const before = await page.evaluate(() => scrollY);
  await page.keyboard.press("Control+Enter");
  await expect(page.locator("h1")).toHaveText("Stable issue edited");
  // A saved edit hides the editor and hands focus back to the button that
  // opened it, on the page that was already mounted.
  await expect(page.locator(".issue-edit-form")).toHaveCount(0);
  await expect(
    page.getByRole("button", { name: "Edit issue", exact: true }),
  ).toBeFocused();
  expect(
    await page
      .locator(".issue-view")
      .evaluate((node, original) => node === original, view),
  ).toBe(true);
  // Keyboard save does not invoke compensating scroll recovery: what stood
  // above the editor stays where it was.
  expect(Math.abs((await page.evaluate(() => scrollY)) - before)).toBeLessThan(
    3,
  );
  await page.getByRole("button", { name: "Edit issue", exact: true }).click();
  await expect(title).toBeFocused();
  await title.press("Escape");
  await expect(page.locator(".issue-edit-form")).toHaveCount(0);
  await expect(
    page.getByRole("button", { name: "Edit issue", exact: true }),
  ).toBeFocused();
  await page.getByRole("button", { name: "Add label", exact: true }).click();
  const popover = page.getByRole("dialog", { name: "Add label", exact: true });
  await popover
    .getByRole("combobox", { name: "Label", exact: true })
    .fill("stable");
  await popover.getByRole("button", { name: "Add", exact: true }).click();
  await expect(page.locator(".issue-sidebar")).toContainText("stable");
  await page.keyboard.press("Escape");
  await page.getByRole("button", { name: "Edit issue", exact: true }).click();
  await expect(page.locator(".cm-content")).toHaveCount(1);
  const editor = await page.locator(".cm-content").elementHandle();
  const comment = page.getByRole("textbox", { name: "Comment", exact: true });
  await comment.fill("Comment keeps the mounted page.");
  await comment.press("Control+Enter");
  await expect(page.locator(".activity-comment-body")).toContainText(
    "Comment keeps the mounted page.",
  );
  await expect(comment).toHaveValue("");
  await expect(comment).toBeFocused();
  expect(
    await page
      .locator(".issue-view")
      .evaluate((node, original) => node === original, view),
  ).toBe(true);
  // A reload from another mutation leaves the open editor mounted.
  expect(
    await page
      .locator(".cm-content")
      .evaluate((node, original) => node === original, editor),
  ).toBe(true);
  await page.getByRole("link", { name: "Boards", exact: true }).click();
  await expect(page.locator(".cm-editor")).toHaveCount(0);
  expect(errors).toEqual([]);
});

test("board views retain drafts and page beyond fifty cards and epic lanes", async ({
  page,
}) => {
  const workspace = await fixture(page, "b");
  for (let i = 0; i < 53; i++)
    await createIssue(page, workspace, `Paging card ${i}`);
  for (let i = 0; i < 52; i++)
    await createIssue(page, workspace, `Paging epic ${i}`, { type: "epic" });
  await page.goto(`${baseURL}/#/boards?workspace=${workspace}`);
  await page.getByRole("button", { name: "Save as view", exact: true }).click();
  const dialog = page.getByRole("dialog", {
    name: "Save board view",
    exact: true,
  });
  await dialog.getByLabel("Name", { exact: true }).fill("Large board draft");
  await dialog.getByRole("checkbox", { name: /Anyone with the link/ }).check();
  await dialog.getByLabel("Cards per column").fill("50");
  await dialog.getByText("Selected", { exact: true }).last().click();
  await dialog.getByText("All", { exact: true }).last().click();
  await expect(dialog.getByLabel("Name", { exact: true })).toHaveValue(
    "Large board draft",
  );
  await expect(dialog.getByLabel("Cards per column")).toHaveValue("50");
  await dialog.getByRole("button", { name: "Save view", exact: true }).click();
  await expect(dialog).toHaveCount(0);
  await expect(page).toHaveURL(/#\/boards\/[^?]+$/);
  const lane = page.locator(".board-lane").filter({
    has: page.getByRole("heading", { name: "No epic", exact: true }),
  });
  await expect(lane.locator(".board-card")).toHaveCount(50);
  await lane.getByRole("button", { name: /Load 3 more/ }).click();
  await expect(lane.locator(".board-card")).toHaveCount(53);
  while (await page.locator(".board-lanes-more").count()) {
    const count = await page.locator(".board-lane").count();
    await page.locator(".board-lanes-more").click();
    await expect
      .poll(() => page.locator(".board-lane").count())
      .toBeGreaterThan(count);
  }
  await expect(page.locator(".board-lane")).toHaveCount(53);
  await page.reload();
  await expect(
    page.getByRole("combobox", { name: "Board view", exact: true }),
  ).toContainText("Large board draft");
  await page.setViewportSize({ width: 390, height: 844 });
  expect(
    await page.evaluate(
      () => document.documentElement.scrollWidth <= innerWidth,
    ),
  ).toBe(true);
});

test("rebased child creation and backlog workflows remain available", async ({
  page,
}) => {
  const workspace = await fixture(page, "r");
  const parent = await createIssue(page, workspace, "Parent issue", {
    type: "epic",
  });
  await page.goto(`${baseURL}/#/issues/${parent.id}`);
  await page.getByRole("button", { name: "Add child inline" }).click();
  const title = page.getByRole("textbox", { name: "Child issue title" });
  await title.fill("Inline child");
  await title.press("Enter");
  await expect(
    page.locator(".child-issues-section .title", { hasText: "Inline child" }),
  ).toBeVisible();
  await expect(title).toBeFocused();
  await expect(title).toHaveValue("");
  await title.press("Escape");
  await expect(
    page.getByRole("button", { name: "Add child inline" }),
  ).toBeFocused();
  await page.getByRole("button", { name: "New child issue" }).click();
  const dialog = page.getByRole("dialog", { name: "New issue", exact: true });
  await expect(
    dialog.getByRole("combobox", { name: "Workspace" }),
  ).toBeDisabled();
  await dialog
    .getByRole("textbox", { name: "Title", exact: true })
    .fill("Backlog child");
  await dialog.getByRole("checkbox", { name: "Assign to me" }).check();
  await dialog.getByRole("checkbox", { name: "Backlog", exact: true }).check();
  await expect(
    dialog.getByRole("checkbox", { name: "Assign to me" }),
  ).not.toBeChecked();
  await dialog
    .getByRole("button", { name: "Create issue", exact: true })
    .click();
  await expect(dialog).not.toBeVisible();
  await page.goto(`${baseURL}/#/boards?workspace=${workspace}`);
  await expect(
    page.locator(".board-card", { hasText: "Backlog child" }),
  ).toHaveCount(0);
  await page.getByRole("button", { name: "Edit view" }).click();
  let viewDialog = page.getByRole("dialog", { name: "Edit default board" });
  const columns = viewDialog.locator(".board-view-section", {
    has: page.getByRole("heading", { name: "Columns", exact: true }),
  });
  await columns.getByRole("checkbox", { name: "Backlog" }).check();
  await viewDialog.getByRole("button", { name: "Save settings" }).click();
  const card = page.locator(".board-card", { hasText: "Backlog child" });
  await expect(card).toBeVisible();
  await page.reload();
  await expect(card).toBeVisible();
  await page
    .getByRole("combobox", { name: `Status of ${parent.id}` })
    .selectOption("backlog");
  await expect(
    page.getByRole("combobox", { name: `Status of ${parent.id}` }),
  ).toHaveValue("backlog");
  await page.getByRole("button", { name: "Edit view" }).click();
  viewDialog = page.getByRole("dialog", { name: "Edit default board" });
  await viewDialog
    .locator(".board-view-section", {
      has: page.getByRole("heading", { name: "Columns", exact: true }),
    })
    .getByRole("checkbox", { name: "Backlog" })
    .uncheck();
  await viewDialog.getByRole("button", { name: "Save settings" }).click();
  await expect(
    page.locator(".board-lane", { hasText: "Parent issue" }),
  ).toHaveCount(0);
  await page.goto(`${baseURL}/#/issues/${parent.id}`);
  await page
    .getByRole("combobox", { name: "Status", exact: true })
    .selectOption("open");
  await expect(
    page.getByRole("combobox", { name: "Status", exact: true }),
  ).toHaveValue("open");
});

test("Markdown changes notify once and still bubble input", async ({
  page,
}) => {
  await page.goto(`${baseURL}/#/issues`);
  await page.evaluate(async () => {
    const { h, render } = await import("/vendor/preact-10.29.8.js");
    const { MarkdownInput } = await import("/components/markdown-input.js");
    const host = document.createElement("form");
    host.id = "markdown-callback-check";
    document.body.append(host);
    window.editorChanges = [];
    window.editorInputEvents = 0;
    host.addEventListener("input", (event) => {
      if (event.target instanceof HTMLTextAreaElement)
        window.editorInputEvents++;
    });
    render(
      h(MarkdownInput, {
        value: "",
        label: "Callback editor",
        onInput: (value) => window.editorChanges.push(value),
      }),
      host,
    );
  });
  const editor = page.locator("#markdown-callback-check .cm-content");
  await editor.fill("one change");
  await expect
    .poll(() => page.evaluate(() => window.editorChanges))
    .toEqual(["one change"]);
  expect(await page.evaluate(() => window.editorInputEvents)).toBe(1);
});

test("the workspace description editor keeps focus in its text area", async ({
  page,
}) => {
  const workspace = await fixture(page, "e");
  await page.goto(`${baseURL}/#/workspaces/${workspace}`);
  await page.getByRole("button", { name: "Edit workspace" }).click();
  const editor = page.locator(".workspace-edit-form .cm-content");
  await editor.click();
  await expect(editor).toBeFocused();
  await page.keyboard.type("Described in place");
  await expect(editor).toBeFocused();
  await editor.click();
  // A <label> around the editor would forward this click to the toolbar's
  // heading select, the first labelable element inside it.
  await expect(editor).toBeFocused();
  // A save that fails keeps the editor open and reports the failure in it.
  const failWorkspaceSave = async (route) => {
    if (route.request().method() !== "PATCH") {
      await route.continue();
      return;
    }
    await route.fulfill({
      status: 500,
      contentType: "application/json",
      body: '{"error":"simulated workspace save failure"}',
    });
  };
  await page.route(`**/api/workspaces/${workspace}`, failWorkspaceSave);
  await page
    .getByRole("button", { name: "Save changes", exact: true })
    .click();
  await expect(page.locator(".workspace-edit-form .edit-error")).toHaveText(
    "simulated workspace save failure",
  );
  await expect(page.locator(".workspace-edit-form")).toBeVisible();
  // The toggle still reads "Hide editor", and did not take focus back.
  await expect(
    page.getByRole("button", { name: "Hide editor" }),
  ).not.toBeFocused();
  await page.unroute(`**/api/workspaces/${workspace}`, failWorkspaceSave);
  await page
    .getByRole("button", { name: "Save changes", exact: true })
    .click();
  await expect(
    page.locator(".workspace-detail-description .markdown"),
  ).toHaveText("Described in place");
  // A saved edit hides the editor and hands focus back to the button that
  // opened it.
  await expect(page.locator(".workspace-edit-form")).toBeHidden();
  await expect(
    page.getByRole("button", { name: "Edit workspace" }),
  ).toBeFocused();
});

test("epic, status, and type filters compose without duplicate page fetches", async ({
  page,
}) => {
  const workspace = await fixture(page, "f");
  const epic = await createIssue(page, workspace, "Filter epic", {
    type: "epic",
  });
  const child = await createIssue(page, workspace, "Filter child", {
    relations: [{ type: "has-parent", other: epic.id }],
    labels: ["frontend"],
  });
  await createIssue(page, workspace, "Filter grandchild", {
    relations: [{ type: "has-parent", other: child.id }],
    labels: ["frontend"],
  });
  await createIssue(page, workspace, "Standalone issue", {
    labels: ["backend"],
    assignees: ["filter-user"],
  });
  const requests = [];
  const epicRequests = [];
  const labelRequests = [];
  const assigneeRequests = [];
  page.on("request", (request) => {
    const url = new URL(request.url());
    if (url.pathname === "/api/issues" && url.searchParams.has("limit"))
      requests.push(url);
    if (
      url.pathname === "/api/issues" &&
      url.searchParams.getAll("type").includes("epic") &&
      !url.searchParams.has("limit")
    )
      epicRequests.push(url);
    if (url.pathname === "/api/labels") labelRequests.push(url);
    if (url.pathname === "/api/assignees") assigneeRequests.push(url);
  });
  await page.goto(`${baseURL}/#/issues?workspace=${workspace}&page=999`);
  await expect(page).not.toHaveURL(/page=999/);
  await expect(page.locator(".filter-count")).toHaveText("4 issues");
  expect(requests).toHaveLength(2);
  expect(epicRequests).toHaveLength(0);
  expect(labelRequests).toHaveLength(0);
  expect(assigneeRequests).toHaveLength(0);
  const labelRow = page.locator(".dynamic-filter-group", {
    has: page.getByText("labels", { exact: true }),
  });
  await labelRow.getByRole("button", { name: "Add label filter" }).click();
  const labelDialog = page.getByRole("dialog", { name: "Add label filter" });
  const labelSelector = labelDialog.getByRole("combobox", {
    name: "Search labels",
  });
  await expect(
    labelDialog.getByRole("option", { name: /#frontend/ }),
  ).toBeVisible();
  expect(labelRequests).toHaveLength(1);
  await labelSelector.fill("front");
  await labelSelector.press("Escape");
  await expect(labelDialog).toHaveCount(0);
  await expect(page).not.toHaveURL(/label=/);
  await labelRow.getByRole("button", { name: "Add label filter" }).click();
  await expect(
    page
      .getByRole("dialog", { name: "Add label filter" })
      .getByRole("combobox", { name: "Search labels" }),
  ).toHaveValue("");
  await expect.poll(() => labelRequests.length).toBe(2);
  await labelSelector.fill("front");
  await labelDialog.getByRole("option", { name: /#frontend/ }).click();
  await expect(page).toHaveURL(/label=frontend/);
  await expect(page.locator(".filter-count")).toHaveText("2 issues");
  await labelRow
    .getByRole("button", { name: "Remove label frontend" })
    .click();
  await expect(page).not.toHaveURL(/label=frontend/);
  await expect(page.locator(".filter-count")).toHaveText("4 issues");

  const assigneeRow = page.locator(".dynamic-filter-group", {
    has: page.getByText("assignees", { exact: true }),
  });
  await assigneeRow
    .getByRole("button", { name: "Add assignee filter" })
    .click();
  const assigneeDialog = page.getByRole("dialog", {
    name: "Add assignee filter",
  });
  const assigneeSelector = assigneeDialog.getByRole("combobox", {
    name: "Search assignees",
  });
  await expect(
    assigneeDialog.getByRole("option", { name: /@filter-user/ }),
  ).toBeVisible();
  expect(assigneeRequests).toHaveLength(1);
  await assigneeSelector.fill("filter");
  await assigneeDialog
    .getByRole("option", { name: /@filter-user/ })
    .click();
  await expect(page).toHaveURL(/assignee=filter-user/);
  await expect(page.locator(".filter-count")).toHaveText("1 issue");
  await assigneeRow
    .getByRole("button", { name: "Remove assignee filter-user" })
    .click();
  await expect(page).not.toHaveURL(/assignee=/);
  await expect(page.locator(".filter-count")).toHaveText("4 issues");

  const epicRow = page.locator(".dynamic-filter-group", {
    has: page.getByText("epic", { exact: true }),
  });
  await epicRow.getByRole("button", { name: "Choose epic filter" }).click();
  await expect.poll(() => epicRequests.length).toBe(1);
  let epicDialog = page.getByRole("dialog", { name: "Choose epic filter" });
  let epicSelector = epicDialog.getByRole("combobox", {
    name: "Search epics",
  });
  await expect(
    epicDialog.getByRole("option", { name: /Filter epic/ }),
  ).toBeVisible();
  await epicSelector.fill("Filter epic");
  await epicDialog.getByRole("option", { name: /Filter epic/ }).click();
  await expect(page.locator(".filter-count")).toHaveText("3 issues");
  await page.reload();
  await expect(epicRow.locator(".facet.active")).toHaveText("Filter epic");
  expect(requests.at(-1).searchParams.get("parent")).toBe(epic.id);
  expect(requests.at(-1).searchParams.get("recursive")).toBe("true");
  expect(requests.at(-1).searchParams.get("include-parent")).toBe("true");
  const trigger = page.getByRole("button", { name: "Configure issue view" });
  await expect(trigger).toHaveText("▾ View");
  await trigger.click();
  const statuses = page.getByRole("dialog", { name: "Issue view options" });
  await expect(statuses).toBeVisible();
  await expect(
    statuses.getByRole("group", { name: "Statuses" }),
  ).toBeVisible();
  await expect(statuses.getByRole("group", { name: "Types" })).toBeVisible();
  await expect(statuses.getByRole("checkbox")).toHaveCount(9);
  const types = statuses.locator("input[data-type]");
  await expect(types).toHaveCount(5);
  for (const checkbox of await types.all()) {
    if ((await checkbox.getAttribute("value")) !== "task")
      await checkbox.uncheck();
  }
  await statuses.getByRole("button", { name: "Done", exact: true }).click();
  await expect(page).toHaveURL(/type=task/);
  await expect(page.locator(".filter-count")).toHaveText("2 issues");
  await expect
    .poll(() => requests.at(-1)?.searchParams.getAll("type"))
    .toEqual(["task"]);
  await trigger.click();
  await statuses.getByRole("button", { name: "Reset", exact: true }).click();
  await statuses.getByRole("button", { name: "Done", exact: true }).click();
  await expect(page).not.toHaveURL(/type=/);
  await expect
    .poll(() => requests.at(-1)?.searchParams.has("type"))
    .toBe(false);
  await trigger.click();
  await expect(types).toHaveCount(5);
  for (const checkbox of await types.all()) await checkbox.uncheck();
  expect(
    await types.evaluateAll(
      (inputs) => inputs.filter((input) => input.checked).length,
    ),
  ).toBe(0);
  const beforeEmptyTypes = requests.length;
  await statuses.getByRole("button", { name: "Done", exact: true }).click();
  await expect(page).toHaveURL(/type=(?:&|$)/);
  await expect(
    page.getByText("No issue types selected.", { exact: true }),
  ).toBeVisible();
  expect(requests).toHaveLength(beforeEmptyTypes);
  await page.reload();
  await expect(
    page.getByText("No issue types selected.", { exact: true }),
  ).toBeVisible();
  await trigger.click();
  await statuses.getByRole("button", { name: "Reset", exact: true }).click();
  await statuses.getByRole("button", { name: "Done", exact: true }).click();
  await expect(page).not.toHaveURL(/type=/);
  await expect(page.locator(".filter-count")).toHaveText("3 issues");
  await trigger.click();
  const statusChoices = statuses.locator("input[data-status]");
  await expect(statusChoices).toHaveCount(4);
  for (const checkbox of await statusChoices.all())
    await checkbox.uncheck();
  const beforeEmpty = requests.length;
  await statuses.getByRole("button", { name: "Done", exact: true }).click();
  await expect(
    page.getByText("No statuses selected.", { exact: true }),
  ).toBeVisible();
  expect(requests).toHaveLength(beforeEmpty);
  await expect(
    epicRow.getByRole("button", { name: `Remove epic ${epic.id}` }),
  ).toBeVisible();
  await epicRow.getByRole("button", { name: "Choose epic filter" }).click();
  epicDialog = page.getByRole("dialog", { name: "Choose epic filter" });
  epicSelector = epicDialog.getByRole("combobox", { name: "Search epics" });
  await epicSelector.fill("No epic");
  await epicDialog.getByRole("option", { name: "No epic", exact: true }).click();
  await trigger.click();
  await statuses.getByRole("button", { name: "Reset", exact: true }).click();
  await statuses.getByRole("button", { name: "Done", exact: true }).click();
  await expect(page.locator(".filter-count")).toHaveText("2 issues");
  expect(requests.at(-1).searchParams.get("parent")).toBe("none");
  expect(requests.at(-1).searchParams.get("parent-type")).toBe("epic");
  await page.reload();
  await expect(
    epicRow.getByRole("button", { name: "Remove epic none" }),
  ).toBeVisible();
  await expect(page.locator(".filter-count")).toHaveText("2 issues");
});
