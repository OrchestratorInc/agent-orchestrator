import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";

const { getMock, postMock, showToastMock } = vi.hoisted(() => ({
	getMock: vi.fn(),
	postMock: vi.fn(),
	showToastMock: vi.fn(),
}));

vi.mock("../lib/api-client", () => ({
	apiClient: { GET: getMock, POST: postMock },
	apiErrorMessage: (_error: unknown, fallback: string) => fallback,
	getApiBaseUrl: () => "http://127.0.0.1:3001",
	subscribeApiBaseUrl: () => () => undefined,
}));
vi.mock("../lib/preview-mode", () => ({ usesPreviewWorkspaceData: false }));
vi.mock("../stores/ui-store", () => ({ useUiStore: (selector: (state: unknown) => unknown) => selector({ showGlobalToast: showToastMock }) }));

import { LegacyWorkspaceCleanupDialog } from "./LegacyWorkspaceCleanupDialog";

function renderDialog() {
	return render(<LegacyWorkspaceCleanupDialog />);
}

describe("LegacyWorkspaceCleanupDialog", () => {
	beforeEach(() => {
		window.localStorage.clear();
		getMock.mockReset();
		postMock.mockReset();
		showToastMock.mockReset();
	});

	it("asks before cleaning a large legacy footprint and remembers the choice", async () => {
		getMock.mockResolvedValue({
			data: { sessions: [{ sessionId: "old-1" }, { sessionId: "old-2" }], totalBytes: 3 * 1024 ** 3, incomplete: false },
			error: undefined,
		});
		postMock.mockResolvedValue({
			data: { cleaned: ["old-1", "old-2"], alreadyGone: [], skipped: [] },
			error: undefined,
		});

		renderDialog();

		expect(await screen.findByText("Archived sessions use about 3 GB")).toBeInTheDocument();
		expect(screen.getByText(/actual space freed may be lower/)).toBeInTheDocument();
		expect(screen.getByText("Cleanup removes worktrees. Tracked and non-ignored edits are saved for later reapply; ignored files aren't saved and will be deleted.")).toBeInTheDocument();
		expect(postMock).not.toHaveBeenCalled();

		fireEvent.click(screen.getByRole("button", { name: "Clean up archived session worktrees" }));
		await waitFor(() => expect(postMock).toHaveBeenCalledWith("/api/v1/sessions/cleanup", {
			body: { sessionIds: ["old-1", "old-2"] },
		}));
		await waitFor(() => expect(window.localStorage.getItem("ao.legacyWorkspaceCleanupPrompt.dismissed.v1")).toBe("true"));
		expect(showToastMock).toHaveBeenCalledWith("Cleanup finished: 2 cleaned, 0 skipped.");
	});

	it("rechecks a small footprint on the next start and prompts if it grows", async () => {
		getMock.mockResolvedValueOnce({
			data: { sessions: [{ sessionId: "old-1" }], totalBytes: (1 << 30) - 1, incomplete: false },
			error: undefined,
		});

		const first = renderDialog();

		await waitFor(() => expect(getMock).toHaveBeenCalledTimes(1));
		expect(window.localStorage.getItem("ao.legacyWorkspaceCleanupPrompt.dismissed.v1")).toBeNull();
		expect(screen.queryByText(/Archived sessions are using/)).not.toBeInTheDocument();
		expect(postMock).not.toHaveBeenCalled();
		first.unmount();

		getMock.mockResolvedValueOnce({
			data: { sessions: [{ sessionId: "old-1" }], totalBytes: 2 * 1024 ** 3, incomplete: false },
			error: undefined,
		});
		renderDialog();
		expect(await screen.findByText("Archived sessions use about 2 GB")).toBeInTheDocument();
	});

	it("does not repeat the prompt after the user keeps worktrees for now", async () => {
		getMock.mockResolvedValue({
			data: { sessions: [{ sessionId: "old-1" }], totalBytes: 2 * 1024 ** 3, incomplete: false },
			error: undefined,
		});

		const first = renderDialog();
		expect(await screen.findByText("Archived sessions use about 2 GB")).toBeInTheDocument();
		fireEvent.click(screen.getByRole("button", { name: "Keep worktrees" }));
		await waitFor(() => expect(window.localStorage.getItem("ao.legacyWorkspaceCleanupPrompt.dismissed.v1")).toBe("true"));
		first.unmount();

		renderDialog();
		expect(screen.queryByRole("dialog")).not.toBeInTheDocument();
		expect(getMock).toHaveBeenCalledTimes(1);
	});
});
