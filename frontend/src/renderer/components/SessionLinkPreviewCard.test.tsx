import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { render, screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { appI18n } from "../i18n";
import type { SessionLinkSource } from "../lib/use-session-link-navigation";
import type { PullRequestFacts, SessionArtifact, WorkspaceSession, WorkspaceSummary } from "../types/workspace";
import { ArtifactLinkPreviewCard, SessionLinkPreviewCard, artifactExcerpt, markdownPreviewSource } from "./SessionLinkPreviewCard";

const mocks = vi.hoisted(() => ({ restore: vi.fn(), source: vi.fn(), artifactFile: vi.fn() }));

vi.mock("../hooks/useSessionWorkspaceFiles", () => ({
	sessionArtifactFileQueryOptions: (sessionId: string, path: string, rawUrl: string) => ({
		queryKey: ["session-artifact-file", rawUrl, sessionId, path],
		queryFn: () => mocks.artifactFile(path),
	}),
}));

vi.mock("../hooks/useRestoreSession", () => ({
	useRestoreSession: () => mocks.restore,
}));

vi.mock("../lib/use-session-link-navigation", () => ({
	useSessionLinkSource: (...args: unknown[]) => mocks.source(...args),
}));

vi.mock("./ui/hover-card", () => ({
	HoverCardContent: ({ children, className }: { children: React.ReactNode; className?: string }) => (
		<div className={className}>{children}</div>
	),
}));

function pr(
	number: number,
	overrides: Partial<PullRequestFacts> = {},
): PullRequestFacts {
	return {
		url: `https://github.com/aoagents/agent-orchestrator/pull/${number}`,
		number,
		state: "open",
		ci: "passing",
		review: "none",
		mergeability: "unknown",
		reviewComments: false,
		updatedAt: "2026-10-08T10:00:00Z",
		...overrides,
	};
}

function session(overrides: Partial<WorkspaceSession> = {}): WorkspaceSession {
	return {
		id: "worker-367",
		workspaceId: "project-a",
		workspaceName: "Agent Orchestrator",
		title: "Implement worker link previews",
		provider: "codex",
		kind: "worker",
		status: "working",
		displayStatus: "Implementing",
		updatedAt: "2026-10-08T10:00:00Z",
		prs: [],
		...overrides,
	};
}

function workspace(worker: WorkspaceSession): WorkspaceSummary {
	return { id: worker.workspaceId, name: worker.workspaceName, path: "/repo", sessions: [worker] };
}

function source(overrides: Partial<SessionLinkSource> = {}): SessionLinkSource {
	return { ready: true, isLoading: false, isError: false, workspaces: [], ...overrides };
}

describe("SessionLinkPreviewCard", () => {
	beforeEach(() => {
		mocks.restore.mockReset();
		mocks.restore.mockResolvedValue({ status: "success" });
		mocks.source.mockReset();
		mocks.source.mockReturnValue(source());
	});

	it("resolves the card from the chat source host", () => {
		const remote = session({ title: "Remote worker" });
		mocks.source.mockReturnValue(source({ workspaces: [workspace(remote)] }));

		render(<SessionLinkPreviewCard href="ao://sessions/project-a/worker-367" sourceHostId="box-a" />);

		expect(mocks.source).toHaveBeenCalledWith("box-a", undefined);
		expect(screen.getByText("Remote worker")).toBeInTheDocument();
	});

	it("keeps stale session data visible when the source refresh fails", () => {
		const stale = session({ title: "Cached worker" });
		mocks.source.mockReturnValue(source({ isError: true, ready: false, workspaces: [workspace(stale)] }));

		render(<SessionLinkPreviewCard href="ao://sessions/project-a/worker-367" />);

		expect(screen.getByText("Cached worker")).toBeInTheDocument();
		expect(screen.queryByRole("alert")).not.toBeInTheDocument();
	});

	it("renders distinct loading, failed, and unavailable states", () => {
		mocks.source.mockReturnValue(source({ isLoading: true, ready: false }));
		const view = render(<SessionLinkPreviewCard href="ao://sessions/project-a/worker-367" />);
		expect(screen.getByRole("status", { name: "Checking…" })).toBeInTheDocument();

		mocks.source.mockReturnValue(source({ isError: true, ready: false }));
		view.rerender(<SessionLinkPreviewCard href="ao://sessions/project-a/worker-367" />);
		expect(screen.getByRole("alert")).toHaveTextContent("Could not load sessions");

		mocks.source.mockReturnValue(source());
		view.rerender(<SessionLinkPreviewCard href="ao://sessions/project-a/worker-367" />);
		expect(screen.getByRole("status")).toHaveTextContent("Unable to verify");
		expect(screen.queryByText(/sidebar/i)).not.toBeInTheDocument();
	});

	it("shows open for pending readiness and blocked only for real blockers", () => {
		const worker = session({
			prs: [
				pr(1, { ci: "pending" }),
				pr(2, { review: "review_required" }),
				pr(3, { mergeability: "mergeable" }),
				pr(4, { state: "merged", mergeability: "mergeable" }),
			],
		});
		mocks.source.mockReturnValue(source({ workspaces: [workspace(worker)] }));

		const { container } = render(<SessionLinkPreviewCard href="ao://sessions/project-a/worker-367" />);

		expect(screen.getByText("4 PRs")).toBeInTheDocument();
		expect(screen.getByText("PR #1").parentElement).toHaveTextContent(/open/i);
		expect(screen.getByText("PR #2").parentElement).toHaveTextContent("Blocked");
		expect(screen.getByText("PR #3").parentElement).toHaveTextContent("Ready to merge");
		expect(screen.getByText("PR #4").parentElement).toHaveTextContent(/merged/i);
		expect(container.innerHTML).not.toContain("text-[");
	});

	it("replaces terminated session details with a remote aware resume action", async () => {
		const terminated = session({ isTerminated: true, status: "terminated", prs: [pr(1)] });
		mocks.source.mockReturnValue(source({ workspaces: [workspace(terminated)] }));

		render(<SessionLinkPreviewCard href="ao://sessions/project-a/worker-367" sourceHostId="box-a" />);

		expect(screen.getByRole("status", { name: "Agent terminated" })).toBeInTheDocument();
		expect(screen.queryByText("Implement worker link previews")).not.toBeInTheDocument();
		expect(screen.queryByText("1 PR")).not.toBeInTheDocument();
		await userEvent.click(screen.getByRole("button", { name: "Resume agent" }));
		expect(mocks.restore).toHaveBeenCalledWith("worker-367", "box-a");
	});

	it("shows restore failures inside the terminated card", async () => {
		const terminated = session({ isTerminated: true, status: "terminated" });
		mocks.source.mockReturnValue(source({ workspaces: [workspace(terminated)] }));
		mocks.restore.mockResolvedValue({ status: "not_resumable", message: "No saved session is available." });

		render(<SessionLinkPreviewCard href="ao://sessions/project-a/worker-367" />);
		await userEvent.click(screen.getByRole("button", { name: "Resume agent" }));

		expect(await screen.findByRole("alert")).toHaveTextContent("No saved session is available.");
	});

	it("localizes the PR count instead of adding an English suffix", () => {
		expect(appI18n.t("pr.count", { count: 1, lng: "es" })).toBe("1 PR");
		expect(appI18n.t("pr.count", { count: 2, lng: "es" })).toBe("2 PR");
	});

	it("formats the updated-at time in the app language, not the OS locale", async () => {
		const worker = session({ updatedAt: new Date(Date.now() - 42 * 60_000).toISOString() });
		mocks.source.mockReturnValue(source({ workspaces: [workspace(worker)] }));
		await appI18n.changeLanguage("zh-CN");
		try {
			render(<SessionLinkPreviewCard href="ao://sessions/project-a/worker-367" />);
			expect(screen.getByText("更新于 42 分钟前")).toBeInTheDocument();
		} finally {
			await appI18n.changeLanguage("en");
		}
	});
});

function artifact(overrides: Partial<SessionArtifact> = {}): SessionArtifact {
	return {
		kind: "markdown",
		name: "plan.md",
		path: "plans/plan.md",
		rawUrl: "http://127.0.0.1/raw/plans/plan.md",
		size: 42,
		updatedAt: "2026-10-08T10:00:00Z",
		...overrides,
	};
}

function renderWithQuery(ui: React.ReactElement) {
	const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
	return render(<QueryClientProvider client={client}>{ui}</QueryClientProvider>);
}

describe("artifactExcerpt", () => {
	it("reads an html page's visible text without scripts or styles", () => {
		const html = "<html><head><title>Q3 plan</title><style>p{}</style></head><body><script>alert(1)</script><p>Ship   links</p></body></html>";
		expect(artifactExcerpt("html", html)).toBe("Q3 plan\nShip links");
	});

	it("drops a markdown file's frontmatter block", () => {
		expect(artifactExcerpt("markdown", "---\nname: Plan\ndescription: Q3\n---\n# Plan\nBody")).toBe("# Plan\nBody");
	});

	it("cuts a long markdown body at a line break", () => {
		const body = `${"a".repeat(1000)}\n${"b".repeat(1000)}`;
		expect(markdownPreviewSource(`---\nname: x\n---\n${body}`)).toBe("a".repeat(1000));
	});

	it("truncates long text", () => {
		const excerpt = artifactExcerpt("markdown", "a".repeat(1000));
		expect(excerpt.length).toBe(481);
		expect(excerpt.endsWith("…")).toBe(true);
	});
});

describe("ArtifactLinkPreviewCard", () => {
	beforeEach(() => {
		mocks.source.mockReset();
		mocks.artifactFile.mockReset();
	});

	it("previews the linked artifact's leading content", async () => {
		const worker = session({ artifactFiles: [artifact()] });
		mocks.source.mockReturnValue(source({ workspaces: [workspace(worker)] }));
		mocks.artifactFile.mockResolvedValue({ binary: false, content: "# Plan\nShip artifact links." });

		renderWithQuery(<ArtifactLinkPreviewCard href="ao://sessions/project-a/worker-367/artifacts/plans/plan.md" sourceHostId="box-a" />);

		expect(mocks.source).toHaveBeenCalledWith("box-a", undefined);
		expect(screen.getByText("plan.md")).toBeInTheDocument();
		expect(screen.getByText("Implement worker link previews")).toHaveTextContent("Implement worker link previews · worker-367");
		expect(await screen.findByText(/Ship artifact links\./)).toBeInTheDocument();
		expect(mocks.artifactFile).toHaveBeenCalledWith("plans/plan.md");
	});

	it("shows a markdown artifact's frontmatter name and description above its body", async () => {
		const worker = session({ artifactFiles: [artifact()] });
		mocks.source.mockReturnValue(source({ workspaces: [workspace(worker)] }));
		mocks.artifactFile.mockResolvedValue({ binary: false, content: "---\nname: Q3 plan\ndescription: \"What ships next quarter\"\ntags: [a]\n---\n# Plan\nShip artifact links." });

		renderWithQuery(<ArtifactLinkPreviewCard href="ao://sessions/project-a/worker-367/artifacts/plans/plan.md" />);

		expect(await screen.findByText("Q3 plan")).toBeInTheDocument();
		expect(screen.getByText("What ships next quarter")).toBeInTheDocument();
		expect(screen.getByText(/Ship artifact links\./)).toBeInTheDocument();
		expect(screen.queryByText(/description:/)).not.toBeInTheDocument();
	});

	it("renders a markdown artifact's body as formatted markdown with inert links", async () => {
		const worker = session({ artifactFiles: [artifact()] });
		mocks.source.mockReturnValue(source({ workspaces: [workspace(worker)] }));
		mocks.artifactFile.mockResolvedValue({ binary: false, content: "# Plan\n\nShip **artifact links** and [docs](https://example.com).\n\n- one\n- two\n" });

		renderWithQuery(<ArtifactLinkPreviewCard href="ao://sessions/project-a/worker-367/artifacts/plans/plan.md" />);

		expect(await screen.findByText("Plan")).toBeInTheDocument();
		expect(screen.queryByText(/^# Plan/)).not.toBeInTheDocument();
		expect(screen.getByText("artifact links").tagName).toBe("STRONG");
		expect(screen.getByText("docs")).not.toHaveAttribute("href");
		expect(screen.queryByRole("link")).not.toBeInTheDocument();
		expect(screen.getAllByRole("listitem").map((item) => item.textContent)).toEqual(["one", "two"]);
	});

	it("previews a CSV artifact as a table of its leading rows", async () => {
		const worker = session({ artifactFiles: [artifact({ kind: "file", name: "sizes.csv", path: "sizes.csv" })] });
		mocks.source.mockReturnValue(source({ workspaces: [workspace(worker)] }));
		mocks.artifactFile.mockResolvedValue({ binary: false, content: "package,size\n\"backend, go\",42\nfrontend,17\n" });

		renderWithQuery(<ArtifactLinkPreviewCard href="ao://sessions/project-a/worker-367/artifacts/sizes.csv" />);

		const table = await screen.findByRole("table");
		expect(within(table).getAllByRole("columnheader").map((cell) => cell.textContent)).toEqual(["package", "size"]);
		expect(within(table).getByRole("cell", { name: "backend, go" })).toBeInTheDocument();
		expect(within(table).getByRole("cell", { name: "17" })).toBeInTheDocument();
	});

	it("previews an HTML artifact as a sandboxed render of its inline page without fetching its source", () => {
		const page = artifact({ kind: "html", name: "chart.html", path: "chart.html", inlineUrl: "http://ao-inline-artifact.abc123.localhost:8080/chart.html" });
		mocks.source.mockReturnValue(source({ workspaces: [workspace(session({ artifactFiles: [page] }))] }));

		const { container } = renderWithQuery(<ArtifactLinkPreviewCard href="ao://sessions/project-a/worker-367/artifacts/chart.html" />);

		const frame = container.querySelector("iframe");
		expect(frame).toHaveAttribute("src", "http://ao-inline-artifact.abc123.localhost:8080/chart.html");
		expect(frame).toHaveAttribute("sandbox", "allow-scripts allow-same-origin");
		expect(frame).toHaveAttribute("tabindex", "-1");
		expect(mocks.artifactFile).not.toHaveBeenCalled();
	});

	it.each([
		["a URL outside the inline artifact origin", "http://127.0.0.1:8080/chart.html", undefined],
		["the app's own origin", "http://localhost:5173/chart.html", undefined],
		["a remote host's proxy URL", "http://ao-inline-artifact.abc123.localhost:8080/chart.html", "box-a"],
	])("never frames %s", async (_name, inlineUrl, hostId) => {
		const page = artifact({ kind: "html", name: "chart.html", path: "chart.html", inlineUrl });
		mocks.source.mockReturnValue(source({ workspaces: [workspace(session({ artifactFiles: [page] }))] }));
		mocks.artifactFile.mockResolvedValue({ binary: false, content: "<html><body><p>Fallback text</p></body></html>" });

		const { container } = renderWithQuery(<ArtifactLinkPreviewCard href="ao://sessions/project-a/worker-367/artifacts/chart.html" sourceHostId={hostId} />);

		expect(await screen.findByText("Fallback text")).toBeInTheDocument();
		expect(container.querySelector("iframe")).toBeNull();
	});

	it("never frames a Cloud session's page", async () => {
		const page = artifact({ kind: "html", name: "chart.html", path: "chart.html", inlineUrl: "http://ao-inline-artifact.abc123.localhost:8080/chart.html" });
		mocks.source.mockReturnValue(source({ workspaces: [workspace(session({ artifactFiles: [page] }))] }));
		mocks.artifactFile.mockResolvedValue({ binary: false, content: "<html><body><p>Cloud text</p></body></html>" });

		const { container } = renderWithQuery(<ArtifactLinkPreviewCard href="ao://sessions/project-a/worker-367/artifacts/chart.html" sourceKind="cloud" />);

		expect(await screen.findByText("Cloud text")).toBeInTheDocument();
		expect(container.querySelector("iframe")).toBeNull();
	});

	it("falls back to an HTML artifact's text when it has no inline page", async () => {
		const page = artifact({ kind: "html", name: "chart.html", path: "chart.html" });
		mocks.source.mockReturnValue(source({ workspaces: [workspace(session({ artifactFiles: [page] }))] }));
		mocks.artifactFile.mockResolvedValue({ binary: false, content: "<html><body><p>Remote chart</p></body></html>" });

		const { container } = renderWithQuery(<ArtifactLinkPreviewCard href="ao://sessions/project-a/worker-367/artifacts/chart.html" />);

		expect(await screen.findByText("Remote chart")).toBeInTheDocument();
		expect(container.querySelector("iframe")).toBeNull();
	});

	it("names an orchestrator's artifacts as the orchestrator's, with the session id once", () => {
		const orchestrator = session({ id: "skills-1", title: "skills-1", kind: "orchestrator", artifactFiles: [artifact()] });
		mocks.source.mockReturnValue(source({ workspaces: [workspace(orchestrator)] }));
		mocks.artifactFile.mockResolvedValue({ binary: false, content: "Plan" });

		renderWithQuery(<ArtifactLinkPreviewCard href="ao://sessions/project-a/skills-1/artifacts/plans/plan.md" />);

		expect(screen.getByText(/^Orchestrator/)).toHaveTextContent(/^Orchestrator · skills-1$/);
		expect(screen.queryByText(/#s-1/)).not.toBeInTheDocument();
	});

	it("shows a worker without a title by its session id alone", () => {
		const worker = session({ id: "skills-2", title: "skills-2", artifactFiles: [artifact()] });
		mocks.source.mockReturnValue(source({ workspaces: [workspace(worker)] }));
		mocks.artifactFile.mockResolvedValue({ binary: false, content: "Plan" });

		renderWithQuery(<ArtifactLinkPreviewCard href="ao://sessions/project-a/skills-2/artifacts/plans/plan.md" />);

		expect(screen.getByText("skills-2")).toHaveTextContent(/^skills-2$/);
	});

	it("reports an artifact the session no longer lists", () => {
		mocks.source.mockReturnValue(source({ workspaces: [workspace(session({ artifactFiles: [] }))] }));

		renderWithQuery(<ArtifactLinkPreviewCard href="ao://sessions/project-a/worker-367/artifacts/gone.md" />);

		expect(screen.getByRole("status")).toHaveTextContent("Artifact unavailable");
		expect(mocks.artifactFile).not.toHaveBeenCalled();
	});
});
