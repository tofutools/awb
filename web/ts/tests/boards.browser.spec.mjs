const { expect, test } = await import(process.env.PLAYWRIGHT_TEST_MODULE ?? "playwright/test");

const baseURL = process.env.AWB_BROWSER_BASE_URL;

const dragInteractionSelector = "a, button, input, select, textarea, label, [data-act], [contenteditable], [role='button']";

async function bareDragPoint(source) {
  return source.evaluate((element, interactiveSelector) => {
    const bounds = element.getBoundingClientRect();
    for (let y = bounds.top + 2; y < bounds.bottom - 2; y += 4) {
      for (let x = bounds.left + 2; x < bounds.right - 2; x += 4) {
        const hit = document.elementFromPoint(x, y);
        if (hit !== null && element.contains(hit) && hit.closest(interactiveSelector) === null) return { x, y };
      }
    }
    throw new Error("draggable surface has no bare point");
  }, dragInteractionSelector);
}

async function pointerDrag(page, source, target, below = false) {
  for (let attempt = 0; ; attempt++) {
    try {
      await source.scrollIntoViewIfNeeded();
      break;
    } catch (error) {
      if (attempt >= 2 || !String(error).includes("not attached")) throw error;
    }
  }
  const dragPoint = await bareDragPoint(source);
  await page.mouse.move(dragPoint.x, dragPoint.y);
  await page.mouse.down();
  await page.mouse.move(dragPoint.x + 8, dragPoint.y + 8);
  await expect(source).toHaveClass(/dragging/);
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
  const inProgressColumn = releaseLane.locator(".board-column[data-status='in_progress']");
  const relatedID = await page.locator(".board-card").first().getAttribute("data-issue");
  expect(relatedID).toBeTruthy();
  await inProgressColumn.getByRole("button", { name: /Create in progress issue/ }).click();
  const createDialog = page.getByRole("dialog", { name: "New issue" });
  await expect(createDialog.getByLabel("Workspace")).toHaveValue("demo");
  await expect(createDialog.getByLabel("Workspace")).toBeDisabled();
  await expect(createDialog).toContainText("Epic:");
  await expect(createDialog.getByText(/Assign to me/).locator("input")).toBeChecked();
  await createDialog.getByLabel("Title").fill("Created from the board");
  await createDialog.locator("select[name='type']").selectOption("feature");
  await createDialog.getByLabel("Priority").selectOption("1");
  await createDialog.getByRole("button", { name: /Add relation/ }).click();
  await createDialog.getByLabel("Relation type").selectOption("related");
  await createDialog.getByLabel("Other issue ID").fill(relatedID);
  await createDialog.getByRole("button", { name: "Add relation", exact: true }).click();
  await expect(createDialog.locator(".issue-create-resource-list.relations")).toContainText(relatedID);
  await createDialog.getByLabel("Attachment files").setInputFiles({
    name: "release-notes.txt",
    mimeType: "text/plain",
    buffer: Buffer.from("Ready to publish.\n"),
  });
  await expect(createDialog.locator(".issue-create-resource-list.attachments")).toContainText("release-notes.txt");
  await createDialog.getByRole("button", { name: "Create issue" }).click();
  await expect(page).toHaveURL(/#\/issues\/demo-[0-9a-f]+$/);
  const createdID = page.url().split("/").at(-1);
  const created = await page.evaluate(async (id) => (await (await fetch(`api/issues/${id}`)).json()), createdID);
  const caller = await page.evaluate(async () => (await (await fetch("api/identity")).json()).identity);
  expect(created.type).toBe("feature");
  expect(created.priority).toBe(1);
  expect(created.relations.some((relation) => relation.type === "has-parent")).toBe(true);
  expect(created.relations.some((relation) => relation.type === "related" && relation.other === relatedID)).toBe(true);
  expect(created.attachments.map((attachment) => attachment.name)).toContain("release-notes.txt");
  if (caller !== "") expect(created.assignees).toContain(caller);

  // Creation waits for every staged upload before rendering the issue. A fast
  // failure must not race navigation ahead of a slower successful upload.
  await page.goto(`${baseURL}/#/issues`);
  await page.getByRole("button", { name: "New issue" }).click();
  const partialDialog = page.getByRole("dialog", { name: "New issue" });
  await partialDialog.getByLabel("Title").fill("Partially uploaded issue");
  await partialDialog.getByLabel("Attachment files").setInputFiles([
    { name: "fails.txt", mimeType: "text/plain", buffer: Buffer.from("reject me\n") },
    { name: "slow.txt", mimeType: "text/plain", buffer: Buffer.from("upload me\n") },
  ]);
  await page.route("**/api/issues/*/attachments?*", async (route) => {
    const name = new URL(route.request().url()).searchParams.get("name");
    if (name === "fails.txt") {
      await route.fulfill({ status: 500, contentType: "application/json", body: '{"error":"simulated upload failure"}' });
      return;
    }
    await new Promise((resolve) => setTimeout(resolve, 350));
    await route.continue();
  });
  await partialDialog.getByRole("button", { name: "Create issue" }).click();
  await expect(partialDialog.getByRole("button", { name: "Create issue" })).toBeDisabled();
  await page.waitForTimeout(100);
  await expect(page).toHaveURL(/#\/issues$/);
  await expect(page).toHaveURL(/#\/issues\/demo-[0-9a-f]+$/);
  await expect(page.locator(".app-notice-error")).toContainText("fails.txt (simulated upload failure)");
  await expect(page.locator(".attachment-section")).toContainText("slow.txt");
  await page.unroute("**/api/issues/*/attachments?*");
  await page.goto(`${baseURL}/#/boards`);

  await releaseLane.getByRole("button", { name: /Collapse Ship the 1.0 release.*swimlane/ }).click();
  await expect(releaseLane.locator(".board-columns")).toBeHidden();
	const initialNoEpicLane = page.locator(".board-lane", { has: page.getByRole("heading", { name: "No epic" }) });
	await initialNoEpicLane.getByRole("button", { name: /Collapse No epic swimlane/ }).click();
	await expect(initialNoEpicLane.locator(".board-columns")).toBeHidden();
  await page.reload();
  const persistedReleaseLane = page.locator(".board-lane", { has: page.getByRole("heading", { name: /demo Ship the 1.0 release/ }) });
  await expect(persistedReleaseLane.getByRole("button", { name: /Expand Ship the 1.0 release.*swimlane/ })).toBeVisible();
  await expect(persistedReleaseLane.locator(".board-columns")).toBeHidden();
	const persistedNoEpicLane = page.locator(".board-lane", { has: page.getByRole("heading", { name: "No epic" }) });
	await expect(persistedNoEpicLane.getByRole("button", { name: /Expand No epic swimlane/ })).toBeVisible();
	await expect(persistedNoEpicLane.locator(".board-columns")).toBeHidden();
  await persistedReleaseLane.getByRole("button", { name: /Expand Ship the 1.0 release.*swimlane/ }).click();
  await expect(persistedReleaseLane.locator(".board-columns")).toBeVisible();
	await persistedNoEpicLane.getByRole("button", { name: /Expand No epic swimlane/ }).click();

  const secondEpic = await page.evaluate(async () => {
    const response = await fetch("api/issues", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ workspace: "demo", title: "Platform epic", type: "epic" }),
    });
    if (!response.ok) throw new Error(await response.text());
    return response.json();
  });
  expect(secondEpic.workspace).toBe("demo");
  await page.reload();
  await expect(page.locator(".board-lane")).toHaveCount(3);

  // A drop on a card in the shared No epic lane must not bubble into the
  // column and accidentally assign manual rank across workspace boundaries.
  const otherIssue = await page.evaluate(async () => {
    const workspace = await fetch("api/workspaces", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ key: "other" }),
    });
    if (!workspace.ok) throw new Error(await workspace.text());
    const issue = await fetch("api/issues", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ workspace: "other", title: "Other workspace issue" }),
    });
    if (!issue.ok) throw new Error(await issue.text());
    return issue.json();
  });
  await page.reload();
  const crossWorkspaceSource = page.locator(`.board-card[data-issue="${otherIssue.id}"]`);
  const crossWorkspaceTarget = page.locator(".board-lane", { has: page.getByRole("heading", { name: "No epic" }) })
    .locator('.board-card[data-issue^="demo-"]').first();
  const transfer = await page.evaluateHandle(() => new DataTransfer());
  await crossWorkspaceSource.dispatchEvent("dragstart", { dataTransfer: transfer });
  await crossWorkspaceTarget.dispatchEvent("drop", { dataTransfer: transfer });
  await expect.poll(() => page.evaluate(async (id) => (await (await fetch(`api/issues/${id}`)).json()).order, otherIssue.id)).toBe(0);

  // A failed card-to-card move must not reset the status control of the card
  // used as the drop target; that card represents a different issue.
  const failedMoveSource = page.locator(".board-card", { hasText: "Build the full text search index" });
  const failedMoveTarget = page.locator(".board-card", { hasText: "Browse the widget catalogue" });
  const targetStatus = failedMoveTarget.getByLabel(/Status for demo-/);
  await expect(targetStatus).toHaveValue("in_progress");
  await page.route("**/api/issues/*/move", async (route) => {
    await route.fulfill({ status: 500, contentType: "application/json", body: '{"message":"simulated failure"}' });
  }, { times: 1 });
  const failedTransfer = await page.evaluateHandle(() => new DataTransfer());
  await failedMoveSource.dispatchEvent("dragstart", { dataTransfer: failedTransfer });
  await failedMoveTarget.dispatchEvent("drop", { dataTransfer: failedTransfer });
  await expect(failedMoveTarget.locator(".edit-error")).toBeVisible();
  await expect(targetStatus).toHaveValue("in_progress");

  await page.getByRole("button", { name: "Save as view" }).click();
  const dialog = page.getByRole("dialog", { name: "Save board view" });
  await dialog.getByLabel("Name").fill("Release train");
  await dialog.getByText("Anyone with the link").click();
  await dialog.getByRole("button", { name: "Save view" }).click();
  await expect(page).toHaveURL(/#\/boards\/view-[0-9a-f]{24}$/);
  const savedViewURL = page.url();
  await expect(page.locator(".board-summary")).toContainText("Release train");
  await expect(page.getByRole("button", { name: "Copy link" })).toBeVisible();

  const closeControl = page.locator(".board-card", { hasText: "Build the full text search index" }).getByLabel(/Status for demo-/);
  await closeControl.selectOption("closed");
  const closeDialog = page.getByRole("dialog", { name: "Close issue?" });
  await expect(closeDialog).toContainText("This can be reopened later.");
  await closeDialog.getByRole("button", { name: "No" }).click();
  await expect(closeControl).toHaveValue("open");

  // Epic-to-epic, epic-to-No-epic, and No-epic-to-epic all preserve the
  // immutable demo workspace while status and sparse position move atomically.
  const openCard = page.locator(".board-card", { hasText: "Build the full text search index" });
  const platformLane = page.locator(".board-lane", { has: page.getByRole("heading", { name: /demo Platform epic/ }) });
  await pointerDrag(page, openCard, platformLane.locator(".board-column[data-status='open']"));
  await expect(platformLane.locator(".board-column[data-status='open']", { hasText: "Build the full text search index" })).toBeVisible();

  const noEpicLane = page.locator(".board-lane", { has: page.getByRole("heading", { name: "No epic" }) });
  await pointerDrag(page, platformLane.locator(".board-card", { hasText: "Build the full text search index" }),
    noEpicLane.locator(".board-column[data-status='in_progress']"));
  await expect(noEpicLane.locator(".board-column[data-status='in_progress']", { hasText: "Build the full text search index" })).toBeVisible();

  const currentReleaseLane = page.locator(".board-lane", { has: page.getByRole("heading", { name: /demo Ship the 1.0 release/ }) });
  await pointerDrag(page, noEpicLane.locator(".board-card", { hasText: "Build the full text search index" }),
    currentReleaseLane.locator(".board-column[data-status='in_progress']"));
  await expect(currentReleaseLane.locator(".board-column[data-status='in_progress']", { hasText: "Build the full text search index" })).toBeVisible();

  // The same gesture reorders within one epic/status cell.
  const movedCard = page.locator(".board-card", { hasText: "Build the full text search index" });
  const destinationCard = page.locator(".board-card", { hasText: "Browse the widget catalogue" });
  await pointerDrag(page, movedCard, destinationCard);
  const releaseTitles = await currentReleaseLane.locator(".board-column[data-status='in_progress'] .board-card .name .title").allTextContents();
  expect(releaseTitles.indexOf("Build the full text search index")).toBeLessThan(releaseTitles.indexOf("Browse the widget catalogue"));

  // Natural issue lists expose the same sparse manual order as row drag/drop.
  await page.goto(`${baseURL}/#/issues?include-closed=true&size=25`);
  await expect(page.getByRole("button", { name: "New issue" })).toBeVisible();
  const sourceRow = page.locator(".issue-table tbody tr", { hasText: "Browse the widget catalogue" });
  const targetRow = page.locator(".issue-table tbody tr", { hasText: "Build the full text search index" });
  await expect(sourceRow).toBeVisible();
  await expect(targetRow).toBeVisible();
  await pointerDrag(page, sourceRow, targetRow);
  await expect.poll(async () => {
    const rowTitles = await page.locator(".issue-table tbody tr .name .title").allTextContents();
    return rowTitles.indexOf("Browse the widget catalogue") < rowTitles.indexOf("Build the full text search index");
  }).toBe(true);

	await page.setViewportSize({ width: 710, height: 900 });
	await page.goto(`${baseURL}/#/boards`);
	const breakpointCard = page.locator(".board-card").first();
	await expect(breakpointCard).toBeVisible();
	await expect.poll(() => breakpointCard.evaluate((card) => card.draggable)).toBe(true);
	const breakpointStatus = breakpointCard.locator("select").first();
	await breakpointStatus.dispatchEvent("pointerdown", { pointerId: 1 });
	await expect.poll(() => breakpointCard.evaluate((card) => card.draggable)).toBe(false);
	await page.locator("body").dispatchEvent("pointerup", { pointerId: 1 });
	await expect.poll(() => breakpointCard.evaluate((card) => card.draggable)).toBe(true);

  await page.setViewportSize({ width: 390, height: 844 });
  await page.goto(`${baseURL}/#/boards`);
  await expect(page.locator(".board-columns").first()).toHaveCSS("overflow-x", "auto");
  await page.evaluate(async () => {
    for (let index = 0; index < 10; index++) {
      const response = await fetch("api/issues", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ workspace: "demo", title: `Overflow epic ${index}`, type: "epic" }),
      });
      if (!response.ok) throw new Error(await response.text());
    }
  });
  await page.reload();
  const responsiveCard = page.locator('.board-card[data-issue^="demo-"]').first();
  const movedID = await responsiveCard.getAttribute("data-issue");
  expect(movedID).toBeTruthy();
  await expect.poll(() => responsiveCard.evaluate((card) => card.draggable)).toBe(false);
  await expect(page.locator(".board-card-drag, .board-card-order-button, .list-row-drag, .list-row-order-button")).toHaveCount(0);
  const epicControl = responsiveCard.getByLabel(/Epic for demo-/);
  const initialEpicValues = await epicControl.locator("option").evaluateAll((options) => options.map((option) => option.value));
  await page.getByRole("button", { name: /Load up to .* more epics/ }).click();
  await expect.poll(async () => epicControl.locator("option").count()).toBeGreaterThan(initialEpicValues.length);
  const loadedEpicValues = await epicControl.locator("option").evaluateAll((options) => options.map((option) => option.value));
  const pagedEpic = loadedEpicValues.find((value) => !initialEpicValues.includes(value));
  expect(pagedEpic).toBeTruthy();
  await responsiveCard.getByLabel(/Epic for demo-/).selectOption(pagedEpic);
  await expect.poll(async () => page.evaluate(async ({ id, epic }) => {
    const issue = await (await fetch(`api/issues/${id}`)).json();
    return issue.relations.some((relation) => relation.type === "has-parent" && relation.other === epic);
  }, { id: movedID, epic: pagedEpic })).toBe(true);

  await page.goto(savedViewURL);
  await page.getByRole("button", { name: "Edit view" }).click();
  await page.getByRole("dialog", { name: "Edit board view" }).getByRole("button", { name: "Delete" }).click();
  const deleteDialog = page.getByRole("dialog", { name: "Delete board view?" });
  await expect(deleteDialog).toContainText("Issues are not affected.");
  await deleteDialog.getByRole("button", { name: "Yes" }).click();
  await expect(page).toHaveURL(/#\/boards$/);
});
