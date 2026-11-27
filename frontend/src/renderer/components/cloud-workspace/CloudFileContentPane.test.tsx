// @vitest-environment jsdom

import "@testing-library/jest-dom/vitest";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";
import type { CloudCpClient } from "../../lib/cloud-cp";
import type { FileAnnotationModel } from "../WorkspaceDiffView";
import { TooltipProvider } from "../ui/tooltip";
import { CloudFileContentPane } from "./CloudFileContentPane";

vi.mock("../ReadOnlyFileView", () => ({ ReadOnlyFileView: ({ annotationRange, chatReferences, commitSha, detail, editing, onEditChange, source }: { annotationRange?: { line: number }; chatReferences?: boolean; commitSha?: string; detail: { content: string }; editing: boolean; onEditChange?: (value: string) => void; source?: { kind: string } }) => editing ? <textarea aria-label="editor" defaultValue={detail.content} onChange={(event) => onEditChange?.(event.target.value)} /> : <pre data-chat={String(chatReferences)} data-commit={commitSha ?? ""} data-range={annotationRange?.line ?? ""} data-source={source?.kind ?? ""}>{detail.content}</pre> }));
vi.mock("./CloudDiffFile", () => ({ CloudDiffFile: ({ annotationRange, chatReferences, commitSha, source, surface, view }: { annotationRange?: { line: number }; chatReferences?: boolean; commitSha?: string; source?: { kind: string }; surface: string; view?: { branch?: string; prNumber?: number } }) => <div data-chat={String(chatReferences)} data-commit={commitSha ?? ""} data-range={annotationRange?.line ?? ""} data-source={source?.kind ?? ""} data-surface={surface} data-testid="cloud-diff" data-view={view?.branch ?? view?.prNumber ?? ""} /> }));
vi.mock("../markdown/MarkdownFileView", () => ({ MarkdownFileView: ({ content }: { content: string }) => <article>{content}</article> }));

const annotation: FileAnnotationModel = { targets: [], status: "idle", error: "", begin: vi.fn(), draftFor: () => "", statusFor: () => "idle", setDraft: vi.fn(), cancel: vi.fn(), submit: vi.fn() };
const detail = { path: "README.md", status: "modified" as const, additions: 1, deletions: 1, size: 8, binary: false, editable: true, fileFingerprint: "fp-1", deleted: false, content: "# Hello", contentTruncated: false, diff: "diff", diffTruncated: false, workspaceVersion: "v1" };

function renderPane(client: CloudCpClient, props: Partial<React.ComponentProps<typeof CloudFileContentPane>> = {}) {
	return render(<QueryClientProvider client={new QueryClient({ defaultOptions: { queries: { retry: false } } })}><TooltipProvider><CloudFileContentPane annotation={annotation} baseUrl="https://cloud.test" client={client} orgId="org-1" path="README.md" sessionId="session-1" split={false} {...props} /></TooltipProvider></QueryClientProvider>);
}

