import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { useState } from "react";
import { expect, it, vi } from "vitest";
import type { CloudCpClient, CloudCpWorkspaceReviewFileSummary, CloudCpWorkspaceReviewResponse } from "../../lib/cloud-cp";
import type { FileAnnotationModel } from "../WorkspaceDiffView";
import { SessionFileTabs } from "../SessionFileTabs";
import { SessionTopbarHost, SessionTopbarPortal, SessionTopbarProvider } from "../SessionTopbarPortal";
import { TurnSettingsBar } from "../chat/TurnSettingsBar";
import { TooltipProvider } from "../ui/tooltip";
import { CloudFileContentPane } from "./CloudFileContentPane";
import type { CloudFileOpenOptions } from "./CloudWorkspaceExplorer";
import { CloudWorkspaceReviewPane } from "./CloudWorkspaceReviewPane";

const annotation: FileAnnotationModel = { targets: [], status: "idle", error: "", begin: vi.fn(), draftFor: () => "", statusFor: () => "idle", setDraft: vi.fn(), cancel: vi.fn(), submit: vi.fn() };
const files: CloudCpWorkspaceReviewFileSummary[] = [
	{ path: "README.md", status: "modified", additions: 1, deletions: 1, size: 30, binary: false, editable: true, fileFingerprint: "tracked" },
	{ path: "notes.txt", status: "untracked", additions: 2, deletions: 0, size: 30, binary: false, editable: true, fileFingerprint: "untracked" },
];
const data: CloudCpWorkspaceReviewResponse = {
	workspaceVersion: "v1", files, truncated: false,
	sections: { unstaged: [files[0]], staged: [], untracked: [files[1]], committed: [] },
	commits: [], summary: { files: 2, additions: 3, deletions: 1 },
};
const models = [{ id: "opus", displayName: "Opus", default: false, efforts: ["high", "low"] }];

function FullFileComposition({ client }: { client: CloudCpClient }) {
	const [file, setFile] = useState<{ path: string; options?: CloudFileOpenOptions } | null>(null);
	return <SessionTopbarProvider><TooltipProvider>
		<SessionTopbarHost data-testid="topbar" />
		<SessionTopbarPortal>
			{file ? <div role="tablist"><SessionFileTabs state={{ activePath: file.path, openPaths: [file.path] }} onActivateFile={() => {}} onCloseFile={() => setFile(null)} /></div> : <span>Chat</span>}
		</SessionTopbarPortal>
		<TurnSettingsBar models={models} settings={{ model: "opus", reasoningEffort: "high", approvalMode: "default" }} onChange={() => {}} />
		{file ? <main><CloudFileContentPane annotation={annotation} baseUrl="https://cloud.test" client={client} initialMode={file.options?.mode} scope={file.options?.scope} orgId="org-test" path={file.path} sessionId="cloud-test" split={false} /></main> : null}
		<CloudWorkspaceReviewPane annotation={annotation} baseUrl="https://cloud.test" client={client} data={data} filter="" onBrowseAll={() => {}} onOpenFile={(path, options) => setFile({ path, options })} orgId="org-test" sessionId="cloud-test" split={false} />
	</TooltipProvider></SessionTopbarProvider>;
}

it("opens tracked and untracked full files directly from review beside the real topbar and model menus", async () => {
	const getWorkspaceReviewFile = vi.fn(async (_org: string, _session: string, query: { path: string }) => ({
		...files.find((file) => file.path === query.path)!,
		deleted: false, content: `first line of ${query.path}\nlast line of ${query.path}\n`, contentTruncated: false,
		diff: "", diffTruncated: false, workspaceVersion: "v1",
	}));
	const client = { getWorkspaceReviewFile, getWorkspaceReviewDiffs: vi.fn().mockResolvedValue({ workspaceVersion: "v1", groups: [] }) } as unknown as CloudCpClient;
	const view = render(<QueryClientProvider client={new QueryClient({ defaultOptions: { queries: { retry: false } } })}><FullFileComposition client={client} /></QueryClientProvider>);
	const user = userEvent.setup();
	for (const [index, file] of files.entries()) {
		if (index > 0) await user.click(screen.getByRole("button", { name: /Untracked/ }));
		await user.click(screen.getByRole("button", { name: "Open full file" }));
		expect(within(screen.getByTestId("topbar")).getByRole("tab", { name: file.path })).toBeInTheDocument();
		expect(getWorkspaceReviewFile).toHaveBeenLastCalledWith("org-test", "cloud-test", expect.objectContaining({ path: file.path, scope: index === 0 ? "unstaged" : "untracked" }));
		await waitFor(() => {
			const content = view.container.querySelector("main diffs-container")?.shadowRoot?.textContent;
			expect(content).toContain(`first line of ${file.path}`);
			expect(content).toContain(`last line of ${file.path}`);
		});
		await user.hover(screen.getByRole("button", { name: `Close ${file.path}` }));
		expect(await screen.findByRole("tooltip")).toHaveTextContent(`Close ${file.path}`);
		await user.click(screen.getByRole("button", { name: "Model and reasoning effort for the next turn" }));
		await user.keyboard("{ArrowDown}{ArrowRight}");
		expect(await screen.findByRole("menuitemradio", { name: "Opus" })).toBeInTheDocument();
		await user.keyboard("{Escape}{Escape}");
		await user.click(screen.getByRole("button", { name: `Close ${file.path}` }));
		expect(screen.queryByRole("main")).not.toBeInTheDocument();
	}
});
