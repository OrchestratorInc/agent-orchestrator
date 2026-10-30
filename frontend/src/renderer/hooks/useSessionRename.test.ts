import { act, renderHook } from "@testing-library/react";
import { describe, expect, it } from "vitest";
import { useSessionRename } from "./useSessionRename";

describe("useSessionRename", () => {
	const worker = { id: "proj-1", title: "fix login", kind: "worker" as const };

	it("starts a rename for a local worker", () => {
		const { result } = renderHook(() => useSessionRename(worker));
		expect(result.current.canRename).toBe(true);
		act(() => result.current.begin());
		expect(result.current.isEditing).toBe(true);
		expect(result.current.draft).toBe("fix login");
	});

	// Whichever surface asks: AO Cloud has no rename route, and the daemon refuses the orchestrator.
	it.each([
		["a Cloud session", { ...worker, cloud: { orgId: "org-1" } }],
		["the project orchestrator", { ...worker, id: "proj-orchestrator", kind: "orchestrator" as const }],
	])("never starts a rename for %s", (_, session) => {
		const { result } = renderHook(() => useSessionRename(session));
		expect(result.current.canRename).toBe(false);
		act(() => result.current.begin());
		expect(result.current.isEditing).toBe(false);
	});
});
