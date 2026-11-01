import { beforeEach, describe, expect, it, vi } from "vitest";
import type { EnvironmentMachineDraft } from "./environmentEditor";
import type { Host } from "./hosts";

const fixtures = vi.hoisted(() => ({
	activeHostMetadata: vi.fn<() => Promise<{ id: string } | null>>(async () => null),
	loadHosts: vi.fn<() => Promise<Host[]>>(async () => []),
	forgetServer: vi.fn(async () => {}),
	forgetPairedHost: vi.fn(async () => {}),
	persist: vi.fn(async () => ({})),
	updateHostAppearance: vi.fn(async () => {}),
}));

vi.mock("./api", () => ({
	ApiError: class ApiError extends Error {
		constructor(readonly status: number) { super(`HTTP ${status}`); }
	},
	pingServer: vi.fn(),
}));
vi.mock("./connectRuntime", () => ({ probeEndpoint: vi.fn() }));
vi.mock("./disconnect", () => ({ forgetServer: fixtures.forgetServer, forgetPairedHost: fixtures.forgetPairedHost }));
vi.mock("./hosts", () => ({
	activeHostMetadata: fixtures.activeHostMetadata,
	loadHosts: fixtures.loadHosts,
	renameHost: vi.fn(),
	saveHost: vi.fn(),
	updateHostAppearance: fixtures.updateHostAppearance,
}));
vi.mock("./manualConnect", async () => {
	class AccountManagedHostError extends Error {}
	class DifferentMachineError extends Error {}
	return { AccountManagedHostError, DifferentMachineError, persistManualHostEdit: fixtures.persist };
});

import { ApiError } from "./api";
import { forgetEnvironmentMachine, ForgetMachineError, saveEnvironmentMachine } from "./environmentActions";
import { MachineSaveError } from "./environmentEditor";
import { DifferentMachineError } from "./manualConnect";

const a: Host = { id: "a", name: "A", platform: "darwin", endpoints: [{ kind: "lan", host: "10.0.0.2", port: 3011, secure: false }], token: "a-token", lastConnected: 2, appearance: { symbol: "server" } };
const b: Host = { ...a, id: "b", name: "B", token: "b-token", lastConnected: 1 };

beforeEach(() => {
	for (const mock of Object.values(fixtures)) mock.mockReset();
	fixtures.persist.mockResolvedValue({});
});

describe("forgetting a machine", () => {
	it.each([false, true])("forgets the default and names the next one (last machine: %s)", async (lastMachine) => {
		fixtures.activeHostMetadata.mockResolvedValue({ id: "a" });
		fixtures.loadHosts.mockResolvedValue(lastMachine ? [] : [b]);
		const result = await forgetEnvironmentMachine(a, "a");
		expect(fixtures.forgetServer).toHaveBeenCalledWith(a);
		expect(fixtures.forgetPairedHost).not.toHaveBeenCalled();
		expect(result.wasDefault).toBe(true);
		expect(result.notice).toBe(lastMachine ? "A forgotten." : "A forgotten. B is now the default.");
		expect(result.nextDefault?.id).toBe(lastMachine ? undefined : "b");
	});

	it("forgets a non-default without touching the default", async () => {
		fixtures.activeHostMetadata.mockResolvedValue({ id: "a" });
		fixtures.loadHosts.mockResolvedValue([a]);
		const result = await forgetEnvironmentMachine(b, "a");
		expect(fixtures.forgetPairedHost).toHaveBeenCalledWith(b);
		expect(fixtures.forgetServer).not.toHaveBeenCalled();
		expect(result).toMatchObject({ wasDefault: false, notice: "B forgotten." });
		expect(result.nextDefault?.id).toBe("a");
	});

	// Account sync may move the default while the page is open: the stored
	// selection decides, and the machine asked for is the one forgotten.
	it("forgets the machine asked for even when the selection moved while the page was open", async () => {
		fixtures.activeHostMetadata.mockResolvedValue({ id: "b" });
		fixtures.loadHosts.mockResolvedValue([b]);
		const result = await forgetEnvironmentMachine(a, "a");
		expect(fixtures.forgetPairedHost).toHaveBeenCalledWith(a);
		expect(fixtures.forgetServer).not.toHaveBeenCalled();
		expect(result).toMatchObject({ wasDefault: false, notice: "A forgotten." });
	});

	it("reports a machine whose saved connection is still there", async () => {
		fixtures.activeHostMetadata.mockResolvedValue({ id: "a" });
		fixtures.forgetServer.mockRejectedValue(new Error("storage failed"));
		fixtures.loadHosts.mockResolvedValue([a]);
		await expect(forgetEnvironmentMachine(a, "a")).rejects.toEqual(new ForgetMachineError("This machine's saved connection couldn't be removed. Try again."));
	});

	it("reports saved machines that can't be read", async () => {
		fixtures.loadHosts.mockRejectedValue(new Error("keychain"));
		await expect(forgetEnvironmentMachine(a, null)).rejects.toEqual(new ForgetMachineError("The saved machines couldn't be read. Try again."));
	});
});

describe("saving a machine", () => {
	const draft = (patch: Partial<EnvironmentMachineDraft["config"]> = {}, symbol: "server" | "cloud" = "server"): EnvironmentMachineDraft => ({
		machineName: "A",
		appearance: { symbol },
		endpointIndex: 0,
		config: { host: "10.0.0.2", httpPort: "3011", muxPort: "14801", secure: false, password: "a-token", hostId: "a", ...patch },
	});

	it("stores a new icon and says whether the route changed", async () => {
		expect(await saveEnvironmentMachine(a, draft({}, "cloud"), "ios")).toEqual({ routeChanged: false });
		expect(fixtures.updateHostAppearance).toHaveBeenCalledWith("a", { symbol: "cloud" });
		expect(await saveEnvironmentMachine(a, draft({ host: "10.0.0.9" }), "ios")).toEqual({ routeChanged: true });
		expect(fixtures.updateHostAppearance).toHaveBeenCalledOnce();
	});

	it("puts a rejected password against the password field", async () => {
		fixtures.persist.mockRejectedValue(new (ApiError as unknown as new (status: number) => Error)(401));
		await expect(saveEnvironmentMachine(a, draft({ password: "wrong" }), "ios")).rejects.toEqual(new MachineSaveError("That password was rejected. Check it and try again.", "password"));
	});

	it("puts another machine answering, or nothing answering, against the address", async () => {
		fixtures.persist.mockRejectedValueOnce(Object.assign(new DifferentMachineError(), { message: "That address belongs to another machine." }));
		const different = await saveEnvironmentMachine(a, draft({ host: "10.0.0.9" }), "ios").catch((error: MachineSaveError) => error);
		expect(different).toMatchObject({ field: "host", message: "That address belongs to another machine." });
		fixtures.persist.mockRejectedValueOnce(new Error("timeout"));
		const unreachable = await saveEnvironmentMachine(a, draft({ host: "10.0.0.9" }), "ios").catch((error: MachineSaveError) => error);
		expect(unreachable).toBeInstanceOf(MachineSaveError);
		expect(unreachable).toMatchObject({ field: "host" });
	});
});
