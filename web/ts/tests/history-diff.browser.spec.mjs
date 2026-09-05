const { expect, test } = await import(process.env.PLAYWRIGHT_TEST_MODULE ?? "playwright/test");

const baseURL = process.env.AWB_BROWSER_BASE_URL;

test("history previews open a complete diff and return focus and scroll", async ({ page }) => {
  test.skip(baseURL === undefined, "set AWB_BROWSER_BASE_URL to a disposable awb serve instance");
  await page.setViewportSize({ width: 1000, height: 640 });
  await page.goto(`${baseURL}/#/issues`);

  const commonPrefix = `${"Unchanged introductory context. ".repeat(35)}\n\n`;
  const commonMiddle = `\n${"Separated unchanged context. ".repeat(20)}\n`;
  const before = `# History diff test\n\n${commonPrefix}First old sentence.${commonMiddle}Final old sentence.\n`;
  const after = `# History diff test\n\n${commonPrefix}First new sentence.${commonMiddle}Final new sentence.\nAdded line.\n`;
  const issue = await page.evaluate(async ({ before }) => {
    const createdResponse = await fetch("api/issues", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ workspace: "demo", title: `History diff ${Date.now()}`, description: before }),
    });
    if (!createdResponse.ok) throw new Error(await createdResponse.text());
    const created = await createdResponse.json();
    const currentResponse = await fetch(`api/issues/${created.id}`);
    const etag = currentResponse.headers.get("ETag");
    if (etag === null) throw new Error("issue response had no ETag");
    return { created, etag };
  }, { before });
  await page.evaluate(async ({ id, etag, after }) => {
    const response = await fetch(`api/issues/${id}`, {
      method: "PATCH",
      headers: { "Content-Type": "application/json", "If-Match": etag },
      body: JSON.stringify({ description: after }),
    });
    if (!response.ok) throw new Error(await response.text());
  }, { id: issue.created.id, etag: issue.etag, after });

  await page.goto(`${baseURL}/#/issues/${issue.created.id}`);
  const preview = page.getByRole("button", { name: "View full description diff" });
  await preview.scrollIntoViewIfNeeded();
  await expect(preview).toHaveAccessibleName(/View full description diff.*Removed:.*old.*Added:.*new/s);
  await expect(preview).toContainText("old");
  await expect(preview).toContainText("new");
  await expect(preview.locator(".history-diff-omitted")).not.toHaveCount(0);
  const scrollBefore = await page.evaluate(() => scrollY);

  await preview.focus();
  await page.keyboard.press("Enter");
  const dialog = page.getByRole("dialog", { name: "description change" });
  await expect(dialog).toBeVisible();
  await expect(dialog.locator("del")).toHaveCount(2);
  await expect(dialog.locator("del").first()).toContainText("old");
  await expect(dialog.locator("ins")).toHaveCount(3);
  await expect(dialog.locator("ins").first()).toContainText("new");
  await expect(dialog).toContainText("Separated unchanged context.");
  await expect(dialog).toContainText("Added line.");

  await page.keyboard.press("Escape");
  await expect(dialog).not.toBeVisible();
  await expect(preview).toBeFocused();
  expect(await page.evaluate(() => scrollY)).toBe(scrollBefore);
});
