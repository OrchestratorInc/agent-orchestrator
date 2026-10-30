import { beforeEach, describe, expect, it } from "vitest";
import { appI18n } from "../../i18n";
import { useUiStore } from "../../stores/ui-store";
import { cloudMeQueryKey } from "../cloud-offering";
import { queryClient } from "../query-client";
import { openUpgradeAfterPlanRefusal } from "./renderer-client";

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
