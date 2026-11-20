import { expect, test } from "@playwright/test";

test("@P0 artifacts decode images in the viewer", async ({ page }) => {
	await page.goto("/e2e/media-preview/harness.html?type=image");
	const image = page.getByRole("img", { name: "logo.png" });
	await expect(image).toBeVisible();
	await expect.poll(() => image.evaluate((element: HTMLImageElement) => element.naturalWidth)).toBeGreaterThan(0);
});

test("@P0 artifacts play and seek video with the app's controls", async ({ page }) => {
	await page.goto("/e2e/media-preview/harness.html?type=video");
	const video = page.locator("video");
	await expect(video).not.toHaveAttribute("controls");
	await expect.poll(() => video.evaluate((element: HTMLVideoElement) => element.duration)).toBeGreaterThan(0);
	await expect(video).toHaveJSProperty("paused", true);
	await page.getByRole("button", { name: "Mute", exact: true }).click();
	await expect(video).toHaveJSProperty("muted", true);
	await page.getByRole("button", { name: "Play", exact: true }).click();
	await expect.poll(() => video.evaluate((element: HTMLVideoElement) => element.currentTime)).toBeGreaterThan(0);
	await page.getByRole("button", { name: "Pause", exact: true }).click();
	await expect(video).toHaveJSProperty("paused", true);
	// The clip is shorter than one 5 s skip, so the arrows clamp to either end.
	await page.getByRole("slider", { name: "Seek" }).focus();
	await page.keyboard.press("ArrowRight");
	await expect.poll(() => video.evaluate((element: HTMLVideoElement) => element.duration - element.currentTime)).toBeLessThan(0.1);
	await page.keyboard.press("ArrowLeft");
	await expect.poll(() => video.evaluate((element: HTMLVideoElement) => element.currentTime)).toBeLessThan(0.1);
});

for (const type of ["audio"] as const) {
	test(`@P0 artifacts play and seek ${type} with native controls`, async ({ page }) => {
		await page.goto(`/e2e/media-preview/harness.html?type=${type}`);
		const media = page.locator(type);
		await expect(media).toHaveAttribute("controls");
		await expect.poll(() => media.evaluate((element: HTMLMediaElement) => element.duration)).toBeGreaterThan(0);
		await expect(media).toHaveJSProperty("paused", true);
		await media.evaluate((element: HTMLMediaElement) => {
			element.muted = true;
			return element.play();
		});
		await expect.poll(() => media.evaluate((element: HTMLMediaElement) => element.currentTime)).toBeGreaterThan(0);
		await media.evaluate((element: HTMLMediaElement) => {
			element.pause();
			element.currentTime = element.duration / 2;
		});
		await expect.poll(() => media.evaluate((element: HTMLMediaElement) => Math.abs(element.currentTime - element.duration / 2))).toBeLessThan(0.1);
	});
}

for (const type of ["image", "video", "audio"] as const) {
	test(`@P0 ${type} fits and centers in wide and narrow viewer panes`, async ({ page }) => {
		for (const viewport of [{ width: 1280, height: 800 }, { width: 400, height: 700 }]) {
			await page.setViewportSize(viewport);
			await page.goto(`/e2e/media-preview/harness.html?type=${type}`);
			const media = page.locator(type === "image" ? "img" : type);
			await expect(media).toBeVisible();
			const pane = page.locator("main > div");
			await expect.poll(async () => {
				const mediaBox = await media.boundingBox();
				const paneBox = await pane.boundingBox();
				if (!mediaBox || !paneBox) return false;
				return mediaBox.x >= paneBox.x && mediaBox.y >= paneBox.y
					&& mediaBox.x + mediaBox.width <= paneBox.x + paneBox.width
					&& mediaBox.y + mediaBox.height <= paneBox.y + paneBox.height
					&& Math.abs(mediaBox.y + mediaBox.height / 2 - paneBox.y - paneBox.height / 2) < 24
					&& (type === "audio" || (mediaBox.width > paneBox.width * 0.9 && mediaBox.height > paneBox.height * 0.8));
			}).toBe(true);
			if (type !== "audio") await expect(media).toHaveCSS("object-fit", "contain");
			expect(await pane.evaluate((element) => element.scrollHeight <= element.clientHeight)).toBe(true);
		}
	});
}
