import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { act, render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import type { ReactNode } from "react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { FileTree } from "./FileTree";
import type { TreeNode } from "../hooks/useSessionWorkspaceTree";

const { getMock } = vi.hoisted(() => ({ getMock: vi.fn() }));

vi.mock("../lib/api-client", () => ({
	apiClient: { GET: getMock },
	apiErrorMessage: (error: unknown, fallback = "Request failed") => {
		if (error instanceof Error) return error.message;
		return fallback;
	},
}));

function renderWithQuery(children: ReactNode) {
	const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
	const view = render(<QueryClientProvider client={client}>{children}</QueryClientProvider>);
	// Give the virtualized tree a measured viewport; the default test
	// ResizeObserver stub does not invoke its callback.
	act(() => {
		for (const callback of resizeCallbacks) callback([{ contentRect: { width: 400, height: 400 } }] as never, {} as ResizeObserver);
	});
	return { ...view, client };
}

const resizeCallbacks: ResizeObserverCallback[] = [];

class CapturingResizeObserver implements ResizeObserver {
	constructor(callback: ResizeObserverCallback) {
		resizeCallbacks.push(callback);
	}
	disconnect() {}
	observe() {}
	unobserve() {}
}

function treeResponse(path: string, entries: unknown[]) {
	return { data: { sessionId: "sess-1", path, entries, truncated: false } };
}

async function findTreeRow(testId: "workspace-file-tree" | "changed-file-tree", path: string) {
	const tree = await screen.findByTestId(testId);
	return waitFor(() => {
		const row = tree.shadowRoot?.querySelector<HTMLElement>(`[data-item-path="${path}"]`);
		expect(row).not.toBeNull();
		return row!;
	});
}

describe("FileTree", () => {
	beforeEach(() => {
		resizeCallbacks.length = 0;
		getMock.mockReset();
		Object.defineProperty(window, "ResizeObserver", { configurable: true, writable: true, value: CapturingResizeObserver });
	});

	it("lists the root directory's entries", async () => {
		getMock.mockResolvedValue(
			treeResponse("", [
				{ name: "src", path: "src", type: "dir", hasChanges: true },
				{ name: "README.md", path: "README.md", type: "file", status: "unmodified" },
			]),
		);

		renderWithQuery(
			<FileTree changedOnly={false} changedOnlyData={[]} onSelectPath={vi.fn()} selectedPath={null} sessionId="sess-1" filterText="" />,
		);

		expect(await findTreeRow("workspace-file-tree", "src/")).toBeInTheDocument();
		expect(await findTreeRow("workspace-file-tree", "README.md")).toBeInTheDocument();
		expect(getMock).toHaveBeenCalledWith("/api/v1/sessions/{sessionId}/workspace/tree", {
			params: { path: { sessionId: "sess-1" }, query: {} },
		});
		const tree = screen.getByTestId("workspace-file-tree");
		expect(tree).toHaveAttribute("aria-label", "File tree");
		expect(tree.shadowRoot?.querySelector('[data-file-tree-virtualized-scroll="true"]')).not.toBeNull();
	});

	it("renders distinct technology icons from file and folder names", async () => {
		getMock.mockResolvedValue(
			treeResponse("", [
				{ name: "src", path: "src", type: "dir", hasChanges: false },
				{ name: "App.tsx", path: "App.tsx", type: "file", status: "unmodified" },
				{ name: "README.md", path: "README.md", type: "file", status: "unmodified" },
			]),
		);

		renderWithQuery(
			<FileTree changedOnly={false} changedOnlyData={[]} onSelectPath={vi.fn()} selectedPath={null} sessionId="sess-1" filterText="" />,
		);

		const reactFile = await findTreeRow("workspace-file-tree", "App.tsx");
		const markdownFile = await findTreeRow("workspace-file-tree", "README.md");
		expect(reactFile.querySelector("[data-icon-token]")?.getAttribute("data-icon-token")).toBe("react");
		expect(markdownFile.querySelector("[data-icon-token]")?.getAttribute("data-icon-token")).toBe("markdown");
	});

	it("lazily fetches a directory's children on expand", async () => {
		getMock.mockImplementation(async (_path: string, options: unknown) => {
			const query = (options as { params?: { query?: { path?: string } } }).params?.query?.path;
			if (!query) return treeResponse("", [{ name: "src", path: "src", type: "dir", hasChanges: false }]);
			if (query === "src") {
				return treeResponse("src", [{ name: "app.go", path: "src/app.go", type: "file", status: "unmodified" }]);
			}
			return treeResponse(query, []);
		});

		renderWithQuery(
			<FileTree changedOnly={false} changedOnlyData={[]} onSelectPath={vi.fn()} selectedPath={null} sessionId="sess-1" filterText="" />,
		);

		await userEvent.click(await findTreeRow("workspace-file-tree", "src/"));

		await waitFor(() =>
			expect(getMock).toHaveBeenCalledWith("/api/v1/sessions/{sessionId}/workspace/tree", {
				params: { path: { sessionId: "sess-1" }, query: { path: "src" } },
			}),
		);
		expect(await findTreeRow("workspace-file-tree", "src/app.go")).toBeInTheDocument();
	});

	it("updates expanded nested folders after invalidation and refreshes a closed folder on reopening", async () => {
		let file = "old.go";
		getMock.mockImplementation(async (_path: string, options: { params: { query: { path?: string } } }) => {
			const dir = options.params.query.path ?? "";
			if (!dir) return treeResponse("", [{ name: "src", path: "src", type: "dir", hasChanges: false }]);
			if (dir === "src") return treeResponse(dir, [{ name: "nested", path: "src/nested", type: "dir", hasChanges: false }]);
			return treeResponse(dir, [{ name: file, path: `${dir}/${file}`, type: "file", status: "modified" }]);
		});
		const view = renderWithQuery(<FileTree changedOnly={false} changedOnlyData={[]} onSelectPath={vi.fn()} selectedPath={null} sessionId="sess-1" filterText="" />);
		await userEvent.click(await findTreeRow("workspace-file-tree", "src/"));
		await userEvent.click(await findTreeRow("workspace-file-tree", "src/nested/"));
		expect(await findTreeRow("workspace-file-tree", "src/nested/old.go")).toBeInTheDocument();
		file = "new.go";
		await act(() => view.client.invalidateQueries({ queryKey: ["session-workspace-tree", "sess-1"] }));
		expect(await findTreeRow("workspace-file-tree", "src/nested/new.go")).toBeInTheDocument();
		expect(screen.getByTestId("workspace-file-tree").shadowRoot?.querySelector('[data-item-path="src/nested/old.go"]')).not.toBeInTheDocument();
		await userEvent.click(await findTreeRow("workspace-file-tree", "src/nested/"));
		file = "reopened.go";
		await act(() => view.client.invalidateQueries({ queryKey: ["session-workspace-tree", "sess-1"] }));
		await userEvent.click(await findTreeRow("workspace-file-tree", "src/nested/"));
		expect(await findTreeRow("workspace-file-tree", "src/nested/reopened.go")).toBeInTheDocument();
	});

	it("coalesces rapid search edits into one cancellable request", async () => {
		getMock.mockImplementation(async (path: string) => path.endsWith("/search") ? { data: { sessionId: "sess-1", query: "target", results: [] } } : treeResponse("", [{ name: "README.md", path: "README.md", type: "file", status: "unmodified" }]));
		const tree = (filterText: string) => <FileTree changedOnly={false} changedOnlyData={[]} onSelectPath={vi.fn()} selectedPath={null} sessionId="sess-1" filterText={filterText} />;
		const view = renderWithQuery(tree(""));
		await findTreeRow("workspace-file-tree", "README.md");
		for (const filter of ["t", "ta", "target"]) view.rerender(<QueryClientProvider client={view.client}>{tree(filter)}</QueryClientProvider>);
		await waitFor(() => expect(getMock.mock.calls.filter(([path]) => path.endsWith("/search"))).toHaveLength(1));
		expect(getMock).toHaveBeenCalledWith("/api/v1/sessions/{sessionId}/workspace/search", { params: { path: { sessionId: "sess-1" }, query: { query: "target", limit: 100 } }, signal: expect.any(AbortSignal) });
	});

	it("calls onSelectPath when a file row is activated", async () => {
		getMock.mockResolvedValue(treeResponse("", [{ name: "README.md", path: "README.md", type: "file", status: "modified" }]));
		const onSelectPath = vi.fn();

		renderWithQuery(
			<FileTree changedOnly={false} changedOnlyData={[]} onSelectPath={onSelectPath} selectedPath={null} sessionId="sess-1" filterText="" />,
		);

		await userEvent.click(await findTreeRow("workspace-file-tree", "README.md"));
		expect(onSelectPath).toHaveBeenCalledWith(expect.objectContaining({ path: "README.md", type: "file" }));
	});

	it("uses the bounded path-search endpoint instead of recursively loading directories", async () => {
		getMock.mockResolvedValue({
			data: {
				sessionId: "sess-1",
				query: "target",
				results: [{ path: "src/nested/target.ts", status: "unmodified", binary: false, size: 12, fileFingerprint: "file-1" }],
				truncated: false,
			},
		});

		renderWithQuery(
			<FileTree changedOnly={false} changedOnlyData={[]} onSelectPath={vi.fn()} selectedPath={null} sessionId="sess-1" filterText="target" />,
		);

		expect(await findTreeRow("workspace-file-tree", "src/nested/target.ts")).toBeInTheDocument();
		expect(getMock).toHaveBeenCalledTimes(1);
		expect(getMock).toHaveBeenCalledWith("/api/v1/sessions/{sessionId}/workspace/search", {
			params: { path: { sessionId: "sess-1" }, query: { query: "target", limit: 100 } },
			signal: expect.any(AbortSignal),
		});
	});

	it("renders the precomputed changed-only tree without calling the tree endpoint", async () => {
		const changedOnlyData: TreeNode[] = [{ name: "notes.txt", path: "notes.txt", type: "file", status: "added" }];

		renderWithQuery(
			<FileTree
				changedOnly={true}
				changedOnlyData={changedOnlyData}
				onSelectPath={vi.fn()}
				selectedPath={null}
				sessionId="sess-1"
				filterText=""
			/>,
		);

		const tree = await screen.findByTestId("changed-file-tree");
		await waitFor(() => expect(tree.shadowRoot?.querySelector('[data-item-path="notes.txt"]')).not.toBeNull());
		expect(getMock).not.toHaveBeenCalled();
	});

	it("selects a changed file from Pierre's path-first tree", async () => {
		const changedOnlyData: TreeNode[] = [{ name: "notes.txt", path: "notes.txt", type: "file", status: "modified" }];
		const onSelectPath = vi.fn();

		renderWithQuery(
			<FileTree
				changedOnly={true}
				changedOnlyData={changedOnlyData}
				onSelectPath={onSelectPath}
				selectedPath={null}
				sessionId="sess-1"
				filterText=""
			/>,
		);

		const tree = await screen.findByTestId("changed-file-tree");
		const row = await waitFor(() => {
			const match = tree.shadowRoot?.querySelector<HTMLElement>('[data-item-path="notes.txt"]');
			expect(match).not.toBeNull();
			return match!;
		});
		await userEvent.click(row);
		expect(onSelectPath).toHaveBeenCalledWith(expect.objectContaining({ path: "notes.txt", type: "file" }));
	});
});
