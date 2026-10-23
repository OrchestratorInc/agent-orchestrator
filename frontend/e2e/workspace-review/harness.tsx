/// <reference path="../../src/renderer/global.d.ts" />
// Real review pane and Pierre virtualizer with deterministic query data. No
// daemon or filesystem writes; image responses are supplied by the browser test.
import { useState } from "react";
import { createRoot } from "react-dom/client";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { I18nextProvider } from "react-i18next";
import { appI18n } from "../../src/renderer/i18n";
import { WorkspaceReviewPane } from "../../src/renderer/components/diffs/WorkspaceReviewPane";
import type { FileAnnotationModel } from "../../src/renderer/components/WorkspaceDiffView";
import { TooltipProvider } from "../../src/renderer/components/ui/tooltip";
import { defaultWorkspaceReviewTarget, sessionWorkspaceDiffsQueryOptions, type WorkspaceFilesResponse } from "../../src/renderer/hooks/useSessionWorkspaceFiles";
import { REVIEW_CONTEXT_LINES } from "../../src/renderer/components/diffs/trailingContext";
import { setApiBaseUrl } from "../../src/renderer/lib/api-client";
import "../../src/renderer/styles.css";

const params = new URLSearchParams(location.search);
const mixed = params.get("mixed") === "1";
function reviewLink(mixed: boolean) {
	const query = new URLSearchParams(params);
	query.set("mixed", mixed ? "1" : "0");
	return `?${query}`;
}
const sessionId = "review-fixture";
setApiBaseUrl(params.get("api") ?? location.origin);
const images: WorkspaceFilesResponse["files"] = ["first.png", "second.png", "last.png"].map((path) => ({ path, status: "added", additions: 0, deletions: 0, size: 600_000, binary: true }));
const files: WorkspaceFilesResponse["files"] = mixed
	? [{ path: "example.ts", status: "modified", additions: 1, deletions: 1, size: 128, binary: false }, ...images]
	: images;
const data: WorkspaceFilesResponse = {
	sessionId, workspaceVersion: "fixture-1", files,
	sections: { committed: [], staged: [], unstaged: files, untracked: [] },
	commits: [], summary: { additions: mixed ? 1 : 0, deletions: mixed ? 1 : 0, files: files.length }, truncated: false,
};
const patch = [
	"diff --git a/example.ts b/example.ts", "index 1111111..2222222 100644",
	"--- a/example.ts", "+++ b/example.ts", "@@ -1,5 +1,5 @@",
	"-const value = 1;", "+const value = 2;", " // context", " // context", " // context", " // context", "",
].join("\n");
const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
client.setQueryData(sessionWorkspaceDiffsQueryOptions({ contextLines: REVIEW_CONTEXT_LINES, paths: files.map((file) => file.path), scope: "combined", sessionId, workspaceVersion: data.workspaceVersion }).queryKey, {
	sessionId, workspaceVersion: data.workspaceVersion,
	groups: mixed ? [{ repository: "", patch, includedPaths: ["example.ts"], truncated: false, deferred: [] }] : [],
});
const noop = () => {};
const annotation: FileAnnotationModel = { targets: [], status: "idle", error: "", begin: noop, draftFor: () => "", statusFor: () => "idle", setDraft: noop, cancel: noop, submit: async () => {} };

function Review() {
	const [allCollapsed, setAllCollapsed] = useState(false);
	const [request, setRequest] = useState<{ expand: boolean; key: number }>();
	const [scrollRequest, setScrollRequest] = useState<{ path: string; key: number }>();
	return <>
		<nav className="flex shrink-0 gap-2" aria-label="Changed files">{images.map(({ path }) => <button key={path} onClick={() => setScrollRequest((current) => ({ path, key: (current?.key ?? 0) + 1 }))}>Reveal {path}</button>)}</nav>
		<button onClick={() => setRequest((current) => ({ expand: allCollapsed, key: (current?.key ?? 0) + 1 }))}>{allCollapsed ? "Expand all files" : "Collapse all files"}</button>
		<WorkspaceReviewPane annotation={annotation} target={defaultWorkspaceReviewTarget(data)} collapseRequest={request} scrollRequest={scrollRequest} onAllCollapsedChange={setAllCollapsed} filter="" onBrowseAll={noop} sessionId={sessionId} split={false} />
	</>;
}

createRoot(document.getElementById("review-root")!).render(
	<QueryClientProvider client={client}>
		<I18nextProvider i18n={appI18n}>
			<TooltipProvider>
				<main className="flex h-dvh flex-col bg-background text-foreground" style={{ maxWidth: 650 }}>
					<nav className="flex shrink-0 gap-4 border-b border-border p-3 text-sm">
						<a href={reviewLink(false)}>Image-only review</a><a href={reviewLink(true)}>Mixed review</a>
					</nav>
					<section className="flex min-h-0 flex-1 flex-col" aria-label="Workspace review">
						<Review />
					</section>
				</main>
			</TooltipProvider>
		</I18nextProvider>
	</QueryClientProvider>,
);
