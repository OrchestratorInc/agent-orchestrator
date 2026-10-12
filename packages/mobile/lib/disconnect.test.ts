import { beforeEach, describe, expect, it, vi } from "vitest";

// The modules under the seam all reach into native storage, so they're mocked
// out: what's worth asserting here is the order and completeness of the steps,
// not their implementations (which have their own coverage).
const calls: string[] = [];
const unpairTargets: (typeof active | null)[] = [];
// Lets a test make the push step fail the way a SecureStore write can.
let unpairThrows: Error | null = null;
let removeHostThrows: Error | null = null;
let activeHostThrows: Error | null = null;
let accountId: string | null = null;
let ignoreThrows: Error | null = null;
vi.mock("./account", () => ({ loadAccount: async () => accountId ? { id: accountId, email: "person@example.com" } : null }));
vi.mock("./accountHostPreferences", () => ({ ignoreAccountHost: async (account: string, hostId: string) => {
	calls.push(`ignore:${account}:${hostId}`);
	if (ignoreThrows) throw ignoreThrows;
} }));
beforeEach(() => { accountId = null; ignoreThrows = null; });

const active = {
	id: "h_active",
	name: "active",
	platform: "darwin",
	endpoints: [{ kind: "lan" as const, host: "192.168.1.42", port: 3011, secure: false }],
	lastConnected: 1,
};
const other = {
	...active,
	id: "h_other",
	name: "other",
	endpoints: [{ kind: "lan" as const, host: "192.168.1.99", port: 3011, secure: false }],
};

vi.mock("./push", () => ({
	// forgetServer unpairs rather than merely unregistering: the phone is leaving
	// this daemon, so its row must go, not just its push token. Unregistering
	// would leave the old desktop listing a phone that has moved on.
	unpairFromServer: vi.fn(async (host: typeof active | null) => {
		unpairTargets.push(host);
		calls.push("unpairFromServer");
		if (unpairThrows) throw unpairThrows;
	}),
	retryPendingUnpairs: vi.fn(async () => {}),
}));
vi.mock("./pendingUnpair", () => ({ markMachineUnpaired: vi.fn(async () => {}) }));
vi.mock("./config", () => ({
	loadConfig: async () => ({ host: "", httpPort: "3011", password: "" }),
	clearConfig: vi.fn(async () => {
		calls.push("clearConfig");
	}),
}));
vi.mock("./hosts", () => ({
	activeHostMetadata: vi.fn(async () => {
		if (activeHostThrows) throw activeHostThrows;
		return active;
	}),
	removeHost: vi.fn(async (id: string) => {
		calls.push(`removeHost:${id}`);
		if (removeHostThrows) throw removeHostThrows;
	}),
}));
vi.mock("./chat/eventCursor", () => ({
	clearEventCursorsForHost: vi.fn(async (host: typeof active) => {
		calls.push(`clearEventCursor:${host.id}`);
	}),
}));
vi.mock("./onboardingStore", () => ({
	clearOnboardingSkipped: vi.fn(async () => {
		calls.push("clearOnboardingSkipped");
	}),
}));

const { forgetPairedHost, forgetServer } = await import("./disconnect");

describe("forgetPairedHost", () => {
	beforeEach(() => {
		calls.length = 0;
		unpairTargets.length = 0;
		unpairThrows = null;
		removeHostThrows = null;
	});

	it("unpairs and removes only the requested machine", async () => {
		await forgetPairedHost(other);

		expect(unpairTargets).toEqual([other]);
		expect(calls).toEqual([
			"unpairFromServer",
			"removeHost:h_other",
			"clearEventCursor:h_other",
		]);
		expect(calls).not.toContain("clearConfig");
		expect(calls).not.toContain("clearOnboardingSkipped");
	});

	it("keeps the credential when durable cleanup cannot be saved", async () => {
		unpairThrows = new Error("SecureStore unavailable");

		await expect(forgetPairedHost(other)).rejects.toThrow("SecureStore unavailable");
		expect(calls).not.toContain("removeHost:h_other");
		expect(calls).not.toContain("clearEventCursor:h_other");
	});

	it("hides a manually paired machine from the signed-in account before removing it", async () => {
		accountId = "user-a";
		await forgetPairedHost(other);
		expect(calls).toEqual(["unpairFromServer", "ignore:user-a:h_other", "removeHost:h_other", "clearEventCursor:h_other"]);
	});

	it("preserves pairing when hiding it from account sync cannot be persisted", async () => {
		accountId = "user-a";
		ignoreThrows = new Error("storage unavailable");
		await expect(forgetPairedHost(other)).rejects.toThrow("storage unavailable");
		expect(calls).not.toContain("removeHost:h_other");
	});

	it("unpairs sign-out removals without hiding them on the next sign-in", async () => {
		accountId = "user-a";
		await forgetPairedHost(other, { ignoreAccount: false });
		expect(calls).toEqual(["unpairFromServer", "removeHost:h_other", "clearEventCursor:h_other"]);
	});
});

