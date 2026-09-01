const { expect, test } = await import(process.env.PLAYWRIGHT_TEST_MODULE ?? "playwright/test");

const baseURL = process.env.AWB_BROWSER_BASE_URL;

async function pointerDrag(page, handle, target, below = false) {
  for (let attempt = 0; ; attempt++) {
    try {
      await handle.scrollIntoViewIfNeeded();
      break;
    } catch (error) {
      if (attempt >= 2 || !String(error).includes("not attached")) throw error;
    }
  }
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
  await expect(page.getByRole("heading", { name: "No epic" })).toBeVisible();
  await expect(page.getByRole("heading", { name: /demo Ship the 1.0 release/ })).toBeVisible();
  await expect(page.getByRole("heading", { name: "Open" }).first()).toBeVisible();

  const releaseLane = page.locator(".board-lane", { has: page.getByRole("heading", { name: /demo Ship the 1.0 release/ }) });
  await releaseLane.getByRole("button", { name: /Collapse Ship the 1.0 release.*swimlane/ }).click();
  await expect(releaseLane.locator(".board-columns")).toBeHidden();
  await page.reload();
  const persistedReleaseLane = page.locator(".board-lane", { has: page.getByRole("heading", { name: /demo Ship the 1.0 release/ }) });
  await expect(persistedReleaseLane.getByRole("button", { name: /Expand Ship the 1.0 release.*swimlane/ })).toBeVisible();
  await expect(persistedReleaseLane.locator(".board-columns")).toBeHidden();
  await persistedReleaseLane.getByRole("button", { name: /Expand Ship the 1.0 release.*swimlane/ }).click();
  await expect(persistedReleaseLane.locator(".board-columns")).toBeVisible();

  const secondEpic = await page.evaluate(async () => {
    const response = await fetch("api/issues", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ project: "demo", title: "Platform epic", type: "epic" }),
    });
    if (!response.ok) throw new Error(await response.text());
    return response.json();
  });
  expect(secondEpic.project).toBe("demo");
  await page.reload();
  await expect(page.locator(".board-lane")).toHaveCount(3);

  await page.getByRole("button", { name: "Save as view" }).click();
  const dialog = page.getByRole("dialog", { name: "Save board view" });
  await dialog.getByLabel("Name").fill("Release train");
  await dialog.getByText("Anyone with the link").click();
  await dialog.getByRole("button", { name: "Save view" }).click();
  await expect(page).toHaveURL(/#\/boards\/view-[0-9a-f]{24}$/);
  await expect(page.locator(".board-summary")).toContainText("Release train");
  await expect(page.getByRole("button", { name: "Copy link" })).toBeVisible();

  // Epic-to-epic, epic-to-No-epic, and No-epic-to-epic all preserve the
  // immutable demo workspace while status and sparse position move atomically.
  const openCard = page.locator(".board-card", { hasText: "Build the full text search index" });
  const drag = openCard.getByLabel(/Drag demo-/);
  const platformLane = page.locator(".board-lane", { has: page.getByRole("heading", { name: /demo Platform epic/ }) });
  await pointerDrag(page, drag, platformLane.locator(".board-column[data-status='open']"));
  await expect(platformLane.locator(".board-column[data-status='open']", { hasText: "Build the full text search index" })).toBeVisible();

  const noEpicLane = page.locator(".board-lane", { has: page.getByRole("heading", { name: "No epic" }) });
  await pointerDrag(page, platformLane.locator(".board-card", { hasText: "Build the full text search index" }).getByLabel(/Drag demo-/),
    noEpicLane.locator(".board-column[data-status='in_progress']"));
  await expect(noEpicLane.locator(".board-column[data-status='in_progress']", { hasText: "Build the full text search index" })).toBeVisible();

  const currentReleaseLane = page.locator(".board-lane", { has: page.getByRole("heading", { name: /demo Ship the 1.0 release/ }) });
  await pointerDrag(page, noEpicLane.locator(".board-card", { hasText: "Build the full text search index" }).getByLabel(/Drag demo-/),
    currentReleaseLane.locator(".board-column[data-status='in_progress']"));
  await expect(currentReleaseLane.locator(".board-column[data-status='in_progress']", { hasText: "Build the full text search index" })).toBeVisible();

  // The same gesture reorders within one epic/status cell.
  const movedCard = page.locator(".board-card", { hasText: "Build the full text search index" });
  const destinationCard = page.locator(".board-card", { hasText: "Browse the widget catalogue" });
  await pointerDrag(page, movedCard.getByLabel(/Drag demo-/), destinationCard);
  const releaseTitles = await currentReleaseLane.locator(".board-column[data-status='in_progress'] .board-card .name .title").allTextContents();
  expect(releaseTitles.indexOf("Build the full text search index")).toBeLessThan(releaseTitles.indexOf("Browse the widget catalogue"));

  // Natural issue lists expose the same sparse manual order as row drag/drop.
  await page.goto(`${baseURL}/#/issues?include-closed=true&size=25`);
  const sourceRow = page.locator(".issue-table tbody tr", { hasText: "Browse the widget catalogue" });
  const targetRow = page.locator(".issue-table tbody tr", { hasText: "Build the full text search index" });
  const sourceHandle = sourceRow.getByLabel(/Drag demo-.* to reorder/);
  await expect(sourceHandle).toBeVisible();
  await expect(targetRow).toBeVisible();
  await pointerDrag(page, sourceHandle, targetRow);
  await expect.poll(async () => {
    const rowTitles = await page.locator(".issue-table tbody tr .name .title").allTextContents();
    return rowTitles.indexOf("Browse the widget catalogue") < rowTitles.indexOf("Build the full text search index");
  }).toBe(true);

  await page.setViewportSize({ width: 390, height: 844 });
  await page.goto(`${baseURL}/#/boards`);
  await expect(page.locator(".board-columns").first()).toHaveCSS("overflow-x", "auto");
  await page.evaluate(async () => {
    for (let index = 0; index < 10; index++) {
      const response = await fetch("api/issues", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ project: "demo", title: `Overflow epic ${index}`, type: "epic" }),
      });
      if (!response.ok) throw new Error(await response.text());
    }
  });
  await page.reload();
  const responsiveCard = page.locator(".board-card", { hasText: "Build the full text search index" });
  await expect(responsiveCard.getByLabel(/Drag demo-/)).toBeHidden();
  const keyboardBoardOrder = responsiveCard.locator(".board-card-order-button:not([disabled])").first();
  await expect(keyboardBoardOrder).toBeEnabled();
  const boardOrderResponse = page.waitForResponse((response) => response.url().includes("/move") && response.request().method() === "POST");
  await keyboardBoardOrder.focus();
  await page.keyboard.press("Enter");
  await boardOrderResponse;
  const reorderedCard = page.locator(".board-card", { hasText: "Build the full text search index" });
  const epicControl = reorderedCard.getByLabel(/Epic for demo-/);
  const initialEpicValues = await epicControl.locator("option").evaluateAll((options) => options.map((option) => option.value));
  await page.getByRole("button", { name: /Load up to .* more epics/ }).click();
  await expect.poll(async () => epicControl.locator("option").count()).toBeGreaterThan(initialEpicValues.length);
  const loadedEpicValues = await epicControl.locator("option").evaluateAll((options) => options.map((option) => option.value));
  const pagedEpic = loadedEpicValues.find((value) => !initialEpicValues.includes(value));
  expect(pagedEpic).toBeTruthy();
  const movedID = await reorderedCard.getAttribute("data-issue");
  await reorderedCard.getByLabel(/Epic for demo-/).selectOption(pagedEpic);
  await expect.poll(async () => page.evaluate(async ({ id, epic }) => {
    const issue = await (await fetch(`api/issues/${id}`)).json();
    return issue.relations.some((relation) => relation.type === "has-parent" && relation.other === epic);
  }, { id: movedID, epic: pagedEpic })).toBe(true);

  await page.evaluate(async () => {
    for (let index = 0; index < 5; index++) {
      const response = await fetch("api/issues", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ project: "demo", title: `Pagination task ${index}`, type: "task" }),
      });
      if (!response.ok) throw new Error(await response.text());
    }
  });
  await page.goto(`${baseURL}/#/issues?include-closed=true&size=25&page=2`);
  const keyboardOrder = page.locator('.issue-table tbody tr').first().getByRole("button", { name: /earlier in workspace demo/ });
  await expect(keyboardOrder).toBeEnabled();
  const listOrderResponse = page.waitForResponse((response) => response.url().includes("/move") && response.request().method() === "POST");
  await keyboardOrder.focus();
  await page.keyboard.press("Enter");
  const listMove = await listOrderResponse;
  expect(listMove.request().postDataJSON().direction).toBe("earlier");
});
