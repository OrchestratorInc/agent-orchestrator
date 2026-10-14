import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { act, render, screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import type { ReactNode } from "react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { aoBridge } from "../lib/bridge";
import type { SessionArtifact, WorkspaceSession } from "../types/workspace";
import { SessionArtifactsView } from "./SessionArtifactsView";
import { TooltipProvider } from "./ui/tooltip";

const { deleteArtifactMock, openPreviewMock } = vi.hoisted(() => ({ deleteArtifactMock: vi.fn(), openPreviewMock: vi.fn() }));

vi.mock("../hooks/useOpenArtifactPreview", () => ({ useOpenArtifactPreview: () => openPreviewMock }));
vi.mock("../hooks/useSessionWorkspaceFiles", () => ({
	deleteSessionArtifactFile: deleteArtifactMock,
	sessionArtifactFileQueryOptions: (sessionId: string, path: string) => ({
		queryKey: ["artifact-file", sessionId, path],
		queryFn: async () => ({ content: `contents of ${path}` }),
	}),
}));
vi.mock("./ArtifactFileView", () => ({
	ArtifactFileView: ({ feedbackRequestKey, path }: { feedbackRequestKey?: number; path: string }) => (
		<div data-feedback-key={feedbackRequestKey ?? ""} data-testid="artifact-view">{`viewer:${path}`}</div>
	),
}));

const artifacts: SessionArtifact[] = [
	{ kind: "markdown", name: "notes.md", path: "notes.md", size: 12, updatedAt: "2026-09-22T00:00:00Z" },
	{ kind: "html", name: "report.html", path: "report.html", previewUrl: "http://artifact.localhost/report.html", size: 20, updatedAt: "2026-09-23T00:00:00Z" },
	{ kind: "file", name: "plan.md", path: "reports/plan.md", size: 20, updatedAt: "2026-09-21T00:00:00Z" },
];

function session(artifactFiles: SessionArtifact[] = artifacts): WorkspaceSession {
	return { id: "sess-1", artifactFiles } as WorkspaceSession;
}

function renderView(ui: ReactNode) {
	const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
	return render(
		<QueryClientProvider client={client}>
			<TooltipProvider>{ui}</TooltipProvider>
		</QueryClientProvider>,
	);
}

describe("SessionArtifactsView", () => {
	beforeEach(() => {
		openPreviewMock.mockReset();
		deleteArtifactMock.mockReset().mockResolvedValue(undefined);
	});

	it("lists every artifact newest first", () => {
		renderView(<SessionArtifactsView session={session()} />);

		expect(within(screen.getByRole("list")).getAllByRole("button").map((button) => button.textContent?.replace(/\d+[a-z]+$|now$/, ""))).toEqual([
			"report.html",
			"notes.md",
			"plan.md",
		]);
	});

	it("shows an empty state when the session has no artifacts", () => {
		renderView(<SessionArtifactsView session={session([])} />);

		expect(screen.getByText("No artifacts in this session yet.")).toBeInTheDocument();
		expect(screen.queryByRole("button", { name: /^Open in / })).not.toBeInTheDocument();
	});

	it("opens the artifact folder in the file manager and the terminal", async () => {
		const openArtifacts = vi.spyOn(aoBridge.editorHandoff, "openArtifacts");
		renderView(<SessionArtifactsView session={session()} />);

		const [fileManager, terminal] = screen.getAllByRole("button", { name: /^Open in / });
		await userEvent.click(fileManager);
		await userEvent.click(terminal);

		expect(openArtifacts).toHaveBeenNthCalledWith(1, { sessionId: "sess-1", targetId: "file-manager" });
		expect(openArtifacts).toHaveBeenNthCalledWith(2, { sessionId: "sess-1", targetId: "terminal" });
		openArtifacts.mockRestore();
	});

	it("shows why the artifact folder could not open", async () => {
		const openArtifacts = vi.spyOn(aoBridge.editorHandoff, "openArtifacts").mockRejectedValue(
			new Error("Error invoking remote method 'editorHandoff:openArtifacts': Error: This session has no artifacts folder yet."),
		);
		renderView(<SessionArtifactsView session={session()} />);

		await userEvent.click(screen.getAllByRole("button", { name: /^Open in / })[0]);

		expect(await screen.findByRole("alert")).toHaveTextContent("This session has no artifacts folder yet.");
		openArtifacts.mockRestore();
	});

	it("clears a failed open after a few seconds", async () => {
		vi.useFakeTimers({ shouldAdvanceTime: true });
		const openArtifacts = vi.spyOn(aoBridge.editorHandoff, "openArtifacts").mockRejectedValue(new Error("Could not open Terminal."));
		try {
			renderView(<SessionArtifactsView request={{ key: 1, path: "notes.md" }} session={session()} />);

			await userEvent.click(screen.getByRole("button", { name: "notes.md" }));
			await userEvent.click(screen.getAllByRole("menuitem")[1]);
			expect(await screen.findByRole("alert")).toHaveTextContent("Could not open Terminal.");

			await act(() => vi.advanceTimersByTimeAsync(5_000));
			expect(screen.queryByRole("alert")).not.toBeInTheDocument();
		} finally {
			openArtifacts.mockRestore();
			vi.useRealTimers();
		}
	});

	it("hides the folder actions for a session on a remote host", () => {
		renderView(<SessionArtifactsView hostId="remote-1" session={session()} />);

		expect(screen.queryByRole("button", { name: /^Open in / })).not.toBeInTheDocument();
	});

	it("opens an html artifact in the Browser panel", async () => {
		renderView(<SessionArtifactsView session={session()} />);

		await userEvent.click(screen.getByRole("button", { name: /report\.html/ }));

		expect(openPreviewMock).toHaveBeenCalledWith("http://artifact.localhost/report.html");
		expect(screen.queryByTestId("artifact-view")).not.toBeInTheDocument();
	});

	it("opens a file artifact in the viewer and returns to the list", async () => {
		renderView(<SessionArtifactsView session={session()} />);

		await userEvent.click(screen.getByRole("button", { name: /plan\.md/ }));
		expect(screen.getByTestId("artifact-view")).toHaveTextContent("viewer:reports/plan.md");

		await userEvent.click(screen.getByRole("button", { name: "Back to artifacts" }));
		expect(screen.queryByTestId("artifact-view")).not.toBeInTheDocument();
		expect(screen.getByRole("button", { name: /notes\.md/ })).toBeInTheDocument();
	});

	it("names the open artifact once, as a menu that reveals it in the file manager and the terminal", async () => {
		const openArtifacts = vi.spyOn(aoBridge.editorHandoff, "openArtifacts");
		renderView(<SessionArtifactsView request={{ key: 1, path: "reports/plan.md" }} session={session()} />);

		expect(screen.getByRole("button", { name: "Back to artifacts" })).toBeInTheDocument();
		expect(screen.getAllByText("plan.md")).toHaveLength(1);
		await userEvent.click(screen.getByRole("button", { name: "plan.md" }));
		const items = screen.getAllByRole("menuitem").map((item) => item.textContent);
		expect(items).toEqual([expect.stringMatching(/^Show in /), expect.stringMatching(/^Show in /), "Copy as Markdown", "Delete artifact"]);
		await userEvent.click(screen.getAllByRole("menuitem")[0]);
		await userEvent.click(screen.getByRole("button", { name: "plan.md" }));
		await userEvent.click(screen.getAllByRole("menuitem")[1]);

		expect(openArtifacts).toHaveBeenNthCalledWith(1, { sessionId: "sess-1", targetId: "file-manager", path: "reports/plan.md" });
		expect(openArtifacts).toHaveBeenNthCalledWith(2, { sessionId: "sess-1", targetId: "terminal", path: "reports/plan.md" });
		openArtifacts.mockRestore();
	});

	it("copies a markdown artifact as Markdown", async () => {
		const writeText = vi.spyOn(aoBridge.clipboard, "writeText").mockResolvedValue(undefined);
		renderView(<SessionArtifactsView request={{ key: 1, path: "notes.md" }} session={session()} />);

		await userEvent.click(screen.getByRole("button", { name: "notes.md" }));
		await userEvent.click(screen.getByRole("menuitem", { name: "Copy as Markdown" }));

		await vi.waitFor(() => expect(writeText).toHaveBeenCalledWith("contents of notes.md"));
		writeText.mockRestore();
	});

	it("copies the contents of any other artifact, and offers only that on a remote host", async () => {
		const writeText = vi.spyOn(aoBridge.clipboard, "writeText").mockResolvedValue(undefined);
		const other: SessionArtifact = { kind: "file", name: "data.json", path: "data.json", size: 2, updatedAt: "2026-09-24T00:00:00Z" };
		renderView(<SessionArtifactsView hostId="remote-1" request={{ key: 1, path: "data.json" }} session={session([other])} />);

		await userEvent.click(screen.getByRole("button", { name: "data.json" }));
		expect(screen.getAllByRole("menuitem").map((item) => item.textContent)).toEqual(["Copy contents", "Delete artifact"]);
		await userEvent.click(screen.getByRole("menuitem", { name: "Copy contents" }));

		await vi.waitFor(() => expect(writeText).toHaveBeenCalledWith("contents of data.json"));
		writeText.mockRestore();
	});

	it("deletes the open artifact after confirming", async () => {
		renderView(<SessionArtifactsView request={{ key: 1, path: "reports/plan.md" }} session={session()} />);

		await userEvent.click(screen.getByRole("button", { name: "plan.md" }));
		await userEvent.click(screen.getByRole("menuitem", { name: "Delete artifact" }));
		const dialog = await screen.findByRole("dialog");
		expect(dialog).toHaveTextContent("This permanently deletes plan.md");
		expect(deleteArtifactMock).not.toHaveBeenCalled();
		await userEvent.click(within(dialog).getByRole("button", { name: "Delete artifact" }));

		await vi.waitFor(() => expect(deleteArtifactMock).toHaveBeenCalledWith({ path: "reports/plan.md", sessionId: "sess-1", hostId: undefined }));
		await vi.waitFor(() => expect(screen.queryByRole("dialog")).not.toBeInTheDocument());
	});

	it("keeps the confirm open with the reason when the delete fails", async () => {
		deleteArtifactMock.mockRejectedValue(new Error("artifact file not found"));
		renderView(<SessionArtifactsView request={{ key: 1, path: "notes.md" }} session={session()} />);

		await userEvent.click(screen.getByRole("button", { name: "notes.md" }));
		await userEvent.click(screen.getByRole("menuitem", { name: "Delete artifact" }));
		const dialog = await screen.findByRole("dialog");
		await userEvent.click(within(dialog).getByRole("button", { name: "Delete artifact" }));

		expect(await within(dialog).findByText("artifact file not found")).toBeInTheDocument();
	});

	it("does not offer delete for a cloud session", async () => {
		renderView(<SessionArtifactsView request={{ key: 1, path: "notes.md" }} session={{ ...session(), cloud: { orgId: "org-1" } } as WorkspaceSession} />);

		await userEvent.click(screen.getByRole("button", { name: "notes.md" }));
		expect(screen.getAllByRole("menuitem").map((item) => item.textContent)).toEqual(["Copy as Markdown"]);
	});

	it("opens a requested artifact and passes its feedback request through", () => {
		renderView(<SessionArtifactsView request={{ feedback: true, key: 7, path: "notes.md" }} session={session()} />);

		const viewer = screen.getByTestId("artifact-view");
		expect(viewer).toHaveTextContent("viewer:notes.md");
		expect(viewer).toHaveAttribute("data-feedback-key", "7");
	});
});
