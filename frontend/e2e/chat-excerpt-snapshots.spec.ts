import { expect, test } from "@playwright/test";

for (const mixed of [false, true]) {
	test(`clickable filename ${mixed ? "with prose" : "alone"} captures and navigates from the whole preview @T0`, async ({ page }) => {
		await page.goto("/e2e/excerpt-snapshots/harness.html?filenames=1");
		const file = page.getByRole("button", { name: "Open src/app.ts in Files", exact: true });
		await expect(file).toBeVisible();
		await file.evaluate((element, mixed) => {
			const selected = mixed ? element.closest("p")! : element.querySelector("code")!;
			const range = document.createRange(); range.selectNodeContents(selected);
			window.getSelection()!.removeAllRanges(); window.getSelection()!.addRange(range);
			document.dispatchEvent(new PointerEvent("pointerup", { bubbles: true }));
		}, mixed);
		await page.getByRole("button", { name: "Add to chat", exact: true }).click();
		await page.getByRole("button", { name: "1 annotation", exact: true }).click();
		const text = mixed ? "Open src/app.ts here" : "src/app.ts";
		const preview = page.getByRole("dialog").locator("pre");
		await expect(preview).toHaveText(text);
		await preview.evaluate(element => {
			const range = document.createRange(); range.selectNodeContents(element);
			window.getSelection()!.removeAllRanges(); window.getSelection()!.addRange(range);
			element.dispatchEvent(new MouseEvent("click", { bubbles: true, detail: 1 }));
		});
		await expect(page.getByRole("dialog")).toBeVisible();
		await page.evaluate(() => window.getSelection()!.removeAllRanges());
		await preview.click();
		await expect(page.getByRole("dialog")).toHaveCount(0);
		await expect.poll(() => page.evaluate(() => {
			const highlight = CSS.highlights.get("chat-annotation");
			return highlight ? [...highlight].map(range => range.toString()).join("") : "";
		})).toBe(text);
		await page.getByLabel("Message the agent").fill("Explain this reference.");
		await page.getByRole("button", { name: "Send message", exact: true }).click();
		await expect(page.getByTestId("sent-payload")).not.toHaveText("[]");
		const [sent] = JSON.parse(await page.getByTestId("sent-payload").textContent() ?? "[]");
		expect(sent.text).toBe(text);
		expect(sent.snapshot.rawText).toBe(mixed ? "Open `src/app.ts` here" : "src/app.ts");
		expect(sent.snapshot.turn.messages[1].text).toBe("Open `src/app.ts` here");
		await page.locator('[data-chat-message-id="sent"]').getByRole("button", { name: "1 annotation" }).click();
		await expect(page.getByRole("dialog").locator("pre")).toHaveText(text);
		await page.getByRole("dialog").locator("pre").click();
		await expect(page.getByRole("dialog")).toHaveCount(0);
	});
}

