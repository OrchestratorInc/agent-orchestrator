import { QueryClient, QueryClientProvider, focusManager } from "@tanstack/react-query";
import { act, fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { useState, type ReactNode } from "react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { SessionFileExplorer } from "./SessionFileExplorer";
import { FilesActionsHostContext, FilesTopbarHostContext } from "./files-topbar-host";
import { TooltipProvider } from "./ui/tooltip";
import { useUiStore } from "../stores/ui-store";
import { sessionSourceFileQueryOptions, type ReviewTarget, type WorkspaceBranches, type WorkspaceCommitSummary, type WorkspaceHistoryResponse } from "../hooks/useSessionWorkspaceFiles";
import { sessionUiKey } from "../lib/hosts";
import type { TreeNode } from "../hooks/useSessionWorkspaceTree";

const { getMock, postMock, hostAGetMock, hostBGetMock } = vi.hoisted(() => ({ getMock: vi.fn(), postMock: vi.fn(), hostAGetMock: vi.fn(), hostBGetMock: vi.fn() }));

vi.mock("../lib/api-client", () => ({
	apiClient: { GET: getMock, POST: postMock },
	getApiBaseUrl: () => "",
	hasTrustedApiBaseUrl: () => false,
	subscribeApiBaseUrl: () => () => undefined,
	apiErrorMessage: (error: unknown, fallback = "Request failed") => {
		if (error instanceof Error) return error.message;
		return fallback;
	},
	apiErrorCode: () => undefined,
}));

vi.mock("../lib/host-clients", () => ({
	clientForHost: (hostId: string) => {
		if (hostId === "host-a") return { GET: hostAGetMock };
		if (hostId === "host-b") return { GET: hostBGetMock };
		throw new Error(`Host ${hostId} is not connected`);
	},
	clientForSessionHost: (hostId?: string) => {
		if (!hostId) return { GET: getMock, POST: postMock };
		if (hostId === "host-a") return { GET: hostAGetMock };
		if (hostId === "host-b") return { GET: hostBGetMock };
		throw new Error(`Host ${hostId} is not connected`);
	},
	baseUrlForHost: (hostId: string) => hostId === "host-a" || hostId === "host-b" ? `http://127.0.0.1:4000/${hostId}` : undefined,
	connectedHost: (hostId: string) => hostId === "host-a" || hostId === "host-b" ? { base: `http://127.0.0.1:4000/${hostId}` } : undefined,
	isQuickTunnelHost: () => false,
	subscribeConnectedHosts: () => () => undefined,
}));

vi.mock("./FileTree", () => ({
	FileTree: ({
		changedOnly,
		changedOnlyData,
		forceChangedOnly = false,
		filterText,
		onSelectPath,
		onPrefetchPath,
	}: {
		changedOnly: boolean;
		changedOnlyData: TreeNode[];
		forceChangedOnly?: boolean;
		filterText: string;
		onSelectPath: (node: { path: string; type: "file" }) => void;
		onPrefetchPath?: (node: TreeNode) => void;
	}) => {
		const [expanded, setExpanded] = useState(false);
		const filePaths = (nodes: TreeNode[]): string[] => nodes.flatMap((node) => node.children ? filePaths(node.children) : [node.path]);
		const selectablePath = filePaths(changedOnlyData)[0] ?? "src/App.tsx";
		return <div>
			<span data-testid="tree-changed-only">{String(changedOnly || forceChangedOnly)}</span>
			<span data-testid="tree-files">{filePaths(changedOnlyData).join(" ")}</span>
			<span data-testid="tree-filter">{filterText}</span>
			<button onClick={() => setExpanded((current) => !current)} type="button">expand src</button>
			{expanded ? <span>src directory expanded</span> : null}
			<button onPointerEnter={() => onPrefetchPath?.({ name: selectablePath.split("/").pop()!, path: selectablePath, type: "file" })} onClick={() => onSelectPath({ path: selectablePath, type: "file" })} type="button">
				select {selectablePath}
			</button>
		</div>;
	},
}));

vi.mock("./FileContentPane", () => ({
	FileContentPane: ({ onOpenInCenter, commitSha, initialEditing, initialMode, path, previousPath, split }: { onOpenInCenter?:(mode:"file")=>void; commitSha?: string; initialEditing?: boolean; initialMode?: string; path: string | null; previousPath?: string; split?: boolean }) => <div data-commit-sha={commitSha ?? ""} data-editing={String(Boolean(initialEditing))} data-mode={initialMode ?? "default"} data-previous-path={previousPath} data-split={String(Boolean(split))} data-testid="content-pane">{path ?? "none"}{onOpenInCenter ? <button onClick={()=>onOpenInCenter("file")} type="button">Open preview in center</button> : null}</div>,
}));

vi.mock("./diffs/WorkspaceReviewPane", () => ({
	WorkspaceReviewPane: ({ canOpenInCenter = true, collapseRequest, filter, onAllCollapsedChange, onBrowseAll, onOpenFile, scrollRequest, target }: { canOpenInCenter?: boolean; collapseRequest?: { expand: boolean; key: number }; filter: string; onAllCollapsedChange?: (allCollapsed: boolean) => void; onBrowseAll?: () => void; onOpenFile?: (path: string, options?: { editing?: boolean; mode?: "diff" | "file" | "rendered" }) => void; scrollRequest?: { path: string; key: number }; target: ReviewTarget }) => (
		<div data-testid="review-pane">
			<span data-testid="review-filter">{filter}</span>
			<span data-testid="review-scroll">{scrollRequest ? `${scrollRequest.path}:${scrollRequest.key}` : ""}</span>
			<span data-testid="review-target">{target.key}</span>
			<span data-testid="review-collapse">{collapseRequest ? `${collapseRequest.expand ? "expand" : "collapse"}:${collapseRequest.key}` : ""}</span>
			<button onClick={() => onAllCollapsedChange?.(true)} type="button">Report all collapsed</button>
			<button onClick={() => onAllCollapsedChange?.(false)} type="button">Report an expanded file</button>
			<button onClick={onBrowseAll} type="button">Browse all files</button>
			<button onClick={() => onOpenFile?.("src/App.tsx", { mode: "file" })} type="button">Open full file</button>
			<button onClick={() => onOpenFile?.("README.md", { mode: "rendered" })} type="button">Render README.md</button>
			<button onClick={() => onOpenFile?.("src/App.tsx", { editing: true, mode: "file" })} type="button">Edit src/App.tsx</button>
			{canOpenInCenter ? <button onClick={() => onOpenFile?.("src/App.tsx", { mode: "diff", center:true } as never)} type="button">Open diff in center</button> : null}
		</div>
	),
}));

function renderWithQuery(children: ReactNode) {
	const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
	return {
		client,
		...render(
			<QueryClientProvider client={client}>
				<TooltipProvider>{children}</TooltipProvider>
			</QueryClientProvider>,
		),
	};
}

describe("SessionFileExplorer", () => {
	beforeEach(() => {
		window.localStorage.clear();
		useUiStore.setState({ inspectorSessions: {} });
		getMock.mockReset().mockResolvedValue({
			data: {
				sessionId: "sess-1",
				files: [{ path: "src/App.tsx", status: "modified", additions: 1, deletions: 0, size: 10, binary: false }],
				sections: { committed: [], staged: [], unstaged: [{ path: "src/App.tsx", status: "modified", additions: 1, deletions: 0, size: 10, binary: false }], untracked: [] },
				commits: [],
				summary: { additions: 1, deletions: 0, files: 1 },
				truncated: false,
				workspaceVersion: "version-1",
			},
		});
		postMock.mockReset();
		hostAGetMock.mockReset();
		hostBGetMock.mockReset();
	});

	it("warms the exact file query on hover so opening it needs no second request", async () => {
		useUiStore.getState().setFilesChangedOnly("hover-session", false);
		const detail = { sessionId: "hover-session", path: "src/App.tsx", content: "ready before click" };
		const previous = getMock.getMockImplementation()!;
		getMock.mockImplementation((path: string, options: unknown) => path.endsWith("/workspace/file") ? Promise.resolve({ data: detail }) : previous(path, options));
		const { client } = renderWithQuery(<SessionFileExplorer sessionId="hover-session" />);
		fireEvent.pointerEnter(screen.getByRole("button", { name: "select src/App.tsx" }));
		await waitFor(() => expect(client.getQueryData(["session-workspace-file", "hover-session", "combined", "", "src/App.tsx"])).toEqual(detail));
		const fileCalls = () => getMock.mock.calls.filter(([path]) => path.endsWith("/workspace/file"));
		expect(fileCalls()).toHaveLength(1);
		await client.fetchQuery(sessionSourceFileQueryOptions("hover-session", { kind: "workspace" }, "src/App.tsx"));
		expect(fileCalls()).toHaveLength(1);
	});

	it("keeps same-id remote file state on A and B separate while opening center tabs", async () => {
		const response = (content: string) => ({
			data: {
				sessionId: "same-id",
				files: [{ path: "src/App.tsx", status: "modified", additions: 1, deletions: 0, size: 10, binary: false, content }],
				sections: { committed: [], staged: [], unstaged: [{ path: "src/App.tsx", status: "modified", additions: 1, deletions: 0, size: 10, binary: false }], untracked: [] },
				commits: [], summary: { additions: 1, deletions: 0, files: 1 }, truncated: false, workspaceVersion: content,
			},
		});
		hostAGetMock.mockImplementation(async (path: string) => path.endsWith("/pr") ? { data: { prs: [] } } : response("A"));
		hostBGetMock.mockImplementation(async (path: string) => path.endsWith("/pr") ? { data: { prs: [] } } : response("B"));
		const openOnA = vi.fn();
		const openOnB = vi.fn();
		const { client } = renderWithQuery(<>
			<SessionFileExplorer hostId="host-a" onOpenFile={openOnA} sessionId="same-id" />
			<SessionFileExplorer hostId="host-b" onOpenFile={openOnB} sessionId="same-id" />
		</>);
		await waitFor(() => {
			expect(client.getQueryData(["session-workspace-files", "host-a", "same-id"])).toMatchObject({ workspaceVersion: "A" });
			expect(client.getQueryData(["session-workspace-files", "host-b", "same-id"])).toMatchObject({ workspaceVersion: "B" });
		});
		const [a, b] = screen.getAllByRole("region", { name: "Session files" });
		await userEvent.click(within(a).getByRole("tab", { name: "Files" }));
		expect(useUiStore.getState().inspectorSessions[sessionUiKey("same-id", "host-a")]?.filesChangedOnly).toBe(false);
		expect(useUiStore.getState().inspectorSessions[sessionUiKey("same-id", "host-b")]?.filesChangedOnly).toBeUndefined();
		await userEvent.click(within(a).getByRole("button", { name: "select src/App.tsx" }));
		expect(openOnA).toHaveBeenCalledWith("src/App.tsx", { mode: "file" });
		expect(openOnB).not.toHaveBeenCalled();
		expect(within(a).queryByTestId("content-pane")).not.toBeInTheDocument();
		expect(within(b).queryByTestId("content-pane")).not.toBeInTheDocument();
		expect(getMock).not.toHaveBeenCalled();
	});

	it("keeps the filtered tree visible and opens a selected file in the center", async () => {
		const onOpenFile = vi.fn();
		useUiStore.getState().setFilesChangedOnly("sess-explorer-1", false);
		renderWithQuery(<SessionFileExplorer onOpenFile={onOpenFile} sessionId="sess-explorer-1" />);

		const input = screen.getByRole("textbox", { name: "Filter files" });
		fireEvent.change(input, { target: { value: "app" } });
		expect(screen.getByTestId("tree-filter")).toHaveTextContent("app");

		expect(screen.queryByTestId("content-pane")).not.toBeInTheDocument();
		await userEvent.click(screen.getByRole("button", { name: "select src/App.tsx" }));
		expect(screen.queryByTestId("content-pane")).not.toBeInTheDocument();
		expect(screen.getByTestId("tree-changed-only")).toBeInTheDocument();
		expect(screen.queryByRole("button", { name: "View options" })).not.toBeInTheDocument();
		expect(onOpenFile).toHaveBeenCalledWith("src/App.tsx", { mode: "file" });
	});

	it("preserves expanded parent directories while files open in the center", async () => {
		const onOpenFile = vi.fn();
		useUiStore.getState().setFilesChangedOnly("sess-explorer-parent", false);
		renderWithQuery(<SessionFileExplorer onOpenFile={onOpenFile} sessionId="sess-explorer-parent" />);

		await userEvent.click(screen.getByRole("button", { name: "expand src" }));
		expect(screen.getByText("src directory expanded")).toBeInTheDocument();
		await userEvent.click(screen.getByRole("button", { name: "select src/App.tsx" }));

		expect(screen.getByText("src directory expanded")).toBeInTheDocument();
		expect(onOpenFile).toHaveBeenCalledOnce();
	});

	it("keeps the tree and content side by side when maximized", async () => {
		const widthSpy = vi.spyOn(HTMLElement.prototype, "offsetWidth", "get").mockReturnValue(500);
		useUiStore.getState().setFilesChangedOnly("sess-explorer-maximized", false);
		const { container } = renderWithQuery(<SessionFileExplorer isMaximized sessionId="sess-explorer-maximized" />);

		// Maximized: both are mounted at once, with no back button.
		expect(screen.getByTestId("tree-changed-only")).toBeInTheDocument();
		expect(screen.getByTestId("content-pane")).toHaveTextContent("none");
		expect(screen.queryByRole("button", { name: "Back to file tree" })).not.toBeInTheDocument();

		await userEvent.click(screen.getByRole("button", { name: "select src/App.tsx" }));
		expect(screen.getByTestId("content-pane")).toHaveTextContent("src/App.tsx");
		expect(screen.getByTestId("tree-changed-only")).toBeInTheDocument();

		// Preview first, tree on the right.
		const panels = container.querySelectorAll('[data-slot="resizable-panel"]');
		expect(panels).toHaveLength(2);
		expect(panels[0]).toHaveStyle({ flexGrow: "74" });
		expect(panels[1]).toHaveStyle({ flexGrow: "26" });

		// With the view tabs showing, the active Files tab doubles as the tree toggle.
		await userEvent.click(screen.getByRole("tab", { name: "Hide file tree" }));
		expect(screen.queryByTestId("tree-changed-only")).not.toBeInTheDocument();
		expect(screen.getByTestId("content-pane")).toHaveTextContent("src/App.tsx");
		await userEvent.click(screen.getByRole("tab", { name: "Show file tree" }));
		expect(screen.getByTestId("tree-changed-only")).toBeInTheDocument();
		widthSpy.mockRestore();
	});

	it("offers the diff layout in All files only while the maximized preview shows a changed file", async () => {
		useUiStore.getState().setFilesChangedOnly("sess-layout-docked", false);
		const docked = renderWithQuery(<SessionFileExplorer onOpenFile={vi.fn()} sessionId="sess-layout-docked" />);
		expect(await screen.findByRole("tab", { name: "Files" })).toHaveAttribute("aria-selected", "true");
		// Docked, the tree opens plain files in the centre: nothing here to lay out.
		expect(screen.queryByRole("button", { name: "Split diff view" })).not.toBeInTheDocument();
		docked.unmount();

		useUiStore.getState().setFilesChangedOnly("sess-layout-max", false);
		renderWithQuery(<SessionFileExplorer isMaximized sessionId="sess-layout-max" />);
		expect(await screen.findByTestId("content-pane")).toHaveTextContent("none");
		expect(screen.queryByRole("button", { name: "Split diff view" })).not.toBeInTheDocument();
		await userEvent.click(screen.getByRole("button", { name: "select src/App.tsx" }));
		expect(screen.getByRole("button", { name: "Split diff view" })).toBeInTheDocument();
	});

	it("keeps the header in place when switching between the Changes and Files views", async () => {
		renderWithQuery(<SessionFileExplorer isMaximized onToggleMaximized={vi.fn()} sessionId="sess-explorer-steady" />);

		expect(await screen.findByTestId("review-pane")).toBeInTheDocument();
		const header = screen.getByRole("tab", { name: "Changes" }).closest("header");
		const changesHeaderClass = header?.className;
		await userEvent.click(screen.getByRole("tab", { name: "Files" }));

		// The split's divider is drawn on the content, not by growing the header.
		expect(screen.getByTestId("content-pane")).toBeInTheDocument();
		expect(header?.className).toBe(changesHeaderClass);
		expect(header?.nextElementSibling).toHaveClass("border-t");
	});

	it("puts the whole maximized header into the overlay titlebar it is given", async () => {
		const titlebar = document.createElement("div");
		document.body.append(titlebar);
		const { container } = renderWithQuery(
			<FilesTopbarHostContext.Provider value={titlebar}>
				<SessionFileExplorer isMaximized onToggleMaximized={vi.fn()} sessionId="sess-explorer-titlebar" />
			</FilesTopbarHostContext.Provider>,
		);

		const filter = screen.getByRole("textbox", { name: "Filter files" });
		expect(titlebar).toContainElement(filter);
		// One row: the pickers before the filter, the view controls and minimize after it.
		const changes = await screen.findByRole("button", { name: /^Changes to review/ });
		const start = titlebar.querySelector(".files-popout-titlebar__start");
		const end = titlebar.querySelector(".files-popout-titlebar__end");
		expect(start).toContainElement(changes);
		expect(end).toContainElement(screen.getByRole("button", { name: "Split diff view" }));
		expect(end).toContainElement(screen.getByRole("tab", { name: "Changes" }));
		expect(end?.lastElementChild).toContainElement(screen.getByRole("button", { name: "Minimize files" }));
		expect(container.querySelector("header")).toBeNull();
		// The titlebar's own border divides it from the review, so the split adds none.
		expect(container.querySelector('[data-slot="resizable-panel-group"]')).not.toHaveClass("border-t");
		titlebar.remove();
	});

	it("puts maximize in the top bar's actions slot and the ⋯ menu before the view tabs", async () => {
		const filterHost = document.createElement("div");
		const actionsHost = document.createElement("div");
		document.body.append(filterHost, actionsHost);
		renderWithQuery(
			<FilesTopbarHostContext.Provider value={filterHost}>
				<FilesActionsHostContext.Provider value={actionsHost}>
					<SessionFileExplorer onToggleMaximized={vi.fn()} sessionId="sess-explorer-actions" />
				</FilesActionsHostContext.Provider>
			</FilesTopbarHostContext.Provider>,
		);

		expect(await screen.findByTestId("review-pane")).toBeInTheDocument();
		expect(filterHost).toContainElement(screen.getByRole("textbox", { name: "Filter files" }));
		expect(actionsHost).toContainElement(screen.getByRole("button", { name: "Maximize files" }));
		const viewOptions = screen.getByRole("button", { name: "Split diff view" });
		expect(actionsHost).not.toContainElement(viewOptions);
		const header = viewOptions.closest("header");
		expect(header).not.toBeNull();
		// Same row as the pickers, right before the Changes / Files tabs.
		expect(viewOptions.compareDocumentPosition(screen.getByRole("tab", { name: "Changes" })) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy();
		expect(header).toContainElement(screen.getByRole("tab", { name: "Changes" }));
		filterHost.remove();
		actionsHost.remove();
	});

	it("defaults to the continuous changes review and can switch to the full file tree", async () => {
		const sessionId = "sess-explorer-2";
		renderWithQuery(<SessionFileExplorer sessionId={sessionId} />);

		expect(await screen.findByTestId("review-pane")).toBeInTheDocument();
		await userEvent.click(screen.getByRole("tab", { name: "Files" }));

		expect(screen.getByTestId("tree-changed-only")).toHaveTextContent("false");
		expect(useUiStore.getState().inspectorSessions[sessionId]?.filesChangedOnly).toBe(false);
	});

	it("defaults to the file tree when the workspace has no changes", async () => {
		getMock.mockResolvedValue({
			data: {
				sessionId: "sess-clean",
				files: [],
				sections: { committed: [], staged: [], unstaged: [], untracked: [] },
				commits: [],
				summary: { additions: 0, deletions: 0, files: 0 },
				truncated: false,
				workspaceVersion: "clean-1",
			},
		});
		const titlebar = document.createElement("div");
		document.body.append(titlebar);
		const { container } = renderWithQuery(
			<FilesTopbarHostContext.Provider value={titlebar}>
				<SessionFileExplorer onOpenFile={vi.fn()} sessionId="sess-clean" />
			</FilesTopbarHostContext.Provider>,
		);

		expect(await screen.findByTestId("tree-changed-only")).toHaveTextContent("false");
		expect(screen.queryByTestId("review-pane")).not.toBeInTheDocument();
		expect(screen.queryByRole("tab", { name: "Changes" })).not.toBeInTheDocument();
		expect(screen.queryByRole("tab", { name: "Files" })).not.toBeInTheDocument();
		// Nothing to review: the whole header row goes, pickers and layout toggle
		// included, so the tree starts right under the top bar.
		expect(screen.queryByRole("button", { name: /^Changes to review/ })).not.toBeInTheDocument();
		expect(screen.queryByRole("button", { name: /^Branch to review/ })).not.toBeInTheDocument();
		expect(screen.queryByRole("button", { name: "Split diff view" })).not.toBeInTheDocument();
		expect(container.querySelector("header")).toBeNull();
		titlebar.remove();
	});

	it("shows only the tree beside the preview when a maximized session has nothing to review", async () => {
		getMock.mockResolvedValue({
			data: {
				sessionId: "sess-clean-max",
				files: [],
				sections: { committed: [], staged: [], unstaged: [], untracked: [] },
				commits: [],
				summary: { additions: 0, deletions: 0, files: 0 },
				truncated: false,
				workspaceVersion: "clean-max-1",
			},
		});
		const titlebar = document.createElement("div");
		document.body.append(titlebar);
		const { container } = renderWithQuery(
			<FilesTopbarHostContext.Provider value={titlebar}>
				<SessionFileExplorer isMaximized onToggleMaximized={vi.fn()} sessionId="sess-clean-max" />
			</FilesTopbarHostContext.Provider>,
		);

		expect(await screen.findByTestId("content-pane")).toBeInTheDocument();
		expect(screen.getByTestId("tree-changed-only")).toHaveTextContent("false");
		expect(container.querySelector("header")).toBeNull();
		expect(screen.queryByRole("button", { name: "Hide file tree" })).not.toBeInTheDocument();
		// No header above it, so the split draws no second divider under the titlebar.
		expect(container.querySelector('[data-slot="resizable-panel-group"]')).not.toHaveClass("border-t");
		titlebar.remove();
	});

	it("puts a rail of the changed files beside the maximized review, and a pick scrolls to it", async () => {
		renderWithQuery(<SessionFileExplorer isMaximized onToggleMaximized={vi.fn()} sessionId="sess-review-rail" />);

		expect(await screen.findByTestId("review-pane")).toBeInTheDocument();
		// Only the review's files, every folder open; no button to hide it.
		expect(screen.getByTestId("tree-changed-only")).toHaveTextContent("true");
		expect(screen.getByTestId("tree-files")).toHaveTextContent("src/App.tsx");
		expect(screen.queryByRole("button", { name: /file tree$/ })).not.toBeInTheDocument();
		expect(screen.getByTestId("review-scroll")).toHaveTextContent("");

		await userEvent.click(screen.getByRole("button", { name: "select src/App.tsx" }));
		expect(screen.getByTestId("review-scroll")).toHaveTextContent("src/App.tsx:1");
		await userEvent.click(screen.getByRole("button", { name: "select src/App.tsx" }));
		expect(screen.getByTestId("review-scroll")).toHaveTextContent("src/App.tsx:2");
	});

	it("keeps the docked review without a changed-files rail", async () => {
		renderWithQuery(<SessionFileExplorer sessionId="sess-review-docked" />);
		expect(await screen.findByTestId("review-pane")).toBeInTheDocument();
		expect(screen.queryByTestId("tree-changed-only")).not.toBeInTheDocument();
	});

	it("keeps the continuous right-side diff visible when opening the full file in center", async () => {
		const onOpenFile = vi.fn();
		renderWithQuery(<SessionFileExplorer onOpenFile={onOpenFile} sessionId="sess-review-navigation" />);

		await userEvent.click(await screen.findByRole("button", { name: "Open full file" }));
		expect(screen.getByTestId("review-pane")).toBeInTheDocument();
		expect(screen.queryByTestId("content-pane")).not.toBeInTheDocument();
		expect(onOpenFile).toHaveBeenCalledWith("src/App.tsx", { mode: "file" });
	});

	it("opens a changed file diff in the center workspace", async () => {
		const onOpenFile = vi.fn();
		renderWithQuery(<SessionFileExplorer onOpenFile={onOpenFile} sessionId="sess-review-center" />);

		await userEvent.click(await screen.findByRole("button", { name: "Open diff in center" }));
		expect(onOpenFile).toHaveBeenCalledWith("src/App.tsx", { mode: "diff", center:true });
	});

	it("opens a review diff action in the syntax-aware file editor", async () => {
		const onOpenFile = vi.fn();
		renderWithQuery(<SessionFileExplorer onOpenFile={onOpenFile} sessionId="sess-review-edit" />);

		await userEvent.click(await screen.findByRole("button", { name: "Edit src/App.tsx" }));
		expect(screen.getByTestId("review-pane")).toBeInTheDocument();
		expect(onOpenFile).toHaveBeenCalledWith("src/App.tsx", { editing: true, mode: "file" });
	});

	it("opens the direct rendered action in the center while retaining the diff", async () => {
		const onOpenFile = vi.fn();
		renderWithQuery(<SessionFileExplorer onOpenFile={onOpenFile} sessionId="sess-review-rendered" />);
		await userEvent.click(await screen.findByRole("button", { name: "Render README.md" }));
		expect(screen.getByTestId("review-pane")).toBeInTheDocument();
		expect(onOpenFile).toHaveBeenCalledWith("README.md", { mode: "rendered" });
	});

	it("edits a review file in place when maximized instead of the hidden center", async () => {
		const onOpenFile = vi.fn();
		renderWithQuery(<SessionFileExplorer isMaximized onOpenFile={onOpenFile} sessionId="sess-review-edit-maximized" />);

		await userEvent.click(await screen.findByRole("button", { name: "Edit src/App.tsx" }));
		const pane = await screen.findByTestId("content-pane");
		expect(pane).toHaveTextContent("src/App.tsx");
		expect(pane).toHaveAttribute("data-editing", "true");
		expect(pane).toHaveAttribute("data-mode", "file");
		expect(onOpenFile).not.toHaveBeenCalled();
	});

	it("drops the open-diff-in-center action from the maximized review", async () => {
		renderWithQuery(<SessionFileExplorer isMaximized onOpenFile={vi.fn()} onToggleMaximized={vi.fn()} sessionId="sess-review-center-maximized" />);

		expect(await screen.findByRole("button", { name: "Edit src/App.tsx" })).toBeInTheDocument();
		expect(screen.queryByRole("button", { name: "Open diff in center" })).not.toBeInTheDocument();
	});

	it("moves the maximized full-file preview into the center while keeping its source", async () => {
		const onOpenFile = vi.fn();
		const onToggleMaximized = vi.fn();
		renderWithQuery(<SessionFileExplorer isMaximized onOpenFile={onOpenFile} onToggleMaximized={onToggleMaximized} sessionId="max-preview-center" />);
		await userEvent.click(await screen.findByRole("button", { name: "Open full file" }));
		await userEvent.click(screen.getByRole("button", { name: "Open preview in center" }));
		expect(onToggleMaximized).toHaveBeenCalledWith(false);
		expect(onOpenFile).toHaveBeenCalledWith("src/App.tsx", expect.objectContaining({ mode: "file", source: { kind: "workspace" }, editing: false }));
	});

	it("opens the rich preview in place when maximized", async () => {
		renderWithQuery(<SessionFileExplorer isMaximized sessionId="sess-review-rendered-maximized" />);

		await userEvent.click(await screen.findByRole("button", { name: "Render README.md" }));
		const pane = await screen.findByTestId("content-pane");
		expect(pane).toHaveTextContent("README.md");
		expect(pane).toHaveAttribute("data-mode", "rendered");
		expect(pane).toHaveAttribute("data-editing", "false");
	});

	it("always wraps file content and does not expose a wrap toggle", async () => {
		renderWithQuery(<SessionFileExplorer sessionId="sess-wrap" />);
		expect(await screen.findByTestId("review-pane")).toBeInTheDocument();
		expect(screen.queryByRole("button", { name: "Wrap lines" })).not.toBeInTheDocument();
	});

	it("switches the diff layout directly", async () => {
		window.localStorage.setItem("ao.files.diffStyle", "unified");
		const onSplitChange = vi.fn();
		renderWithQuery(<SessionFileExplorer onSplitChange={onSplitChange} sessionId="sess-explorer-3" />);
		expect(await screen.findByTestId("review-pane")).toBeInTheDocument();
  await userEvent.click(screen.getByRole("button",{name:"Split diff view"}));
  expect(screen.getByRole("button",{name:"Unified diff view"})).toHaveAttribute("aria-pressed","true");

		expect(onSplitChange).toHaveBeenCalledWith(true);
		expect(window.localStorage.getItem("ao.files.diffStyle")).toBe("split");
	});

	it("collapses and expands all files with one click", async () => {
		renderWithQuery(<SessionFileExplorer sessionId="sess-explorer-collapse" />);
		expect(await screen.findByTestId("review-pane")).toBeInTheDocument();

		expect(screen.getByRole("button", { name: "Collapse all files" }).querySelector("svg")).toHaveClass("lucide-list-chevrons-down-up");
		await userEvent.click(screen.getByRole("button", { name: "Collapse all files" }));
		expect(screen.getByTestId("review-collapse")).toHaveTextContent("collapse:1");

		// Once the review reports every file collapsed, the menu offers the way back.
		await userEvent.click(screen.getByRole("button", { name: "Report all collapsed" }));
		expect(screen.getByRole("button", { name: "Expand all files" }).querySelector("svg")).toHaveClass("lucide-list-chevrons-up-down");
		await userEvent.click(screen.getByRole("button", { name: "Expand all files" }));
		expect(screen.getByTestId("review-collapse")).toHaveTextContent("expand:2");
		await userEvent.click(screen.getByRole("button", { name: "Report an expanded file" }));
		expect(screen.getByRole("button", { name: "Collapse all files" }).querySelector("svg")).toHaveClass("lucide-list-chevrons-down-up");
	});

	it("opens the maximize tooltip on hover or keyboard focus, not when focus is handed back", async () => {
		renderWithQuery(<SessionFileExplorer onToggleMaximized={vi.fn()} sessionId="sess-explorer-tooltip" />);
		const maximize = await screen.findByRole("button", { name: "Maximize files" });
		// Closing the maximized overlay restores focus to this button in code.
		const focusVisible = vi.spyOn(maximize, "matches").mockImplementation((selector) => selector !== ":focus-visible");
		act(() => maximize.focus());
		await new Promise((resolve) => setTimeout(resolve, 50));
		expect(screen.queryByRole("tooltip")).not.toBeInTheDocument();
		act(() => maximize.blur());
		focusVisible.mockImplementation((selector) => selector === ":focus-visible");
		act(() => maximize.focus());
		expect(await screen.findByRole("tooltip")).toHaveTextContent("Maximize files");
		focusVisible.mockRestore();
	});

	it("lets the caller toggle between rail and maximized layouts", async () => {
		const onToggleMaximized = vi.fn();
		renderWithQuery(<SessionFileExplorer onToggleMaximized={onToggleMaximized} sessionId="sess-explorer-4" />);

		await userEvent.click(screen.getByRole("button", { name: "Maximize files" }));
		expect(onToggleMaximized).toHaveBeenCalledWith(true);
	});
});

describe("SessionFileExplorer review pickers", () => {
	const SHA = (letter: string) => letter.repeat(40);
	const changed = { path: "src/App.tsx", status: "modified" as const, additions: 1, deletions: 0, size: 10, binary: false };
	const staged = { path: "README.md", status: "modified" as const, additions: 2, deletions: 1, size: 10, binary: false };
	const manifest = {
		files: [changed, staged],
		sections: { uncommitted: [changed, staged], committed: [], staged: [staged], unstaged: [changed], untracked: [] },
		commits: [],
		summary: { additions: 3, deletions: 1, files: 2 },
		truncated: false,
		workspaceVersion: "version-1",
	};
	const commit = (letter: string): WorkspaceCommitSummary => ({
		sha: SHA(letter),
		subject: `Commit ${letter}`,
		author: "Ada",
		timestamp: "2026-10-01T00:00:00Z",
		files: [{ path: `${letter}.ts`, status: "modified", additions: 2, deletions: 1, size: 10, binary: false, editable: false, fileFingerprint: `fp:${letter}` }],
	});
	const branchList: WorkspaceBranches = {
		sessionId: "",
		supported: true,
		truncated: false,
		branches: [
			{ kind: "local", name: "feature/login", checkedOut: true, isDefault: false, tipSha: SHA("1"), updatedAt: "2026-10-02T00:00:00Z" },
			{ kind: "local", name: "main", checkedOut: false, isDefault: true, tipSha: SHA("2"), updatedAt: "2026-10-01T00:00:00Z" },
			{ kind: "pull_request", name: "fix/crash", checkedOut: false, isDefault: false, tipSha: SHA("3"), prNumber: 42, prUrl: "https://github.com/acme/app/pull/42", updatedAt: "2026-09-30T00:00:00Z" },
		],
	};
	const checkedOutHistory: WorkspaceHistoryResponse = {
		sessionId: "",
		commits: [commit("a"), commit("b"), commit("c")],
		view: { kind: "checked_out", rangeMode: "since_base", tipSha: SHA("1"), baseSha: SHA("9") },
	};
	const mainHistory: WorkspaceHistoryResponse = {
		sessionId: "",
		commits: [commit("e"), commit("f")],
		rangeSummary: { files: 2, additions: 4, deletions: 2 },
		view: { kind: "branch", name: "main", rangeMode: "since_base", baseRef: "origin/main", tipSha: SHA("2"), baseSha: SHA("8") },
	};
	const READ_PATHS = ["/workspace/manifest", "/workspace/branches", "/workspace/history", "/workspace/file/revision"];

	function routeReads(branches: WorkspaceBranches = branchList) {
		getMock.mockImplementation(async (path: string, init: { params: { path: { sessionId: string }; query?: { branch?: string } } }) => {
			const sessionId = init.params.path.sessionId;
			if (path.endsWith("/workspace/manifest")) return { data: { ...manifest, sessionId } };
			if (path.endsWith("/workspace/branches")) return { data: { ...branches, sessionId } };
			if (path.endsWith("/workspace/history")) return { data: { ...(init.params.query?.branch === "main" ? mainHistory : checkedOutHistory), sessionId } };
			throw new Error(`Unexpected GET ${path}`);
		});
		postMock.mockImplementation(async (_path: string, init: { params: { path: { sessionId: string } } }) => ({
			data: { sessionId: init.params.path.sessionId, tipSha: SHA("2"), contiguous: true, combinable: true, files: [changed], summary: { files: 1, additions: 1, deletions: 0 }, truncated: false },
		}));
	}

	// An open menu hides the rest of the page from role queries, its own trigger included.
	const changesTrigger = (scope: HTMLElement = document.body) => within(scope).getByRole("button", { hidden: true, name: /^Changes to review:/ });
	const selectionRequests = () => postMock.mock.calls.filter(([path]) => String(path).endsWith("/workspace/history/selection"));

	beforeEach(() => routeReads());

	it("shows Changes and Branch dropdowns side by side instead of the Workspace button", async () => {
		renderWithQuery(<SessionFileExplorer sessionId="sess-pickers" />);

		const changes = await screen.findByRole("button", { name: "Changes to review: Uncommitted" });
		const branch = await screen.findByRole("button", { name: "Branch to review: feature/login" });
		const header = changes.closest("header");
		expect(header).toHaveClass("@container/files-header");
		expect(header).toContainElement(branch);
		expect(changes.compareDocumentPosition(branch) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy();
		expect(screen.queryByRole("button", { name: "Workspace" })).not.toBeInTheDocument();
		expect(screen.queryByRole("button", { name: "File source" })).not.toBeInTheDocument();
		expect(screen.getByTestId("review-target")).toHaveTextContent("uncommitted");
	});

	it("hides the layout and collapse buttons while the picked changes are empty", async () => {
		const nothingStaged = { ...manifest, sections: { ...manifest.sections, staged: [], unstaged: [changed] } };
		getMock.mockImplementation(async (path: string, init: { params: { path: { sessionId: string } } }) => {
			if (path.endsWith("/workspace/manifest")) return { data: { ...nothingStaged, sessionId: init.params.path.sessionId } };
			if (path.endsWith("/workspace/branches")) return { data: { ...branchList, sessionId: init.params.path.sessionId } };
			if (path.endsWith("/workspace/history")) return { data: { ...checkedOutHistory, sessionId: init.params.path.sessionId } };
			throw new Error(`Unexpected GET ${path}`);
		});
		renderWithQuery(<SessionFileExplorer sessionId="sess-empty-scope" />);

		expect(await screen.findByRole("button", { name: "Collapse all files" })).toBeInTheDocument();
		expect(screen.getByRole("button", { name: "Split diff view" })).toBeInTheDocument();
		// Nothing is staged, so the menu leaves Staged out.
		await userEvent.click(changesTrigger());
		expect((await screen.findAllByRole("menuitemradio")).map((item) => item.textContent?.replace(/[+−]\d+/g, ""))).toEqual(["Uncommitted", "Unstaged"]);
		await userEvent.keyboard("{Escape}");

		// Staged picked earlier and since emptied.
		act(() => useUiStore.getState().setFilesSelection("sess-empty-scope", { branch: { kind: "checked_out" }, changes: { kind: "staged" } }));
		await waitFor(() => expect(screen.getByTestId("review-target")).toHaveTextContent("staged"));
		expect(screen.queryByRole("button", { name: "Split diff view" })).not.toBeInTheDocument();
		expect(screen.queryByRole("button", { name: "Collapse all files" })).not.toBeInTheDocument();
		// The view tabs stay: All files still has something to show.
		expect(screen.getByRole("tab", { name: "Files" })).toBeInTheDocument();

		await userEvent.click(changesTrigger());
		await userEvent.click(await screen.findByRole("menuitemradio", { name: /^Unstaged/ }));
		expect(await screen.findByRole("button", { name: "Split diff view" })).toBeInTheDocument();
		expect(screen.getByRole("button", { name: "Collapse all files" })).toBeInTheDocument();
	});

	it("drops the maximized changed-files rail while the review has no files", async () => {
		const nothingStaged = { ...manifest, sections: { ...manifest.sections, staged: [], unstaged: [changed] } };
		getMock.mockImplementation(async (path: string, init: { params: { path: { sessionId: string } } }) => {
			if (path.endsWith("/workspace/manifest")) return { data: { ...nothingStaged, sessionId: init.params.path.sessionId } };
			if (path.endsWith("/workspace/branches")) return { data: { ...branchList, sessionId: init.params.path.sessionId } };
			if (path.endsWith("/workspace/history")) return { data: { ...checkedOutHistory, sessionId: init.params.path.sessionId } };
			throw new Error(`Unexpected GET ${path}`);
		});
		// jsdom draws every element as an empty box at 0,0, so the rail's resize
		// handle would claim every click; move it out of the way.
		const rect = HTMLElement.prototype.getBoundingClientRect;
		const rectSpy = vi.spyOn(HTMLElement.prototype, "getBoundingClientRect").mockImplementation(function (this: HTMLElement) {
			return this.dataset.slot === "resizable-handle" ? new DOMRect(-1000, -1000, 1, 1) : rect.call(this);
		});
		const { container } = renderWithQuery(<SessionFileExplorer isMaximized sessionId="sess-empty-rail" />);
		expect(await screen.findByTestId("tree-changed-only")).toHaveTextContent("true");

		// Staged picked earlier and since emptied.
		act(() => useUiStore.getState().setFilesSelection("sess-empty-rail", { branch: { kind: "checked_out" }, changes: { kind: "staged" } }));
		await waitFor(() => expect(screen.getByTestId("review-target")).toHaveTextContent("staged"));
		// Nothing to list: the review takes the whole width, with no split.
		expect(screen.queryByTestId("tree-changed-only")).not.toBeInTheDocument();
		expect(container.querySelectorAll('[data-slot="resizable-panel"]')).toHaveLength(1);
		expect(container.querySelector('[data-slot="resizable-handle"]')).toBeNull();

		await userEvent.click(changesTrigger());
		await userEvent.click(await screen.findByRole("menuitemradio", { name: /^Unstaged/ }));
		expect(await screen.findByTestId("tree-changed-only")).toHaveTextContent("true");
		const review = screen.getByTestId("review-pane");

		// A filter that matches none of the files empties the rail, so it goes too,
		// without remounting the review beside it.
		const filter = screen.getByRole("textbox", { name: "Filter files" });
		await userEvent.type(filter, "nothing-matches");
		await waitFor(() => expect(screen.queryByTestId("tree-changed-only")).not.toBeInTheDocument());
		expect(screen.getByTestId("review-pane")).toBe(review);
		await userEvent.clear(filter);
		expect(await screen.findByTestId("tree-changed-only")).toBeInTheDocument();
		expect(screen.getByTestId("review-pane")).toBe(review);
		rectSpy.mockRestore();
	});

	it("keeps the pickers for a session with no changes that has a pull request", async () => {
		const clean = { ...manifest, files: [], sections: { uncommitted: [], committed: [], staged: [], unstaged: [], untracked: [] }, summary: { additions: 0, deletions: 0, files: 0 } };
		const titlebar = document.createElement("div");
		document.body.append(titlebar);
		const render = (sessionId: string, branches: WorkspaceBranches) => {
			getMock.mockImplementation(async (path: string, init: { params: { path: { sessionId: string } } }) => {
				if (path.endsWith("/workspace/manifest")) return { data: { ...clean, sessionId: init.params.path.sessionId } };
				if (path.endsWith("/workspace/branches")) return { data: { ...branches, sessionId: init.params.path.sessionId } };
				if (path.endsWith("/workspace/history")) return { data: { ...checkedOutHistory, commits: [], sessionId: init.params.path.sessionId } };
				throw new Error(`Unexpected GET ${path}`);
			});
			return renderWithQuery(
				<FilesTopbarHostContext.Provider value={titlebar}>
					<SessionFileExplorer onOpenFile={vi.fn()} projectKind="single_repo" sessionId={sessionId} />
				</FilesTopbarHostContext.Provider>,
			);
		};

		const withPr = render("sess-clean-pr", branchList);
		expect(await screen.findByRole("button", { name: "Branch to review: feature/login" })).toBeInTheDocument();
		expect(screen.getByTestId("tree-changed-only")).toHaveTextContent("false");
		withPr.unmount();

		// The same session without its PR has nothing to review: the row goes.
		const noPr = render("sess-clean-no-pr", { ...branchList, branches: branchList.branches.filter((row) => row.prNumber == null) });
		await waitFor(() => expect(noPr.container.querySelector("header")).toBeNull());
		expect(screen.getByTestId("tree-changed-only")).toHaveTextContent("false");
		titlebar.remove();
	});

	it("lists local branches with the checked-out one first and updated times", async () => {
		renderWithQuery(<SessionFileExplorer sessionId="sess-branch-list" />);

		await userEvent.click(await screen.findByRole("button", { name: "Branch to review: feature/login" }));
		const rows = await screen.findAllByRole("menuitemradio");
		expect(rows.map((row) => row.textContent)).toEqual([
			expect.stringMatching(/^feature\/login.*Checked out · Updated/),
			expect.stringMatching(/^main.*Updated/),
			expect.stringMatching(/^fix\/crash.*Pull request #42 · Updated/),
		]);
		expect(rows[0]).toHaveAttribute("aria-checked", "true");
	});

	it("picking another branch offers only commits, hides the view tabs, and sends only read requests", async () => {
		const sessionId = "sess-other-branch";
		renderWithQuery(<SessionFileExplorer sessionId={sessionId} />);
		expect(await screen.findByRole("tablist", { name: "File view" })).toBeInTheDocument();

		await userEvent.click(await screen.findByRole("button", { name: "Branch to review: feature/login" }));
		await userEvent.click(await screen.findByRole("menuitemradio", { name: /^main/ }));

		expect(await screen.findByRole("button", { name: "Branch to review: main" })).toBeInTheDocument();
		await waitFor(() => expect(changesTrigger()).toHaveAccessibleName("Changes to review: All commits"));
		expect(screen.getByRole("tablist", { name: "File view" })).toBeInTheDocument();
		expect(screen.getByTestId("review-target")).toHaveTextContent("branch:main:all");
		expect(useUiStore.getState().inspectorSessions[sessionId]?.filesSelection).toEqual({ branch: { kind: "branch", name: "main", tipSha: SHA("2") }, changes: null });

		await userEvent.click(changesTrigger());
		expect(screen.queryByRole("menuitemradio")).not.toBeInTheDocument();
		expect(await screen.findByRole("menuitemcheckbox", { name: /^All commits/ })).toHaveAttribute("aria-checked", "true");
		expect(screen.getByRole("menuitemcheckbox", { name: /^Commit e/ })).toBeInTheDocument();

		await waitFor(() => expect(selectionRequests()).toHaveLength(1));
		expect(selectionRequests()[0][1].body).toEqual(expect.objectContaining({ branch: "main", commitShas: [], expectedTipSha: SHA("2"), expectedBaseSha: SHA("8") }));
		expect(getMock).toHaveBeenCalledWith("/api/v1/sessions/{sessionId}/workspace/history", expect.objectContaining({ params: { path: { sessionId }, query: { branch: "main" } } }));
		for (const [path] of getMock.mock.calls) expect(READ_PATHS.some((read) => String(path).endsWith(read))).toBe(true);
		for (const [path] of postMock.mock.calls) expect(String(path)).toMatch(/\/workspace\/(history\/selection|diffs)$/);
	});

	it("names the session's branch on the picker before the branch list arrives", async () => {
		getMock.mockImplementation(async (path: string, init: { params: { path: { sessionId: string } } }) => {
			const sessionId = init.params.path.sessionId;
			if (path.endsWith("/workspace/manifest")) return { data: { ...manifest, sessionId } };
			// The list is slow: it never answers in this test.
			if (path.endsWith("/workspace/branches")) return new Promise(() => {});
			if (path.endsWith("/workspace/history")) return { data: { ...checkedOutHistory, sessionId } };
			throw new Error(`Unexpected GET ${path}`);
		});
		renderWithQuery(<SessionFileExplorer checkedOutBranch="feature/login" projectKind="single_repo" sessionId="sess-branch-fallback" />);
		expect(await screen.findByRole("button", { name: "Branch to review: feature/login" })).toBeInTheDocument();
		expect(screen.queryByRole("button", { name: "Branch to review: Checked out" })).not.toBeInTheDocument();
	});

	it("keeps another branch current on its own, with no reload button", async () => {
		let mainTip = SHA("2");
		getMock.mockImplementation(async (path: string, init: { params: { path: { sessionId: string }; query?: { branch?: string } } }) => {
			const sessionId = init.params.path.sessionId;
			if (path.endsWith("/workspace/manifest")) return { data: { ...manifest, sessionId } };
			if (path.endsWith("/workspace/branches")) {
				return { data: { ...branchList, sessionId, branches: branchList.branches.map((row) => (row.name === "main" ? { ...row, tipSha: mainTip } : row)) } };
			}
			if (path.endsWith("/workspace/history")) return { data: { ...(init.params.query?.branch === "main" ? mainHistory : checkedOutHistory), sessionId } };
			throw new Error(`Unexpected GET ${path}`);
		});
		getMock.mockClear();
		const reads = (suffix: string, branch?: string) => getMock.mock.calls.filter(([path, init]) => init.params.path.sessionId === "sess-auto-refresh" && String(path).endsWith(suffix) && (branch === undefined || init.params.query?.branch === branch)).length;
		const regainFocus = () => act(() => {
			focusManager.setFocused(false);
			focusManager.setFocused(true);
		});
		renderWithQuery(<SessionFileExplorer sessionId="sess-auto-refresh" />);
		await screen.findByRole("button", { name: "Branch to review: feature/login" });
		await waitFor(() => expect(reads("/workspace/branches")).toBe(1));

		// On the checked-out branch workspace events keep things current: focus alone reads nothing.
		regainFocus();
		await new Promise((resolve) => setTimeout(resolve, 50));
		expect(reads("/workspace/branches")).toBe(1);

		await userEvent.click(screen.getByRole("button", { name: "Branch to review: feature/login" }));
		await userEvent.click(await screen.findByRole("menuitemradio", { name: /^main/ }));
		await waitFor(() => expect(screen.getByTestId("review-target")).toHaveTextContent("branch:main:all"));
		expect(screen.queryByRole("button", { name: "Reload selected revision" })).not.toBeInTheDocument();
		const branchReads = reads("/workspace/branches");
		const mainReads = reads("/workspace/history", "main");

		// Someone pushes to main; coming back to the window picks it up.
		mainTip = SHA("7");
		regainFocus();
		await waitFor(() => expect(reads("/workspace/branches")).toBe(branchReads + 1));
		await waitFor(() => expect(reads("/workspace/history", "main")).toBe(mainReads + 1));
		focusManager.setFocused(undefined);
	});

	it("falls back with a notice when the selected branch disappears", async () => {
		const sessionId = "sess-branch-gone";
		useUiStore.getState().setFilesSelection(sessionId, { branch: { kind: "branch", name: "spike/old" }, changes: null });
		renderWithQuery(<SessionFileExplorer sessionId={sessionId} />);

		expect(await screen.findByRole("status")).toHaveTextContent("spike/old is no longer available; showing the checked-out branch.");
		expect(useUiStore.getState().inspectorSessions[sessionId]?.filesSelection).toBeUndefined();
		expect(await screen.findByRole("button", { name: "Branch to review: feature/login" })).toBeInTheDocument();
	});

	it("commit checkboxes keep the menu open and send commitShas, and All commits clears the ticks", async () => {
		renderWithQuery(<SessionFileExplorer sessionId="sess-ticks" />);

		await userEvent.click(await screen.findByRole("button", { name: "Changes to review: Uncommitted" }));
		await userEvent.click(await screen.findByRole("menuitem", { name: /^Commits/ }));
		await userEvent.click(await screen.findByRole("menuitemcheckbox", { name: /^Commit a/ }));
		await userEvent.click(screen.getByRole("menuitemcheckbox", { name: /^Commit c/ }));
		expect(screen.getAllByRole("menu").length).toBeGreaterThan(0);
		expect(screen.getByRole("menuitemcheckbox", { name: /^Commit c/ })).toHaveAttribute("aria-checked", "true");

		await waitFor(() => expect(selectionRequests().at(-1)?.[1].body).toEqual(expect.objectContaining({ commitShas: [SHA("a"), SHA("c")], workspaceVersion: "version-1" })));
		expect(changesTrigger()).toHaveAccessibleName("Changes to review: 2 commits");

		await userEvent.click(screen.getByRole("menuitemcheckbox", { name: /^All commits/ }));
		expect(screen.getByRole("menuitemcheckbox", { name: /^Commit a/ })).toHaveAttribute("aria-checked", "false");
		expect(changesTrigger()).toHaveAccessibleName("Changes to review: All commits");
	});

	it("docked and maximized explorers share the selection", async () => {
		// jsdom draws every element as an empty box at 0,0, so the maximized
		// review's resize handle would claim every click; move it out of the way.
		const rect = HTMLElement.prototype.getBoundingClientRect;
		const rectSpy = vi.spyOn(HTMLElement.prototype, "getBoundingClientRect").mockImplementation(function (this: HTMLElement) {
			return this.dataset.slot === "resizable-handle" ? new DOMRect(-1000, -1000, 1, 1) : rect.call(this);
		});
		renderWithQuery(<>
			<SessionFileExplorer sessionId="sess-shared" />
			<SessionFileExplorer isMaximized sessionId="sess-shared" />
		</>);
		const [docked, maximized] = screen.getAllByRole("region", { name: "Session files" });

		await userEvent.click(await within(docked).findByRole("button", { name: "Changes to review: Uncommitted" }));
		await userEvent.click(await screen.findByRole("menuitemradio", { name: /^Staged/ }));

		expect(changesTrigger(docked)).toHaveAccessibleName("Changes to review: Staged");
		expect(changesTrigger(maximized)).toHaveAccessibleName("Changes to review: Staged");
		expect(within(maximized).getByTestId("review-target")).toHaveTextContent("staged");
		rectSpy.mockRestore();
	});

	it("left trigger stays in the All files view and switches to Changes", async () => {
		const sessionId = "sess-all-files";
		useUiStore.getState().setFilesChangedOnly(sessionId, false);
		renderWithQuery(<SessionFileExplorer sessionId={sessionId} />);

		expect(await screen.findByTestId("tree-changed-only")).toHaveTextContent("false");
		await userEvent.click(await screen.findByRole("button", { name: "Changes to review: Uncommitted" }));
		await userEvent.click(await screen.findByRole("menuitemradio", { name: /^Unstaged/ }));

		expect(await screen.findByTestId("review-pane")).toBeInTheDocument();
		expect(screen.getByTestId("review-target")).toHaveTextContent("unstaged");
		expect(useUiStore.getState().inspectorSessions[sessionId]?.filesChangedOnly).toBe(true);
	});
});
