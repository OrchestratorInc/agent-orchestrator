// @vitest-environment jsdom

import "@testing-library/jest-dom/vitest";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { render as renderRaw, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import type { ReactNode } from "react";
import { describe, expect, it, vi } from "vitest";
import type { FileCodeReference } from "../../../shared/file-annotations";
import type { CloudCpClient, CloudCpWorkspaceReviewFileSummary } from "../../lib/cloud-cp";
import type { CodeSelection } from "../diffs/useCodeSelection";
import type { FileAnnotationModel } from "../WorkspaceDiffView";
import { CloudDiffFile } from "./CloudDiffFile";

// The diff loads a revealed reference's full file through a query.
const render = (ui: ReactNode) => renderRaw(<QueryClientProvider client={new QueryClient({ defaultOptions: { queries: { retry: false } } })}>{ui}</QueryClientProvider>);

const chat = vi.hoisted(() => ({
	ask: vi.fn<(reference: FileCodeReference) => boolean>(() => true),
	onAsk: undefined as ((selection: CodeSelection) => boolean | void) | undefined,
	enabled: undefined as boolean | undefined,
}));

vi.mock("../../lib/chat-context-bus", () => ({ useAskInChat: () => chat.ask }));
vi.mock("../diffs/useCodeSelection", async (importActual) => ({
	...await importActual<typeof import("../diffs/useCodeSelection")>(),
	useCodeSelection: (_ref: unknown, onAsk: (selection: CodeSelection) => boolean | void, enabled: boolean) => {
		chat.onAsk = onAsk;
		chat.enabled = enabled;
		return { ask: vi.fn(), dismiss: vi.fn(), source: { get: () => null, subscribe: () => () => undefined } };
	},
}));

vi.mock("@pierre/diffs", () => ({
	parsePatchFiles: () => [{ files: [{ name: "src/App.tsx", type: "changed", hunks: [], deletionLines: [], additionLines: [] }] }],
}));

vi.mock("@pierre/diffs/react", () => ({
	FileDiff: ({ lineAnnotations, options, renderGutterUtility }: {
		lineAnnotations?: unknown[];
		options: { collapsedContextThreshold: number; diffStyle: string; loadDiffFiles: (metadata: unknown) => Promise<unknown> };
		renderGutterUtility?: (getLine: () => { lineNumber: number; side: "additions" }) => ReactNode;
	}) => {
		void options.loadDiffFiles({ name: "src/App.tsx", type: "changed" });
		return <div data-annotations={lineAnnotations?.length ?? 0} data-collapsed={options.collapsedContextThreshold} data-testid="file-diff" data-style={options.diffStyle}>{renderGutterUtility?.(() => ({ lineNumber: 2, side: "additions" }))}</div>;
	},
}));

const file: CloudCpWorkspaceReviewFileSummary = {
	path: "src/App.tsx", status: "modified", additions: 1, deletions: 1, size: 20,
	binary: false, editable: true, fileFingerprint: "fp-1",
};

const annotation: FileAnnotationModel = {
	targets: [], status: "idle", error: "", begin: vi.fn(), draftFor: () => "", statusFor: () => "idle", setDraft: vi.fn(), cancel: vi.fn(), submit: vi.fn(),
};

const patch = "diff --git a/src/App.tsx b/src/App.tsx\n--- a/src/App.tsx\n+++ b/src/App.tsx\n@@ -1,2 +1,2 @@\n one\n-two\n+TWO\n";
const revision = { path: file.path, side: "after", revision: "r1", workspaceVersion: "v1", size: 4, exists: true, binary: false, truncated: false, content: "text" };
const client = { getWorkspaceReviewRevision: vi.fn().mockResolvedValue(revision) } as unknown as CloudCpClient;

function renderDiff(props: Partial<React.ComponentProps<typeof CloudDiffFile>> = {}) {
	return render(<CloudDiffFile annotation={annotation} baseUrl="https://cloud.test" client={client} file={file} onActiveSelectionChange={vi.fn()} orgId="org-1" patch={patch} scope="combined" sessionId="session-1" split={false} surface="focused" workspaceVersion="v1" {...props} />);
}

describe("CloudDiffFile", () => {
	it("renders split diffs and loads before and after revisions through Cloud", async () => {
		const getWorkspaceReviewRevision = vi.fn().mockResolvedValue({
			path: file.path, side: "after", revision: "r1", workspaceVersion: "v1", size: 4,
			exists: true, binary: false, truncated: false, content: "text",
		});
		render(<CloudDiffFile annotation={annotation} baseUrl="https://cloud.test" client={{ getWorkspaceReviewRevision } as unknown as CloudCpClient} commitSha="abc" file={file} onActiveSelectionChange={vi.fn()} orgId="org-1" patch="diff" scope="committed" sessionId="session-1" split surface="review" workspaceVersion="v1" />);
		expect(screen.getByTestId("file-diff")).toHaveAttribute("data-style", "split");
		await waitFor(() => expect(getWorkspaceReviewRevision).toHaveBeenCalledTimes(2));
		expect(getWorkspaceReviewRevision).toHaveBeenCalledWith("org-1", "session-1", expect.objectContaining({ path: file.path, side: "before", scope: "committed", commitSha: "abc" }));
	});

	it("does not render another file's patch when the requested file has no diff", () => {
		const unchanged = { ...file, path: "README.md", status: "unmodified" as const, additions: 0, deletions: 0 };
		const revision = { path: unchanged.path, side: "after", revision: "r1", workspaceVersion: "v1", size: 4, exists: true, binary: false, truncated: false, content: "text" };
		render(<CloudDiffFile annotation={annotation} baseUrl="https://cloud.test" client={{ getWorkspaceReviewRevision: vi.fn().mockResolvedValue(revision) } as unknown as CloudCpClient} fallback={<div>No diff for this file</div>} file={unchanged} onActiveSelectionChange={vi.fn()} orgId="org-1" patch="multi-file diff" scope="combined" sessionId="session-1" split={false} surface="review" workspaceVersion="v1" />);
		expect(screen.getByText("No diff for this file")).toBeInTheDocument();
		expect(screen.queryByTestId("file-diff")).not.toBeInTheDocument();
	});

	// The focused tab and the review pane keep their own comment boxes, as in a local review.
	it("opens a line comment on the surface the diff is shown in", async () => {
		const begin = vi.fn();
		renderDiff({ annotation: { ...annotation, begin } });
		await userEvent.click(screen.getByRole("button", { name: "Add feedback" }));
		expect(begin).toHaveBeenCalledWith(expect.objectContaining({ path: "src/App.tsx", side: "new", line: 2, lineText: "TWO", surface: "focused" }));
	});

	it("shows only its own surface's line comments", () => {
		const open: FileAnnotationModel = { ...annotation, targets: [{ path: "src/App.tsx", side: "new", line: 2, scope: "combined", surface: "focused" }] };
		const { unmount } = renderDiff({ annotation: open, surface: "focused" });
		expect(screen.getByTestId("file-diff")).toHaveAttribute("data-annotations", "1");
		unmount();
		renderDiff({ annotation: open, surface: "review" });
		expect(screen.getByTestId("file-diff")).toHaveAttribute("data-annotations", "0");
	});

	it("adds highlighted diff code to the chat as a workspace reference", () => {
		chat.ask.mockClear();
		renderDiff({ commitSha: "abc", scope: "untracked" });
		expect(chat.enabled).toBe(true);
		chat.onAsk?.({
			host: document.createElement("div"),
			rows: [{ kind: "add", oldNo: null, newNo: 2, text: "TWO", side: "new" }],
			range: { start: 2, end: 2, side: "additions", endSide: "additions" },
			point: { x: 0, y: 0 },
			dragging: false,
		} as unknown as CodeSelection);
		expect(chat.ask).toHaveBeenCalledWith(expect.objectContaining({
			path: "src/App.tsx", side: "new", line: 2, endLine: 2, source: { kind: "workspace" }, commitSha: "abc",
			scope: "combined", fileFingerprint: "fp-1", workspaceVersion: "v1",
		}));
	});

	it("keeps Add to chat off where the path would not name the file in the session", () => {
		renderDiff({ chatReferences: false });
		expect(chat.enabled).toBe(false);
	});

	it("reads a view's revisions from its commits, not the worktree's version", async () => {
		const getWorkspaceReviewRevision = vi.fn().mockResolvedValue(revision);
		const view = { prNumber: 12, sourceUrl: "https://github.com/acme/app/pull/12", expectedTipSha: "a".repeat(40), commitShas: ["c".repeat(40)] };
		renderDiff({ client: { getWorkspaceReviewRevision } as unknown as CloudCpClient, scope: "committed", view });
		await waitFor(() => expect(getWorkspaceReviewRevision).toHaveBeenCalledTimes(2));
		expect(getWorkspaceReviewRevision).toHaveBeenCalledWith("org-1", "session-1", { ...view, path: file.path, scope: "committed", side: "before" });
		expect(getWorkspaceReviewRevision.mock.calls[0][2]).not.toHaveProperty("workspaceVersion");
	});

	it("names a PR's picked commit in a chat reference, and offers no reference for a branch view", () => {
		chat.ask.mockClear();
		const tip = "a".repeat(40);
		const sha = "c".repeat(40);
		const url = "https://github.com/acme/app/pull/12";
		renderDiff({ scope: "committed", source: { kind: "snapshot", view: { prNumber: 12, sourceUrl: url, expectedTipSha: tip }, commitShas: [sha], label: "PR #12" } });
		expect(chat.enabled).toBe(true);
		chat.onAsk?.({
			host: document.createElement("div"),
			rows: [{ kind: "add", oldNo: null, newNo: 2, text: "TWO", side: "new" }],
			range: { start: 2, end: 2, side: "additions", endSide: "additions" },
			point: { x: 0, y: 0 },
			dragging: false,
		} as unknown as CodeSelection);
		expect(chat.ask).toHaveBeenCalledWith(expect.objectContaining({ source: { kind: "pull_request", number: 12, url, snapshot: tip }, commitSha: sha, scope: "committed" }));
		renderDiff({ scope: "committed", source: { kind: "snapshot", view: { branch: "feat/x" }, commitShas: [sha], label: "feat/x" } });
		expect(chat.enabled).toBe(false);
	});

	it("loads the full file and keeps no context collapsed to reveal a chat reference's lines", async () => {
		const getWorkspaceReviewRevision = vi.fn().mockResolvedValue(revision);
		renderDiff({ annotationRange: { line: 2, endLine: 2, side: "new", lines: [{ kind: "add", side: "new", oldNo: null, newNo: 2, text: "TWO" }], requestKey: 1 }, client: { getWorkspaceReviewRevision } as unknown as CloudCpClient });
		expect(screen.getByTestId("file-diff")).toHaveAttribute("data-collapsed", String(Number.MAX_SAFE_INTEGER));
		await waitFor(() => expect(getWorkspaceReviewRevision).toHaveBeenCalledWith("org-1", "session-1", expect.objectContaining({ path: file.path, side: "after", workspaceVersion: undefined })));
		// The mocked renderer draws no rows, so the reveal cannot verify them.
		expect(await screen.findByText(/Loading selected lines|could not be verified/)).toBeInTheDocument();
	});

	it("keeps unchanged context collapsed without a reference to reveal", () => {
		renderDiff();
		expect(screen.getByTestId("file-diff")).toHaveAttribute("data-collapsed", "8");
	});
});
