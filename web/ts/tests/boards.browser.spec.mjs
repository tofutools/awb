const { expect, test } = await import(process.env.PLAYWRIGHT_TEST_MODULE ?? "playwright/test");

const baseURL = process.env.AWB_BROWSER_BASE_URL;

test("save, share and work from a responsive board", async ({ page }) => {
  test.skip(baseURL === undefined, "set AWB_BROWSER_BASE_URL to a disposable awb serve instance");
  await page.setViewportSize({ width: 1440, height: 1000 });
  await page.goto(`${baseURL}/#/boards`);
  await expect(page.getByRole("heading", { name: "Boards" })).toBeVisible();
  await expect(page.locator(".board-lane")).toHaveCount(2);
  await expect(page.getByRole("heading", { name: "demo DEMO" })).toBeVisible();
  await expect(page.getByRole("heading", { name: "Open" }).first()).toBeVisible();

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
  const dragBox = await drag.boundingBox();
  const targetBox = await target.boundingBox();
  expect(dragBox).not.toBeNull();
  expect(targetBox).not.toBeNull();
  await page.mouse.move(dragBox.x + dragBox.width / 2, dragBox.y + dragBox.height / 2);
  await page.mouse.down();
  await page.mouse.move(dragBox.x + dragBox.width / 2 + 4, dragBox.y + dragBox.height / 2 + 4);
  await page.mouse.move(targetBox.x + targetBox.width / 2, targetBox.y + targetBox.height / 2, { steps: 12 });
  await page.mouse.up();
  await expect(page.locator(".board-column[data-status='in_progress']", { hasText: "Build the full text search index" })).toBeVisible();

  await page.screenshot({ path: "screenshots/board-views-desktop.png", fullPage: true });
  await page.setViewportSize({ width: 390, height: 844 });
  await expect(page.locator(".board-columns").first()).toHaveCSS("overflow-x", "auto");
  await page.screenshot({ path: "screenshots/board-views-narrow.png", fullPage: true });
});