test("diagram annotations navigate to the selected repeated block and fall back after edits @T0", async ({ page }) => {
	await page.setViewportSize({ width: 1100, height: 800 });
	await page.goto("/e2e/excerpt-snapshots/harness.html?navigation=1");
	const blocks = page.locator('[data-chat-message-id="source"] [data-excerpt-flowchart]');
	const target = blocks.nth(1);
	await target.getByRole("button", { name: "Code", exact: true }).click();
	await target.locator("pre").evaluate(element => {
		const range = document.createRange(); range.selectNodeContents(element);
		window.getSelection()!.removeAllRanges(); window.getSelection()!.addRange(range);
		document.dispatchEvent(new PointerEvent("pointerup", { bubbles: true }));
	});
	await page.getByRole("button", { name: "Add to chat", exact: true }).click();
	const scroller = page.locator(".chat-scroll-viewport");
	const targetVisible = () => target.evaluate(element => {
		const rect = element.getBoundingClientRect();
		const view = element.closest(".chat-scroll-viewport")!.getBoundingClientRect();
		return rect.top >= view.top && rect.bottom <= view.bottom;
	});
	const scrollAway = async () => {
		// Reader scroll intent cancels source-anchor following before moving away.
		await scroller.dispatchEvent("wheel");
		await scroller.evaluate(element => { element.scrollTop = 0; });
		await expect.poll(targetVisible).toBe(false);
	};
	await scrollAway();
	await page.getByRole("button", { name: "1 annotation", exact: true }).click();
	await page.getByRole("button", { name: "Flowchart attached", exact: true }).click();
	await expect.poll(targetVisible).toBe(true);
	// Simulate the late layout shift caused by diagrams above the target rendering.
	await target.evaluate(element => {
		const spacer = document.createElement("div"); spacer.style.height = "800px";
		element.before(spacer);
	});
	await expect.poll(targetVisible).toBe(true);
	expect(await page.evaluate(() => CSS.highlights.has("chat-annotation"))).toBe(false);
	await expect(page.locator(".chat-annotation-target")).toHaveCount(0);
	// Once the user scrolls, later layout changes must not pull them back.
	await scroller.dispatchEvent("wheel");
	await target.evaluate(element => {
		const spacer = document.createElement("div"); spacer.style.height = "800px";
		element.before(spacer);
	});
	await page.evaluate(() => new Promise<void>(resolve => requestAnimationFrame(() => requestAnimationFrame(() => resolve()))));
	expect(await targetVisible()).toBe(false);
	await page.getByLabel("Message the agent").fill("Explain the flowchart.");
	await page.getByRole("button", { name: "Send message", exact: true }).click();
	await expect(page.getByTestId("sent-payload")).not.toHaveText("[]");
	const sent = JSON.parse(await page.getByTestId("sent-payload").textContent() ?? "[]");
	expect(sent[0].text).toBe("");
	const positions = await target.evaluate(element => ({ start: Number((element as HTMLElement).dataset.excerptStart), end: Number((element as HTMLElement).dataset.excerptEnd), offsetUnit: "utf-16" }));
	expect(sent[0].snapshot.diagramRange).toEqual(positions);
	await scrollAway();
	await page.locator('[data-chat-message-id="sent"]').getByRole("button", { name: "1 annotation" }).click();
	await page.getByRole("button", { name: "Flowchart attached", exact: true }).click();
	await expect.poll(targetVisible).toBe(true);
	await page.screenshot({ path: test.info().outputPath("diagram-navigation.png") });
	await page.getByRole("button", { name: "Change source revision" }).click();
	await scrollAway();
	await page.locator('[data-chat-message-id="sent"]').getByRole("button", { name: "1 annotation" }).click();
	await page.getByRole("button", { name: "Flowchart attached", exact: true }).click();
	await expect.poll(targetVisible).toBe(false);
	await expect(page.locator('[data-chat-message-id="source"]')).toBeInViewport();
	expect(await page.evaluate(() => CSS.highlights.has("chat-annotation"))).toBe(false);
});

test("diagram source stays hidden behind the flowchart badge @T0", async ({ page }) => {
	await page.goto("/e2e/excerpt-snapshots/harness.html?diagram=1");
	await page.getByRole("button", { name: "Code", exact: true }).click();
	const source = page.locator("[data-excerpt-flowchart] pre");
	await source.scrollIntoViewIfNeeded();
	await source.evaluate(element => {
		const range = document.createRange(); range.selectNodeContents(element);
		window.getSelection()!.removeAllRanges(); window.getSelection()!.addRange(range);
		document.dispatchEvent(new PointerEvent("pointerup", { bubbles: true }));
	});
	await page.getByRole("button", { name: "Add to chat", exact: true }).click();
	await page.getByRole("button", { name: "1 annotation", exact: true }).click();
	const preview = page.getByRole("dialog");
	await expect(preview.getByText("Flowchart attached", { exact: true })).toBeVisible();
	const badge = preview.getByText("Flowchart attached", { exact: true });
	await expect(badge.locator("..")).toContainText("1. Selected text:");
	await expect(preview).not.toContainText("flowchart LR");
	const close = preview.getByRole("button", { name: "Remove annotation 1", exact: true });
	const resting = await close.evaluate(element => getComputedStyle(element).color);
	await close.hover();
	const hover = await close.evaluate(element => ({ color: getComputedStyle(element).color, background: getComputedStyle(element).backgroundColor, before: getComputedStyle(element, "::before").display, transition: getComputedStyle(element).transitionProperty }));
	expect(hover.color).not.toBe(resting);
	expect(hover.background).toBe("rgba(0, 0, 0, 0)");
	expect(hover.before).toBe("none");
	expect(hover.transition).toBe("none");
	await page.screenshot({ path: test.info().outputPath("flowchart-badge.png") });
	await page.keyboard.press("Escape");
	await page.getByLabel("Message the agent").fill("Explain the first annotation.");
	await page.getByRole("button", { name: "Send message", exact: true }).click();
	await expect(page.getByTestId("sent-payload")).not.toHaveText("[]");
	const sent = JSON.parse(await page.getByTestId("sent-payload").textContent() ?? "[]");
	expect(sent[0].text).toBe("");
	expect(sent[0].snapshot.rawText).toContain("flowchart LR");
	expect(sent[0].snapshot.badgeOnly).toBe(true);
	await page.locator('[data-chat-message-id="sent"]').getByRole("button", { name: "1 annotation" }).click();
	await expect(page.getByRole("dialog").getByText("Flowchart attached", { exact: true })).toBeVisible();
});

