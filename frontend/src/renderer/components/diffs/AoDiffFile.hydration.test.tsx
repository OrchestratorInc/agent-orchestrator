import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { createElement, useLayoutEffect, useRef } from "react";
import { expect, it, vi } from "vitest";
import type { FileAnnotationRange } from "../../../shared/chat-file-reference";
import type { WorkspaceFileDetail } from "../../hooks/useSessionWorkspaceFiles";
import type { FileAnnotationModel } from "../WorkspaceDiffView";
import { AoDiffFile } from "./AoDiffFile";

const { revision } = vi.hoisted(() => ({ revision: vi.fn() }));
vi.mock("../../hooks/useSessionWorkspaceFiles", async (original) => ({
	...await original<typeof import("../../hooks/useSessionWorkspaceFiles")>(), fetchWorkspaceFileRevision: revision,
}));
vi.mock("@pierre/diffs/react", () => ({
	FileDiff: ({ selectedLines, options }: { selectedLines: unknown; options: { onPostRender: () => void } }) => {
		const ref = useRef<HTMLElement>(null);
		useLayoutEffect(() => {
			const host = ref.current!;
			const root = host.shadowRoot ?? host.attachShadow({ mode: "open" });
			root.innerHTML = '<div data-line="2" data-line-type="change-addition">new();</div>';
			options.onPostRender();
		});
		return createElement("diffs-container", { ref, "data-selected": JSON.stringify(selectedLines) });
	},
}));

it("retries failed hydration for the same request and highlights without reopening", async () => {
	const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
	const range: FileAnnotationRange = { requestKey: 42, line: 2, endLine: 2, side: "new", lines: [{ kind: "add", side: "new", oldNo: null, newNo: 2, text: "new();" }] };
	const detail: WorkspaceFileDetail = {
		sessionId: "session", path: "a.ts", status: "modified", additions: 1, deletions: 1, size: 14, binary: false, deleted: false,
		content: "keep\nnew();\n", contentTruncated: false, diffTruncated: false, workspaceVersion: "v1", fileFingerprint: "hydration-test",
		diff: "diff --git a/a.ts b/a.ts\n--- a/a.ts\n+++ b/a.ts\n@@ -1,2 +1,2 @@\n keep\n-old();\n+new();\n",
	};
	const annotation: FileAnnotationModel = { targets: [], status: "idle", error: "", begin: vi.fn(), draftFor: () => "", statusFor: () => "idle", setDraft: vi.fn(), cancel: vi.fn(), submit: vi.fn() };
	revision.mockRejectedValue(new Error("temporary source failure"));
	const view = render(<QueryClientProvider client={client}><AoDiffFile annotation={annotation} annotationRange={range} detail={detail} fallback={null} onActiveSelectionChange={vi.fn()} sessionId="session" split={false} /></QueryClientProvider>);
	expect(await screen.findByRole("alert")).toHaveTextContent("Could not load the selected lines.");
	expect(document.querySelector("diffs-container")).toHaveAttribute("data-selected", "null");
	expect(revision).toHaveBeenCalledTimes(2);
	revision.mockImplementation(({ side }: { side: string }) => Promise.resolve({ content: `keep\n${side === "before" ? "old" : "new"}();\n`, revision: side, binary: false, truncated: false }));
	const retry = screen.getByRole("button", { name: "Retry" });
	fireEvent.pointerDown(retry, { button: 0 });
	fireEvent.click(retry);
	await waitFor(() => expect(document.querySelector("diffs-container")).toHaveAttribute("data-selected", expect.stringContaining('"start":2')));
	expect(screen.queryByRole("alert")).not.toBeInTheDocument();
	expect(revision).toHaveBeenCalledTimes(4);
	expect(client.getQueryCache().getAll().filter(query => query.queryKey[0] === "files-preview-end-of-file").map(query => query.queryKey.at(-1))).toEqual([42]);
	view.unmount(); client.clear();
});
