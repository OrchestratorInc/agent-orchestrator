import { act, render } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

const mocks = vi.hoisted(() => ({
	getVersion: vi.fn<() => Promise<string>>(),
	decide: vi.fn((version?: string) => version?.includes("-nightly.") ?? false),
}));
vi.mock("../lib/bridge", () => ({ aoBridge: { app: { getVersion: () => mocks.getVersion() } } }));
vi.mock("../lib/build-channel", () => ({ isTeamsEnabled: (version?: string) => mocks.decide(version) }));

async function loadHook() {
	return (await import("./useTeamsEnabled")).useTeamsEnabled;
}

function Probe({ useTeamsEnabled }: { useTeamsEnabled: () => boolean }) {
	return <span>{useTeamsEnabled() ? "teams" : "no teams"}</span>;
}

describe("useTeamsEnabled", () => {
	beforeEach(() => {
		vi.resetModules();
		mocks.getVersion.mockReset();
		mocks.decide.mockClear();
	});
	afterEach(() => vi.useRealTimers());

	it("decides from the app's version once it is read, with no query client in the tree", async () => {
		mocks.getVersion.mockResolvedValue("0.14.2-nightly.202611050300");
		const useTeamsEnabled = await loadHook();
		const { container } = render(<Probe useTeamsEnabled={useTeamsEnabled} />);
		// Before the version is known: decided without one.
		expect(mocks.decide).toHaveBeenCalledWith(undefined);
		await act(async () => { await Promise.resolve(); await Promise.resolve(); await Promise.resolve(); });
		expect(container.textContent).toBe("teams");
		expect(mocks.decide).toHaveBeenLastCalledWith("0.14.2-nightly.202611050300");
	});

	it("reads the version once for every component that asks", async () => {
		mocks.getVersion.mockResolvedValue("0.14.1");
		const useTeamsEnabled = await loadHook();
		const { container } = render(<><Probe useTeamsEnabled={useTeamsEnabled} /><Probe useTeamsEnabled={useTeamsEnabled} /></>);
		await act(async () => { await Promise.resolve(); await Promise.resolve(); await Promise.resolve(); });
		expect(container.textContent).toBe("no teamsno teams");
		render(<Probe useTeamsEnabled={useTeamsEnabled} />);
		expect(mocks.getVersion).toHaveBeenCalledTimes(1);
	});

	it("tries again after a failed read", async () => {
		mocks.getVersion.mockRejectedValueOnce(new Error("bridge not ready")).mockResolvedValue("0.14.2-nightly.202611050300");
		const useTeamsEnabled = await loadHook();
		const first = render(<Probe useTeamsEnabled={useTeamsEnabled} />);
		await act(async () => { await Promise.resolve(); await Promise.resolve(); await Promise.resolve(); });
		expect(first.container.textContent).toBe("no teams");
		first.unmount();
		const second = render(<Probe useTeamsEnabled={useTeamsEnabled} />);
		await act(async () => { await Promise.resolve(); await Promise.resolve(); await Promise.resolve(); });
		expect(second.container.textContent).toBe("teams");
		expect(mocks.getVersion).toHaveBeenCalledTimes(2);
	});
});
