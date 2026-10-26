/// <reference path="../../src/renderer/global.d.ts" />
import { useState } from "react";
import { createRoot } from "react-dom/client";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { I18nextProvider } from "react-i18next";
import { CloudWorkspaceExplorer } from "../../src/renderer/components/cloud-workspace/CloudWorkspaceExplorer";
import { TooltipProvider } from "../../src/renderer/components/ui/tooltip";
import { appI18n } from "../../src/renderer/i18n";
import { aoBridge } from "../../src/renderer/lib/bridge";
import type { CloudCpWorkspaceReviewFileSummary, CloudCpWorkspaceReviewResponse } from "../../src/renderer/lib/cloud-cp";
import type { WorkspaceSession } from "../../src/renderer/types/workspace";
import "../../src/renderer/styles.css";

const session: WorkspaceSession = {
	id: "cloud-review-fixture", cloud: { orgId: "fixture-org" }, workspaceId: "fixture-project", workspaceName: "Fixture",
	title: "Cloud review fixture", provider: "codex", status: "working", prs: [], updatedAt: "2026-10-24T00:00:00Z",
};
const file = (path: string): CloudCpWorkspaceReviewFileSummary => ({ path, status: "modified", additions: 1, deletions: 1, size: 20, binary: false, editable: true, fileFingerprint: `fp:${path}` });
const staged = [file("app.ts"), file("staged.ts"), file("package-lock.json")];
let changed = false;
let siblingPresent = true;
function summary(sibling: boolean): CloudCpWorkspaceReviewResponse {
	const files = sibling ? [{ ...file("app.ts"), status: "untracked" as const }] : staged;
	return {
		workspaceVersion: changed ? "v2" : "v1", files, truncated: false,
		sections: { staged: sibling ? [] : files, unstaged: changed && !sibling ? [file("new.ts")] : [], untracked: sibling ? files : [], committed: [] },
		commits: [{ sha: sibling ? "987654321" : "abcdef123", subject: "Previous work", author: "Fixture", timestamp: "2026-10-24T00:00:00Z", files: [file("old.ts")] }],
		summary: { files: files.length, additions: files.length, deletions: files.length },
		...(!sibling ? { repositories: [{ name: "repository", label: "main", main: true, present: true }, { name: "tools", present: siblingPresent }] } : {}),
	};
}

window.ao = aoBridge;
aoBridge.cloud.getSession = async () => ({ authProvider: "local", user: { id: "fixture-user", email: "fixture@example.test", displayName: "Fixture" }, storedAt: "2026-10-24T00:00:00Z" });
aoBridge.cloud.onSessionChanged = () => () => undefined;
aoBridge.cloudCp.openStream = async () => ({ streamId: "fixture-stream" });
aoBridge.cloudCp.closeStream = () => undefined;
aoBridge.cloudCp.onStreamEvent = () => () => undefined;
aoBridge.cloudCp.request = async ({ path, body }) => {
	const url = new URL(path, location.origin);
	const sibling = url.searchParams.get("repository") === "tools";
	let response: unknown;
	if (url.pathname.endsWith("/review")) response = summary(sibling);
	else if (url.pathname.endsWith("/diffs")) {
		const request = JSON.parse(body ?? "{}");
		response = { workspaceVersion: changed ? "v2" : "v1", groups: (request.paths ?? []).map((path: string) => ({
			patch: `diff --git a/${path} b/${path}\n--- a/${path}\n+++ b/${path}\n@@ -1 +1 @@\n-const value = 1;\n+const value = 2;\n`,
			includedPaths: [path], truncated: false, deferred: [], errors: [],
		})) };
	} else if (url.pathname.endsWith("/tree")) response = { path: "", truncated: false, entries: staged.map(({ path, size }) => ({ name: path, path, type: "file", size, status: "modified" })) };
	else if (url.pathname.endsWith("/search")) response = { results: staged.filter(({ path }) => path.includes(url.searchParams.get("query") ?? "")), truncated: false };
	else if (url.pathname.endsWith("/revision")) response = { content: "const value = 2;\n", binary: false, truncated: false };
	else return { status: 404, headers: { "Content-Type": "application/json" }, body: JSON.stringify({ error: { message: `Unexpected fixture path: ${path}` } }) };
	return { status: 200, headers: { "Content-Type": "application/json" }, body: JSON.stringify(response) };
};

const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false, staleTime: Infinity } } });
queryClient.setQueryData(["settings"], { cloudEnabled: true, cloudControlPlaneUrl: location.origin });

function Harness() {
	const [maximized, setMaximized] = useState(false);
	return <main className="flex h-dvh flex-col bg-background text-foreground">
		<nav className="flex shrink-0 gap-4 border-b border-border p-3 text-sm">
			<span>Fixture-backed renderer; no live Cloud or native app</span>
			<button onClick={() => { changed = true; }}>Agent adds unstaged file</button>
			<button onClick={() => { siblingPresent = false; }}>Remove sibling checkout</button>
		</nav>
		<div className="min-h-0 flex-1" style={{ width: maximized ? "100%" : 650 }}>
			<CloudWorkspaceExplorer isMaximized={maximized} key={maximized ? "maximized" : "docked"} onToggleMaximized={setMaximized} session={session} />
		</div>
	</main>;
}

createRoot(document.getElementById("review-root")!).render(<QueryClientProvider client={queryClient}><I18nextProvider i18n={appI18n}><TooltipProvider><Harness /></TooltipProvider></I18nextProvider></QueryClientProvider>);
