import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { act, fireEvent, render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, describe, expect, it, vi } from "vitest";
import type { EditorHandoffState, OpenSessionTargetInput } from "../../shared/editor-handoff";
import { useUiStore } from "../stores/ui-store";
import { appShortcutBus } from "../test/app-shortcut-bus";
import { TopbarOpenEditorButton } from "./TopbarOpenEditorButton";
import { TooltipProvider } from "./ui/tooltip";

vi.mock("../lib/telemetry", () => ({ captureRendererEvent: vi.fn() }));

const openMock = vi.fn(async ({ targetId }: OpenSessionTargetInput) => {
	if (targetId === "file-manager") return { id: "file-manager" as const, name: "Finder", kind: "file_manager" as const };
	if (targetId === "terminal") return { id: "terminal" as const, name: "Terminal", kind: "terminal" as const };
	if (targetId === "vscode") return { id: "vscode" as const, name: "VS Code", kind: "editor" as const };
	return { id: "cursor" as const, name: "Cursor", kind: "editor" as const };
});

const availableState: EditorHandoffState = {
	targets: [
		{ id: "cursor", name: "Cursor", kind: "editor" },
		{ id: "vscode", name: "VS Code", kind: "editor" },
		{ id: "file-manager", name: "Finder", kind: "file_manager" },
		{ id: "terminal", name: "Terminal", kind: "terminal" },
	],
	preferredEditorId: "cursor",
	workspaceAvailable: true,
};

function setState(state: EditorHandoffState) {
	window.ao!.editorHandoff.getState = vi.fn().mockResolvedValue(state);
	window.ao!.editorHandoff.open = openMock;
}

function renderButton(
	{ sessionCreatedAt, sessionTerminated }: { sessionCreatedAt?: string; sessionTerminated?: boolean } = {},
) {
	const client = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } });
	return render(
		<QueryClientProvider client={client}>
			<TooltipProvider>
				<TopbarOpenEditorButton
					sessionId="sess-1"
					projectId="proj-1"
					sessionCreatedAt={sessionCreatedAt}
					sessionTerminated={sessionTerminated}
				/>
			</TooltipProvider>
		</QueryClientProvider>,
	);
}

