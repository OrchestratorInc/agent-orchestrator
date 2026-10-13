import { QueryClient } from "@tanstack/react-query";
import { describe, expect, it } from "vitest";
import type { CloudCpBillingSummary, CloudCpMeResponse, CloudCpOffering, CloudCpOrganization } from "./cloud-cp/types";
import { paidPlanBadge } from "../hooks/useCloudBilling";
import {
	billingModeFromSummary,
	cachedCloudBillingMode,
	canManageBilling,
	cloudMeQueryKey,
	offeringKindFor,
	offersProviderChoice,
	projectSandboxProvider,
	resolveCloudOffering,
	selectActiveOrganization,
} from "./cloud-offering";

const user = { id: "u-1", email: "a@example.com", displayName: "A", authProvider: "workos" };

function offering(overrides: Partial<CloudCpOffering>): CloudCpOffering {
	return { providers: [], defaultProvider: "", billingMode: "self_serve", kind: "none", features: [], ...overrides };
}

function org(id: string, overrides: Partial<CloudCpOrganization> = {}): CloudCpOrganization {
	return { id, slug: id, displayName: id, role: "owner", ...overrides };
}

const billing = (overrides: Partial<CloudCpBillingSummary>): CloudCpBillingSummary =>
	({ enabled: true, exempt: false, plan: "free", entitled: false, ...overrides });

describe("offeringKindFor", () => {
	it("derives the kind from the providers", () => {
		expect(offeringKindFor(["coder"])).toBe("b2b");
		expect(offeringKindFor(["freestyle"])).toBe("b2c");
		expect(offeringKindFor(["docker"])).toBe("b2c");
		expect(offeringKindFor(["freestyle", "coder"])).toBe("both");
		expect(offeringKindFor([])).toBe("none");
	});
});

describe("billingModeFromSummary", () => {
	it("prefers the server's billingMode, else reads enabled/exempt", () => {
		expect(billingModeFromSummary(undefined)).toBeUndefined();
		expect(billingModeFromSummary(billing({ billingMode: "contract" }))).toBe("contract");
		expect(billingModeFromSummary(billing({ enabled: false }))).toBe("off");
		expect(billingModeFromSummary(billing({ exempt: true }))).toBe("contract");
		expect(billingModeFromSummary(billing({}))).toBe("self_serve");
	});
});

describe("resolveCloudOffering", () => {
	it("uses the server offering when /me carries one", () => {
		const me: CloudCpMeResponse = {
			user, organizations: [org("o1")],
			offering: offering({ providers: ["coder"], defaultProvider: "coder", billingMode: "contract", kind: "b2b" }),
		};
		expect(resolveCloudOffering(me)).toMatchObject({ providers: ["coder"], defaultProvider: "coder", billingMode: "contract", kind: "b2b", source: "server" });
	});

	it("uses the active org's own offering over the top-level one", () => {
		const personal = org("personal", { offering: offering({ providers: ["freestyle"], defaultProvider: "freestyle", kind: "b2c" }) });
		const team = org("team", { offering: offering({ providers: ["coder"], defaultProvider: "coder", billingMode: "contract", kind: "b2b" }) });
		const me: CloudCpMeResponse = { user, organizations: [personal, team], offering: personal.offering };
		expect(resolveCloudOffering(me)?.kind).toBe("b2b");
		expect(resolveCloudOffering(me, undefined, false, "personal")?.kind).toBe("b2c");
	});

	it("falls back to sandboxProviders and the billing summary on an older control plane", () => {
		const me: CloudCpMeResponse = { user, organizations: [org("o1")], sandboxProviders: { available: ["freestyle"], default: "coder" } };
		const pending = resolveCloudOffering(me);
		expect(pending).toMatchObject({ providers: ["freestyle"], defaultProvider: "freestyle", kind: "b2c", source: "fallback" });
		expect(pending?.billingMode).toBeUndefined();
		expect(resolveCloudOffering(me, billing({}))?.billingMode).toBe("self_serve");
		expect(resolveCloudOffering(me, billing({ exempt: true }))?.billingMode).toBe("contract");
		expect(resolveCloudOffering(me, undefined, true)?.billingMode).toBe("off");
	});

	it("reads customCoder from the server, and from the providers on an older control plane", () => {
		const generic = (customCoder?: boolean): CloudCpMeResponse => ({
			user, organizations: [org("o1")],
			offering: offering({ providers: ["coder", "freestyle"], defaultProvider: "freestyle", kind: "both", customCoder }),
		});
		expect(resolveCloudOffering(generic(false))?.customCoder).toBe(false);
		expect(resolveCloudOffering(generic(true))?.customCoder).toBe(true);
		// A control plane without the field offered Coder only to orgs with the
		// coder capability.
		expect(resolveCloudOffering(generic(undefined))?.customCoder).toBe(true);
		const legacy: CloudCpMeResponse = { user, organizations: [org("o1")], sandboxProviders: { available: ["freestyle"], default: "freestyle" } };
		expect(resolveCloudOffering(legacy)?.customCoder).toBe(false);
	});

	it("resolves nothing before /me loads, and no cloud without providers", () => {
		expect(resolveCloudOffering(undefined)).toBeUndefined();
		expect(resolveCloudOffering({ user, organizations: [] })).toMatchObject({ providers: [], defaultProvider: "", kind: "none" });
	});
});

