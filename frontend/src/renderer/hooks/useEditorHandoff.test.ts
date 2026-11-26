import { QueryClient } from "@tanstack/react-query";
import { describe, expect, it, vi } from "vitest";
import { aoBridge } from "../lib/bridge";
import { editorHandoffErrorMessage, editorHandoffQueryKey, prefetchEditorHandoffState } from "./useEditorHandoff";

vi.mock("../lib/bridge", () => ({ aoBridge: { editorHandoff: { getState: vi.fn() } } }));

// Electron wraps ipcMain rejections; the topbar used to render the wrapper
// verbatim, so a missing worktree read as "Error invoking remote method
// 'editorHandoff:open': Error: Session workspace is not available".
describe("editorHandoffErrorMessage", () => {
	it("strips the Electron remote-method wrapper", () => {
		const raw = new Error(
			"Error invoking remote method 'editorHandoff:open': Error: Session workspace is not available",
		);
		expect(editorHandoffErrorMessage(raw)).toBe("Session workspace is not available");
	});

	it("strips the wrapper for other channels and error classes", () => {
		expect(
			editorHandoffErrorMessage(
				new Error("Error invoking remote method 'editorHandoff:getState': TypeError: boom"),
			),
		).toBe("boom");
	});

	it("leaves an already-clean message alone", () => {
		expect(editorHandoffErrorMessage(new Error("That editor is not installed. Choose another option."))).toBe(
			"That editor is not installed. Choose another option.",
		);
	});

	it("keeps the original when stripping would leave nothing", () => {
		const raw = new Error("Error invoking remote method 'editorHandoff:open': Error:");
		expect(editorHandoffErrorMessage(raw)).toBe(raw.message);
	});

	it("returns null for non-Error rejections", () => {
		expect(editorHandoffErrorMessage("nope")).toBeNull();
		expect(editorHandoffErrorMessage(undefined)).toBeNull();
	});
});

describe("prefetchEditorHandoffState", () => {
	const getState = vi.mocked(aoBridge.editorHandoff.getState);

	it("reads an older session's workspace once", async () => {
		const queryClient = new QueryClient();
		getState.mockReset().mockResolvedValue({ workspaceAvailable: false, targets: [] } as never);
		prefetchEditorHandoffState(queryClient, { id: "s1", createdAt: "2026-01-01T00:00:00Z" });
		await vi.waitFor(() => expect(queryClient.getQueryData(editorHandoffQueryKey("s1"))).toEqual({ workspaceAvailable: false, targets: [] }));
		expect(getState).toHaveBeenCalledTimes(1);
	});

	it("waits out a new session's workspace like the menu's own read, so a menu opening mid-read gets the final answer", async () => {
		const queryClient = new QueryClient();
		getState
			.mockReset()
			.mockResolvedValueOnce({ workspaceAvailable: false, targets: [] } as never)
			.mockResolvedValue({ workspaceAvailable: true, targets: [] } as never);
		prefetchEditorHandoffState(queryClient, { id: "s2", createdAt: new Date().toISOString() });
		await vi.waitFor(
			() => expect(queryClient.getQueryData(editorHandoffQueryKey("s2"))).toEqual({ workspaceAvailable: true, targets: [] }),
			{ timeout: 3000 },
		);
		expect(getState).toHaveBeenCalledTimes(2);
	});
});