describe("CloudFileContentPane", () => {
	it("switches between diff, complete file, and rendered Markdown", async () => {
		const client = { getWorkspaceReviewFile: vi.fn().mockResolvedValue(detail) } as unknown as CloudCpClient;
		renderPane(client);
		expect(await screen.findByTestId("cloud-diff")).toBeInTheDocument();
		// Its comment boxes stay apart from the review pane's.
		expect(screen.getByTestId("cloud-diff")).toHaveAttribute("data-surface", "focused");
		expect(screen.getByTestId("cloud-diff")).toHaveAttribute("data-chat", "true");
		await userEvent.click(screen.getByRole("tab", { name: "File" }));
		expect(screen.getByText("# Hello")).toBeInTheDocument();
		await userEvent.click(screen.getByRole("tab", { name: "Rich preview" }));
		expect(screen.getByRole("article")).toHaveTextContent("# Hello");
	});

	it("saves edits with the current fingerprint and reports dirty state", async () => {
		const updateWorkspaceReviewFile = vi.fn().mockResolvedValue({ ...detail, content: "# Changed", fileFingerprint: "fp-2" });
		const client = { getWorkspaceReviewFile: vi.fn().mockResolvedValue(detail), updateWorkspaceReviewFile } as unknown as CloudCpClient;
		const onDirtyChange = vi.fn();
		renderPane(client, { initialMode: "file", onDirtyChange });
		await userEvent.click(await screen.findByRole("button", { name: "Edit file" }));
		const editor = screen.getByRole("textbox", { name: "Edit README.md" }).querySelector<HTMLElement>(".cm-content");
		expect(editor).toBeInTheDocument();
		await userEvent.click(editor!);
		await userEvent.keyboard("{Control>}a{/Control}# Changed");
		await waitFor(() => expect(onDirtyChange).toHaveBeenLastCalledWith(true));
		await userEvent.click(screen.getByRole("button", { name: "Save" }));
		await waitFor(() => expect(updateWorkspaceReviewFile).toHaveBeenCalledWith("org-1", "session-1", { path: "README.md", content: "# Changed", expectedFileFingerprint: "fp-1" }));
	});

	it("saves only the file on screen when ⌘S is pressed with several Cloud file tabs kept open", async () => {
		// The session view keeps every open Cloud file tab mounted, hidden behind
		// the one on screen; ⌘S must not also save an edit in a hidden tab.
		const file = (path: string) => ({ ...detail, path, content: `old ${path}`, fileFingerprint: `fp-${path}` });
		const getWorkspaceReviewFile = vi.fn(async (_org: string, _session: string, query: { path: string }) => file(query.path));
		const updateWorkspaceReviewFile = vi.fn(async (_org: string, _session: string, body: { path: string }) => file(body.path));
		const client = { getWorkspaceReviewFile, updateWorkspaceReviewFile } as unknown as CloudCpClient;
		render(<QueryClientProvider client={new QueryClient({ defaultOptions: { queries: { retry: false } } })}><TooltipProvider>
			<div data-testid="shown"><CloudFileContentPane active annotation={annotation} baseUrl="https://cloud.test" client={client} initialMode="file" orgId="org-1" path="a.ts" sessionId="session-1" split={false} /></div>
			<div data-testid="hidden"><CloudFileContentPane active={false} annotation={annotation} baseUrl="https://cloud.test" client={client} initialMode="file" orgId="org-1" path="b.ts" sessionId="session-1" split={false} /></div>
		</TooltipProvider></QueryClientProvider>);
		for (const id of ["hidden", "shown"]) {
			const pane = screen.getByTestId(id);
			await userEvent.click(await within(pane).findByRole("button", { name: "Edit file" }));
			await userEvent.type(within(pane).getByRole("textbox", { name: "editor" }), " edited");
		}
		await userEvent.keyboard("{Control>}s{/Control}");
		await waitFor(() => expect(updateWorkspaceReviewFile).toHaveBeenCalledWith("org-1", "session-1", expect.objectContaining({ path: "a.ts" })));
		expect(updateWorkspaceReviewFile).toHaveBeenCalledTimes(1);
	});

	it("leaves line feedback to the file renderer instead of opening a second header composer", async () => {
		const client = { getWorkspaceReviewFile: vi.fn().mockResolvedValue(detail) } as unknown as CloudCpClient;
		const lineAnnotation: FileAnnotationModel = {
			...annotation,
			targets: [{
				path: "README.md",
				side: "file",
				line: 2,
				lineKind: "context",
				lineText: "Hello",
				scope: "combined",
				surface: "focused",
			}],
		};
		renderPane(client, { annotation: lineAnnotation, initialMode: "file" });
		await screen.findByText("# Hello");
		expect(screen.queryByRole("textbox", { name: /feedback/i })).not.toBeInTheDocument();
	});

	it("reads another branch's file from its view, read-only, and hands the view to the diff", async () => {
		const getWorkspaceReviewFile = vi.fn().mockResolvedValue({ ...detail, editable: false, historical: true });
		const view = { branch: "feat/x", expectedTipSha: "a".repeat(40), commitShas: ["c".repeat(40)] };
		const source = { kind: "snapshot" as const, view: { branch: "feat/x" }, commitShas: view.commitShas, label: "feat/x" };
		renderPane({ getWorkspaceReviewFile } as unknown as CloudCpClient, { commitSha: "ignored", scope: "unstaged", source, view });
		expect(await screen.findByTestId("cloud-diff")).toHaveAttribute("data-view", "feat/x");
		expect(screen.getByTestId("cloud-diff")).toHaveAttribute("data-source", "snapshot");
		expect(screen.getByTestId("cloud-diff")).toHaveAttribute("data-commit", "");
		expect(getWorkspaceReviewFile).toHaveBeenCalledWith("org-1", "session-1", { path: "README.md", scope: "committed", commitSha: undefined, ...view, prNumber: undefined, sourceUrl: undefined, expectedBaseSha: undefined });
		expect(screen.getByRole("button", { name: "Edit file" })).toBeDisabled();
	});

	it("keeps another branch's file read-only even when it is opened for editing", async () => {
		const updateWorkspaceReviewFile = vi.fn();
		const getWorkspaceReviewFile = vi.fn().mockResolvedValue(detail);
		const view = { branch: "feat/x", expectedTipSha: "a".repeat(40) };
		renderPane({ getWorkspaceReviewFile, updateWorkspaceReviewFile } as unknown as CloudCpClient, { initialEditing: true, initialMode: "file", view });
		expect(await screen.findByText("# Hello")).toBeInTheDocument();
		expect(screen.queryByLabelText("editor")).not.toBeInTheDocument();
		expect(screen.queryByRole("button", { name: "Save file" })).not.toBeInTheDocument();
		expect(updateWorkspaceReviewFile).not.toHaveBeenCalled();
	});

	it("offers no chat references from a sibling repository's complete file either", async () => {
		const client = { getWorkspaceReviewFile: vi.fn().mockResolvedValue(detail) } as unknown as CloudCpClient;
		renderPane(client, { chatReferences: false, initialMode: "file" });
		expect(await screen.findByText("# Hello")).toHaveAttribute("data-chat", "false");
	});

	it("reveals a chat reference's lines in the diff and the complete file, keeping the commit it came from", async () => {
		const client = { getWorkspaceReviewFile: vi.fn().mockResolvedValue(detail) } as unknown as CloudCpClient;
		const annotationRange = { line: 3, endLine: 4, side: "new" as const, lines: [], requestKey: 7 };
		renderPane(client, { annotationRange, commitSha: "abc", scope: "committed" });
		expect(await screen.findByTestId("cloud-diff")).toHaveAttribute("data-range", "3");
		await userEvent.click(screen.getByRole("tab", { name: "File" }));
		// Code added to the chat from the complete file names the commit it was read at.
		expect(screen.getByText("# Hello")).toHaveAttribute("data-commit", "abc");
		expect(screen.getByText("# Hello")).toHaveAttribute("data-range", "3");
	});
});