describe("offersProviderChoice", () => {
	it("offers a choice only for several providers the server sent", () => {
		const both = offering({ providers: ["coder", "freestyle"], defaultProvider: "coder", kind: "both" });
		expect(offersProviderChoice(resolveCloudOffering({ user, organizations: [org("o1")], offering: both }))).toBe(true);
		const single = offering({ providers: ["coder"], defaultProvider: "coder", kind: "b2b" });
		expect(offersProviderChoice(resolveCloudOffering({ user, organizations: [org("o1")], offering: single }))).toBe(false);
		// An older control plane lists several providers but ignores a project's pick.
		const legacy: CloudCpMeResponse = {
			user,
			organizations: [org("o1")],
			sandboxProviders: { available: ["nodeops", "coder"], default: "coder" },
		};
		expect(offersProviderChoice(resolveCloudOffering(legacy))).toBe(false);
		expect(offersProviderChoice(undefined)).toBe(false);
	});
});

describe("selectActiveOrganization", () => {
	it("prefers the org whose offering includes coder over an older personal org", () => {
		const personal = org("personal", { offering: offering({ kind: "b2c" }) });
		const team = org("team", { offering: offering({ kind: "both" }) });
		expect(selectActiveOrganization([personal, team])?.id).toBe("team");
	});

	it("keeps the first org otherwise", () => {
		expect(selectActiveOrganization([org("a"), org("b")])?.id).toBe("a");
		expect(selectActiveOrganization([])).toBeUndefined();
	});
});

describe("projectSandboxProvider", () => {
	it("reads the stored provider, else the org default", () => {
		expect(projectSandboxProvider({ sandboxProvider: "Coder" }, { defaultProvider: "freestyle" })).toBe("coder");
		expect(projectSandboxProvider({}, { defaultProvider: "freestyle" })).toBe("freestyle");
		expect(projectSandboxProvider(undefined, undefined)).toBe("");
	});

	it("keeps a project with Coder config on Coder whatever the org default", () => {
		expect(projectSandboxProvider({ coder: { templateId: "tpl-1" } }, { defaultProvider: "freestyle" })).toBe("coder");
		expect(projectSandboxProvider({ coder: null }, { defaultProvider: "freestyle" })).toBe("freestyle");
		expect(projectSandboxProvider({ sandboxProvider: "freestyle", coder: {} }, { defaultProvider: "coder" })).toBe("freestyle");
	});
});

describe("canManageBilling", () => {
	it("allows owners and admins only", () => {
		expect(canManageBilling({ role: "owner" })).toBe(true);
		expect(canManageBilling({ role: "admin" })).toBe(true);
		expect(canManageBilling({ role: "member" })).toBe(false);
		expect(canManageBilling(undefined)).toBe(false);
	});
});

describe("cachedCloudBillingMode", () => {
	it("reads the /me offering, then a cached billing summary", () => {
		const client = new QueryClient();
		expect(cachedCloudBillingMode(client)).toBeUndefined();
		client.setQueryData(["cloud-billing", "o1"], billing({ exempt: true }));
		expect(cachedCloudBillingMode(client)).toBe("contract");
		client.setQueryData([...cloudMeQueryKey, "https://cp", "u-1"], { user, organizations: [], offering: offering({ billingMode: "self_serve" }) });
		expect(cachedCloudBillingMode(client)).toBe("self_serve");
	});
});

describe("paidPlanBadge", () => {
	it("shows no badge outside self-serve billing", () => {
		const paid = billing({ entitled: true, plan: "starter" });
		expect(paidPlanBadge(paid)).toBe("Starter");
		expect(paidPlanBadge({ ...paid, billingMode: "contract" })).toBeNull();
		expect(paidPlanBadge({ ...paid, exempt: true })).toBeNull();
	});
});
