import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import * as codeSelection from "./useCodeSelection";
import { subscribeChatComposerReferences } from "../../lib/chat-context-bus";
import { render, screen } from "@testing-library/react";
import type { ReactNode } from "react";
import { describe, expect, it, vi } from "vitest";
import type { WorkspaceFileDetail } from "../../hooks/useSessionWorkspaceFiles";
import type { FileAnnotationModel } from "../WorkspaceDiffView";
import { AoDiffFile } from "./AoDiffFile";

const { revisionMock, loader } = vi.hoisted(()=>({revisionMock:vi.fn(),loader:{current:undefined as undefined | ((metadata:never)=>Promise<unknown>)}}));
vi.mock("../../hooks/useSessionWorkspaceFiles",async(importOriginal)=>({...await importOriginal<typeof import("../../hooks/useSessionWorkspaceFiles")>(),fetchWorkspaceFileRevision:revisionMock}));

vi.mock("@pierre/diffs", () => ({
	parsePatchFiles: () => [{ files: [{ name: "src/App.tsx", type: "changed" }] }],
}));

vi.mock("@pierre/diffs/react", () => ({
	FileDiff: ({ options, renderGutterUtility }: {
		options: { enableGutterUtility?: boolean; unsafeCSS?: string; loadDiffFiles?: (metadata:never)=>Promise<unknown> };
		renderGutterUtility?: (getHoveredLine: () => { lineNumber: number; side: "additions" }) => ReactNode;
	}) => {loader.current=options.loadDiffFiles;return <div data-gutter-enabled={String(Boolean(options.enableGutterUtility))} data-surface-css={options.unsafeCSS}>{renderGutterUtility?.(() => ({ lineNumber: 2, side: "additions" }))}</div>;},
}));

function detail(): WorkspaceFileDetail {
	return {
		sessionId: "sess-1",
		path: "src/App.tsx",
		status: "modified",
		additions: 1,
		deletions: 1,
		size: 24,
		binary: false,
		deleted: false,
		content: "const value = 2;\n",
		contentTruncated: false,
		diff: "@@ -1,2 +1,2 @@\n const first = 1;\n-const value = 1;\n+const value = 2;\n",
		diffTruncated: false,
		workspaceVersion: "workspace-1",
		fileFingerprint: "file-1",
	};
}

function annotation(): FileAnnotationModel {
	return {
		targets: [],
		status: "idle",
		error: "",
		begin: vi.fn(),
		draftFor: () => "", statusFor: () => "idle",
		setDraft: vi.fn(),
		cancel: vi.fn(),
		submit: vi.fn(),
	};
}

describe("AoDiffFile", () => {
	it("captures a single PR commit with its original comparison identity", () => {
		const accept=vi.fn(()=>true),remove=subscribeChatComposerReferences("sess-1",accept);
		const hook=vi.spyOn(codeSelection,"useCodeSelection"),sha="a".repeat(40);
		const view=render(<QueryClientProvider client={new QueryClient()}><AoDiffFile annotation={annotation()} detail={{...detail(),previousPath:"old.ts"}} fallback={null} onActiveSelectionChange={vi.fn()} sessionId="sess-1" split={false} scope="committed" source={{kind:"snapshot",label:"PR",commitShas:[sha],view:{prNumber:42,sourceUrl:"https://example.test/repo/pull/42",expectedTipSha:sha}}}/></QueryClientProvider>);
		const args=hook.mock.lastCall!;expect(args[2]).toBe(true);
		args[1]({host:document.createElement("div"),point:{x:0,y:0},dragging:false,range:{start:1,end:1,side:"deletions"},rows:[{kind:"del",side:"old",oldNo:1,newNo:null,text:"old code"}]});
		expect(accept).toHaveBeenCalledWith(expect.objectContaining({commitSha:sha,previousPath:"old.ts",side:"old",source:expect.objectContaining({kind:"pull_request",number:42,snapshot:sha})}));
		view.unmount();hook.mockRestore();remove();
	});
	it("starts line feedback using Pierre's hovered-side contract", () => {
		const model = annotation();
		render(
			<QueryClientProvider client={new QueryClient({ defaultOptions: { queries: { retry: false } } })}>
				<AoDiffFile
					annotation={model}
					detail={detail()}
					fallback={null}
					onActiveSelectionChange={vi.fn()}
					sessionId="sess-1"
					split={false}
				/>
			</QueryClientProvider>,
		);

		expect(screen.getByRole("button", { name: "Add feedback" }).parentElement).toHaveAttribute("data-gutter-enabled", "true");
		expect(screen.getByRole("button", { name: "Add feedback" }).parentElement).toHaveAttribute("data-surface-css", expect.stringContaining("--diffs-bg: var(--ao-file-canvas, var(--color-bg-primary))"));
		screen.getByRole("button", { name: "Add feedback" }).click();
		expect(model.begin).toHaveBeenCalledWith(expect.objectContaining({
			line: 2,
			lineText: "const value = 2;",
			path: "src/App.tsx",
			side: "new",
			surface: "focused",
		}));
	});
});

it("expands historical diff context from the pinned selection instead of the live worktree",async()=>{
 const source={kind:"snapshot" as const,view:{branch:"feature",expectedTipSha:"a".repeat(40),expectedBaseSha:"b".repeat(40)},commitShas:["c".repeat(40)],label:"feature"};
 revisionMock.mockResolvedValue({content:"pinned content",revision:"pinned",binary:false,truncated:false});
 render(<QueryClientProvider client={new QueryClient()}><AoDiffFile annotation={annotation()} detail={detail()} fallback={null} onActiveSelectionChange={vi.fn()} sessionId="sess-1" source={source} split={false}/></QueryClientProvider>);
 await loader.current!({type:"changed"} as never);
 for(const side of ["before","after"]) expect(revisionMock).toHaveBeenCalledWith(expect.objectContaining({side,view:source.view,commitShas:source.commitShas,scope:"committed",workspaceVersion:undefined}));
});
