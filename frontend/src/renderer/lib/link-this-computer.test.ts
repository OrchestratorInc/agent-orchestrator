import { beforeEach, expect, it, vi } from "vitest";

// AO Cloud's directory and the daemon's token are faked; the order between
// them is what is tested: nothing may move the machine's account token to an
// account that would then refuse to list it.
const fake = vi.hoisted(() => ({
	directory: { hosts: [] as Array<{ hostId: string }>, fixedAddresses: true, hostKeys: true },
	calls: [] as string[],
}));
vi.mock("./account-remote-hosts", async (original) => ({
	...(await original<typeof import("./account-remote-hosts")>()),
	readAccountHostDirectory: vi.fn(async () => { fake.calls.push("directory"); return fake.directory; }),
	saveAccountRemoteHost: vi.fn(async () => { fake.calls.push("save"); }),
}));
vi.mock("./api-client", () => ({
	apiClient: { POST: vi.fn(async () => { fake.calls.push("token"); return { data: { hostId: "h_this", token: "0f".repeat(32), hostKey: "k".repeat(43) } }; }) },
	apiErrorMessage: () => "failed",
}));
vi.mock("./bridge", () => ({ aoBridge: { remotes: { computerName: async () => "Mac" } } }));

import { saveAccountRemoteHost } from "./account-remote-hosts";
import { linkThisComputer } from "./link-this-computer";

const link = (options?: { url?: string; ifListed?: boolean }) => linkThisComputer("https://cp.example.test", "user_01", "h_this", "pw", options);

beforeEach(() => {
	fake.directory = { hosts: [], fixedAddresses: true, hostKeys: true };
	fake.calls.length = 0;
});

it("links at the fixed address when AO Cloud gives one", async () => {
	expect(await link()).toEqual({ hostId: "h_this", added: true });
	expect(fake.calls).toEqual(["directory", "token", "save"]);
	// The listing carries the machine's host key, only where AO Cloud takes it.
	expect(saveAccountRemoteHost).toHaveBeenLastCalledWith("https://cp.example.test", expect.objectContaining({ hostKey: "k".repeat(43) }));
	fake.directory.hostKeys = false;
	await link();
	expect(saveAccountRemoteHost).toHaveBeenLastCalledWith("https://cp.example.test", expect.objectContaining({ hostKey: undefined }));
	fake.directory.hostKeys = true;
	// Linked again, it was already listed.
	fake.directory.hosts = [{ hostId: "h_this" }];
	expect(await link()).toEqual({ hostId: "h_this", added: false });
});

it("issues no token for an address-free link AO Cloud would refuse", async () => {
	fake.directory.fixedAddresses = false;
	expect(await link()).toEqual({ hostId: "", added: false });
	expect(fake.calls).toEqual(["directory"]);
	// With the tunnel's address it links.
	expect((await link({ url: "https://quiet-river.trycloudflare.com:443" })).hostId).toBe("h_this");
});

it("links only a listed computer when asked to", async () => {
	expect((await link({ ifListed: true })).hostId).toBe("");
	expect(fake.calls).toEqual(["directory"]);
	fake.directory.hosts = [{ hostId: "h_this" }];
	expect((await link({ ifListed: true })).hostId).toBe("h_this");
});
