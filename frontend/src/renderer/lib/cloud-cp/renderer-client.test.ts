import { beforeEach, describe, expect, it, vi } from "vitest";
import { appI18n } from "../../i18n";
import { useUiStore } from "../../stores/ui-store";
import { cloudMeQueryKey } from "../cloud-offering";
import { queryClient } from "../query-client";
import { cloudCpFetch, openUpgradeAfterPlanRefusal } from "./renderer-client";

function seedBillingMode(billingMode: "self_serve" | "contract" | "off") {
	queryClient.setQueryData([...cloudMeQueryKey, "https://cp.test", "user-1"], {
		user: { id: "user-1" },
		organizations: [],
		offering: { providers: ["freestyle"], defaultProvider: "freestyle", billingMode, kind: "b2c", features: [] },
	});
}

describe("openUpgradeAfterPlanRefusal", () => {
	let now = 1_000_000_000;
	beforeEach(async () => {
		await appI18n.changeLanguage("en");
		queryClient.clear();
		useUiStore.setState({ settingsModal: null, globalToasts: [] });
		// Each case starts past the previous one's cooldown.
		now += 10 * 60_000;
	});

	it("opens Upgrade for a self-serve org", () => {
		seedBillingMode("self_serve");
		expect(openUpgradeAfterPlanRefusal(now)).toBe(true);
		expect(useUiStore.getState().settingsModal).toMatchObject({ scope: "global", section: "billing" });
	});

	it("shows a neutral contact-your-admin notice to a contract org", () => {
		seedBillingMode("contract");
		expect(openUpgradeAfterPlanRefusal(now)).toBe(true);
		expect(useUiStore.getState().settingsModal).toBeNull();
		expect(useUiStore.getState().globalToasts.at(-1)).toMatchObject({
			title: "Plan limit reached",
			body: "Contact your organization's admin to raise it.",
		});
	});

	it("never opens Upgrade before the billing mode is known", () => {
		expect(openUpgradeAfterPlanRefusal(now)).toBe(true);
		expect(useUiStore.getState().settingsModal).toBeNull();
	});
});

describe("cloudCpFetch", () => {
	// The main process cannot be told to stop, but the caller stops waiting
	// when its signal fires, as with fetch.
	it("gives up when the caller's signal fires", async () => {
		const w = window as unknown as { ao?: { cloudCp?: { request: () => Promise<never> } } };
		const before = w.ao;
		w.ao = { ...before, cloudCp: { request: () => new Promise<never>(() => undefined) } };
		try {
			const controller = new AbortController();
			const pending = cloudCpFetch("https://cp.test/api/cloud/v1/me/hosts", { signal: controller.signal });
			controller.abort(new Error("timed out"));
			await expect(pending).rejects.toThrow("timed out");
		} finally {
			w.ao = before;
		}
	});

	it("sends nothing for a caller already aborted", async () => {
		const w = window as unknown as { ao?: { cloudCp?: { request: () => Promise<never> } } };
		const before = w.ao;
		const request = vi.fn(() => new Promise<never>(() => undefined));
		w.ao = { ...before, cloudCp: { request } };
		try {
			const pending = cloudCpFetch("https://cp.test/api/cloud/v1/me/hosts", { method: "DELETE", signal: AbortSignal.abort(new Error("gone")) });
			await expect(pending).rejects.toThrow("gone");
			expect(request).not.toHaveBeenCalled();
		} finally {
			w.ao = before;
		}
	});
});