describe("forgetServer", () => {
	beforeEach(() => {
		calls.length = 0;
		unpairTargets.length = 0;
		unpairThrows = null;
		removeHostThrows = null;
		activeHostThrows = null;
	});

	// Clearing only the config would leave the daemon still pushing to this
	// device, still listing it as paired, and leave the password in the keystore.
	it("unpairs, clears the config, and re-arms onboarding", async () => {
		await forgetServer();
		expect(unpairTargets).toEqual([active]);
		expect(calls).toEqual([
			"unpairFromServer",
			"removeHost:h_active",
			"clearEventCursor:h_active",
			"clearConfig",
			"clearOnboardingSkipped",
		]);
	});

	// The local queue captures the credential before removeHost deletes it.
	it("persists remote cleanup before the credentials are thrown away", async () => {
		await forgetServer();
		expect(calls.indexOf("unpairFromServer")).toBeLessThan(calls.indexOf("removeHost:h_active"));
	});

	it("hides the default machine from account sync before deleting its credentials", async () => {
		accountId = "user-a";
		await forgetServer();
		expect(calls.indexOf("ignore:user-a:h_active")).toBeGreaterThan(calls.indexOf("unpairFromServer"));
		expect(calls.indexOf("ignore:user-a:h_active")).toBeLessThan(calls.indexOf("removeHost:h_active"));
	});

	it("remembers Forget for an account-owned default machine after sign-in expires", async () => {
		const { activeHostMetadata } = await import("./hosts");
		vi.mocked(activeHostMetadata).mockResolvedValueOnce({ ...active, accountUserId: "expired-account" });
		await forgetServer();
		expect(calls.indexOf("ignore:expired-account:h_active")).toBeGreaterThan(calls.indexOf("unpairFromServer"));
		expect(calls.indexOf("ignore:expired-account:h_active")).toBeLessThan(calls.indexOf("removeHost:h_active"));
	});

	it("keeps the default pairing if secure cleanup persistence fails", async () => {
		unpairThrows = new Error("SecureStore unavailable");
		await expect(forgetServer()).rejects.toThrow("SecureStore unavailable");
		expect(unpairTargets).toEqual([active]);
		expect(calls).not.toContain("removeHost:h_active");
		expect(calls).not.toContain("clearConfig");
		expect(calls).not.toContain("clearOnboardingSkipped");
	});

	it("still performs safe local cleanup when active-host lookup throws", async () => {
		activeHostThrows = new Error("SecureStore unavailable");

		await expect(forgetServer()).rejects.toThrow("SecureStore unavailable");
		expect(unpairTargets).toEqual([null]);
		expect(calls).toContain("unpairFromServer");
		expect(calls).toContain("clearConfig");
		expect(calls).toContain("clearOnboardingSkipped");
	});
});

// The bug this covers: forgetting cleared only the legacy config, so the host
// record and its token in ao.hostToken.<id> survived. resolveActiveConfig then
// raced that machine's endpoints on the next launch and silently reconnected to
// the server the user had just forgotten.
describe("forgetServer and the host list", () => {
	beforeEach(() => {
		calls.length = 0;
		unpairTargets.length = 0;
		unpairThrows = null;
		removeHostThrows = null;
		activeHostThrows = null;
	});

	it("removes the paired machine, not just the resolved address", async () => {
		await forgetServer();
		expect(calls).toContain("removeHost:h_active");
	});

	it("forgets the machine the caller chose even if the selection moved", async () => {
		await forgetServer(other);
		expect(unpairTargets).toEqual([other]);
		expect(calls).toContain("removeHost:h_other");
		expect(calls).not.toContain("removeHost:h_active");
	});

	it("finishes local cleanup when removing the host token fails", async () => {
		removeHostThrows = new Error("SecureStore unavailable");

		await expect(forgetServer()).rejects.toThrow("SecureStore unavailable");
		expect(calls).toContain("clearEventCursor:h_active");
		expect(calls).toContain("clearConfig");
		expect(calls).toContain("clearOnboardingSkipped");
	});
});
