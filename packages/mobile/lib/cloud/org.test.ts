import { describe, expect, it, vi } from "vitest";
import { orgDisplayNameForAccount, resolveOrg } from "./org";

describe("orgDisplayNameForAccount", () => {
	it("prefers the display name", () => {
		expect(orgDisplayNameForAccount({ displayName: "Ada L", email: "ada@x.com" })).toBe("Ada L");
	});

	it("falls back to the email local part", () => {
		expect(orgDisplayNameForAccount({ displayName: "", email: "ada@x.com" })).toBe("ada");
	});

	it("falls back again when there is nothing to name it after", () => {
		expect(orgDisplayNameForAccount({ displayName: "", email: "" })).toBe("Workspace");
	});

	// The control plane caps the name at 80 characters.
	it("truncates to the control plane's limit", () => {
		expect(orgDisplayNameForAccount({ displayName: "x".repeat(200), email: "" })).toHaveLength(80);
	});
});

describe("resolveOrg", () => {
	it("uses the first organization when one exists", async () => {
		const client = {
			getCurrentAccount: vi.fn(async () => ({
				user: { id: "u1", displayName: "Ada", email: "ada@x.com" },
				organizations: [{ id: "o1", slug: "ada", displayName: "Ada", role: "owner" }],
			})),
			createOrganization: vi.fn(),
		};
		expect((await resolveOrg(client as never)).id).toBe("o1");
		expect(client.createOrganization).not.toHaveBeenCalled();
	});

	it("creates one for an account with no organizations", async () => {
		const client = {
			getCurrentAccount: vi.fn(async () => ({
				user: { id: "u1", displayName: "Ada", email: "ada@x.com" },
				organizations: [],
			})),
			createOrganization: vi.fn(async () => ({
				organization: { id: "o-new", slug: "ada", displayName: "Ada", role: "owner" },
			})),
		};
		expect((await resolveOrg(client as never)).id).toBe("o-new");
		expect(client.createOrganization).toHaveBeenCalledWith({ displayName: "Ada" });
	});
});
