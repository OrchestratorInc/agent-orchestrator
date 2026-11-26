import { beforeEach, describe, expect, it } from "vitest";
import type { WorkspaceSession, WorkspaceSummary } from "../types/workspace";
import { isUnpinnedHere, REMOTE_UNPINS_STORAGE_KEY, useRemoteUnpinStore, withRemoteUnpins } from "./remote-unpin-store";

const pinned = { id: "s-1", hostId: "box-a", isPinned: true, pinnedAt: "2026-11-01T00:00:00Z" } as WorkspaceSession;
const workspace = { id: "p-1", hostId: "box-a", sessions: [pinned] } as unknown as WorkspaceSummary;

describe("remote unpins", () => {
	beforeEach(() => {
		window.localStorage.clear();
		useRemoteUnpinStore.setState({ unpins: {} });
	});

	it("shows a session unpinned here as unpinned, and keeps it across restarts", () => {
		useRemoteUnpinStore.getState().add("box-a", "s-1");
		const shown = withRemoteUnpins(workspace, useRemoteUnpinStore.getState().unpins);
		expect(shown.sessions[0]).toMatchObject({ isPinned: false, pinnedAt: undefined });
		expect(JSON.parse(window.localStorage.getItem(REMOTE_UNPINS_STORAGE_KEY) ?? "[]")).toEqual([
			expect.objectContaining({ hostId: "box-a", sessionId: "s-1" }),
		]);
	});

	it("leaves the same session ID on another host pinned", () => {
		useRemoteUnpinStore.getState().add("box-b", "s-1");
		expect(withRemoteUnpins(workspace, useRemoteUnpinStore.getState().unpins)).toBe(workspace);
	});

	it("lets a later pin from the other computer win, whatever this computer's clock says", () => {
		// The host's clock runs a day ahead of this computer's.
		const unpin = { hostId: "box-a", sessionId: "s-1", at: Date.parse("2026-10-31T00:00:00Z"), pinnedAt: "2026-11-01T00:00:00Z" };
		expect(isUnpinnedHere(unpin, "2026-11-01T00:00:00Z")).toBe(true);
		expect(isUnpinnedHere(unpin, "2026-11-02T00:00:00Z")).toBe(false);
		expect(isUnpinnedHere(unpin, undefined)).toBe(true);
		expect(isUnpinnedHere({ ...unpin, pinnedAt: undefined }, "2026-11-02T00:00:00Z")).toBe(true);
	});

	it("drops removals for hosts that are no longer saved", () => {
		useRemoteUnpinStore.getState().add("box-a", "s-1");
		useRemoteUnpinStore.getState().add("gone", "s-2");
		useRemoteUnpinStore.getState().keepHosts(["box-a"]);
		expect(Object.values(useRemoteUnpinStore.getState().unpins)).toEqual([expect.objectContaining({ hostId: "box-a" })]);
	});
});
