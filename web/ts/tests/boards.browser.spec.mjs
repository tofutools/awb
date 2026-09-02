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

async function bareDropPoint(target, below) {
  return target.evaluate((element, { interactiveSelector, below }) => {
    const bounds = element.getBoundingClientRect();
    const midpoint = bounds.top + bounds.height / 2;
    const start = below ? bounds.bottom - 8 : bounds.top + 8;
    const end = below ? midpoint : midpoint;
    const step = below ? -4 : 4;
    for (let y = start; below ? y >= end : y <= end; y += step) {
      for (let x = bounds.left + 8; x < bounds.right - 8; x += 4) {
        const hit = document.elementFromPoint(x, y);
        if (hit !== null && element.contains(hit) && hit.closest(interactiveSelector) === null) return { x, y };
      }
    }
    throw new Error("drop target has no bare point in the requested half");
  }, { interactiveSelector: dragInteractionSelector, below });
}

async function pointerDrag(page, source, target, below = false) {
  const boardTarget = await target.evaluate((element) => element.classList.contains("board-card"));
  const idleTargetBackground = boardTarget
    ? await target.evaluate((element) => getComputedStyle(element).backgroundColor)
    : "";
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
  expect(await source.evaluate((element) => Number(getComputedStyle(element).opacity))).toBeGreaterThan(0.6);
  await target.scrollIntoViewIfNeeded();
  const positionalTarget = await target.evaluate((element) => element.classList.contains("board-card") || element instanceof HTMLTableRowElement);
  const targetBox = positionalTarget ? null : await target.boundingBox();
  if (!positionalTarget) expect(targetBox).not.toBeNull();
  const targetPoint = positionalTarget
    ? await bareDropPoint(target, below)
    : {
        x: targetBox.x + targetBox.width / 2,
        y: targetBox.y + (below ? targetBox.height * 0.8 : targetBox.height * 0.2),
      };
  await page.mouse.move(
    targetPoint.x,
    targetPoint.y,
    { steps: 12 },
  );
  await expect(source).toHaveClass(/dragging/);
  if (positionalTarget) {
    await expect(target).toHaveClass(below ? /drop-after/ : /drop-before/);
    if (await target.evaluate((element) => element.classList.contains("board-card"))) {
      expect(await target.evaluate((element, after) =>
        (after ? element.nextElementSibling : element.previousElementSibling)?.classList.contains("board-drop-indicator") === true,
      below)).toBe(true);
      await expect.poll(() => target.evaluate((element) => getComputedStyle(element).backgroundColor)).toBe(idleTargetBackground);
    }
  }
  await page.mouse.up();
}

