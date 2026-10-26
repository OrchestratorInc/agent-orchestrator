import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { act, render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { useState, type ComponentProps, type ReactNode } from "react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import {
	defaultWorkspaceReviewTarget,
	prefetchDefaultWorkspaceReviewDiffs,
	type FilesChanges,
	type ReviewTarget,
	type WorkspaceFilesResponse,
	type WorkspaceHistoryResponse,
} from "../../hooks/useSessionWorkspaceFiles";
import { CHECKED_OUT_BRANCH, buildReviewTarget, isLegacyManifest } from "../../lib/files-review-selection";
import type { FileAnnotationModel } from "../WorkspaceDiffView";
import { TooltipProvider } from "../ui/tooltip";
import { WorkspaceReviewPane } from "./WorkspaceReviewPane";

const { getMock, postMock, scrollToMock } = vi.hoisted(() => ({ getMock: vi.fn(), postMock: vi.fn(), scrollToMock: vi.fn() }));

vi.mock("../../lib/api-client", () => ({
	apiClient: { POST: postMock, GET: getMock },
	apiErrorMessage: (error: unknown, fallback = "Request failed") => error instanceof Error ? error.message : fallback,
	getApiBaseUrl: () => "",
}));

vi.mock("../../lib/host-clients", async (importOriginal) => ({
	...await importOriginal<typeof import("../../lib/host-clients")>(),
	clientForSessionHost: () => ({ GET: getMock, POST: postMock }),
}));

// Minimal patch-only "change" metadata. A patch containing "ends-at-eof" gets a
// last hunk that ends on a change (the file provably ends there); any other
// keeps three lines of trailing context (the file may continue).
vi.mock("@pierre/diffs", () => ({
	hydratePartialDiff: (_mode: string, fileDiff: object) => ({ ...fileDiff, isPartial: false }),
	parsePatchFiles: (patch: string) => patch ? [{ files: [{
		name: patch.includes("package-lock.json") ? "package-lock.json" : patch.includes("README.md") ? "README.md" : "src/App.tsx",
		type: "change",
		isPartial: true,
		deletionLines: [],
		additionLines: [patch],
		hunks: [{
			deletionStart: 1,
			deletionCount: 3,
			additionStart: 1,
			additionCount: 4,
			noEOFCRAdditions: false,
			noEOFCRDeletions: false,
			hunkContent: patch.includes("ends-at-eof")
				? [{ type: "context", lines: 3 }, { type: "change", additions: 1, deletions: 0 }]
				: [{ type: "change", additions: 1, deletions: 0 }, { type: "context", lines: 3 }],
		}],
	}] }] : [],
}));

vi.mock("@pierre/diffs/react", () => ({
	CodeView: ({ className, items, options, ref, renderAnnotation, renderCustomHeader, renderGutterUtility, renderCodeViewFooter }: {
		ref?: { current: unknown };
		className: string;
		items: Array<{ id: string; collapsed?: boolean; fileDiff?: { isPartial?: boolean; additionLines?: string[] }; annotations?: Array<{ lineNumber: number; side: string }> }>;
		options: { enableGutterUtility?: boolean; overflow?: string; unsafeCSS?: string };
		renderAnnotation?: (annotation: { lineNumber: number; side: string }, item: { id: string }) => ReactNode;
		renderCodeViewFooter?: () => ReactNode;
		renderCustomHeader: (item: { id: string }) => ReactNode;
		renderGutterUtility?: (getHoveredLine: () => { lineNumber: number; side: "additions" }, item: { id: string }) => ReactNode;
	}) => {
		if (ref) ref.current = { scrollTo: scrollToMock };
		return (
		<div className={className} data-gutter-enabled={String(Boolean(options.enableGutterUtility))} data-overflow={options.overflow} data-surface-css={options.unsafeCSS} data-testid="code-view">
			{items.map((item) => (
				<div data-collapsed={String(Boolean(item.collapsed))} data-partial={String(item.fileDiff?.isPartial)} key={item.id}>
					{renderCustomHeader(item)}
					<pre data-testid="review-patch">{item.fileDiff?.additionLines?.join("\n")}</pre>
					{item.annotations?.map((entry) => <div data-annotation-line={entry.lineNumber} data-annotation-side={entry.side} key={`${entry.side}:${entry.lineNumber}`}>{renderAnnotation?.(entry, item)}</div>)}
				</div>
			))}
			{renderCodeViewFooter?.()}
			{items[0] ? renderGutterUtility?.(() => ({ lineNumber: 7, side: "additions" }), items[0]) : null}
		</div>
		);
	},
}));

function annotation(): FileAnnotationModel {
	return { targets: [], status: "idle", error: "", begin: vi.fn(), draftFor: () => "", statusFor: () => "idle", setDraft: vi.fn(), cancel: vi.fn(), submit: vi.fn() };
}

function workspace(files: WorkspaceFilesResponse["files"]): WorkspaceFilesResponse {
	return {
		sessionId: "sess-1",
		workspaceVersion: "workspace-1",
		files,
		sections: { committed: [], staged: [], unstaged: files, untracked: [] },
		commits: [],
		summary: { additions: 1, deletions: 1, files: files.length },
		truncated: false,
	};
}

// A workspace project (multi-repository) session leaves every git-state section
// and the commit list empty, so its review can only ask for the combined scope.
function workspaceProject(files: WorkspaceFilesResponse["files"]): WorkspaceFilesResponse {
	const data = workspace([]);
	data.files = files;
	data.summary.files = files.length;
	return data;
}

function committedWorkspace(files: WorkspaceFilesResponse["files"]): WorkspaceFilesResponse {
	const data = workspace([]);
	const commitFiles = files.map((file) => ({
		...file,
		editable: file.editable ?? false,
		fileFingerprint: file.fileFingerprint ?? `commit:${file.path}`,
	}));
	data.files = files;
	data.sections.committed = files;
	data.summary.files = files.length;
	data.commits = [{
		author: "Ada Lovelace",
		files: commitFiles,
		sha: "commit-1",
		subject: "Test commit",
		timestamp: "2026-09-10T10:00:00Z",
	}];
	return data;
}

function renderWithQuery(children: ReactNode) {
	return render(<QueryClientProvider client={new QueryClient({ defaultOptions: { queries: { retry: false } } })}><TooltipProvider>{children}</TooltipProvider></QueryClientProvider>);
}

function history(data: WorkspaceFilesResponse, view?: WorkspaceHistoryResponse["view"]): WorkspaceHistoryResponse {
	return { sessionId: data.sessionId, commits: data.commits, commitsTruncated: false, view };
}

// What the Files header hands the pane: the default review, or the picked changes.
function reviewTarget(data: WorkspaceFilesResponse, changes?: FilesChanges): ReviewTarget {
	if (!changes) return defaultWorkspaceReviewTarget(data);
	const legacy = isLegacyManifest(data);
	return buildReviewTarget({ branch: CHECKED_OUT_BRANCH, changes, manifest: data, legacy, history: history(data) });
}

function commitTarget(data: WorkspaceFilesResponse, sha = data.commits[0].sha): ReviewTarget {
	return reviewTarget(data, { kind: "commits", shas: [sha] });
}

function ReviewWithCollapseControls(props: ComponentProps<typeof WorkspaceReviewPane>) {
	const [allCollapsed, setAllCollapsed] = useState(false);
	const [request, setRequest] = useState<{ expand: boolean; key: number }>();
	return <>
		<button onClick={() => setRequest((current) => ({ expand: allCollapsed, key: (current?.key ?? 0) + 1 }))}>{allCollapsed ? "Expand all files" : "Collapse all files"}</button>
		<WorkspaceReviewPane {...props} collapseRequest={request} onAllCollapsedChange={setAllCollapsed} />
	</>;
}

