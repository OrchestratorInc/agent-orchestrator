import { describe, expect, it } from "vitest";
import { orgDisplayNameForAccount } from "./useCloudOrg";

describe("orgDisplayNameForAccount", () => {
	it.each([
		["a full name", { displayName: "Asha Rao", email: "asha@acme.dev" }, "Asha's workspace"],
		["one name", { displayName: "Dana", email: "dana@acme.dev" }, "Dana's workspace"],
		// The control plane names someone without a profile name by their email.
		["the email the control plane fell back to", { displayName: "asha.k77@example.com", email: "asha.k77@example.com" }, "asha.k77's workspace"],
		["no name", { displayName: "  ", email: "dana.kim@acme.dev" }, "dana.kim's workspace"],
		["neither", { displayName: "", email: "" }, "Workspace"],
	])("names a new workspace after %s, never an email", (_case, user, name) => {
		expect(orgDisplayNameForAccount(user)).toBe(name);
	});

	it("keeps the name within the control plane's 80 characters", () => {
		const name = orgDisplayNameForAccount({ displayName: "A".repeat(100), email: "a@acme.dev" });
		expect(name).toHaveLength(80);
		expect(name.startsWith("A".repeat(68))).toBe(true);
		expect(name.endsWith("'s workspace")).toBe(true);
	});
});