test("reply controls retain the existing floating layout on hover and focus @T0", async ({ page }) => {
	await page.setViewportSize({ width: 1000, height: 850 });
	await page.goto("/e2e/excerpt-snapshots/harness.html");
	await page.locator('[data-chat-message-id="question"]').hover();
	const edit = page.getByRole("button", { name: "Copy user message", exact: true });
	await expect(edit).toBeVisible();
	const assertFloating = async () => {
		expect(await edit.evaluate(element => {
			const row = element.closest(".h-7");
			if (!row) throw new Error("Reply action row missing");
			return getComputedStyle(row).position;
		})).toBe("absolute");
	};
	await page.screenshot({ path: test.info().outputPath("reply-controls.png") });
	await assertFloating();
	await edit.focus();
	await page.mouse.move(0, 0);
	await expect(edit).toBeFocused();
	await assertFloating();
	await page.setViewportSize({ width: 400, height: 850 });
	await assertFloating();
});

test("captured excerpts span formatting and code and survive source deletion @T0", async ({ page }) => {
	await page.setViewportSize({ width: 1200, height: 900 });
	await page.emulateMedia({ reducedMotion: "reduce" });
	await page.goto("/e2e/excerpt-snapshots/harness.html");
	const body = page.locator('[data-chat-message-id="source"] [data-chat-message-body]');
	await expect(body).toContainText("return total;");
	await body.evaluate(element => {
		const first = element.querySelector("strong")!.firstChild!;
		const code = element.querySelector("pre code")!;
		const walker = document.createTreeWalker(code, NodeFilter.SHOW_TEXT);
		let last: Text | undefined;
		while (walker.nextNode()) if ((walker.currentNode as Text).data.includes("total")) last = walker.currentNode as Text;
		if (!last) throw new Error("Code text missing");
		const range = document.createRange(); range.setStart(first, 0); range.setEnd(last, last.data.indexOf("total") + 5);
		window.getSelection()!.removeAllRanges(); window.getSelection()!.addRange(range);
		document.dispatchEvent(new PointerEvent("pointerup", { bubbles: true }));
	});
	await page.getByRole("button", { name: "Add to chat", exact: true }).click();
	await page.getByRole("button", { name: "1 annotation", exact: true }).click();
	await expect(page.getByText("1. Selected text:", { exact: true })).toBeVisible();
    await expect(page.getByText("View raw excerpt", { exact: true })).toHaveCount(0);
    await expect(page.getByText("Completed turn context", { exact: true })).toHaveCount(0);
	await page.screenshot({ path: test.info().outputPath("composer-excerpt.png") });
	await page.keyboard.press("Escape");
	await page.getByRole("button", { name: "1 annotation", exact: true }).click();
	await page.getByRole("button", { name: /^bold text and italic text\./ }).click();
	await expect.poll(() => page.evaluate(() => CSS.highlights.has("chat-annotation"))).toBe(true);
	await page.getByRole("button", { name: "Remove source message" }).click();
	await page.getByLabel("Message the agent").fill("Explain the selected code.");
	await page.getByRole("button", { name: "Send message", exact: true }).click();
	await expect(page.getByTestId("sent-payload")).toContainText('"version":1');
	const sent = JSON.parse(await page.getByTestId("sent-payload").textContent() ?? "[]");
	expect(sent[0].text).toContain("bold text and italic text.");
	expect(sent[0].text).not.toContain("Copy code");
	expect(sent[0].snapshot.rawText).toContain("```ts");
	expect(sent[0].snapshot.turn.messages).toHaveLength(2);
	await page.locator('[data-chat-message-id="sent"]').getByRole("button", { name: "1 annotation" }).click();
	await page.screenshot({ path: test.info().outputPath("sent-excerpt.png") });
	await page.getByRole("button", { name: sent[0].text, exact: true }).click();
	await expect(page.getByText("The referenced message is no longer available in this chat.")).toBeVisible();
});