describe("WorkspaceReviewPane", () => {
	beforeEach(() => {
		window.localStorage.clear();
		getMock.mockReset();
		postMock.mockReset().mockResolvedValue({
			data: {
				sessionId: "sess-1",
				workspaceVersion: "workspace-1",
				groups: [{ repository: "", patch: "diff --git a/src/App.tsx b/src/App.tsx\n", truncated: false, includedPaths: ["src/App.tsx"], deferred: [] }],
			},
		});
	});

	it("scrolls to a file the changed-files rail picks, once its diff is in the list", async () => {
		scrollToMock.mockReset();
		const data = workspace([{ path: "src/App.tsx", status: "modified", additions: 1, deletions: 1, size: 20, binary: false, fileFingerprint: "file-1" }]);
		const target = reviewTarget(data);
		const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } });
		const pane = (scrollRequest?: { path: string; key: number }) => (
			<QueryClientProvider client={queryClient}>
				<TooltipProvider>
					<WorkspaceReviewPane annotation={annotation()} target={target} filter="" onBrowseAll={vi.fn()} scrollRequest={scrollRequest} sessionId="sess-1" split={false} />
				</TooltipProvider>
			</QueryClientProvider>
		);
		const view = render(pane());
		expect(await screen.findByTestId("code-view")).toBeInTheDocument();
		await screen.findByTestId("review-patch");
		expect(scrollToMock).not.toHaveBeenCalled();

		view.rerender(pane({ path: "src/App.tsx", key: 1 }));
		await waitFor(() => expect(scrollToMock).toHaveBeenCalledWith({ type: "item", id: `${target.key}:src/App.tsx`, align: "start" }));
		// A request is acted on once; a new key asks again.
		view.rerender(pane({ path: "src/App.tsx", key: 1 }));
		expect(scrollToMock).toHaveBeenCalledTimes(1);
		view.rerender(pane({ path: "src/App.tsx", key: 2 }));
		await waitFor(() => expect(scrollToMock).toHaveBeenCalledTimes(2));
	});

	it("shows an empty review as an icon, its message and the way to the file tree", async () => {
		const onBrowseAll = vi.fn();
		const data = workspace([]);
		renderWithQuery(<WorkspaceReviewPane annotation={annotation()} target={reviewTarget(data, { kind: "unstaged" })} filter="" onBrowseAll={onBrowseAll} sessionId="sess-1" split={false} />);
		const empty = await screen.findByTestId("empty-message");
		expect(empty).toHaveClass("flex-1", "justify-center", "items-center");
		expect(empty.querySelector("svg")).not.toBeNull();
		expect(empty).toHaveTextContent("No unstaged changes.");
		const browse = screen.getByRole("button", { name: "Browse all files" });
		await userEvent.click(browse);
		expect(onBrowseAll).toHaveBeenCalledOnce();
	});

	it("reuses the matching prefetch query without another diff request", async () => {
		const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } });
		const data = workspace([{ path: "src/App.tsx", status: "modified", additions: 1, deletions: 1, size: 20, binary: false, fileFingerprint: "file-1" }]);
		await prefetchDefaultWorkspaceReviewDiffs(queryClient, "sess-1", data);

		render(<QueryClientProvider client={queryClient}><TooltipProvider><WorkspaceReviewPane annotation={annotation()} target={reviewTarget(data)} filter="" onBrowseAll={vi.fn()} sessionId="sess-1" split={false} /></TooltipProvider></QueryClientProvider>);

		expect(screen.getByTestId("code-view")).toBeInTheDocument();
		expect(screen.queryByText("Loading diff...")).not.toBeInTheDocument();
		expect(postMock).toHaveBeenCalledTimes(1);
	});

	it("reuses the prefetched Uncommitted review the Files header opens on", async () => {
		const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } });
		const file = { path: "src/App.tsx", status: "modified" as const, additions: 1, deletions: 1, size: 20, binary: false, fileFingerprint: "file-1" };
		const data = workspace([file]);
		data.sections.uncommitted = [file];
		await prefetchDefaultWorkspaceReviewDiffs(queryClient, "sess-1", data);
		expect(postMock).toHaveBeenCalledWith("/api/v1/sessions/{sessionId}/workspace/diffs", expect.objectContaining({ body: expect.objectContaining({ scope: "uncommitted" }) }));

		render(<QueryClientProvider client={queryClient}><TooltipProvider><WorkspaceReviewPane annotation={annotation()} target={reviewTarget(data, { kind: "uncommitted" })} filter="" onBrowseAll={vi.fn()} sessionId="sess-1" split={false} /></TooltipProvider></QueryClientProvider>);

		expect(screen.getByTestId("code-view")).toBeInTheDocument();
		expect(postMock).toHaveBeenCalledTimes(1);
	});

	it("renders an image preview for a binary image in the Changes panel", async () => {
		const data = workspace([{ path: "sample.png", status: "added", additions: 0, deletions: 0, size: 128, binary: true }]);

		renderWithQuery(<WorkspaceReviewPane annotation={annotation()} target={reviewTarget(data)} filter="" onBrowseAll={vi.fn()} sessionId="sess-1" split={false} />);

		expect(await screen.findByAltText("After version of sample.png")).toHaveAttribute(
			"src",
			expect.stringContaining("/api/v1/sessions/sess-1/workspace/file/blob"),
		);
		expect(screen.queryByText("Binary file preview is not available.")).not.toBeInTheDocument();
	});

	it.each(["shot.png", "archive.bin"])("keeps large binary %s expanded and reuses its prefetched batch", async (path) => {
		const data = workspace([
			{ path: "src/App.tsx", status: "modified", additions: 1, deletions: 0, size: 20, binary: false },
			{ path, status: "added", additions: 0, deletions: 0, size: 600_000, binary: true },
		]);
		const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
		await prefetchDefaultWorkspaceReviewDiffs(client, "sess-1", data);
		expect(postMock.mock.calls[0]?.[1]?.body.paths).toEqual(["src/App.tsx", path]);
		render(<QueryClientProvider client={client}><TooltipProvider><WorkspaceReviewPane annotation={annotation()} target={reviewTarget(data)} filter="" sessionId="sess-1" split={false} /></TooltipProvider></QueryClientProvider>);
		const collapse = screen.getByRole("button", { name: `Collapse ${path}` });
		expect(collapse).toHaveAttribute("aria-expanded", "true");
		if (path.endsWith(".png")) expect(screen.getByRole("img", { name: `After version of ${path}` })).toBeInTheDocument();
		await userEvent.click(collapse);
		await userEvent.click(screen.getByRole("button", { name: `Expand ${path}` }));
		expect(postMock).toHaveBeenCalledTimes(1);
	});

	it.each([false, true])("keeps loaded text mounted while deferred files load (expand all: %s)", async (expandAll) => {
		scrollToMock.mockReset();
		const data = workspace([
			{ path: "package-lock.json", status: "modified", additions: 1, deletions: 0, size: 20, binary: false },
			{ path: "src/App.tsx", status: "modified", additions: 1, deletions: 0, size: 20, binary: false },
		]);
		let finish!: (value: unknown) => void;
		postMock.mockResolvedValueOnce({ data: { sessionId: "sess-1", workspaceVersion: "workspace-1", groups: [{ repository: "", patch: "diff --git a/src/App.tsx b/src/App.tsx\n", truncated: false, includedPaths: ["src/App.tsx"], deferred: [] }] } });
		postMock.mockImplementationOnce(() => new Promise((resolve) => { finish = resolve; }));
		renderWithQuery(<ReviewWithCollapseControls annotation={annotation()} target={reviewTarget(data)} filter="" sessionId="sess-1" split={false} />);
		const existing = await screen.findByTestId("review-patch");
		await userEvent.click(screen.getByRole("button", { name: expandAll ? "Collapse all files" : "Expand package-lock.json" }));
		if (expandAll) await userEvent.click(screen.getByRole("button", { name: "Expand all files" }));
		await waitFor(() => expect(postMock).toHaveBeenCalledTimes(2));
		expect(postMock.mock.calls[1]?.[1]?.body.paths).toEqual(["package-lock.json"]);
		expect(existing).toBeInTheDocument();
		expect(screen.getByTestId("review-patch")).toBe(existing);
		expect(scrollToMock).not.toHaveBeenCalled();
		await act(async () => finish({ data: { sessionId: "sess-1", workspaceVersion: "workspace-1", groups: [{ repository: "", patch: "diff --git a/package-lock.json b/package-lock.json\n", truncated: false, includedPaths: ["package-lock.json"], deferred: [] }] } }));
		await waitFor(() => expect(screen.getAllByTestId("review-patch")).toHaveLength(2));
		expect(existing).toBeInTheDocument();
		if (expandAll) expect(scrollToMock).not.toHaveBeenCalled();
		else await waitFor(() => expect(scrollToMock).toHaveBeenCalledExactlyOnceWith({ type: "item", id: `${reviewTarget(data).key}:package-lock.json`, align: "start" }));
		scrollToMock.mockClear();
		await userEvent.click(screen.getByRole("button", { name: "Collapse package-lock.json" }));
		await userEvent.click(screen.getByRole("button", { name: "Expand package-lock.json" }));
		expect(postMock).toHaveBeenCalledTimes(2);
		expect(scrollToMock).not.toHaveBeenCalled();
	});

	it("opens fallback files with the header icon and status", async () => {
		const onOpenFile = vi.fn();
		const data = workspace([{ path: "shot.png", status: "added", additions: 0, deletions: 0, size: 128, binary: true }]);
		renderWithQuery(<WorkspaceReviewPane annotation={annotation()} target={reviewTarget(data)} onOpenFile={onOpenFile} filter="" sessionId="sess-1" split={false} />);
		expect(screen.getByRole("button", { name: "Collapse shot.png" })).toContainElement(screen.getByText("A"));
		const open = screen.getByRole("button", { name: "Open full file" });
		expect(open.querySelector("svg")).not.toBeNull();
		expect(open).toHaveClass("border-transparent");
		await userEvent.click(open);
		expect(onOpenFile).toHaveBeenCalledWith("shot.png", expect.objectContaining({ mode: "file", scope: "combined" }));
	});

	it.each([false, true])("marks image and binary files viewed with saved progress (mixed: %s)", async (mixed) => {
		const files: WorkspaceFilesResponse["files"] = ["sample.png", "archive.bin"].map((path) => ({ path, status: "added", additions: 0, deletions: 0, size: 128, binary: true, fileFingerprint: `v1:${path}` }));
		if (mixed) files.unshift({ path: "src/App.tsx", status: "modified", additions: 1, deletions: 0, size: 20, binary: false, fileFingerprint: "text-v1" });
		const data = workspace(files);
		const pane = () => <WorkspaceReviewPane annotation={annotation()} target={reviewTarget(data)} filter="" onBrowseAll={vi.fn()} sessionId="sess-1" split={false} />;
		const first = renderWithQuery(pane());
		if (mixed) await screen.findByTestId("review-patch");
		for (const file of files) {
			await userEvent.click(await screen.findByRole("checkbox", { name: `Mark ${file.path} as viewed` }));
			expect(screen.getByRole("button", { name: `Expand ${file.path}` })).toHaveAttribute("aria-expanded", "false");
		}
		expect(screen.getAllByRole("checkbox").filter((box) => box.getAttribute("data-state") === "checked")).toHaveLength(files.length);
		expect(screen.queryByRole("img")).not.toBeInTheDocument();
		first.unmount();
		renderWithQuery(pane());
		expect(screen.getAllByRole("checkbox").filter((box) => box.getAttribute("data-state") === "checked")).toHaveLength(files.length);
		for (const file of files) expect(await screen.findByRole("checkbox", { name: `Mark ${file.path} as not viewed` })).toBeChecked();
		await userEvent.click(screen.getByRole("checkbox", { name: "Mark sample.png as not viewed" }));
		expect(screen.getAllByRole("checkbox").filter((box) => box.getAttribute("data-state") === "checked")).toHaveLength(files.length - 1);
		expect(screen.getByRole("button", { name: "Expand sample.png" })).toHaveAttribute("aria-expanded", "false");
		await userEvent.click(screen.getByRole("button", { name: "Expand sample.png" }));
		expect(screen.getByRole("img", { name: "After version of sample.png" })).toBeInTheDocument();
	});

	it.each([false, true])("collapses image bodies individually and together with text files (mixed: %s)", async (mixed) => {
		const images: WorkspaceFilesResponse["files"] = ["one.png", "two.png"].map((path) => ({ path, status: "added", additions: 0, deletions: 0, size: 128, binary: true }));
		const data = workspace(mixed ? [{ path: "src/App.tsx", status: "modified", additions: 1, deletions: 0, size: 20, binary: false }, ...images] : images);
		renderWithQuery(<ReviewWithCollapseControls annotation={annotation()} target={reviewTarget(data)} filter="" onBrowseAll={vi.fn()} sessionId="sess-1" split={false} />);

		const firstImage = await screen.findByAltText("After version of one.png");
		// Image-only reviews must mount CodeView too, so its footer owns scrolling.
		expect(screen.getByTestId("code-view")).toContainElement(firstImage);
		await userEvent.click(screen.getByRole("button", { name: "Collapse one.png" }));
		expect(screen.queryByAltText("After version of one.png")).not.toBeInTheDocument();
		expect(screen.getByRole("button", { name: "Expand one.png" })).toHaveAttribute("aria-expanded", "false");
		expect(screen.getByAltText("After version of two.png")).toBeInTheDocument();
		await userEvent.click(screen.getByRole("button", { name: "Expand one.png" }));
		expect(screen.getByAltText("After version of one.png")).toBeInTheDocument();

		await userEvent.click(screen.getByRole("button", { name: "Collapse all files" }));
		expect(screen.queryAllByRole("img")).toHaveLength(0);
		for (const { path } of images) expect(screen.getByRole("button", { name: `Expand ${path}` })).toBeInTheDocument();
		if (mixed) expect(screen.getByTestId("code-view").querySelector("[data-collapsed]")).toHaveAttribute("data-collapsed", "true");
		await userEvent.click(screen.getByRole("button", { name: "Expand all files" }));
		expect(screen.getAllByRole("img")).toHaveLength(2);
		if (mixed) expect(screen.getByTestId("code-view").querySelector("[data-collapsed]")).toHaveAttribute("data-collapsed", "false");
	});

	it("does not reuse parsed metadata when an equal-length patch changes", async () => {
		const firstPatch = "diff --git a/src/App.tsx b/src/App.tsx\n-old\n+one\n";
		const secondPatch = "diff --git a/src/App.tsx b/src/App.tsx\n-old\n+two\n";
		postMock
			.mockResolvedValueOnce({ data: { sessionId: "sess-1", workspaceVersion: "workspace-1", groups: [{ repository: "", patch: firstPatch, truncated: false, includedPaths: ["src/App.tsx"], deferred: [] }] } })
			.mockResolvedValueOnce({ data: { sessionId: "sess-1", workspaceVersion: "workspace-1", groups: [{ repository: "", patch: secondPatch, truncated: false, includedPaths: ["src/App.tsx"], deferred: [] }] } });
		const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } });
		const data = workspace([{ path: "src/App.tsx", status: "modified", additions: 1, deletions: 1, size: 20, binary: false, fileFingerprint: "file-1" }]);
		const view = render(<QueryClientProvider client={queryClient}><TooltipProvider><WorkspaceReviewPane annotation={annotation()} target={reviewTarget(data)} filter="" onBrowseAll={vi.fn()} sessionId="sess-1" split={false} /></TooltipProvider></QueryClientProvider>);

		expect((await screen.findByTestId("review-patch")).textContent).toBe(firstPatch);
		view.unmount();
		await queryClient.invalidateQueries({ queryKey: ["session-workspace-diffs", "sess-1"], refetchType: "none" });
		render(<QueryClientProvider client={queryClient}><TooltipProvider><WorkspaceReviewPane annotation={annotation()} target={reviewTarget(data)} filter="" onBrowseAll={vi.fn()} sessionId="sess-1" split={false} /></TooltipProvider></QueryClientProvider>);

		await waitFor(() => expect(screen.getByTestId("review-patch").textContent).toBe(secondPatch));
		expect(postMock).toHaveBeenCalledTimes(2);
	});

	it("bounds a 5,123-file review and jumps directly to a distant file without loading intervening pages", async () => {
		const files = Array.from({ length: 5123 }, (_, index) => ({ path: `file-${index}.ts`, status: "modified" as const, additions: 1, deletions: 1, size: 20, binary: false }));
		const target = reviewTarget(workspace(files));
		const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
		const pane = (filter = "", scrollRequest?: { path: string; key: number }, selected = target) => (
			<QueryClientProvider client={client}><TooltipProvider><WorkspaceReviewPane annotation={annotation()} target={selected} filter={filter} scrollRequest={scrollRequest} sessionId="sess-1" split={false} /></TooltipProvider></QueryClientProvider>
		);
		const view = render(pane());
		await waitFor(() => expect(postMock).toHaveBeenCalledTimes(4), { timeout: 10_000 });
		expect(screen.getByText("Files 1–96 of 5123")).toBeInTheDocument();
		expect(document.querySelectorAll('[role="checkbox"]')).toHaveLength(96);
		expect(postMock.mock.calls.flatMap((call) => call[1].body.paths)).toEqual(files.slice(0, 96).map((file) => file.path));
		await userEvent.click(screen.getByRole("button", { name: "Next files" }));
		await waitFor(() => expect(postMock).toHaveBeenCalledTimes(8), { timeout: 10_000 });
		expect(screen.getByText("Files 97–192 of 5123")).toBeInTheDocument();
		expect(document.querySelectorAll('[role="checkbox"]')).toHaveLength(96);
		expect(screen.queryByRole("checkbox", { name: "Mark file-0.ts as viewed" })).not.toBeInTheDocument();
		view.rerender(pane("", { path: "file-5100.ts", key: 1 }));
		await waitFor(() => expect(screen.getByText("Files 5089–5123 of 5123")).toBeInTheDocument());
		await waitFor(() => expect(postMock).toHaveBeenCalledTimes(10), { timeout: 10_000 });
		expect(postMock.mock.calls.slice(8).flatMap((call) => call[1].body.paths)).toEqual(files.slice(5088).map((file) => file.path));
		expect(document.querySelectorAll('[role="checkbox"]')).toHaveLength(35);
		expect(screen.getByRole("button", { name: "Next files" })).toBeDisabled();
		// Filtering a late page must show its first result immediately.
		view.rerender(pane("file-4000.ts"));
		expect(await screen.findByRole("checkbox", { name: "Mark file-4000.ts as viewed" })).toBeInTheDocument();
		expect(document.querySelectorAll('[role="checkbox"]')).toHaveLength(1);
		view.rerender(pane());
		expect(screen.getByText("Files 1–96 of 5123")).toBeInTheDocument();
		await userEvent.click(screen.getByRole("button", { name: "Next files" }));
		expect(screen.getByText("Files 97–192 of 5123")).toBeInTheDocument();
		view.rerender(pane("", undefined, { ...target, selectionKey: "moved-pr-head" }));
		expect(screen.getByText("Files 1–96 of 5123")).toBeInTheDocument();
	}, 60_000);

	it("preserves the review page across manifest refreshes and clamps it when files disappear", async () => {
		const files = Array.from({ length: 193 }, (_, index) => ({ path: `file-${index}.dat`, status: "modified" as const, additions: 0, deletions: 0, size: 20, binary: true, fileFingerprint: "before" }));
		const target = reviewTarget(workspace(files));
		const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
		const pane = (selected = target) => (
			<QueryClientProvider client={client}><TooltipProvider><WorkspaceReviewPane annotation={annotation()} target={selected} filter="" sessionId="sess-1" split={false} /></TooltipProvider></QueryClientProvider>
		);
		const view = render(pane());
		await userEvent.click(screen.getByRole("button", { name: "Next files" }));
		expect(screen.getByText("Files 97–192 of 193")).toBeInTheDocument();
		const refreshed = { ...target, workspaceVersion: "workspace-2", files: files.map((file) => ({ ...file, fileFingerprint: "after" })) };
		view.rerender(pane(refreshed));
		expect(screen.getByText("Files 97–192 of 193")).toBeInTheDocument();
		expect(document.querySelectorAll('[role="checkbox"]')).toHaveLength(96);
		await userEvent.click(screen.getByRole("button", { name: "Next files" }));
		expect(screen.getByText("Files 193–193 of 193")).toBeInTheDocument();
		view.rerender(pane({ ...refreshed, workspaceVersion: "workspace-3", files: refreshed.files.slice(0, 100) }));
		expect(screen.getByText("Files 97–100 of 100")).toBeInTheDocument();
		expect(document.querySelectorAll('[role="checkbox"]')).toHaveLength(4);
		view.rerender(pane({ ...refreshed, workspaceVersion: "workspace-4", files: refreshed.files.slice(0, 50) }));
		expect(await screen.findByRole("checkbox", { name: "Mark file-0.dat as viewed" })).toBeInTheDocument();
		expect(screen.queryByRole("button", { name: "Next files" })).not.toBeInTheDocument();
		expect(document.querySelectorAll('[role="checkbox"]')).toHaveLength(50);
	}, 60_000);

	it("bounds binary and deferred fallback rows, including Expand all", async () => {
		const files = Array.from({ length: 5123 }, (_, index) => ({ path: `file-${index}.dat`, status: "modified" as const, additions: 0, deletions: 0, size: 600_000, binary: index % 2 === 0 }));
		renderWithQuery(<ReviewWithCollapseControls annotation={annotation()} target={reviewTarget(workspace(files))} filter="" sessionId="sess-1" split={false} />);
		expect(document.querySelectorAll('[role="checkbox"]')).toHaveLength(96);
		await userEvent.click(screen.getByRole("button", { name: "Collapse all files" }));
		await userEvent.click(screen.getByRole("button", { name: "Expand all files" }));
		await waitFor(() => expect(postMock.mock.calls.flatMap((call) => call[1].body.paths)).toContain("file-95.dat"));
		expect(postMock.mock.calls.flatMap((call) => call[1].body.paths).every((path: string) => files.slice(0, 96).some((file) => file.path === path))).toBe(true);
		expect(document.querySelectorAll('[role="checkbox"]')).toHaveLength(96);
	}, 60_000);

	it("keeps other pages' viewed and deferred files collapsed when collapsing or expanding a page", async () => {
		const files = Array.from({ length: 99 }, (_, index) => ({ path: `file-${index}.dat`, status: "modified" as const, additions: 0, deletions: 0, size: index === 98 ? 600_000 : 20, binary: index !== 98 }));
		renderWithQuery(<ReviewWithCollapseControls annotation={annotation()} target={reviewTarget(workspace(files))} filter="" sessionId="sess-1" split={false} />);
		await userEvent.click(screen.getByRole("button", { name: "Next files" }));
		await userEvent.click(screen.getByRole("checkbox", { name: "Mark file-96.dat as viewed" }));
		await userEvent.click(screen.getByRole("button", { name: "Previous files" }));
		await userEvent.click(screen.getByRole("button", { name: "Collapse all files" }));
		await userEvent.click(screen.getByRole("button", { name: "Next files" }));
		expect(screen.getByRole("checkbox", { name: "Mark file-96.dat as not viewed" })).toBeChecked();
		expect(screen.getByRole("button", { name: "Expand file-96.dat" })).toHaveAttribute("aria-expanded", "false");
		expect(screen.getByRole("button", { name: "Expand file-98.dat" })).toHaveAttribute("aria-expanded", "false");
		expect(screen.getByRole("button", { name: "Collapse file-97.dat" })).toHaveAttribute("aria-expanded", "true");
		await userEvent.click(screen.getByRole("button", { name: "Previous files" }));
		await userEvent.click(screen.getByRole("button", { name: "Expand all files" }));
		await userEvent.click(screen.getByRole("button", { name: "Next files" }));
		expect(screen.getByRole("button", { name: "Expand file-96.dat" })).toHaveAttribute("aria-expanded", "false");
		expect(screen.getByRole("button", { name: "Expand file-98.dat" })).toHaveAttribute("aria-expanded", "false");
		expect(screen.getByRole("button", { name: "Collapse file-97.dat" })).toHaveAttribute("aria-expanded", "true");
	}, 60_000);

	it("keeps a deferred diff loaded after expanding all on another page", async () => {
		const files = [
			...Array.from({ length: 96 }, (_, index) => ({ path: `file-${index}.dat`, status: "modified" as const, additions: 0, deletions: 0, size: 20, binary: true })),
			{ path: "src/App.tsx", status: "modified" as const, additions: 1, deletions: 1, size: 600_000, binary: false },
		];
		renderWithQuery(<ReviewWithCollapseControls annotation={annotation()} target={reviewTarget(workspace(files))} filter="" sessionId="sess-1" split={false} />);
		await userEvent.click(screen.getByRole("button", { name: "Next files" }));
		await userEvent.click(screen.getByRole("button", { name: "Expand src/App.tsx" }));
		await screen.findByTestId("review-patch");
		await userEvent.click(screen.getByRole("button", { name: "Previous files" }));
		await userEvent.click(screen.getByRole("button", { name: "Collapse all files" }));
		await userEvent.click(screen.getByRole("button", { name: "Expand all files" }));
		await userEvent.click(screen.getByRole("button", { name: "Next files" }));
		expect(await screen.findByTestId("review-patch")).toBeInTheDocument();
		expect(screen.getByRole("button", { name: "Collapse src/App.tsx" })).toHaveAttribute("aria-expanded", "true");
		expect(screen.queryByRole("button", { name: "Load diff" })).not.toBeInTheDocument();
	}, 60_000);

	it("reuses prefetched batches when opening a review with more than 24 files", async () => {
		const files = Array.from({ length: 57 }, (_, index) => ({ path: index === 0 ? "src/App.tsx" : `file-${index}.ts`, status: "modified" as const, additions: 1, deletions: 1, size: 20, binary: false }));
		const data = workspace(files);
		const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
		await prefetchDefaultWorkspaceReviewDiffs(client, "sess-1", data);
		expect(postMock.mock.calls.map((call) => call[1].body.paths.length)).toEqual([24, 24, 9]);
		render(<QueryClientProvider client={client}><TooltipProvider><WorkspaceReviewPane annotation={annotation()} target={reviewTarget(data)} filter="" onBrowseAll={vi.fn()} sessionId="sess-1" split={false} /></TooltipProvider></QueryClientProvider>);
		expect(screen.getByTestId("code-view")).toBeInTheDocument();
		expect(screen.queryByText("Loading diff...")).not.toBeInTheDocument();
		await waitFor(() => expect(screen.getByRole("checkbox", { name: "Mark src/App.tsx as viewed" })).toBeInTheDocument());
		expect(postMock).toHaveBeenCalledTimes(3);
	});

	it.each([false, true])("renders a same-sized edit with an unchanged version (pane already open: %s)", async (alreadyOpen) => {
		const data = workspace([{ path: "src/App.tsx", status: "modified", additions: 1, deletions: 1, size: 20, binary: false }]);
		const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
		// Both patches have identical lengths and first/last 80 characters.
		const patch = (change: string) => `diff --git a/src/App.tsx b/src/App.tsx\n${"x".repeat(120)}${change}${"x".repeat(120)}`;
		const response = (change: string) => ({ data: { sessionId: "sess-1", workspaceVersion: "workspace-1", groups: [{ repository: "", patch: patch(change), truncated: false, includedPaths: ["src/App.tsx"], deferred: [], errors: [] }] } });
		const mount = () => render(<QueryClientProvider client={client}><TooltipProvider><WorkspaceReviewPane annotation={annotation()} target={reviewTarget(data)} filter="" onBrowseAll={vi.fn()} sessionId="sess-1" split={false} /></TooltipProvider></QueryClientProvider>);
		postMock.mockResolvedValue(response("old-content"));
		await prefetchDefaultWorkspaceReviewDiffs(client, "sess-1", data);
		if (alreadyOpen) {
			mount();
			expect(screen.getByTestId("review-patch")).toHaveTextContent("old-content");
		}
		postMock.mockResolvedValue(response("new-content"));
		await client.invalidateQueries({ queryKey: ["session-workspace-diffs", "sess-1"], refetchType: "none" });
		await prefetchDefaultWorkspaceReviewDiffs(client, "sess-1", data);
		if (!alreadyOpen) mount();
		await waitFor(() => expect(screen.getByTestId("review-patch")).toHaveTextContent("new-content"));
		expect(screen.getByTestId("review-patch")).not.toHaveTextContent("old-content");
	});

	it("requests grouped patches and renders a continuous review for a selected commit", async () => {
		const data = committedWorkspace([{ path: "src/App.tsx", status: "modified", additions: 1, deletions: 1, size: 20, binary: false, editable: true, fileFingerprint: "file-1" }]);
		renderWithQuery(<WorkspaceReviewPane annotation={annotation()} target={commitTarget(data)} filter="" onBrowseAll={vi.fn()} sessionId="sess-1" split={false} />);

		expect(await screen.findByTestId("code-view")).toBeInTheDocument();
		await screen.findByTestId("review-patch");
		expect(postMock).toHaveBeenCalledWith("/api/v1/sessions/{sessionId}/workspace/diffs", expect.objectContaining({
			body: expect.objectContaining({ commitSha: "commit-1", paths: ["src/App.tsx"], scope: "committed", workspaceVersion: "workspace-1" }),
		}));
		expect(screen.getByTestId("code-view")).toHaveClass("overflow-y-auto");
		expect(screen.getByTestId("code-view")).toHaveAttribute("data-overflow", "wrap");
		expect(screen.getByTestId("code-view")).toHaveAttribute("data-surface-css", expect.stringContaining("--diffs-bg: var(--color-bg-primary)"));
		await userEvent.click(screen.getByRole("checkbox", { name: "Mark src/App.tsx as viewed" }));
		expect(screen.getByTestId("code-view").querySelector("[data-collapsed]"))?.toHaveAttribute("data-collapsed", "true");
		expect(screen.getByRole("checkbox", { name: "Mark src/App.tsx as not viewed" })).toHaveClass("size-4");
		// Checked styling uses theme tokens with opaque ink, so the check follows
		// light and dark mode without disappearing on transparent window surfaces.
		const viewedBox = screen.getByRole("checkbox", { name: "Mark src/App.tsx as not viewed" });
		expect(viewedBox).toHaveAttribute("data-state", "checked");
		expect(viewedBox).toHaveClass("data-[state=checked]:bg-foreground", "data-[state=checked]:text-on-foreground");
		expect(viewedBox.getAttribute("style") ?? "").not.toMatch(/#fff|#000/);
	});

	it("loads a file that ends at its last change up front, so its diff has no trailing context row", async () => {
		postMock.mockResolvedValue({
			data: {
				sessionId: "sess-1",
				workspaceVersion: "workspace-1",
				groups: [{ repository: "", patch: "diff --git a/README.md b/README.md\nends-at-eof\n", truncated: false, includedPaths: ["README.md"], deferred: [] }],
			},
		});
		getMock.mockImplementation(async (_url: string, init: { params: { query: { side: "before" | "after" } } }) => ({
			data: { binary: false, content: init.params.query.side === "before" ? "a\n" : "a\nb\n", revision: `rev-${init.params.query.side}`, truncated: false },
		}));
		const data = workspace([{ path: "README.md", status: "modified", additions: 1, deletions: 0, size: 20, binary: false }]);
		renderWithQuery(<WorkspaceReviewPane annotation={annotation()} target={reviewTarget(data)} filter="" onBrowseAll={vi.fn()} sessionId="sess-1" split={false} />);

		await waitFor(() => expect(screen.getByTestId("code-view").querySelector("[data-partial]")).toHaveAttribute("data-partial", "false"));
		for (const side of ["before", "after"]) {
			expect(getMock).toHaveBeenCalledWith("/api/v1/sessions/{sessionId}/workspace/file/revision", expect.objectContaining({
				params: expect.objectContaining({ query: expect.objectContaining({ path: "README.md", side }) }),
			}));
		}
	});

	it("limits automatic full-context reads to one pair and cancels obsolete hydration", async () => {
		postMock.mockResolvedValue({ data: { sessionId: "sess-1", workspaceVersion: "workspace-1", groups: [
			{ repository: "", patch: "README.md ends-at-eof", includedPaths: ["README.md"], deferred: [] },
			{ repository: "", patch: "src/App.tsx ends-at-eof", includedPaths: ["src/App.tsx"], deferred: [] },
		] } });
		let release = () => {};
		const ready = new Promise<void>((resolve) => { release = resolve; });
		getMock.mockImplementation(async (_url: string, init: { signal: AbortSignal; params: { query: { path: string; side: string } } }) => {
			await ready;
			return { data: { binary: false, content: "contents", revision: `rev-${init.params.query.side}`, truncated: false } };
		});
		const data = workspace(["README.md", "src/App.tsx"].map((path) => ({ path, status: "modified", additions: 1, deletions: 0, size: 20, binary: false })));
		const view = renderWithQuery(<WorkspaceReviewPane annotation={annotation()} target={reviewTarget(data)} filter="" onBrowseAll={vi.fn()} sessionId="sess-1" split={false} />);
		await waitFor(() => expect(getMock).toHaveBeenCalledTimes(2));
		expect(getMock.mock.calls.every((call) => call[1].params.query.path === "README.md")).toBe(true);
		release();
		await waitFor(() => expect(getMock).toHaveBeenCalledTimes(4));
		expect(getMock.mock.calls.slice(2).every((call) => call[1].params.query.path === "src/App.tsx")).toBe(true);
		view.unmount();
	});

	it("does not let a stalled host block the same session on another host", async () => {
		postMock.mockResolvedValue({ data: { sessionId: "sess-1", workspaceVersion: "workspace-1", groups: [
			{ repository: "", patch: "README.md ends-at-eof", includedPaths: ["README.md"], deferred: [] },
		] } });
		const signals: AbortSignal[] = [];
		getMock.mockImplementation((_url: string, init: { signal: AbortSignal }) => {
			signals.push(init.signal);
			return new Promise(() => {});
		});
		const data = workspace([{ path: "README.md", status: "modified", additions: 1, deletions: 0, size: 20, binary: false }]);
		const first = renderWithQuery(<WorkspaceReviewPane annotation={annotation()} target={reviewTarget(data)} filter="" hostId="host-a" onBrowseAll={vi.fn()} sessionId="sess-1" split={false} />);
		await waitFor(() => expect(getMock).toHaveBeenCalledTimes(2));
		const second = renderWithQuery(<WorkspaceReviewPane annotation={annotation()} target={reviewTarget(data)} filter="" hostId="host-b" onBrowseAll={vi.fn()} sessionId="sess-1" split={false} />);
		await waitFor(() => expect(getMock).toHaveBeenCalledTimes(4));
		first.unmount();
		second.unmount();
		await waitFor(() => expect(signals.every((signal) => signal.aborted)).toBe(true));
	});

	it("aborts a stalled context pair at its deadline and starts the next file", async () => {
		vi.useFakeTimers();
		let view: ReturnType<typeof renderWithQuery> | undefined;
		try {
			postMock.mockResolvedValue({ data: { sessionId: "sess-1", workspaceVersion: "workspace-1", groups: [
				{ repository: "", patch: "README.md ends-at-eof", includedPaths: ["README.md"], deferred: [] },
				{ repository: "", patch: "src/App.tsx ends-at-eof", includedPaths: ["src/App.tsx"], deferred: [] },
			] } });
			const stalledSignals: AbortSignal[] = [];
			getMock.mockImplementation((_url: string, init: { signal: AbortSignal; params: { query: { path: string; side: string } } }) => {
				if (init.params.query.path === "README.md") {
					stalledSignals.push(init.signal);
					// Even a transport that never settles after abort must release the slot.
					return new Promise(() => {});
				}
				return Promise.resolve({ data: { binary: false, content: "contents", revision: `rev-${init.params.query.side}`, truncated: false } });
			});
			const data = workspace(["README.md", "src/App.tsx"].map((path) => ({ path, status: "modified", additions: 1, deletions: 0, size: 20, binary: false })));
			view = renderWithQuery(<WorkspaceReviewPane annotation={annotation()} target={reviewTarget(data)} filter="" onBrowseAll={vi.fn()} sessionId="sess-1" split={false} />);
			await act(async () => { await vi.waitFor(() => expect(getMock).toHaveBeenCalledTimes(2)); });
			await act(async () => { await vi.advanceTimersByTimeAsync(30_000); });
			expect(stalledSignals).toHaveLength(2);
			expect(stalledSignals.every((signal) => signal.aborted && signal.reason.name === "TimeoutError")).toBe(true);
			expect(getMock).toHaveBeenCalledTimes(4);
			expect(getMock.mock.calls.slice(2).every((call) => call[1].params.query.path === "src/App.tsx")).toBe(true);
		} finally {
			view?.unmount();
			vi.useRealTimers();
		}
	});

	it("does not start queued context reads after the review is closed", async () => {
		postMock.mockResolvedValue({ data: { sessionId: "sess-1", workspaceVersion: "workspace-1", groups: [
			{ repository: "", patch: "README.md ends-at-eof", includedPaths: ["README.md"], deferred: [] },
			{ repository: "", patch: "src/App.tsx ends-at-eof", includedPaths: ["src/App.tsx"], deferred: [] },
		] } });
		const signals: AbortSignal[] = [];
		getMock.mockImplementation((_url: string, init: { signal: AbortSignal }) => { signals.push(init.signal); return new Promise(() => {}); });
		const data = workspace(["README.md", "src/App.tsx"].map((path) => ({ path, status: "modified", additions: 1, deletions: 0, size: 20, binary: false })));
		const view = renderWithQuery(<WorkspaceReviewPane annotation={annotation()} target={reviewTarget(data)} filter="" onBrowseAll={vi.fn()} sessionId="sess-1" split={false} />);
		await waitFor(() => expect(getMock).toHaveBeenCalledTimes(2));
		view.unmount();
		await waitFor(() => expect(signals.every((signal) => signal.aborted)).toBe(true));
		expect(getMock).toHaveBeenCalledTimes(2);
	});

	it("shows the patch while a file that ends at its last change loads its contents", async () => {
		postMock.mockResolvedValue({
			data: {
				sessionId: "sess-1",
				workspaceVersion: "workspace-1",
				groups: [{ repository: "", patch: "diff --git a/README.md b/README.md\nends-at-eof\n", truncated: false, includedPaths: ["README.md"], deferred: [] }],
			},
		});
		let release = () => {};
		const contentsReady = new Promise<void>((resolve) => {
			release = resolve;
		});
		getMock.mockImplementation(async (_url: string, init: { params: { query: { side: "before" | "after" } } }) => {
			await contentsReady;
			return { data: { binary: false, content: init.params.query.side === "before" ? "a\n" : "a\nb\n", revision: `rev-${init.params.query.side}`, truncated: false } };
		});
		const data = workspace([{ path: "README.md", status: "modified", additions: 1, deletions: 0, size: 20, binary: false }]);
		renderWithQuery(<WorkspaceReviewPane annotation={annotation()} target={reviewTarget(data)} filter="" onBrowseAll={vi.fn()} sessionId="sess-1" split={false} />);

		await waitFor(() => expect(getMock).toHaveBeenCalledTimes(2));
		expect(screen.queryByText("Loading diff...")).not.toBeInTheDocument();
		await waitFor(() => expect(screen.getByTestId("code-view").querySelector("[data-partial]")).toHaveAttribute("data-partial", "true"));

		release();
		await waitFor(() => expect(screen.getByTestId("code-view").querySelector("[data-partial]")).toHaveAttribute("data-partial", "false"));
	});

	it("keeps the patch-only diff when the contents request never returns", async () => {
		postMock.mockResolvedValue({
			data: {
				sessionId: "sess-1",
				workspaceVersion: "workspace-1",
				groups: [{ repository: "", patch: "diff --git a/README.md b/README.md\nends-at-eof\n", truncated: false, includedPaths: ["README.md"], deferred: [] }],
			},
		});
		getMock.mockImplementation(() => new Promise(() => {}));
		const data = workspace([{ path: "README.md", status: "modified", additions: 1, deletions: 0, size: 20, binary: false }]);
		renderWithQuery(<WorkspaceReviewPane annotation={annotation()} target={reviewTarget(data)} filter="" onBrowseAll={vi.fn()} sessionId="sess-1" split={false} />);

		await waitFor(() => expect(getMock).toHaveBeenCalledTimes(2));
		expect(screen.queryByText("Loading diff...")).not.toBeInTheDocument();
		await waitFor(() => expect(screen.getByTestId("code-view").querySelector("[data-partial]")).toHaveAttribute("data-partial", "true"));
	});

	it("keeps the patch-only diff (and its load-more row) when the file may continue", async () => {
		const data = workspace([{ path: "src/App.tsx", status: "modified", additions: 1, deletions: 0, size: 20, binary: false }]);
		renderWithQuery(<WorkspaceReviewPane annotation={annotation()} target={reviewTarget(data)} filter="" onBrowseAll={vi.fn()} sessionId="sess-1" split={false} />);

		expect(await screen.findByTestId("code-view")).toBeInTheDocument();
		await screen.findByTestId("review-patch");
		await waitFor(() => expect(screen.getByTestId("code-view").querySelector("[data-partial]")).toHaveAttribute("data-partial", "true"));
		expect(getMock).not.toHaveBeenCalled();
	});

	it("keeps saved viewed files collapsed when the review pane remounts", async () => {
		const data = committedWorkspace([{ path: "src/App.tsx", status: "modified", additions: 1, deletions: 1, size: 20, binary: false, fileFingerprint: "file-1" }]);
		const first = renderWithQuery(<WorkspaceReviewPane annotation={annotation()} target={reviewTarget(data)} filter="" onBrowseAll={vi.fn()} sessionId="sess-1" split={false} />);
		expect(await screen.findByTestId("code-view")).toBeInTheDocument();
		await screen.findByTestId("review-patch");

		await screen.findByTestId("review-patch");
		await userEvent.click(await screen.findByRole("checkbox", { name: "Mark src/App.tsx as viewed" }));
		await waitFor(() => expect(screen.getByTestId("code-view").querySelector("[data-collapsed]")).toHaveAttribute("data-collapsed", "true"));
		first.unmount();

		renderWithQuery(<WorkspaceReviewPane annotation={annotation()} target={reviewTarget(data)} filter="" onBrowseAll={vi.fn()} sessionId="sess-1" split={false} />);
		expect(await screen.findByRole("checkbox", { name: "Mark src/App.tsx as not viewed" })).toBeInTheDocument();
		await waitFor(() => expect(screen.getByTestId("code-view").querySelector("[data-collapsed]")).toHaveAttribute("data-collapsed", "true"));
	});

	it("collapses and expands file items through controlled CodeView state", async () => {
		const data = committedWorkspace([{ path: "src/App.tsx", status: "modified", additions: 1, deletions: 1, size: 20, binary: false, fileFingerprint: "file-1" }]);
		renderWithQuery(<WorkspaceReviewPane annotation={annotation()} target={reviewTarget(data)} filter="" onBrowseAll={vi.fn()} sessionId="sess-1" split={false} />);
		expect(await screen.findByTestId("code-view")).toBeInTheDocument();
		await screen.findByTestId("review-patch");

		await screen.findByRole("checkbox", { name: "Mark src/App.tsx as viewed" });
		await userEvent.click(screen.getByRole("button", { name: "Collapse src/App.tsx" }));
		expect(screen.getByTestId("code-view").querySelector("[data-collapsed]"))?.toHaveAttribute("data-collapsed", "true");
		await userEvent.click(screen.getByRole("button", { name: "Expand src/App.tsx" }));
		expect(screen.getByTestId("code-view").querySelector("[data-collapsed]"))?.toHaveAttribute("data-collapsed", "false");
	});

	it("collapses and expands every file when the Files menu asks, and reports which it is", async () => {
		const data = committedWorkspace([{ path: "src/App.tsx", status: "modified", additions: 1, deletions: 1, size: 20, binary: false, fileFingerprint: "file-1" }]);
		const onAllCollapsedChange = vi.fn();
		function Harness() {
			const [request, setRequest] = useState<{ expand: boolean; key: number }>();
			return (
				<>
					<button onClick={() => setRequest((current) => ({ expand: false, key: (current?.key ?? 0) + 1 }))} type="button">collapse</button>
					<button onClick={() => setRequest((current) => ({ expand: true, key: (current?.key ?? 0) + 1 }))} type="button">expand</button>
					<WorkspaceReviewPane annotation={annotation()} collapseRequest={request} onAllCollapsedChange={onAllCollapsedChange} target={reviewTarget(data)} filter="" sessionId="sess-1" split={false} />
				</>
			);
		}
		renderWithQuery(<Harness />);
		expect(await screen.findByTestId("code-view")).toBeInTheDocument();
		await screen.findByTestId("review-patch");
		expect(onAllCollapsedChange).toHaveBeenLastCalledWith(false);
		// The pane carries no title row of its own: the Files header names the review.
		expect(screen.queryByText(/of 1 viewed/)).not.toBeInTheDocument();
		expect(screen.queryByRole("button", { name: "Collapse all files" })).not.toBeInTheDocument();

		await userEvent.click(screen.getByRole("button", { name: "collapse" }));
		expect(screen.getByTestId("code-view").querySelectorAll('[data-collapsed="true"]')).toHaveLength(1);
		expect(onAllCollapsedChange).toHaveBeenLastCalledWith(true);

		await userEvent.click(screen.getByRole("button", { name: "expand" }));
		expect(screen.getByTestId("code-view").querySelectorAll('[data-collapsed="false"]')).toHaveLength(1);
		expect(onAllCollapsedChange).toHaveBeenLastCalledWith(false);
	});

	it("shows a feedback box for every open line comment and drops only the one that closes", async () => {
		const first = { path: "src/App.tsx", side: "new" as const, line: 3, scope: "committed", surface: "review" as const };
		const second = { path: "src/App.tsx", side: "old" as const, line: 5, scope: "committed", surface: "review" as const };
		const data = committedWorkspace([{ path: "src/App.tsx", status: "modified", additions: 1, deletions: 1, size: 20, binary: false, fileFingerprint: "file-1" }]);
		const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } });
		const pane = (targets: FileAnnotationModel["targets"]) => (
			<QueryClientProvider client={queryClient}><TooltipProvider>
				<WorkspaceReviewPane annotation={{ ...annotation(), targets }} target={reviewTarget(data)} filter="" onBrowseAll={vi.fn()} sessionId="sess-1" split={false} />
			</TooltipProvider></QueryClientProvider>
		);
		const { rerender } = render(pane([first, second]));

		expect(await screen.findByRole("textbox", { name: /new line 3/ })).toBeInTheDocument();
		expect(screen.getByRole("textbox", { name: /old line 5/ })).toBeInTheDocument();
		expect(document.querySelector('[data-annotation-side="additions"][data-annotation-line="3"]')).not.toBeNull();
		expect(document.querySelector('[data-annotation-side="deletions"][data-annotation-line="5"]')).not.toBeNull();

		rerender(pane([second]));
		expect(screen.queryByRole("textbox", { name: /new line 3/ })).not.toBeInTheDocument();
		expect(screen.getByRole("textbox", { name: /old line 5/ })).toBeInTheDocument();
	});

	it("keeps a file feedback composer mounted when that file is collapsed", async () => {
		const model = annotation();
		model.targets = [{ path: "src/App.tsx", side: "file", scope: "committed", surface: "review" }];
		const data = committedWorkspace([{ path: "src/App.tsx", status: "modified", additions: 1, deletions: 1, size: 20, binary: false, fileFingerprint: "file-1" }]);
		renderWithQuery(<WorkspaceReviewPane annotation={model} target={reviewTarget(data)} filter="" onBrowseAll={vi.fn()} sessionId="sess-1" split={false} />);
		expect(await screen.findByRole("textbox", { name: /Feedback for src\/App\.tsx/ })).toBeInTheDocument();

		await userEvent.click(screen.getByRole("button", { name: "Collapse src/App.tsx" }));
		expect(model.cancel).not.toHaveBeenCalled();
		expect(screen.getByLabelText(/Feedback for src\/App\.tsx/)).toBeInTheDocument();
	});

	it("opens file feedback from an already collapsed file header", async () => {
		const model = annotation();
		const data = committedWorkspace([{ path: "src/App.tsx", status: "modified", additions: 1, deletions: 1, size: 20, binary: false, fileFingerprint: "file-1" }]);
		const pane = () => <QueryClientProvider client={queryClient}><TooltipProvider><WorkspaceReviewPane annotation={model} target={reviewTarget(data)} filter="" onBrowseAll={vi.fn()} sessionId="sess-1" split={false} /></TooltipProvider></QueryClientProvider>;
		const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } });
		const { rerender } = render(pane());
		await screen.findByTestId("review-patch");
		await userEvent.click(await screen.findByRole("button", { name: "Collapse src/App.tsx" }));
		await userEvent.click(screen.getAllByRole("button", { name: "Add feedback" })[0]);
		expect(model.begin).toHaveBeenCalledWith(expect.objectContaining({ path: "src/App.tsx", side: "file", surface: "review" }));
		model.targets = [{ path: "src/App.tsx", side: "file", scope: "committed", surface: "review" }];
		rerender(pane());
		expect(screen.getByRole("button", { name: "Expand src/App.tsx" })).toBeInTheDocument();
		expect(await screen.findByLabelText(/Feedback for src\/App\.tsx/)).toBeInTheDocument();
	});

	it("routes the gutter plus and file actions without opening an external pane", async () => {
		const model = annotation();
		const onOpenFile = vi.fn();
		postMock.mockResolvedValue({
			data: {
				sessionId: "sess-1",
				workspaceVersion: "workspace-1",
				groups: [{ repository: "", patch: "diff --git a/README.md b/README.md\n", truncated: false, includedPaths: ["README.md"], deferred: [] }],
			},
		});
		const data = committedWorkspace([{ path: "README.md", status: "modified", additions: 1, deletions: 1, size: 20, binary: false, fileFingerprint: "file-1" }]);
		renderWithQuery(<WorkspaceReviewPane annotation={model} target={commitTarget(data)} filter="" onBrowseAll={vi.fn()} onOpenFile={onOpenFile} sessionId="sess-1" split={false} />);
		expect(await screen.findByTestId("code-view")).toBeInTheDocument();
		await screen.findByTestId("review-patch");

		const inlineFeedback = screen.getAllByRole("button", { name: "Add feedback" })[1];
		expect(screen.getByTestId("code-view")).toHaveAttribute("data-gutter-enabled", "true");
		expect(inlineFeedback).not.toHaveClass("opacity-0");
		await userEvent.click(inlineFeedback);
		expect(model.begin).toHaveBeenCalledWith(expect.objectContaining({ path: "README.md", side: "new", line: 7 }));
		await userEvent.click(screen.getByRole("button", { name: "Open full file" }));
		expect(onOpenFile).toHaveBeenCalledWith("README.md", expect.objectContaining({source:expect.objectContaining({kind:"snapshot",commitShas:["commit-1"]}),mode:"file",scope:"committed"}));
	});

	it("opens a file diff in the center pane through the dedicated action", async () => {
		const onOpenFile = vi.fn();
		const data = committedWorkspace([{ path: "src/App.tsx", status: "modified", additions: 1, deletions: 1, size: 20, binary: false, fileFingerprint: "file-1" }]);
		renderWithQuery(<WorkspaceReviewPane annotation={annotation()} target={commitTarget(data)} filter="" onBrowseAll={vi.fn()} onOpenFile={onOpenFile} sessionId="sess-1" split={false} />);
		expect(await screen.findByTestId("code-view")).toBeInTheDocument();
		await screen.findByTestId("review-patch");

		await userEvent.click(screen.getByRole("button", { name: "Open diff in center" }));
		expect(onOpenFile).toHaveBeenCalledWith("src/App.tsx", expect.objectContaining({source:expect.objectContaining({kind:"snapshot",commitShas:["commit-1"]}),mode:"diff",center:true,scope:"committed"}));
	});

	it("hides the open-in-center action when no center pane is reachable", async () => {
		const data = committedWorkspace([{ path: "src/App.tsx", status: "modified", additions: 1, deletions: 1, size: 20, binary: false, fileFingerprint: "file-1" }]);
		renderWithQuery(<WorkspaceReviewPane annotation={annotation()} canOpenInCenter={false} target={reviewTarget(data)} filter="" onBrowseAll={vi.fn()} onOpenFile={vi.fn()} sessionId="sess-1" split={false} />);
		expect(await screen.findByTestId("code-view")).toBeInTheDocument();
		await screen.findByTestId("review-patch");

		expect(screen.queryByRole("button", { name: "Open diff in center" })).not.toBeInTheDocument();
		expect(await screen.findByRole("button", { name: "Open full file" })).toBeInTheDocument();
	});

	it("opens a changed diff directly in syntax-aware edit mode", async () => {
		const onOpenFile = vi.fn();
		const data = committedWorkspace([{ path: "src/App.tsx", status: "modified", additions: 1, deletions: 1, size: 20, binary: false, editable: true, fileFingerprint: "file-1" }]);
		renderWithQuery(<WorkspaceReviewPane annotation={annotation()} target={{...commitTarget(data),source:undefined}} filter="" onBrowseAll={vi.fn()} onOpenFile={onOpenFile} sessionId="sess-1" split={false} />);
		expect(await screen.findByTestId("code-view")).toBeInTheDocument();
		await screen.findByTestId("review-patch");

		await userEvent.click(screen.getByRole("button", { name: "Edit file" }));
		expect(onOpenFile).toHaveBeenCalledWith("src/App.tsx", { editing: true, mode: "file", scope: "committed" });
	});

	it("does not advertise editing for a file the daemon marks read-only", async () => {
		const data = committedWorkspace([{ path: "src/App.tsx", status: "modified", additions: 1, deletions: 1, size: 20, binary: false, editable: false, fileFingerprint: "file-1" }]);
		renderWithQuery(<WorkspaceReviewPane annotation={annotation()} target={reviewTarget(data)} filter="" onBrowseAll={vi.fn()} onOpenFile={vi.fn()} sessionId="sess-1" split={false} />);
		expect(await screen.findByTestId("code-view")).toBeInTheDocument();
		await screen.findByTestId("review-patch");

		expect(screen.queryByRole("button", { name: "Edit file" })).not.toBeInTheDocument();
	});

	it("anchors whole-file feedback directly below the matching file header", async () => {
		const model = annotation();
		model.targets = [{ path: "src/App.tsx", side: "file", scope: "committed" }];
		const data = committedWorkspace([{ path: "src/App.tsx", status: "modified", additions: 1, deletions: 1, size: 20, binary: false, fileFingerprint: "file-1" }]);
		renderWithQuery(<WorkspaceReviewPane annotation={model} target={reviewTarget(data)} filter="" onBrowseAll={vi.fn()} sessionId="sess-1" split={false} />);

		const composer = await screen.findByRole("textbox", { name: /Feedback for src\/App\.tsx/ });
		// A popover under the header, portaled above the list (so a later file's
		// header can't cover it), not a row inside one diff column.
		// (jsdom boxes are zero-sized, so Radix's hideWhenDetached hides it from role queries.)
		expect(composer.closest('[role="dialog"]')).toHaveAttribute("aria-label", "Add feedback");
		expect(composer.closest("[data-annotation-line]")).toBeNull();
		expect(screen.getByTestId("code-view").querySelector("[data-annotation-line]")).toBeNull();
	});

	it("opens deleted markdown as source because no current rendered revision exists", async () => {
		const onOpenFile = vi.fn();
		postMock.mockResolvedValue({
			data: {
				sessionId: "sess-1",
				workspaceVersion: "workspace-1",
				groups: [{ repository: "", patch: "diff --git a/README.md b/README.md\n", truncated: false, includedPaths: ["README.md"], deferred: [] }],
			},
		});
		const data = committedWorkspace([{ path: "README.md", status: "deleted", additions: 0, deletions: 1, size: 20, binary: false, fileFingerprint: "file-1" }]);
		renderWithQuery(<WorkspaceReviewPane annotation={annotation()} target={commitTarget(data)} filter="" onBrowseAll={vi.fn()} onOpenFile={onOpenFile} sessionId="sess-1" split={false} />);
		expect(await screen.findByTestId("code-view")).toBeInTheDocument();
		await screen.findByTestId("review-patch");

		await userEvent.click(screen.getByRole("button", { name: "Open full file" }));
		expect(onOpenFile).toHaveBeenCalledWith("README.md", expect.objectContaining({source:expect.objectContaining({kind:"snapshot",commitShas:["commit-1"]}),mode:"file",scope:"committed"}));
	});

	it("offers the full file browser when there are no changes", async () => {
		const onBrowseAll = vi.fn();
		renderWithQuery(<WorkspaceReviewPane annotation={annotation()} target={reviewTarget(workspace([]))} filter="" onBrowseAll={onBrowseAll} sessionId="sess-1" split={false} />);

		expect(screen.getByText("No changed files found.")).toBeInTheDocument();
		expect(screen.queryByRole("button", { name: /Unstaged|Staged|Review changes/ })).not.toBeInTheDocument();
		await userEvent.click(screen.getByRole("button", { name: "Browse all files" }));
		expect(onBrowseAll).toHaveBeenCalledOnce();
		expect(postMock).not.toHaveBeenCalled();
	});

	it("filters the review without requesting unrelated files", async () => {
		const files = [
			{ path: "src/App.tsx", status: "modified" as const, additions: 1, deletions: 1, size: 20, binary: false, fileFingerprint: "file-1" },
			{ path: "docs/guide.md", status: "modified" as const, additions: 1, deletions: 0, size: 20, binary: false, fileFingerprint: "file-2" },
		];
		renderWithQuery(<WorkspaceReviewPane annotation={annotation()} target={reviewTarget(committedWorkspace(files))} filter="app" onBrowseAll={vi.fn()} sessionId="sess-1" split={false} />);

		await waitFor(() => expect(postMock).toHaveBeenCalled());
		expect(postMock.mock.calls[0]?.[1]?.body.paths).toEqual(["src/App.tsx"]);
	});

	it("renders a child repository diff and offers retry for a file its settled group left out", async () => {
		const data = workspaceProject([
			{ path: "alpha/README.md", status: "modified", additions: 1, deletions: 1, size: 20, binary: false, fileFingerprint: "alpha-1" },
			{ path: "beta/workspace-test.txt", status: "added", additions: 1, deletions: 0, size: 20, binary: false, fileFingerprint: "beta-1" },
		]);
		const onOpenFile = vi.fn();
		postMock.mockResolvedValue({
			data: {
				sessionId: "sess-1",
				workspaceVersion: "workspace-1",
				groups: [
					{ repository: "alpha", patch: "diff --git a/README.md b/README.md\n", truncated: false, includedPaths: ["alpha/README.md"], deferred: [] },
					{ repository: "beta", patch: "", truncated: false, includedPaths: ["beta/workspace-test.txt"], deferred: [] },
				],
			},
		});
		renderWithQuery(<WorkspaceReviewPane annotation={annotation()} target={reviewTarget(data)} filter="" onBrowseAll={vi.fn()} onOpenFile={onOpenFile} sessionId="sess-1" split={false} />);

		expect(await screen.findByText("alpha/README.md")).toBeInTheDocument();
		expect(await screen.findByText("Unable to load this diff.")).toBeInTheDocument();
		expect(screen.queryByText("Loading diff...")).not.toBeInTheDocument();
		await userEvent.click(screen.getByRole("button", { name: "Retry" }));
		await waitFor(() => expect(postMock).toHaveBeenCalledTimes(2));
		await userEvent.click(within(screen.getByRole("button", { name: "Collapse beta/workspace-test.txt" }).parentElement!).getByRole("button", { name: "Open full file" }));
		expect(onOpenFile).toHaveBeenCalledWith("beta/workspace-test.txt", expect.objectContaining({ mode: "file", scope: "combined" }));
	});

	it.each([
		{ path: "package-lock.json", size: 20 },
		{ path: "src/App.tsx", size: 600_000 },
	])("loads deferred $path on individual expansion and reuses it on re-expansion", async ({ path, size }) => {
		const data = committedWorkspace([{ path, status: "modified", additions: 800, deletions: 700, size, binary: false, fileFingerprint: "deferred-1" }]);
		postMock.mockResolvedValue({ data: { sessionId: "sess-1", workspaceVersion: "workspace-1", groups: [{ repository: "", patch: `diff --git a/${path} b/${path}\n`, truncated: false, includedPaths: [path], deferred: [] }] } });
		renderWithQuery(<WorkspaceReviewPane annotation={annotation()} target={reviewTarget(data)} filter="" onBrowseAll={vi.fn()} sessionId="sess-1" split={false} />);

		expect(screen.getByRole("button", { name: `Expand ${path}` })).toHaveAttribute("aria-expanded", "false");
		expect(postMock).not.toHaveBeenCalled();
		await userEvent.click(screen.getByRole("button", { name: `Expand ${path}` }));
		await waitFor(() => expect(postMock).toHaveBeenCalledTimes(1));
		expect(postMock.mock.calls[0]?.[1]?.body.paths).toEqual([path]);
		await screen.findByTestId("review-patch");
		expect(screen.getByRole("checkbox", { name: `Mark ${path} as viewed` })).toBeInTheDocument();
		expect(screen.queryByRole("button", { name: "Load diff" })).not.toBeInTheDocument();

		await userEvent.click(screen.getByRole("button", { name: `Collapse ${path}` }));
		expect(screen.getByRole("button", { name: `Expand ${path}` })).toHaveAttribute("aria-expanded", "false");
		await userEvent.click(screen.getByRole("button", { name: `Expand ${path}` }));
		expect(screen.getByRole("button", { name: `Collapse ${path}` })).toHaveAttribute("aria-expanded", "true");
		expect(postMock).toHaveBeenCalledTimes(1);
	});

	it("requests the target scope and view", async () => {
		const file = { path: "src/App.tsx", status: "modified" as const, additions: 1, deletions: 1, size: 20, binary: false, editable: false, fileFingerprint: "file-1" };
		const data = committedWorkspace([file]);
		const shas = ["b".repeat(40), "a".repeat(40)];
		const view = { kind: "branch" as const, name: "feature/menus", tipSha: "c".repeat(40), baseSha: "d".repeat(40), rangeMode: "since_base" as const };
		const target = buildReviewTarget({
			branch: { kind: "branch", name: "feature/menus" },
			changes: { kind: "commits", shas },
			manifest: data,
			legacy: false,
			history: { ...history(data, view), commits: shas.map((sha) => ({ sha, subject: sha, author: "Ada", timestamp: "2026-09-10T10:00:00Z", files: [file] })) },
			selection: { pending: false, data: { sessionId: "sess-1", tipSha: view.tipSha, baseSha: view.baseSha, contiguous: true, combinable: true, files: [file], summary: { files: 1, additions: 1, deletions: 1 }, truncated: false } },
		});
		renderWithQuery(<WorkspaceReviewPane annotation={annotation()} target={target} filter="" onBrowseAll={vi.fn()} sessionId="sess-1" split={false} />);

		await waitFor(() => expect(postMock).toHaveBeenCalledWith("/api/v1/sessions/{sessionId}/workspace/diffs", expect.objectContaining({
			body: expect.objectContaining({
				branch: "feature/menus",
				commitShas: shas,
				expectedBaseSha: view.baseSha,
				expectedTipSha: view.tipSha,
				paths: ["src/App.tsx"],
				scope: "committed",
				workspaceVersion: undefined,
			}),
		})));
	});

	it("renders the not-combinable message with Include commits in between", async () => {
		const data = committedWorkspace([{ path: "src/App.tsx", status: "modified", additions: 1, deletions: 1, size: 20, binary: false }]);
		const target: ReviewTarget = {
			...reviewTarget(data, { kind: "commits", shas: ["b".repeat(40), "a".repeat(40)] }),
			files: undefined,
			state: "not_combinable",
			conflict: { commitSha: "a".repeat(40), paths: ["src/App.tsx"], reason: "conflict", subject: "Rename the app" },
		};
		const onIncludeCommitsBetween = vi.fn();
		const onShowAllCommits = vi.fn();
		renderWithQuery(<WorkspaceReviewPane annotation={annotation()} target={target} filter="" onBrowseAll={vi.fn()} onIncludeCommitsBetween={onIncludeCommitsBetween} onShowAllCommits={onShowAllCommits} sessionId="sess-1" split={false} />);

		expect(screen.getByText(/"Rename the app" depends on changes from commits you didn't select/)).toBeInTheDocument();
		await userEvent.click(screen.getByRole("button", { name: "Include commits in between" }));
		expect(onIncludeCommitsBetween).toHaveBeenCalledOnce();
		await userEvent.click(screen.getByRole("button", { name: "Show all commits" }));
		expect(onShowAllCommits).toHaveBeenCalledOnce();
		expect(postMock).not.toHaveBeenCalled();
	});

	it("says it is combining commits while the selection loads", () => {
		const data = committedWorkspace([{ path: "src/App.tsx", status: "modified", additions: 1, deletions: 1, size: 20, binary: false }]);
		const target: ReviewTarget = { ...reviewTarget(data, { kind: "commits", shas: ["b".repeat(40), "a".repeat(40)] }), files: undefined, state: "pending" };
		renderWithQuery(<WorkspaceReviewPane annotation={annotation()} target={target} filter="" onBrowseAll={vi.fn()} sessionId="sess-1" split={false} />);

		expect(screen.getByText("Combining commits…")).toBeInTheDocument();
		expect(postMock).not.toHaveBeenCalled();
	});

	it("offers Retry when the commits it combines fail to load", async () => {
		const data = committedWorkspace([{ path: "src/App.tsx", status: "modified", additions: 1, deletions: 1, size: 20, binary: false }]);
		const target: ReviewTarget = { ...reviewTarget(data, { kind: "commits", shas: ["b".repeat(40), "a".repeat(40)] }), files: undefined, state: "error", error: new Error("Unable to combine commits") };
		const onRetry = vi.fn();
		renderWithQuery(<WorkspaceReviewPane annotation={annotation()} target={target} filter="" onRetry={onRetry} sessionId="sess-1" split={false} />);

		expect(screen.getByText("Unable to combine commits")).toBeInTheDocument();
		await userEvent.click(screen.getByRole("button", { name: "Retry" }));
		expect(onRetry).toHaveBeenCalledOnce();
	});

	it("keeps open actions available for historical targets", async () => {
		const image = { path: "logo.png", status: "modified" as const, additions: 0, deletions: 0, size: 128, binary: true };
		const file = { path: "src/App.tsx", status: "modified" as const, additions: 1, deletions: 1, size: 20, binary: false, editable: true, fileFingerprint: "file-1" };
		const data = committedWorkspace([file, image]);
		const target: ReviewTarget = { ...commitTarget(data), view: { branch: "feature/menus" }, source:{kind:"snapshot",view:{branch:"feature/menus"},label:"feature/menus"}, openable: true };
		renderWithQuery(<WorkspaceReviewPane annotation={annotation()} target={target} filter="" onBrowseAll={vi.fn()} onOpenFile={vi.fn()} sessionId="sess-1" split={false} />);

		expect(await screen.findByTestId("code-view")).toBeInTheDocument();
		await screen.findByTestId("review-patch");
		expect(screen.getByRole("button", { name: "Edit file" })).toBeDisabled();
		expect(screen.getAllByRole("button", { name: "Open full file" })).toHaveLength(2);
		expect(screen.getByRole("button", { name: "Open diff in center" })).toBeInTheDocument();
		expect(screen.getAllByRole("button", { name: "Add feedback" }).length).toBeGreaterThan(0);
		// The image preview reads the worktree, so another branch's image falls back to the placeholder.
		expect(screen.getByText("Binary file preview is not available.")).toBeInTheDocument();
		expect(screen.queryByAltText(/version of logo\.png/)).not.toBeInTheDocument();
	});

	it("keys viewed state by target key", async () => {
		const file = { path: "src/App.tsx", status: "modified" as const, additions: 1, deletions: 1, size: 20, binary: false, fileFingerprint: "file-1" };
		const data = committedWorkspace([file]);
		data.commits = [
			{ ...data.commits[0], sha: "b".repeat(40), subject: "Second" },
			{ ...data.commits[0], sha: "a".repeat(40), subject: "First" },
		];
		const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } });
		const pane = (target: ReviewTarget) => (
			<QueryClientProvider client={queryClient}><TooltipProvider>
				<WorkspaceReviewPane annotation={annotation()} target={target} filter="" onBrowseAll={vi.fn()} sessionId="sess-1" split={false} />
			</TooltipProvider></QueryClientProvider>
		);
		const { rerender } = render(pane(commitTarget(data, "a".repeat(40))));
		await screen.findByTestId("review-patch");
		await userEvent.click(await screen.findByRole("checkbox", { name: "Mark src/App.tsx as viewed" }));
		expect(JSON.parse(window.localStorage.getItem(`ao.files.viewed.sess-1.commit:${"a".repeat(40)}`) ?? "{}")).toEqual({ "src/App.tsx": "file-1" });

		rerender(pane(commitTarget(data, "b".repeat(40))));
		expect(await screen.findByRole("checkbox", { name: "Mark src/App.tsx as viewed" })).not.toBeChecked();
		rerender(pane(commitTarget(data, "a".repeat(40))));
		expect(await screen.findByRole("checkbox", { name: "Mark src/App.tsx as not viewed" })).toBeChecked();
	});
});
