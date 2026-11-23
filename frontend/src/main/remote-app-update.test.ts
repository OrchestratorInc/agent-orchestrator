import { describe, expect, it, vi } from "vitest";
import { runRemoteAppUpdate, type RemoteAppUpdateDeps } from "./remote-app-update";
import type { UpdateStatus } from "./update-settings";

function deps(overrides: Partial<RemoteAppUpdateDeps> & { statuses?: UpdateStatus[] } = {}): RemoteAppUpdateDeps {
	const statuses = overrides.statuses ?? [{ state: "available", version: "0.16.0" }, { state: "downloaded", version: "0.16.0" }];
	let index = 0;
	return {
		packaged: true,
		hasUnsavedDrafts: () => false,
		installBlocker: () => undefined,
		check: vi.fn(async () => undefined),
		download: vi.fn(async () => {
			index += 1;
		}),
		status: () => statuses[Math.min(index, statuses.length - 1)],
		install: vi.fn(async () => undefined),
		...overrides,
	};
}

describe("runRemoteAppUpdate", () => {
	it("checks, downloads and reports the version it will install", async () => {
		const d = deps();
		await expect(runRemoteAppUpdate(d, { step: "prepare" })).resolves.toEqual({ version: "0.16.0" });
		expect(d.download).toHaveBeenCalledTimes(1);
		expect(d.install).not.toHaveBeenCalled();
	});

	it("uses a build that is already downloaded", async () => {
		const d = deps({ statuses: [{ state: "not-available", staged: { version: "0.16.1", stagedAt: 1, escalated: false } }] });
		await expect(runRemoteAppUpdate(d, { step: "prepare" })).resolves.toEqual({ version: "0.16.1" });
		expect(d.download).not.toHaveBeenCalled();
	});

	it("downloads a newer version than the one already staged", async () => {
		const d = deps({ statuses: [
			{ state: "available", version: "0.16.1", staged: { version: "0.16.0", stagedAt: 1, escalated: false } },
			{ state: "downloaded", version: "0.16.1", staged: { version: "0.16.1", stagedAt: 2, escalated: false } },
		] });
		await expect(runRemoteAppUpdate(d, { step: "prepare" })).resolves.toEqual({ version: "0.16.1" });
		expect(d.download).toHaveBeenCalledTimes(1);
	});

	it("installs the prepared version", async () => {
		const d = deps();
		await expect(runRemoteAppUpdate(d, { step: "install", version: "0.16.0" })).resolves.toEqual({ version: "0.16.0" });
		expect(d.install).toHaveBeenCalledWith("0.16.0");
	});

	it.each([
		["a draft that isn't saved", { hasUnsavedDrafts: () => true }, "Orchestrator on that computer has a draft that isn't saved yet. Update it there."],
		["a location it can't install from", { installBlocker: () => "Orchestrator's location isn't writable." }, "Orchestrator's location isn't writable."],
		["a development build", { packaged: false }, "Updates are only available in the installed app."],
	])("refuses with %s before quitting", async (_label, override, message) => {
		const d = deps(override);
		await expect(runRemoteAppUpdate(d, { step: "install", version: "0.16.0" })).rejects.toThrow(message);
		expect(d.install).not.toHaveBeenCalled();
	});

	it("refuses when a draft appears while the update downloads", async () => {
		let drafts = false;
		const d = deps({
			hasUnsavedDrafts: () => drafts,
			download: vi.fn(async () => { drafts = true; }),
			status: () => (drafts ? { state: "downloaded", version: "0.16.0" } : { state: "available", version: "0.16.0" }),
		});
		await expect(runRemoteAppUpdate(d, { step: "prepare" })).rejects.toThrow("isn't saved yet");
	});

	it.each([
		[[{ state: "not-available" }], "No newer version is available for that computer."],
		[[{ state: "error", message: "Couldn't reach the update server." }], "Couldn't reach the update server."],
		[[{ state: "available", version: "0.16.0" }, { state: "error", message: "Download stopped responding. Try again." }], "Download stopped responding. Try again."],
	] as [UpdateStatus[], string][])("reports what the updater found", async (statuses, message) => {
		await expect(runRemoteAppUpdate(deps({ statuses }), { step: "prepare" })).rejects.toThrow(message);
	});

	it("names the version it installs next without downloading or installing", async () => {
		const d = deps({ statuses: [{ state: "available", version: "0.16.0" }] });
		await expect(runRemoteAppUpdate(d, { step: "check" })).resolves.toEqual({ version: "0.16.0" });
		expect(d.download).not.toHaveBeenCalled();
		expect(d.install).not.toHaveBeenCalled();
		await expect(runRemoteAppUpdate(deps({ statuses: [{ state: "downloading", version: "0.16.1" }] }), { step: "check" })).resolves.toEqual({ version: "0.16.1" });
		await expect(runRemoteAppUpdate(deps({ statuses: [{ state: "not-available", staged: { version: "0.16.0", stagedAt: 1, escalated: false } }] }), { step: "check" })).resolves.toEqual({ version: "0.16.0" });
	});

	it("names the version when the feed answers, without waiting for the download the check starts", async () => {
		let status: UpdateStatus = { state: "checking" };
		let finishDownload = () => {};
		let checkSettled = false;
		const check = vi.fn(() => new Promise<void>((resolve) => {
			setTimeout(() => { status = { state: "downloading", version: "0.16.0" }; }, 150);
			finishDownload = resolve;
		}).finally(() => { checkSettled = true; }));
		await expect(runRemoteAppUpdate(deps({ check, status: () => status }), { step: "check" })).resolves.toEqual({ version: "0.16.0" });
		expect(checkSettled).toBe(false);
		finishDownload();
	});

	it("refuses the version question when it could not update", async () => {
		await expect(runRemoteAppUpdate(deps({ statuses: [{ state: "not-available" }] }), { step: "check" })).rejects.toThrow("No newer version is available for that computer.");
		await expect(runRemoteAppUpdate(deps({ statuses: [{ state: "error", message: "Couldn't reach the update server." }] }), { step: "check" })).rejects.toThrow("Couldn't reach the update server.");
		await expect(runRemoteAppUpdate(deps({ hasUnsavedDrafts: () => true }), { step: "check" })).rejects.toThrow("isn't saved yet");
	});

	it("won't reinstall a version that already failed to apply", async () => {
		await expect(runRemoteAppUpdate(deps(), { step: "prepare", notApplied: "0.16.0" })).rejects.toThrow(
			"The update to 0.16.0 didn't apply on that computer. Update Orchestrator there.",
		);
	});

	it("fails when the installer wants a different version confirmed", async () => {
		const d = deps({ install: vi.fn(async () => ({ state: "confirmation-required" as const, version: "0.16.2" })) });
		await expect(runRemoteAppUpdate(d, { step: "install", version: "0.16.0" })).rejects.toThrow("Orchestrator 0.16.2 is ready to install instead. Try again.");
	});
});
