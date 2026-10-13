import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import type { ReactNode } from "react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import type { SessionArtifact, WorkspaceSession } from "../types/workspace";
import { SessionArtifactsView } from "./SessionArtifactsView";
import { TooltipProvider } from "./ui/tooltip";

const { openPreviewMock } = vi.hoisted(() => ({ openPreviewMock: vi.fn() }));

vi.mock("../hooks/useOpenArtifactPreview", () => ({ useOpenArtifactPreview: () => openPreviewMock }));
vi.mock("./ArtifactFileView", () => ({
	ArtifactFileView: ({ artifactName, feedbackRequestKey, path }: { artifactName: string; feedbackRequestKey?: number; path: string }) => (
		<div data-feedback-key={feedbackRequestKey ?? ""} data-testid="artifact-view">{`${artifactName}:${path}`}</div>
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
	beforeEach(() => openPreviewMock.mockReset());

	it("lists every artifact newest first", () => {
		renderView(<SessionArtifactsView session={session()} />);

		expect(screen.getAllByRole("button").map((button) => button.textContent?.replace(/\d+[a-z]+$|now$/, ""))).toEqual([
			"report.html",
			"notes.md",
			"plan.md",
		]);
	});

	it("shows an empty state when the session has no artifacts", () => {
		renderView(<SessionArtifactsView session={session([])} />);

		expect(screen.getByText("No artifacts in this session yet.")).toBeInTheDocument();
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
		expect(screen.getByTestId("artifact-view")).toHaveTextContent("plan.md:reports/plan.md");

		await userEvent.click(screen.getByRole("button", { name: "Back to artifacts" }));
		expect(screen.queryByTestId("artifact-view")).not.toBeInTheDocument();
		expect(screen.getByRole("button", { name: /notes\.md/ })).toBeInTheDocument();
	});

	it("opens a requested artifact and passes its feedback request through", () => {
		renderView(<SessionArtifactsView request={{ feedback: true, key: 7, path: "notes.md" }} session={session()} />);

		const viewer = screen.getByTestId("artifact-view");
		expect(viewer).toHaveTextContent("notes.md:notes.md");
		expect(viewer).toHaveAttribute("data-feedback-key", "7");
	});
});
