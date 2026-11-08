import { describe, expect, it } from "vitest";
import { agentAccountLabel, withoutSameLogins } from "./useAgentAccounts";

const cli = { id: "device-claude", sameLoginAs: "acct-a" };
const a = { id: "acct-a" };
const b = { id: "acct-b" };
const ids = (accounts: { id: string }[]) => accounts.map((account) => account.id);

describe("withoutSameLogins", () => {
	it("lists the CLI's login once, as the saved account signed in as it", () => {
		expect(ids(withoutSameLogins([cli, a, b]))).toEqual(["acct-a", "acct-b"]);
	});

	it("keeps the CLI's login when it is no saved account's", () => {
		expect(ids(withoutSameLogins([{ id: "device-claude" }, a, b]))).toEqual(["device-claude", "acct-a", "acct-b"]);
	});

	it("keeps the CLI's login when the account it names is not listed", () => {
		expect(ids(withoutSameLogins([cli, b]))).toEqual(["device-claude", "acct-b"]);
	});

	it("keeps the CLI's login when it is the kept account, and its saved twin too", () => {
		expect(ids(withoutSameLogins([cli, a, b], "device-claude"))).toEqual(["device-claude", "acct-a", "acct-b"]);
		expect(ids(withoutSameLogins([cli, a, b], "acct-a"))).toEqual(["acct-a", "acct-b"]);
	});
});

describe("agentAccountLabel", () => {
	const t = (key: string, options?: { provider: string }) => key === "settings.agentAccounts.cliLoginOf" ? `${options?.provider} CLI login` : key;
	const acme = { id: "acct-a", provider: "claude" as const, device: false, email: "a@example.com", orgName: "Acme" };
	const personal = { id: "acct-p", provider: "claude" as const, device: false, email: "A@example.com", orgName: "Personal" };
	const device = { id: "device-claude", provider: "claude" as const, device: true, email: "a@example.com", orgName: "Acme" };
	const other = { id: "acct-b", provider: "claude" as const, device: false, email: "b@example.com", orgName: "Acme" };

	it("is the email when no other listed account shares it", () => {
		expect(agentAccountLabel(acme, [acme, other], t)).toBe("a@example.com");
	});

	it("adds the organization when the same email is in another organization", () => {
		expect(agentAccountLabel(acme, [acme, personal], t)).toBe("a@example.com · Acme");
		expect(agentAccountLabel(personal, [acme, personal], t)).toBe("A@example.com · Personal");
	});

	it("marks the CLI's login when the organization does not tell them apart", () => {
		expect(agentAccountLabel(device, [device, acme], t)).toBe("a@example.com · Claude Code CLI login");
		expect(agentAccountLabel(acme, [device, acme], t)).toBe("a@example.com");
		expect(agentAccountLabel({ ...device, orgName: undefined }, [device, { ...acme, orgName: undefined }], t)).toBe("a@example.com · Claude Code CLI login");
	});
});
