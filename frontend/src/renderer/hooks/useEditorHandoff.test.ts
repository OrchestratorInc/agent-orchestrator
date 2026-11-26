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

	it("keeps a found workspace fresh so the menu opens on it", async () => {
		const queryClient = new QueryClient();
		getState.mockResolvedValueOnce({ workspaceAvailable: true, targets: [] } as never);
		prefetchEditorHandoffState(queryClient, "s1");
		await vi.waitFor(() => expect(queryClient.getQueryData(editorHandoffQueryKey("s1"))).toBeDefined());
		await Promise.resolve();
		expect(queryClient.getQueryState(editorHandoffQueryKey("s1"))?.isInvalidated).toBe(false);
	});

	it("leaves a missing workspace stale so the menu's own read retries it", async () => {
		const queryClient = new QueryClient();
		getState.mockResolvedValueOnce({ workspaceAvailable: false, targets: [] } as never);
		prefetchEditorHandoffState(queryClient, "s2");
		await vi.waitFor(() => expect(queryClient.getQueryState(editorHandoffQueryKey("s2"))?.isInvalidated).toBe(true));
	});
});