describe("TopbarOpenEditorButton", () => {
	// The bug this PR chases: the worktree goes away after getState was cached,
	// so the control is still enabled, the click fails, and nothing refreshes the
	// state, leaving it enabled to fail again. Failing the open must re-read
	// availability and disable the control.
	it("refreshes availability after a failed open so the control stops inviting the click", async () => {
		const getState = vi
			.fn()
			.mockResolvedValueOnce(availableState)
			.mockResolvedValue({ ...availableState, workspaceAvailable: false, unavailableReason: "Session workspace is not available" });
		window.ao!.editorHandoff.getState = getState;
		window.ao!.editorHandoff.open = vi.fn().mockRejectedValue(
			new Error("Error invoking remote method 'editorHandoff:open': Error: Session workspace is not available"),
		);
		renderButton();

		const main = await screen.findByRole("button", { name: "Open in Cursor" });
		expect(main).toBeEnabled();

		await userEvent.click(main);

		// the failure must trigger a refetch, not just show a message
		await waitFor(() => expect(getState).toHaveBeenCalledTimes(2));
		await waitFor(() =>
			expect(screen.getByRole("button", { name: "Session workspace is not available" })).toBeDisabled(),
		);
	});

	// Regression: an ipcMain rejection arrives wrapped as "Error invoking remote
	// method 'editorHandoff:open': Error: <reason>", and the topbar used to paint
	// that whole string into the error.
	it("shows the reason, not Electron's remote-method wrapper, when the open fails", async () => {
		setState(availableState);
		window.ao!.editorHandoff.open = vi.fn().mockRejectedValue(
			new Error("Error invoking remote method 'editorHandoff:open': Error: Session workspace is not available"),
		);
		renderButton();

		await userEvent.click(await screen.findByRole("button", { name: "Open in Cursor" }));

		await waitFor(() => expect(useUiStore.getState().globalToast).toMatchObject({
			body: "Session workspace is not available",
			tone: "error",
		}));
	});


	beforeEach(() => {
		openMock.mockClear();
		openMock.mockImplementation(async ({ targetId }: OpenSessionTargetInput) => {
			if (targetId === "file-manager") return { id: "file-manager", name: "Finder", kind: "file_manager" };
			if (targetId === "terminal") return { id: "terminal", name: "Terminal", kind: "terminal" };
			if (targetId === "vscode") return { id: "vscode", name: "VS Code", kind: "editor" };
			return { id: "cursor", name: "Cursor", kind: "editor" };
		});
		setState(availableState);
	});

	it("discovers newly installed terminals when opening the menu and renders their native icon", async () => {
		const target = { id: `terminal-app:${"a".repeat(64)}` as const, name: "Warp", kind: "terminal" as const, iconDataUrl: "data:image/png;base64,dGVzdA==" };
		window.ao!.editorHandoff.getState = vi.fn().mockResolvedValueOnce(availableState)
			.mockResolvedValue({ ...availableState, targets: [...availableState.targets, target] });
		renderButton();
		await userEvent.click(await screen.findByRole("button", { name: "Open workspace options" }));
		const warp = await screen.findByRole("menuitem", { name: "Warp" });
		expect(window.ao!.editorHandoff.getState).toHaveBeenNthCalledWith(1, "sess-1");
		expect(window.ao!.editorHandoff.getState).toHaveBeenNthCalledWith(2, "sess-1", { refreshTerminals: true });
		expect(warp.querySelector("image")).toHaveAttribute("href", target.iconDataUrl);
		await userEvent.click(warp);
		await waitFor(() => expect(openMock).toHaveBeenCalledWith({ sessionId: "sess-1", targetId: target.id }));
	});

	it("copies the workspace through the native bridge without opening a target", async () => {
		const copy = vi.spyOn(window.ao!.editorHandoff, "copyWorkspacePath").mockResolvedValue(undefined);
		renderButton();
		await userEvent.click(await screen.findByRole("button", { name: "Open workspace options" }));
		await userEvent.click(await screen.findByRole("menuitem", { name: "Copy path" }));
		await waitFor(() => expect(copy).toHaveBeenCalledWith("sess-1"));
		expect(openMock).not.toHaveBeenCalled();
		expect(useUiStore.getState().globalToast?.title).toBe("Copied to clipboard");
	});

	it("reports copy failures and refreshes workspace availability", async () => {
		vi.spyOn(window.ao!.editorHandoff, "copyWorkspacePath").mockRejectedValue(
			new Error("Error invoking remote method 'editorHandoff:copyWorkspacePath': Error: Workspace removed"),
		);
		const getState = vi.fn().mockResolvedValueOnce(availableState).mockResolvedValue({ ...availableState, workspaceAvailable: false });
		window.ao!.editorHandoff.getState = getState;
		renderButton();
		await userEvent.click(await screen.findByRole("button", { name: "Open workspace options" }));
		await userEvent.click(await screen.findByRole("menuitem", { name: "Copy path" }));
		await waitFor(() => expect(useUiStore.getState().globalToast).toMatchObject({ title: "Could not copy session path", body: "Workspace removed", tone: "error" }));
		await waitFor(() => expect(screen.getByRole("button", { name: "Open workspace options" })).toBeDisabled());
		expect(openMock).not.toHaveBeenCalled();
	});

	it("uses the bundled Ghostty symbol even when a native placeholder exists and launches Ghostty", async () => {
		const iconDataUrl = "data:image/png;base64,dGVzdA==";
		setState({ ...availableState, targets: [...availableState.targets, { id: "ghostty", name: "Ghostty", kind: "terminal", iconDataUrl }] });
		renderButton();
		await userEvent.click(await screen.findByRole("button", { name: "Open workspace options" }));
		const ghostty = await screen.findByRole("menuitem", { name: "Ghostty" });
		expect(ghostty.querySelector("image")).not.toBeInTheDocument();
		expect(ghostty.querySelector("svg path")).toBeInTheDocument();
		const menu = screen.getByRole("menu");
		expect(menu.textContent).toBe("FinderTerminalGhosttyCursorVS CodeCopy path");
		expect(menu.querySelector("[role='separator']")).not.toBeInTheDocument();
		expect(screen.getAllByRole("menuitem").map((item) => item.textContent)).toEqual(["Finder", "Terminal", "Ghostty", "Cursor", "VS Code", "Copy path"]);
		for (const item of screen.getAllByRole("menuitem")) expect(item.querySelector("svg")).toBeInTheDocument();
		expect(screen.queryByRole("menuitem", { name: "Open in Ghostty" })).not.toBeInTheDocument();
		await userEvent.click(ghostty);
		await waitFor(() => expect(openMock).toHaveBeenCalledWith({ sessionId: "sess-1", targetId: "ghostty" }));
	});

	it("shows Finder's real app icon but keeps the Terminal, Cursor and VS Code symbols over native app tiles", async () => {
		const iconDataUrl = "data:image/png;base64,dGVzdA==";
		setState({ ...availableState, targets: availableState.targets.map((target) => ({ ...target, iconDataUrl })) });
		renderButton();
		await userEvent.click(await screen.findByRole("button", { name: "Open workspace options" }));
		const finder = await screen.findByRole("menuitem", { name: "Finder" });
		const terminal = screen.getByRole("menuitem", { name: "Terminal" });
		expect(finder.querySelector("image")).toHaveAttribute("href", iconDataUrl);
		expect(terminal.querySelector("svg.icon-square-terminal")).toBeInTheDocument();
		expect(terminal.querySelector("image")).not.toBeInTheDocument();
		for (const name of ["Cursor", "VS Code"]) {
			const item = screen.getByRole("menuitem", { name });
			expect(item.querySelector("image")).not.toBeInTheDocument();
			expect(item.querySelector("svg path")).toBeInTheDocument();
		}
		expect(screen.getByRole("menuitem", { name: "VS Code" }).querySelector("svg")).toHaveStyle({ color: "#1F9CF0" });
		expect(screen.getByLabelText("Open in Cursor").querySelector("image")).not.toBeInTheDocument();
	});

	it.each(["wezterm", "kitty"] as const)("keeps a visible SVG fallback when the %s app icon fails to load", async (id) => {
		setState({ ...availableState, targets: [...availableState.targets, { id, name: id, kind: "terminal", iconDataUrl: "data:image/png;base64,dGVzdA==" }] });
		renderButton();
		await userEvent.click(await screen.findByRole("button", { name: "Open workspace options" }));
		const item = await screen.findByRole("menuitem", { name: id });
		fireEvent.error(item.querySelector("image")!);
		expect(item.querySelector("image")).not.toBeInTheDocument();
		expect(item.querySelector("svg path")).toBeInTheDocument();
	});

	it("uses persisted Cursor as the primary target and sends no filesystem path", async () => {
		renderButton();
		const button = await screen.findByRole("button", { name: "Open in Cursor" });
		expect(button).not.toHaveAttribute("data-priority");
		expect(button.querySelector("[data-compact-label]")).not.toBeInTheDocument();
		expect(button.querySelector("svg")).toBeInTheDocument();
		expect(button).toHaveClass("size-control-md");
		expect(button).toHaveClass("border-0");
		const options = screen.getByRole("button", { name: "Open workspace options" });
		expect(options).toHaveClass("size-control-md", "hover:bg-transparent");
		expect(button).toHaveClass("hover:bg-transparent");
		// Disabled buttons retain a hoverable tooltip wrapper.
		const group = button.parentElement?.parentElement;
		expect(group).toHaveClass("gap-0", "inline-flex");
		expect(screen.getAllByRole("button")).toHaveLength(2);
		expect(button).toHaveClass("before:hidden");
		expect(options).toHaveClass("before:hidden");
		await userEvent.click(button);
		await waitFor(() => expect(openMock).toHaveBeenCalledWith({ sessionId: "sess-1" }));
	});

	it("keeps the options button expanded while its menu is open", async () => {
		renderButton();
		const options = await screen.findByRole("button", { name: "Open workspace options" });

		await userEvent.click(options);

		expect(options).toHaveAttribute("aria-expanded", "true");
		expect(options).toHaveAttribute("data-state", "open");
		expect(options.parentElement?.parentElement).toHaveAttribute("data-state", "open");
		expect(screen.getByRole("menu")).toBeInTheDocument();
	});

	// No supported editor: the main button opens Finder instead of sitting empty,
	// with no guidance copy, and the menu still offers every native fallback.
	it("falls back to Finder when the saved editor is no longer installed", async () => {
		setState({
			targets: [
				{ id: "file-manager", name: "Finder", kind: "file_manager" },
				{ id: "terminal", name: "Terminal", kind: "terminal" },
			],
			preferredEditorId: "cursor",
			workspaceAvailable: true,
		});
		openMock.mockClear();
		renderButton();
		const main = await screen.findByRole("button", { name: "Open in Finder" });
		expect(main).toBeEnabled();
		expect(main.querySelector("svg.icon-code-xml, svg.icon-code")).not.toBeInTheDocument();
		await userEvent.click(main);
		await waitFor(() => expect(openMock).toHaveBeenCalledWith({ sessionId: "sess-1", targetId: "file-manager" }));
		expect(screen.queryByRole("alert")).not.toBeInTheDocument();
		await userEvent.click(screen.getByRole("button", { name: "Open workspace options" }));
		expect((await screen.findAllByRole("menuitem")).map((item) => item.textContent)).toEqual([
			"Finder",
			"Terminal",
			"Copy path",
		]);
		expect(screen.queryByRole("note")).not.toBeInTheDocument();
		expect(document.body.textContent).not.toContain("No supported editor found");
	});

	it("disables every launch action for a missing workspace and explains why only on the button", async () => {
		setState({
			...availableState,
			workspaceAvailable: false,
			unavailableReason: "Session workspace is not available.",
		});
		renderButton();
		expect(await screen.findByRole("button", { name: "Session workspace is not available." })).toBeDisabled();
		expect(screen.getByRole("button", { name: "Open workspace options" })).toBeDisabled();
		// A standing unavailable state must not take inline topbar space.
		expect(screen.queryByRole("alert")).not.toBeInTheDocument();
	});

	it("never renders a transient unavailable error while a fresh session becomes ready", async () => {
		vi.useFakeTimers();
		const renderedAlerts: string[] = [];
		const alertObserver = new MutationObserver(() => {
			for (const alert of document.querySelectorAll('[role="alert"]')) {
				renderedAlerts.push(alert.textContent ?? "");
			}
		});
		alertObserver.observe(document.body, { childList: true, subtree: true, characterData: true });
		try {
			const getState = vi
				.fn()
				.mockResolvedValueOnce({
					...availableState,
					workspaceAvailable: false,
					unavailableReason: "Session workspace is not available.",
				})
				.mockResolvedValue(availableState);
			window.ao!.editorHandoff.getState = getState;
			renderButton({ sessionCreatedAt: new Date().toISOString() });

			await act(async () => {});
			expect(getState).toHaveBeenCalledTimes(1);
			expect(screen.queryByRole("alert")).not.toBeInTheDocument();
			expect(screen.getByRole("button", { name: "Preparing workspace…" })).toBeDisabled();

			await act(async () => {
				await vi.advanceTimersByTimeAsync(499);
			});
			expect(getState).toHaveBeenCalledTimes(1);
			expect(screen.queryByRole("alert")).not.toBeInTheDocument();
			expect(screen.getByRole("button", { name: "Preparing workspace…" })).toBeDisabled();

			await act(async () => {
				await vi.advanceTimersByTimeAsync(1);
			});
			await act(async () => {
				await vi.runOnlyPendingTimersAsync();
			});
			expect(getState).toHaveBeenCalledTimes(2);
			expect(screen.getByRole("button", { name: "Open in Cursor" })).toBeEnabled();
			expect(screen.queryByRole("alert")).not.toBeInTheDocument();
			expect(renderedAlerts).toEqual([]);
		} finally {
			alertObserver.disconnect();
			vi.useRealTimers();
		}
	});

	it("explains the unavailable workspace only after the fresh-session readiness timeout", async () => {
		vi.useFakeTimers();
		try {
			const getState = vi.fn().mockResolvedValue({
				...availableState,
				workspaceAvailable: false,
				unavailableReason: "Session workspace is not available.",
			});
			window.ao!.editorHandoff.getState = getState;
			renderButton({ sessionCreatedAt: new Date().toISOString() });

			await act(async () => {});
			expect(screen.queryByRole("alert")).not.toBeInTheDocument();
			expect(screen.getByRole("button", { name: "Preparing workspace…" })).toBeDisabled();

			await act(async () => {
				await vi.advanceTimersByTimeAsync(5_000);
			});
			await act(async () => {
				await vi.runOnlyPendingTimersAsync();
			});
			expect(getState).toHaveBeenCalledTimes(11);
			expect(getState.mock.calls.every((args) => args.length === 1 && args[0] === "sess-1")).toBe(true);
			expect(screen.getByRole("button", { name: "Session workspace is not available." })).toBeDisabled();
		} finally {
			vi.useRealTimers();
		}
	});

	it("does not call the workspace missing when the check itself failed, and checks again", async () => {
		vi.useFakeTimers();
		try {
			const getState = vi
				.fn()
				.mockResolvedValueOnce({
					...availableState,
					workspaceAvailable: false,
					unavailableReason: "Internal server error",
					unavailableCode: "INTERNAL_ERROR",
				})
				.mockResolvedValue(availableState);
			window.ao!.editorHandoff.getState = getState;
			renderButton();

			await act(async () => {
				await vi.advanceTimersByTimeAsync(0);
			});
			expect(screen.getByRole("button", { name: "Checking workspace…" })).toBeDisabled();
			expect(screen.queryByText(/Internal server error|not available/)).not.toBeInTheDocument();

			await act(async () => {
				await vi.advanceTimersByTimeAsync(5_000);
			});
			await act(async () => {
				await vi.runOnlyPendingTimersAsync();
			});
			expect(getState).toHaveBeenCalledTimes(2);
			expect(screen.getByRole("button", { name: "Open in Cursor" })).toBeEnabled();
		} finally {
			vi.useRealTimers();
		}
	});

	it("stops checking after three failed checks and shows the reason", async () => {
		vi.useFakeTimers();
		try {
			const getState = vi.fn().mockResolvedValue({
				...availableState,
				workspaceAvailable: false,
				unavailableReason: "AO daemon is not ready.",
				unavailableCode: "SERVICE_UNAVAILABLE",
			});
			window.ao!.editorHandoff.getState = getState;
			renderButton();

			for (let i = 0; i < 6; i += 1) {
				await act(async () => {
					await vi.advanceTimersByTimeAsync(5_000);
				});
			}
			expect(getState).toHaveBeenCalledTimes(3);
			expect(screen.getByRole("button", { name: "AO daemon is not ready." })).toBeDisabled();
		} finally {
			vi.useRealTimers();
		}
	});

	it("does not poll a terminated session whose workspace is gone", async () => {
		const getState = vi.fn().mockResolvedValue({
			...availableState,
			workspaceAvailable: false,
			unavailableReason: "Session workspace is not available.",
		});
		window.ao!.editorHandoff.getState = getState;
		renderButton({ sessionCreatedAt: new Date().toISOString(), sessionTerminated: true });

		expect(await screen.findByRole("button", { name: "Session workspace is not available." })).toBeDisabled();
		expect(getState).toHaveBeenCalledTimes(1);
	});

	it("opens safe native fallbacks from the menu", async () => {
		renderButton();
		await userEvent.click(await screen.findByRole("button", { name: "Open workspace options" }));
		await userEvent.click(await screen.findByRole("menuitem", { name: "Finder" }));
		await waitFor(() => expect(openMock).toHaveBeenCalledWith({ sessionId: "sess-1", targetId: "file-manager" }));
	});

	// Nothing saved reads as the Cursor default, so this is the fresh-install case
	// on a Mac with VS Code but no Cursor.
	it("opens Finder with its real icon until an editor is chosen, then shows the editor", async () => {
		openMock.mockClear();
		const iconDataUrl = "data:image/png;base64,ZmluZGVy";
		setState({
			...availableState,
			targets: availableState.targets
				.filter((target) => target.id !== "cursor")
				.map((target) => target.kind === "file_manager" ? { ...target, iconDataUrl } : target),
			preferredEditorId: "cursor",
		});
		renderButton();
		const main = await screen.findByRole("button", { name: "Open in Finder" });
		expect(main.querySelector("image")).toHaveAttribute("href", iconDataUrl);
		await userEvent.hover(main);
		expect((await screen.findAllByText(/workspace in Finder/)).length).toBeGreaterThan(0);
		await userEvent.click(main);
		await waitFor(() => expect(openMock).toHaveBeenCalledWith({ sessionId: "sess-1", targetId: "file-manager" }));
		act(() => appShortcutBus.emit({ id: "open-in-editor" }));
		await waitFor(() => expect(openMock).toHaveBeenCalledTimes(2));
		expect(openMock).toHaveBeenLastCalledWith({ sessionId: "sess-1", targetId: "file-manager" });

		await userEvent.click(screen.getByRole("button", { name: "Open workspace options" }));
		await userEvent.click(await screen.findByRole("menuitem", { name: "VS Code" }));
		expect(await screen.findByRole("button", { name: "Open in VS Code" })).toBeEnabled();
	});

	it("updates the primary target after a chosen editor succeeds", async () => {
		renderButton();
		await userEvent.click(await screen.findByRole("button", { name: "Open workspace options" }));
		await userEvent.click(await screen.findByRole("menuitem", { name: "VS Code" }));
		expect(await screen.findByRole("button", { name: "Open in VS Code" })).toBeEnabled();
	});

	it("gives each editor its own mark and keeps real brand color behavior", async () => {
		renderButton();
		await userEvent.click(await screen.findByRole("button", { name: "Open workspace options" }));
		const vscode = await screen.findByRole("menuitem", { name: "VS Code" });
		const cursor = await screen.findByRole("menuitem", { name: "Cursor" });
		expect(vscode.querySelector("svg path")?.getAttribute("d")).not.toEqual(
			cursor.querySelector("svg path")?.getAttribute("d"),
		);
		expect(vscode.querySelector("svg")?.style.color).toBe("rgb(31, 156, 240)");
		expect(cursor.querySelector("svg")?.style.color).toBe("");
	});

	it("opens the preferred editor from the open-in-editor shortcut", async () => {
		openMock.mockClear();
		setState(availableState);
		renderButton();
		await screen.findByRole("button", { name: "Open in Cursor" });

		act(() => appShortcutBus.emit({ id: "open-in-editor" }));

		await waitFor(() => expect(openMock).toHaveBeenCalledWith({ sessionId: "sess-1" }));
	});

	it("opens Finder from the open-in-editor shortcut when no editor is installed", async () => {
		openMock.mockClear();
		setState({
			targets: [{ id: "file-manager", name: "Finder", kind: "file_manager" }],
			preferredEditorId: "cursor",
			workspaceAvailable: true,
		});
		renderButton();
		await waitFor(() => expect(window.ao!.editorHandoff.getState).toHaveBeenCalled());
		await screen.findByRole("button", { name: "Open in Finder" });

		act(() => appShortcutBus.emit({ id: "open-in-editor" }));

		await waitFor(() =>
			expect(openMock).toHaveBeenCalledWith({ sessionId: "sess-1", targetId: "file-manager" }),
		);
	});

	it("ignores the open-in-editor shortcut while the workspace is unavailable", async () => {
		openMock.mockClear();
		setState({ ...availableState, workspaceAvailable: false, unavailableReason: "Workspace removed" });
		renderButton();
		await screen.findByRole("button", { name: "Workspace removed" });

		act(() => appShortcutBus.emit({ id: "open-in-editor" }));

		expect(openMock).not.toHaveBeenCalled();
	});

	it("surfaces an Electron launch failure", async () => {
		openMock.mockRejectedValueOnce(new Error("Could not open Cursor. Check that it is installed and try again."));
		renderButton();
		await userEvent.click(await screen.findByRole("button", { name: "Open in Cursor" }));
		await waitFor(() => expect(useUiStore.getState().globalToast).toMatchObject({
			title: "Open in Cursor",
			body: "Could not open Cursor. Check that it is installed and try again.",
			tone: "error",
		}));
		expect(screen.queryByRole("alert")).toBeNull();
	});
});
