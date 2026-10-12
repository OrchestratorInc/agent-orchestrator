import type { CurrentAccount, OrganizationMembership } from "@aoagents/cloud-client";
import { describe, expect, it } from "vitest";
import { offersProviderChoice, resolveMobileCloudOffering, selectActiveOrganization } from "./offering";

const offering = (providers: string[]) => ({ providers, defaultProvider: providers[0] ?? "", kind: (providers.includes("coder") ? "b2b" : providers.length ? "b2c" : "none") as "b2b" | "b2c" | "none", billingMode: "off" as const, features: [] });
const personal: OrganizationMembership = { id: "personal", displayName: "Personal", slug: "personal", role: "owner", offering: offering(["freestyle"]) };
const team: OrganizationMembership = { id: "team", displayName: "Team", slug: "team", role: "member", offering: offering(["coder"]) };
const account: CurrentAccount = { user: { id: "u1", displayName: "One", email: "one@test.dev", authProvider: "workos" }, organizations: [personal, team] };
describe("mobile Cloud offering", () => {
	it("prefers the reported organization, then the team offering, then the first org", () => {
		expect(selectActiveOrganization([personal, team], "personal")).toBe(personal);
		expect(selectActiveOrganization([personal, team])).toBe(team);
		expect(selectActiveOrganization([personal])).toBe(personal);
	});
	it("does not borrow another organization's top-level offering", () => {
		expect(resolveMobileCloudOffering({ ...account, activeOrganizationId: "personal", offering: offering(["freestyle"]) }, { ...team, offering: undefined }).source).toBe("fallback");
	});
	it("uses the active organization's server offering even without BYO Coder credentials", () => {
		expect(resolveMobileCloudOffering(account, team)).toMatchObject({ providers: ["coder"], defaultProvider: "coder", source: "server" });
	});
	it("only shows provider choice for a server-backed multiple-provider offering", () => {
		for (const providers of [[], ["coder"]]) expect(offersProviderChoice(resolveMobileCloudOffering(account, { ...team, offering: offering(providers) }))).toBe(false);
		expect(offersProviderChoice(resolveMobileCloudOffering(account, { ...team, offering: offering(["coder", "freestyle"]) }))).toBe(true);
		expect(offersProviderChoice(resolveMobileCloudOffering({ ...account, sandboxProviders: { available: ["coder", "docker"], default: "forbidden" } }, { ...team, offering: undefined }))).toBe(false);
	});
	it("never defaults to a forbidden legacy provider", () => {
		expect(resolveMobileCloudOffering({ ...account, sandboxProviders: { available: ["docker"], default: "coder" } }, { ...team, offering: undefined }).defaultProvider).toBe("docker");
	});
});