async function columnBackgroundDrag(page, source, column, target) {
  const transfer = await page.evaluateHandle(() => new DataTransfer());
  const targetBox = await target.boundingBox();
  expect(targetBox).not.toBeNull();
  const cards = column.locator(".board-cards");
  await source.dispatchEvent("dragstart", { dataTransfer: transfer });
  await cards.dispatchEvent("dragover", { clientY: targetBox.y + 1, dataTransfer: transfer });
  await expect(target).toHaveClass(/drop-before/);
  await expect(column.locator(".board-drop-indicator")).toHaveCount(1);
  expect(await target.evaluate((element) => element.previousElementSibling?.classList.contains("board-drop-indicator") === true)).toBe(true);
  await expect(column.locator(".drop-after")).toHaveCount(0);
  await cards.dispatchEvent("drop", { clientY: targetBox.y + 1, dataTransfer: transfer });
  await source.dispatchEvent("dragend", { dataTransfer: transfer });
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
  const hoverCard = page.locator(".board-card").first();
  const idleBackground = await hoverCard.evaluate((card) => getComputedStyle(card).backgroundColor);
  await hoverCard.hover();
  await expect.poll(() => hoverCard.evaluate((card) => getComputedStyle(card).backgroundColor)).not.toBe(idleBackground);

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
  await expect(page).toHaveURL(/#\/boards$/);
  await expect(page.locator(".app-notice")).toContainText(/Issue demo-[0-9a-f]+ was created\./);
  const createdCard = page.locator(".board-card", { hasText: "Created from the board" });
  await expect(createdCard).toBeVisible();
  const createdID = await createdCard.getAttribute("data-issue");
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
  await expect(page.locator(".app-notice-error")).toContainText("fails.txt (simulated upload failure)");
  const partialRow = page.locator(".issue-table tbody tr", { hasText: "Partially uploaded issue" });
  await expect(partialRow).toBeVisible();
  const partialHref = await partialRow.locator("a[href^='#/issues/']").getAttribute("href");
  const partialID = partialHref?.split("/").at(-1);
  const partialIssue = await page.evaluate(async (id) => (await (await fetch(`api/issues/${id}`)).json()), partialID);
  expect(partialIssue.attachments.map((attachment) => attachment.name)).toContain("slow.txt");
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
  const assignedIssueID = await failedMoveTarget.getAttribute("data-issue");
  expect(assignedIssueID).toBeTruthy();
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

  // Opening assigned work is allowed for every assignee set, but the UI must
  // make the automatic unassignment explicit before sending the move.
  await targetStatus.selectOption("open");
  let openDialog = page.getByRole("dialog", { name: "Move issue to Open?" });
  await expect(openDialog).toContainText("This will unassign alice, bob.");
  await openDialog.getByRole("button", { name: "No" }).click();
  await expect(targetStatus).toHaveValue("in_progress");
  await targetStatus.selectOption("open");
  openDialog = page.getByRole("dialog", { name: "Move issue to Open?" });
  await openDialog.getByRole("button", { name: "Yes" }).click();
  await expect.poll(() => page.evaluate(async (id) => {
    const issue = await (await fetch(`api/issues/${id}`)).json();
    return { status: issue.status, assignees: issue.assignees };
  }, assignedIssueID)).toEqual({ status: "open", assignees: [] });
  await page.locator(".board-card", { hasText: "Browse the widget catalogue" }).getByLabel(/Status for demo-/).selectOption("in_progress");
  await expect.poll(() => page.locator(".board-card", { hasText: "Browse the widget catalogue" }).getByText(/@/).count()).toBe(1);

  // Restarting closed work is a new claim by the caller, regardless of who
  // completed it previously.
  const closedCard = page.locator(".board-card", { hasText: "Design the catalogue database schema" });
  await closedCard.getByLabel(/Status for demo-/).selectOption("in_progress");
  await expect.poll(() => page.locator(".board-card", { hasText: "Design the catalogue database schema" }).getByText(/@/).count()).toBe(1);

  await page.getByRole("button", { name: "Save as view" }).click();
  const dialog = page.getByRole("dialog", { name: "Save board view" });
  await dialog.getByLabel("Name").fill("Release train");
  await dialog.getByText("Anyone with the link").click();
  const epicScope = dialog.locator(".board-view-scope-card", { hasText: "Epic lanes" });
  await epicScope.getByText("Selected", { exact: true }).click();
  await epicScope.locator(".board-view-choice", { hasText: "Ship the 1.0 release" }).click();
  await epicScope.getByText("No epic", { exact: true }).click();
  await dialog.getByRole("button", { name: "Save view" }).click();
  await expect(page).toHaveURL(/#\/boards\/view-[0-9a-f]{24}$/);
  const savedViewURL = page.url();
  await expect(page.locator(".board-summary")).toContainText("Release train");
  await expect(page.locator(".board-summary")).toContainText("1 lane selection");
  await expect(page.locator(".board-lane")).toHaveCount(1);
  await expect(page.getByRole("button", { name: "Copy link" })).toBeVisible();

  await page.getByRole("button", { name: "Edit view" }).click();
  const editView = page.getByRole("dialog", { name: "Edit board view" });
  await expect(editView.getByRole("button", { name: "Delete view" })).toBeVisible();
  const editEpicScope = editView.locator(".board-view-scope-card", { hasText: "Epic lanes" });
  await editEpicScope.getByText("All", { exact: true }).click();
  await editEpicScope.getByText("No epic", { exact: true }).click();
  await editView.getByRole("button", { name: "Save changes" }).click();
  await expect(page.locator(".board-summary")).toContainText("All epic lanes");

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
  const emptyPlatformColumn = platformLane.locator(".board-column[data-status='open']");
  const emptyTransfer = await page.evaluateHandle(() => new DataTransfer());
  await openCard.dispatchEvent("dragstart", { dataTransfer: emptyTransfer });
  await emptyPlatformColumn.dispatchEvent("dragover", { dataTransfer: emptyTransfer });
  await expect(emptyPlatformColumn).toHaveClass(/drop-empty/);
  await expect(emptyPlatformColumn.locator(".board-drop-indicator")).toHaveCSS("height", "4px");
  await openCard.dispatchEvent("dragend", { dataTransfer: emptyTransfer });
  await pointerDrag(page, openCard, emptyPlatformColumn);
  await expect(platformLane.locator(".board-column[data-status='open']", { hasText: "Build the full text search index" })).toBeVisible();

  const noEpicLane = page.locator(".board-lane", { has: page.getByRole("heading", { name: "No epic" }) });
  await pointerDrag(page, platformLane.locator(".board-card", { hasText: "Build the full text search index" }),
    noEpicLane.locator(".board-column[data-status='in_progress']"));
  await expect(noEpicLane.locator(".board-column[data-status='in_progress']", { hasText: "Build the full text search index" })).toBeVisible();

  const currentReleaseLane = page.locator(".board-lane", { has: page.getByRole("heading", { name: /demo Ship the 1.0 release/ }) });
  await pointerDrag(page, noEpicLane.locator(".board-card", { hasText: "Build the full text search index" }),
    currentReleaseLane.locator(".board-column[data-status='in_progress']"));
  await expect(currentReleaseLane.locator(".board-column[data-status='in_progress']", { hasText: "Build the full text search index" })).toBeVisible();

  // The same gesture reorders within one epic/status cell. Each half of the
  // target card exposes, and applies, its corresponding insertion edge.
  await pointerDrag(
    page,
    page.locator(".board-card", { hasText: "Build the full text search index" }),
    page.locator(".board-card", { hasText: "Browse the widget catalogue" }),
    true,
  );
  const releaseTitles = () => currentReleaseLane.locator(".board-column[data-status='in_progress'] .board-card .name .title").allTextContents();
  await expect.poll(async () => {
    const titles = await releaseTitles();
    return titles.indexOf("Build the full text search index") > titles.indexOf("Browse the widget catalogue");
  }).toBe(true);
  await pointerDrag(
    page,
    page.locator(".board-card", { hasText: "Build the full text search index" }),
    page.locator(".board-card", { hasText: "Browse the widget catalogue" }),
  );
  await expect.poll(async () => {
    const titles = await releaseTitles();
    return titles.indexOf("Build the full text search index") < titles.indexOf("Browse the widget catalogue");
  }).toBe(true);

  // Column whitespace resolves to the nearest visible card edge instead of
  // presenting every populated column as an append-to-bottom target.
  const reorderedColumn = currentReleaseLane.locator(".board-column[data-status='in_progress']");
  const reorderedCards = reorderedColumn.locator(".board-card");
  const sourceID = await reorderedCards.last().getAttribute("data-issue");
  const targetID = await reorderedCards.first().getAttribute("data-issue");
  expect(sourceID).toBeTruthy();
  expect(targetID).toBeTruthy();
  await columnBackgroundDrag(
    page,
    reorderedColumn.locator(`.board-card[data-issue='${sourceID}']`),
    reorderedColumn,
    reorderedColumn.locator(`.board-card[data-issue='${targetID}']`),
  );
  await expect.poll(() => reorderedColumn.locator(".board-card").first().getAttribute("data-issue")).toBe(sourceID);

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

  const hideableLane = page.locator(".board-lane", { has: page.getByRole("heading", { name: /Overflow epic/ }) }).first();
  const hideableEpicID = await hideableLane.getByRole("button", { name: /Hide .* from boards/ }).getAttribute("aria-label")
    .then((label) => label?.split(" ")[1]);
  expect(hideableEpicID).toBeTruthy();
  await hideableLane.getByRole("button", { name: `Hide ${hideableEpicID} from boards` }).click();
  await expect(hideableLane).toHaveCount(0);
  await expect.poll(() => page.evaluate(async (id) => (await (await fetch(`api/issues/${id}`)).json()).board_hidden, hideableEpicID)).toBe(true);

  await page.goto(savedViewURL);
  await page.getByRole("button", { name: "Edit view" }).click();
  await page.getByRole("dialog", { name: "Edit board view" }).getByRole("button", { name: "Delete" }).click();
  const deleteDialog = page.getByRole("dialog", { name: "Delete board view?" });
  await expect(deleteDialog).toContainText("Issues are not affected.");
  await deleteDialog.getByRole("button", { name: "Yes" }).click();
  await expect(page).toHaveURL(/#\/boards$/);
});
