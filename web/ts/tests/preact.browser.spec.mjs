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
async function createIssue(page, workspace, title, extra = {}) {
  const response = await page.request.post(`${baseURL}/api/issues`, {
    data: { workspace, title, ...extra },
  });
  expect(response.ok(), await response.text()).toBe(true);
  return response.json();
}

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
  await expect(
    page.getByRole("checkbox", { name: "Show closed" }),
  ).toBeChecked();
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
  await dialog
    .getByLabel("Attachment files")
    .setInputFiles({
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
  const editor = await page.locator(".cm-content").elementHandle();
  const view = await page.locator(".issue-view").elementHandle();
  await page
    .locator(".issue-edit-form")
    .getByRole("button", { name: "Save changes" })
    .scrollIntoViewIfNeeded();
  const before = await page.evaluate(() => scrollY);
  await page.keyboard.press("Control+Enter");
  await expect(page.locator("h1")).toHaveText("Stable issue edited");
  expect(
    await page
      .locator(".cm-content")
      .evaluate((node, original) => node === original, editor),
  ).toBe(true);
  expect(
    await page
      .locator(".issue-view")
      .evaluate((node, original) => node === original, view),
  ).toBe(true);
  // Keyboard save does not move focus or invoke compensating scroll recovery.
  expect(Math.abs((await page.evaluate(() => scrollY)) - before)).toBeLessThan(
    3,
  );
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
  await page.getByRole("button", { name: "Edit issue", exact: true }).click();
  await expect(page.locator(".cm-content")).toHaveCount(1);
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
  const lane = page
    .locator(".board-lane")
    .filter({
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
