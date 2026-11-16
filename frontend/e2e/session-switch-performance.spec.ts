import { expect, test } from "@playwright/test";
import { installSessionSwitchFixture, measureSessionSwitch } from "./performance/session-switch";

// AO_PERF_APP=1 AO_PERF_PRODUCTION=1 AO_PERF_BENCH=1 AO_E2E_PORT=5497 \
//   npm run test:e2e -- session-switch-performance --repeat-each=5 --workers=1
test("production first opens and session revisits", async ({ page, browser }, testInfo) => {
	test.skip(process.env.AO_PERF_BENCH !== "1", "Opt-in evidence; no hardware-dependent timing threshold");
	test.setTimeout(120_000);
	await page.setViewportSize({ width: 1440, height: 1000 });
	await page.emulateMedia({ reducedMotion: "no-preference" });
	const errors: string[] = [];
	page.on("pageerror", (error) => errors.push(error.message));
	page.on("requestfailed", (request) => errors.push(`${request.url()}: ${request.failure()?.errorText}`));
	page.on("response", (response) => { if (response.status() >= 400) errors.push(`${response.status()}: ${response.url()}`); });
	await installSessionSwitchFixture(page);
	await page.goto("/#/projects/fake-proj");
	await page.getByRole("button", { name: "Show 4 more agents", exact: true }).click();
	await page.getByRole("button", { name: "Open Bench 0", exact: true }).waitFor();
	await page.mouse.move(0, 0);
	// Let the existing idle chunk preload run before the first session open.
	await page.waitForTimeout(2500);
	const samples = [];
	for (const id of [...Array.from({ length: 10 }, (_, i) => i), ...Array.from({ length: 20 }, (_, i) => i % 4)]) {
		const sample = await measureSessionSwitch(page, id);
		expect(sample.turns).toBe(18);
		expect(sample.logs).toBe(1);
		samples.push({ ...sample, ms: sample.finished - sample.started });
		await page.waitForTimeout(150);
	}
	expect(errors).toEqual([]);
	await testInfo.attach("session-switch-measurements", { contentType: "application/json",
		body: JSON.stringify({ browser: browser.version(), node: process.version, samples, errors }, null, 2) });
});
