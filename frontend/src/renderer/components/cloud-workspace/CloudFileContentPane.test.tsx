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

vi.mock("../ReadOnlyFileView", () => ({ ReadOnlyFileView: ({ detail, editing, onEditChange }: { detail: { content: string }; editing: boolean; onEditChange?: (value: string) => void }) => editing ? <textarea aria-label="editor" defaultValue={detail.content} onChange={(event) => onEditChange?.(event.target.value)} /> : <pre>{detail.content}</pre> }));
vi.mock("./CloudDiffFile", () => ({ CloudDiffFile: () => <div data-testid="cloud-diff" /> }));
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
});
