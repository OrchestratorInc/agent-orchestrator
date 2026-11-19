import { describe, expect, it, vi } from "vitest";

vi.mock("expo-router", () => ({ useFocusEffect: () => {} }));
vi.mock("react-native", () => ({ AppState: { currentState: "active" } }));
vi.mock("../appStatePoll", () => ({ shouldPoll: () => true }));
vi.mock("../connectionError", () => ({ userFacingError: () => "" }));

import type { WorkspaceManifest } from "./api";
import { cachedManifest, rememberManifest } from "./useWorkspace";

const manifest = (sessionId: string) => ({ sessionId }) as WorkspaceManifest;

describe("the manifest a sheet reuses", () => {
	// Megabytes each on a big worktree, so only the newest read is kept.
	it("holds one manifest, the newest, not one per session", () => {
		const mac = { key: "mac" };
		rememberManifest(mac, "s1", manifest("s1"));
		expect(cachedManifest(mac, "s1")?.sessionId).toBe("s1");
		rememberManifest(mac, "s2", manifest("s2"));
		expect(cachedManifest(mac, "s2")?.sessionId).toBe("s2");
		expect(cachedManifest(mac, "s1")).toBeNull();
		expect(cachedManifest({ key: "other" }, "s2")).toBeNull();
		expect(cachedManifest(null, "s2")).toBeNull();
	});
});
