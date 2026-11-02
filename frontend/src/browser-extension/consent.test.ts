import { describe, expect, it } from "vitest";
import type { ChromeApi } from "./chrome-api";
import { ConsentManager } from "./consent";

function fakeChrome() {
	const data: Record<string, unknown> = {};
	const removed = new Set<(id: number) => void>();
	const messages = new Set<(message: unknown, sender: unknown, respond: (value?: unknown) => void) => unknown>();
	const created: string[] = [];
	const chrome = {
		runtime: {
			id: "ext",
			getURL: (path: string) => `chrome-extension://ext/${path}`,
			onMessage: { addListener: (cb: never) => messages.add(cb), removeListener: () => undefined },
		},
		windows: {
			create: async ({ url }: { url: string }) => {
				created.push(url);
				return { id: 7, focused: true };
			},
			remove: async () => undefined,
			onRemoved: { addListener: (cb: never) => removed.add(cb), removeListener: () => undefined },
		},
		storage: {
			local: {
				get: async (key: string) => (key in data ? { [key]: structuredClone(data[key]) } : {}),
				set: async (items: Record<string, unknown>) => {
					await new Promise((resolve) => setTimeout(resolve, 5));
					Object.assign(data, structuredClone(items));
				},
			},
		},
	} as unknown as ChromeApi;
	const send = (message: unknown, url = "chrome-extension://ext/consent.html?session=s1") =>
		new Promise((resolve) => {
			for (const cb of messages) cb(message, { id: "ext", url }, resolve);
		});
	const closeWindow = () => removed.forEach((cb) => cb(7));
	return { chrome, created, send, closeWindow };
}

describe("ConsentManager", () => {
	it("is not fooled by the consent window closing while Allow is stored", async () => {
		const fake = fakeChrome();
		const consent = new ConsentManager(fake.chrome);
		consent.start();
		const pending = consent.ensure("s1");
		await new Promise((resolve) => setTimeout(resolve, 0));
		expect(fake.created[0]).toContain("consent.html?session=s1");
		const ack = fake.send({ type: "ao-consent-decision", sessionId: "s1", allow: true, trusted: true });
		fake.closeWindow(); // The page closes itself right after clicking.
		await expect(pending).resolves.toMatchObject({ trusted: true });
		await expect(ack).resolves.toEqual({ ok: true });
		await expect(consent.ensure("s1")).resolves.toMatchObject({ trusted: true });
	});

	it("treats closing the window without answering as a denial", async () => {
		const fake = fakeChrome();
		const consent = new ConsentManager(fake.chrome);
		consent.start();
		const pending = consent.ensure("s2");
		await new Promise((resolve) => setTimeout(resolve, 0));
		fake.closeWindow();
		await expect(pending).rejects.toMatchObject({ code: "BROWSER_CHROME_CONSENT_DENIED" });
	});

	it("ignores decisions that do not come from the consent page", async () => {
		const fake = fakeChrome();
		const consent = new ConsentManager(fake.chrome);
		consent.start();
		const pending = consent.ensure("s3");
		await new Promise((resolve) => setTimeout(resolve, 0));
		void fake.send({ type: "ao-consent-decision", sessionId: "s3", allow: true, trusted: true }, "https://evil.test/");
		await new Promise((resolve) => setTimeout(resolve, 20));
		expect(await consent.approvals()).toEqual({});
		await consent.revoke();
		await expect(pending).rejects.toMatchObject({ code: "BROWSER_CHROME_STOPPED" });
	});
});
