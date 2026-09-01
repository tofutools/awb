const { expect, test } = await import(process.env.PLAYWRIGHT_TEST_MODULE ?? "playwright/test");

const baseURL = process.env.AWB_BROWSER_BASE_URL;

async function pointerDrag(page, handle, target, below = false) {
  await handle.scrollIntoViewIfNeeded();
  const dragBox = await handle.boundingBox();
  expect(dragBox).not.toBeNull();
  await page.mouse.move(dragBox.x + dragBox.width / 2, dragBox.y + dragBox.height / 2);
  await page.mouse.down();
  await page.mouse.move(dragBox.x + dragBox.width / 2 + 4, dragBox.y + dragBox.height / 2 + 4);
  await target.scrollIntoViewIfNeeded();
  const targetBox = await target.boundingBox();
  expect(targetBox).not.toBeNull();
  await page.mouse.move(
    targetBox.x + targetBox.width / 2,
    targetBox.y + (below ? targetBox.height * 0.8 : targetBox.height * 0.2),
    { steps: 12 },
  );
  await page.mouse.up();
}

test("save, share and work from a responsive board", async ({ page }) => {
  test.skip(baseURL === undefined, "set AWB_BROWSER_BASE_URL to a disposable awb serve instance");
  await page.setViewportSize({ width: 1440, height: 1000 });
  await page.goto(`${baseURL}/#/boards`);
  await expect(page.getByRole("heading", { name: "Boards" })).toBeVisible();
  await expect(page.locator(".board-lane")).toHaveCount(2);
  await expect(page.getByRole("heading", { name: "demo DEMO" })).toBeVisible();
  await expect(page.getByRole("heading", { name: "Open" }).first()).toBeVisible();

  const demoLane = page.locator(".board-lane", { hasText: "DEMO" });
  await demoLane.getByRole("button", { name: "Collapse DEMO swimlane" }).click();
  await expect(demoLane.locator(".board-columns")).toBeHidden();
  await page.reload();
  const persistedDemoLane = page.locator(".board-lane", { hasText: "DEMO" });
  await expect(persistedDemoLane.getByRole("button", { name: "Expand DEMO swimlane" })).toBeVisible();
  await expect(persistedDemoLane.locator(".board-columns")).toBeHidden();
  await persistedDemoLane.getByRole("button", { name: "Expand DEMO swimlane" }).click();
  await expect(persistedDemoLane.locator(".board-columns")).toBeVisible();

  await page.getByRole("button", { name: "Save as view" }).click();
  const dialog = page.getByRole("dialog", { name: "Save board view" });
  await dialog.getByLabel("Name").fill("Release train");
  await dialog.getByText("Anyone with the link").click();
  await dialog.getByRole("button", { name: "Save view" }).click();
  await expect(page).toHaveURL(/#\/boards\/view-[0-9a-f]{24}$/);
  await expect(page.locator(".board-summary")).toContainText("Release train");
  await expect(page.getByRole("button", { name: "Copy link" })).toBeVisible();

  const webCard = page.locator(".board-card", { hasText: "Polish narrow board layout" });
  await webCard.getByLabel(/Move web-/).selectOption("in_progress");
  await expect(page.locator(".board-column[data-status='in_progress']", { hasText: "Polish narrow board layout" })).toBeVisible();

  const openCard = page.locator(".board-card", { hasText: "Build the full text search index" });
  const drag = openCard.getByLabel(/Drag demo-/);
  const target = page.locator(".board-lane", { hasText: "DEMO" }).locator(".board-column[data-status='in_progress']");
  await pointerDrag(page, drag, target);
  await expect(page.locator(".board-column[data-status='in_progress']", { hasText: "Build the full text search index" })).toBeVisible();

  // The same gesture moves between project swimlanes and places the card
  // immediately before its target within the destination cell.
  const movedCard = page.locator(".board-card", { hasText: "Build the full text search index" });
  const destinationCard = page.locator(".board-card", { hasText: "Polish narrow board layout" });
  await pointerDrag(page, movedCard.getByLabel(/Drag demo-/), destinationCard);
  const webLane = page.locator(".board-lane", { hasText: "WEB" });
  await expect(webLane.locator(".board-column[data-status='in_progress']", { hasText: "Build the full text search index" })).toBeVisible();
  const webTitles = await webLane.locator(".board-column[data-status='in_progress'] .board-card .name .title").allTextContents();
  expect(webTitles.indexOf("Build the full text search index")).toBeLessThan(webTitles.indexOf("Polish narrow board layout"));

  // Natural issue lists expose the same sparse manual order as row drag/drop.
  await page.goto(`${baseURL}/#/issues?include-closed=true&size=25`);
  const sourceRow = page.locator(".issue-table tbody tr", { hasText: "Polish narrow board layout" });
  const targetRow = page.locator(".issue-table tbody tr", { hasText: "Build the full text search index" });
  await pointerDrag(page, sourceRow.getByLabel(/Drag web-.* to reorder/), targetRow);
  await expect.poll(async () => {
    const rowTitles = await page.locator(".issue-table tbody tr .name .title").allTextContents();
    return rowTitles.indexOf("Polish narrow board layout") < rowTitles.indexOf("Build the full text search index");
  }).toBe(true);

  await page.setViewportSize({ width: 390, height: 844 });
  await page.goto(`${baseURL}/#/boards`);
  await expect(page.locator(".board-columns").first()).toHaveCSS("overflow-x", "auto");
});
