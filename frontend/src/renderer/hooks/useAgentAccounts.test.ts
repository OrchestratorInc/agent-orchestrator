import { describe, expect, it } from "vitest";
import { withoutSameLogins } from "./useAgentAccounts";

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

	it("keeps the account a session runs on, and leaves out its twin instead", () => {
		expect(ids(withoutSameLogins([cli, a, b], "device-claude"))).toEqual(["device-claude", "acct-b"]);
		expect(ids(withoutSameLogins([cli, a, b], "acct-a"))).toEqual(["acct-a", "acct-b"]);
	});
});
