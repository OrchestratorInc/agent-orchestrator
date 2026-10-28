import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { useUiStore } from "../stores/ui-store";
import { useArtifactsViewStore } from "../stores/artifacts-view-store";
import { CLOUD_PROJECT_KIND, STANDALONE_WORKSPACE_ID, type WorkspaceSession, type WorkspaceSummary } from "../types/workspace";
import { ArtifactsView } from "./ArtifactsView";
import { TooltipProvider } from "./ui/tooltip";

const mocks = vi.hoisted(() => ({
	workspaces: [] as WorkspaceSummary[],
	agents: [] as Array<Record<string, unknown>>,
	navigate: vi.fn(),
	post: vi.fn(),
	get: vi.fn(),
	capture: vi.fn(),
	deleteCloudArtifact: vi.fn(),
	hideShellTopbar: true,
}));

vi.mock("./PdfPreview", () => ({
	PdfPreview: ({ firstPageOnly, path, src }: { firstPageOnly?: boolean; path: string; src: string }) => <div data-testid="pdf-thumbnail" data-first-page-only={firstPageOnly} data-src={src}>{path}</div>,
}));
vi.mock("./chat/MermaidBlock", () => ({
	MermaidBlock: ({ code }: { code: string }) => <div data-testid="mermaid-thumbnail">{code}</div>,
}));
vi.mock("@tanstack/react-router", () => ({ useNavigate: () => mocks.navigate }));
vi.mock("../hooks/useWorkspaceQuery", () => ({
	workspaceQueryKey: ["workspaces"],
	cloudSessionsQueryKey: ["cloud-sessions"],
	workspaceQueryKeyForHost: () => ["workspaces"],
	useWorkspaceQuery: () => ({ data: mocks.workspaces, isLoading: false, error: null }),
}));
vi.mock("../hooks/useCloudCp", () => ({
	useCloudCp: () => ({ client: { deleteSessionArtifact: mocks.deleteCloudArtifact } }),
}));
vi.mock("../hooks/useCloudArtifacts", async (importOriginal) => ({
	...await importOriginal<typeof import("../hooks/useCloudArtifacts")>(),
	useCloudArtifactAccess: (session: Pick<WorkspaceSession, "cloud">) => ({ data: session.cloud ? "https://cp.test/artifact-files/token/" : undefined }),
}));
vi.mock("./CloudArtifactFrame", () => ({
	CloudArtifactFrame: ({ title, url }: { title: string; url: string }) => <iframe data-testid="cloud-artifact-preview" title={title} src={url} />,
}));
vi.mock("./ArtifactFileView", () => ({
	ArtifactFileView: ({ cloud, path, rawUrl, readOnly }: { cloud?: boolean; path: string; rawUrl?: string; readOnly?: boolean }) => <div data-testid="artifact-file-preview" data-cloud={cloud} data-path={path} data-raw-url={rawUrl} data-read-only={readOnly} />,
}));
vi.mock("../hooks/useAgentReadinessQuery", () => ({ useAgentReadinessQuery: () => ({ data: { agents: mocks.agents } }) }));
vi.mock("../lib/api-client", async (importOriginal) => ({
	...await importOriginal<typeof import("../lib/api-client")>(),
	apiClient: { POST: mocks.post, GET: mocks.get },
}));
vi.mock("../lib/telemetry", async (importOriginal) => ({
	...await importOriginal<typeof import("../lib/telemetry")>(),
	captureRendererEvent: mocks.capture,
}));
vi.mock("../lib/platform", async (importOriginal) => ({
	...await importOriginal<typeof import("../lib/platform")>(),
	hidesShellTopbar: () => mocks.hideShellTopbar,
}));

const codexReady = {
	id: "codex",
	label: "Codex",
	installation: { state: "installed", freshness: "fresh" },
	authentication: { state: "authorized", freshness: "fresh" },
	effectiveReadiness: "ready",
	usageCount: 0,
	lastUsedAt: null,
};

function session(id: string, title: string, files: Array<{ name: string; updatedAt: string; kind?: "html" | "markdown" | "file" }>): WorkspaceSession {
	return {
		id,
		title,
		workspaceId: "ws",
		workspaceName: "ws",
		provider: "claude-code",
		kind: "worker",
		status: "idle",
		updatedAt: "2026-10-01T00:00:00Z",
		prs: [],
		outputType: files.length > 0 ? "artifact" : "none",
		artifactFiles: files.map((file) => ({
			kind: file.kind ?? "markdown",
			name: file.name,
			path: file.name,
			size: 1,
			updatedAt: file.updatedAt,
			...(file.kind === "html" ? { previewUrl: `http://preview/${file.name}` } : {}),
		})),
	} as WorkspaceSession;
}

const withArtifacts: WorkspaceSummary[] = [
	{
		id: "ao",
		name: "agent-orchestrator",
		path: "/code/ao",
		sessions: [
			session("ao-1", "Design the Artifacts entry point", [{ name: "artifacts-user-flow.html", updatedAt: "2026-10-11T10:00:00Z", kind: "html" }]),
			session("ao-2", "Review pricing", [{ name: "pricing.md", updatedAt: "2026-10-10T10:00:00Z" }]),
		],
	},
	{
		id: STANDALONE_WORKSPACE_ID,
		name: "Standalone",
		path: "",
		sessions: [session("standalone-1", "Compare caching options", [{ name: "caching-options.md", updatedAt: "2026-10-08T10:00:00Z" }])],
	},
];

const withoutArtifacts: WorkspaceSummary[] = [
	{ id: "ao", name: "agent-orchestrator", path: "/code/ao", sessions: [] },
	{ id: "px0", name: "px0", path: "/code/px0", sessions: [] },
	{ id: "cloud", name: "cloud-project", path: "", kind: CLOUD_PROJECT_KIND, sessions: [] },
	{ id: STANDALONE_WORKSPACE_ID, name: "Standalone", path: "", sessions: [] },
];

function renderView() {
	const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
	return render(
		<QueryClientProvider client={client}>
			<TooltipProvider>
				<ArtifactsView />
			</TooltipProvider>
		</QueryClientProvider>,
	);
}

describe("ArtifactsView", () => {
	afterEach(() => vi.unstubAllGlobals());
	beforeEach(() => {
		window.localStorage.removeItem("ao.artifacts.view");
		useArtifactsViewStore.setState({ view: "gallery", collapsedSections: [], pinnedArtifactIds: [] });
		vi.stubGlobal("IntersectionObserver", class {
			constructor(private callback: (entries: { isIntersecting: boolean }[]) => void) {}
			observe() { this.callback([{ isIntersecting: true }]); }
			disconnect() {}
		});
		vi.stubGlobal("ResizeObserver", class {
			observe() {}
			unobserve() {}
			disconnect() {}
		});
		mocks.workspaces = withArtifacts;
		mocks.agents = [codexReady];
		mocks.hideShellTopbar = true;
		mocks.navigate.mockReset();
		mocks.post.mockReset();
		mocks.get.mockReset();
		mocks.capture.mockReset();
		mocks.deleteCloudArtifact.mockReset().mockResolvedValue(undefined);
		useUiStore.setState({ settingsModal: null });
		useUiStore.getState().clearGlobalToast();
	});

	it("shows recent artifacts above project galleries with session and project details", () => {
		renderView();

		expect(screen.getByRole("heading", { level: 1, name: "Artifacts" })).not.toHaveClass("sr-only");
		expect(screen.queryByRole("region", { name: "Pinned" })).not.toBeInTheDocument();
		expect(screen.getAllByRole("region").map((section) => section.getAttribute("aria-label"))).toEqual(["Recent", "agent-orchestrator", "Scratchpad"]);
		const gallery = within(screen.getByRole("region", { name: "Recent" }));
		expect(gallery.getAllByRole("listitem").map((row) => row.textContent)).toEqual([
			expect.stringContaining("artifacts-user-flow.html"),
			expect.stringContaining("pricing.md"),
			expect.stringContaining("caching-options.md"),
		]);
		expect(within(screen.getByRole("region", { name: "agent-orchestrator" })).getByText("Design the Artifacts entry point")).toBeInTheDocument();
		expect(gallery.getByText("agent-orchestrator · Design the Artifacts entry point")).toBeInTheDocument();
		expect(gallery.getByText("Scratchpad · Compare caching options")).toBeInTheDocument();
		expect(screen.queryByRole("button", { name: /team/i })).not.toBeInTheDocument();
	});

	it("filters project sections and recents together, then clears both filters", async () => {
		const user = userEvent.setup();
		renderView();
		await user.click(screen.getByRole("button", { name: "Filter by project" }));
		await user.click(screen.getByRole("menuitemradio", { name: "Scratchpad" }));
		expect(screen.queryByRole("region", { name: "agent-orchestrator" })).not.toBeInTheDocument();
		expect(screen.queryByRole("region", { name: "Recent" })).not.toBeInTheDocument();
		expect(screen.getAllByRole("listitem")).toHaveLength(1);
		expect(screen.getByRole("heading", { level: 2, name: "Scratchpad" })).toBeInTheDocument();
		expect(screen.queryByRole("button", { name: "Scratchpad" })).not.toBeInTheDocument();
		await user.type(screen.getByRole("searchbox", { name: "Search artifacts" }), "no such thing");
		await user.click(screen.getByRole("button", { name: "Clear filters" }));
		expect(screen.getByRole("searchbox", { name: "Search artifacts" })).toHaveValue("");
		expect(screen.getByRole("region", { name: "agent-orchestrator" })).toBeInTheDocument();
		expect(screen.getByRole("button", { name: "Filter by project" })).toHaveTextContent("All projects");
	});

	it("searches by file name and session title, then clears filters", async () => {
		const user = userEvent.setup();
		renderView();

		await user.type(screen.getByRole("searchbox", { name: "Search artifacts" }), "pricing");
		expect(screen.getAllByText("pricing.md")).toHaveLength(1);
		expect(screen.queryByText("artifacts-user-flow.html")).not.toBeInTheDocument();
		expect(screen.queryByText("Scratchpad")).not.toBeInTheDocument();

		await user.clear(screen.getByRole("searchbox", { name: "Search artifacts" }));
		await user.type(screen.getByRole("searchbox", { name: "Search artifacts" }), "no such thing");
		expect(screen.getByText("No matches")).toBeInTheDocument();

		await user.click(screen.getByRole("button", { name: "Clear filters" }));
		expect(screen.getByRole("searchbox", { name: "Search artifacts" })).toHaveValue("");
		expect(screen.getAllByRole("listitem")).toHaveLength(6);
	});

	it("searches project names and the translated Scratchpad label", async () => {
		const user = userEvent.setup();
		renderView();

		const search = screen.getByRole("searchbox", { name: "Search artifacts" });
		await user.type(search, "agent-orchestrator");
		expect(screen.getByRole("region", { name: "agent-orchestrator" })).toBeInTheDocument();
		expect(screen.queryByRole("region", { name: "Scratchpad" })).not.toBeInTheDocument();
		await user.clear(search);
		await user.type(search, "Scratchpad");

		expect(screen.getAllByRole("listitem")).toHaveLength(1);
		expect(screen.getAllByText("caching-options.md")).toHaveLength(1);
		expect(screen.queryByText("pricing.md")).not.toBeInTheDocument();
	});

	it("shows a single project once with a static heading in either view", async () => {
		const user = userEvent.setup();
		mocks.workspaces = [withArtifacts[0]];
		useArtifactsViewStore.setState({ collapsedSections: ["project:ao"] });
		renderView();
		expect(screen.queryByRole("region", { name: "Recent" })).not.toBeInTheDocument();
		expect(screen.getByRole("heading", { level: 2, name: "agent-orchestrator" })).toBeInTheDocument();
		expect(screen.queryByRole("button", { name: "agent-orchestrator" })).not.toBeInTheDocument();
		expect(screen.getAllByRole("listitem")).toHaveLength(2);
		expect(screen.getByText("agent-orchestrator · Review pricing")).toBeInTheDocument();
		await user.click(screen.getByRole("button", { name: "List view" }));
		expect(screen.getAllByRole("listitem")).toHaveLength(2);
		expect(screen.getByText("agent-orchestrator · Review pricing")).toBeInTheDocument();
		expect(screen.getByRole("heading", { level: 2, name: "agent-orchestrator" })).toBeInTheDocument();
		expect(screen.queryByRole("button", { name: "agent-orchestrator" })).not.toBeInTheDocument();
	});

	it("keeps gallery cloud actions on the cloud session instead of local file actions", async () => {
		const cloud = session("cloud-1", "Cloud notes", [{ name: "notes.md", updatedAt: "2026-10-11T10:00:00Z" }]);
		cloud.cloud = { orgId: "org-1" };
		mocks.workspaces = [{ id: "cloud-project", name: "Cloud project", path: "", kind: CLOUD_PROJECT_KIND, cloudOrgId: "org-1", sessions: [cloud] }];
		renderView();
		await userEvent.click(screen.getByRole("button", { name: "More actions: notes.md" }));
		expect(screen.queryByRole("menuitem", { name: /^Show in / })).not.toBeInTheDocument();
		expect(screen.getByRole("menuitem", { name: "Download" })).toBeInTheDocument();
		await userEvent.click(screen.getByRole("menuitem", { name: "Delete artifact" }));
		const dialog = screen.getByRole("dialog");
		expect(dialog).toHaveTextContent("from the session's cloud storage and from its sandbox");
		await userEvent.click(within(dialog).getByRole("button", { name: "Delete artifact" }));
		await waitFor(() => expect(mocks.deleteCloudArtifact).toHaveBeenCalledWith("org-1", "cloud-1", "notes.md"));
	});


	it.each(["html", "markdown"] as const)("previews a cloud %s artifact using cloud access", async (kind) => {
		const name = kind === "html" ? "report.html" : "notes.md";
		const cloud = session("cloud-1", "Cloud notes", [{ name, kind, updatedAt: "2026-10-11T10:00:00Z" }]);
		cloud.cloud = { orgId: "org-1" };
		mocks.workspaces = [{ id: "cloud-project", name: "Cloud project", path: "", kind: CLOUD_PROJECT_KIND, cloudOrgId: "org-1", sessions: [cloud] }];
		renderView();
		await userEvent.click(screen.getByRole("button", { name: new RegExp(`^${name}`) }));
		const dialog = screen.getByRole("dialog", { name });
		if (kind === "html") {
			expect(within(dialog).getByTestId("cloud-artifact-preview")).toHaveAttribute("src", `https://cp.test/artifact-files/token/${name}?v=2026-10-11T10%3A00%3A00Z`);
		} else {
			const preview = within(dialog).getByTestId("artifact-file-preview");
			expect(preview).toHaveAttribute("data-cloud", "true");
			expect(preview).toHaveAttribute("data-read-only", "true");
			expect(preview).toHaveAttribute("data-raw-url", `https://cp.test/artifact-files/token/${name}?v=2026-10-11T10%3A00%3A00Z`);
		}
	});

	it("pins and unpins an artifact across sections and remembers the pin on return", async () => {
		const user = userEvent.setup();
		const { unmount } = renderView();
		const project = within(screen.getByRole("region", { name: "agent-orchestrator" }));
		await user.click(project.getByRole("button", { name: "Pin artifact: pricing.md" }));
		expect(within(screen.getByRole("region", { name: "Pinned" })).getByText("pricing.md")).toBeInTheDocument();
		expect(project.getByRole("button", { name: "Unpin artifact: pricing.md" })).toHaveAttribute("aria-pressed", "true");
		expect(JSON.parse(window.localStorage.getItem("ao.artifacts.view") ?? "{}").pinnedArtifactIds).toHaveLength(1);
		unmount();
		renderView();
		await user.click(within(screen.getByRole("region", { name: "Pinned" })).getByRole("button", { name: "Unpin artifact: pricing.md" }));
		expect(screen.queryByRole("region", { name: "Pinned" })).not.toBeInTheDocument();
		expect(within(screen.getByRole("region", { name: "agent-orchestrator" })).getByText("pricing.md")).toBeInTheDocument();
	});

	it("remembers list view and collapsed project sections when returning", async () => {
		const user = userEvent.setup();
		const { unmount } = renderView();
		await user.click(screen.getByRole("button", { name: "List view" }));
		await user.click(within(screen.getByRole("region", { name: "agent-orchestrator" })).getByRole("button", { name: "agent-orchestrator" }));
		unmount();
		renderView();
		expect(screen.getByRole("button", { name: "List view" })).toHaveAttribute("aria-pressed", "true");
		expect(within(screen.getByRole("region", { name: "agent-orchestrator" })).getByRole("button", { name: "agent-orchestrator" })).toHaveAttribute("aria-expanded", "false");
		expect(JSON.parse(window.localStorage.getItem("ao.artifacts.view") ?? "{}")).toMatchObject({ view: "list", collapsedSections: ["project:ao"] });
	});

	it("shows a placeholder for text artifacts without a usable URL", () => {
		renderView();
		const project = within(screen.getByRole("region", { name: "agent-orchestrator" }));
		expect(within(project.getByRole("button", { name: /^pricing\.md/ }).closest("li")!).getByTitle("No preview available")).toBeInTheDocument();
	});

	it("checks HTML availability before framing only its dedicated inline origin", async () => {
		const html = session("ao-html", "Design review", [{ name: "page.html", kind: "html", updatedAt: "2026-10-11T10:00:00Z" }]);
		html.artifactFiles![0].inlineUrl = "http://ao-inline-artifact.ao-html.localhost:3002/page.html";
		html.artifactFiles![0].rawUrl = "http://127.0.0.1:3002/page.html?raw=true";
		let respond!: (response: Response) => void;
		const fetch = vi.fn(() => new Promise<Response>((resolve) => { respond = resolve; }));
		vi.stubGlobal("fetch", fetch);
		mocks.workspaces = [{ id: "ao", name: "AO", path: "/code/ao", sessions: [html] }];
		const { container, unmount } = renderView();
		expect(container.querySelector("iframe")).toBeNull();
		expect(fetch).toHaveBeenCalledWith(html.artifactFiles![0].rawUrl, { signal: expect.any(AbortSignal), cache: "no-store" });
		respond(new Response("<h1>Design review</h1>"));
		await waitFor(() => expect(container.querySelector("iframe")).toHaveAttribute("src", html.artifactFiles![0].inlineUrl));
		expect(container.querySelector("iframe")).toHaveAttribute("sandbox", "allow-scripts allow-same-origin");
		expect(container.querySelector("iframe")).toHaveAttribute("tabindex", "-1");
		unmount();
		html.artifactFiles![0].inlineUrl = "http://127.0.0.1:3002/api/v1/settings";
		const unsafe = renderView();
		expect(unsafe.container.querySelector("iframe")).toBeNull();
		expect(within(screen.getByRole("region", { name: "AO" })).getByTitle("No preview available")).toBeInTheDocument();
	});

	it("runs one HTML thumbnail across pinned, recent, and project copies", async () => {
		const html = session("ao-html", "Design review", [{ name: "page.html", kind: "html", updatedAt: "2026-10-11T10:00:00Z" }]);
		html.artifactFiles![0].inlineUrl = "http://ao-inline-artifact.ao-html.localhost:3002/page.html";
		html.artifactFiles![0].rawUrl = "http://127.0.0.1:3002/page.html?raw=true";
		mocks.workspaces = [{ id: "ao", name: "AO", path: "/code/ao", sessions: [html] }, withArtifacts[1]];
		useArtifactsViewStore.setState({ pinnedArtifactIds: [JSON.stringify(["ao-html", "page.html"])] });
		const fetch = vi.fn().mockImplementation(async () => new Response("<h1>Design review</h1>"));
		vi.stubGlobal("fetch", fetch);
		const { container } = renderView();

		await waitFor(() => expect(container.querySelectorAll("iframe")).toHaveLength(1));
		expect(screen.getByRole("region", { name: "Pinned" }).querySelector("iframe")).not.toBeNull();
		expect(screen.getByRole("region", { name: "Recent" }).querySelector("iframe")).toBeNull();
		expect(screen.getByRole("region", { name: "AO" }).querySelector("iframe")).toBeNull();
		expect(fetch).toHaveBeenCalledTimes(1);

		await userEvent.click(screen.getByRole("button", { name: "Pinned" }));
		expect(container.querySelectorAll("iframe")).toHaveLength(1);
		expect(fetch).toHaveBeenCalledTimes(1);
	});

	it("caps live HTML thumbnails at four and keeps the remaining cards as placeholders", async () => {
		const pages: Parameters<typeof session>[2] = Array.from({ length: 6 }, (_, index) => ({ name: `page-${index}.html`, kind: "html", updatedAt: "2026-10-11T10:00:00Z" }));
		const html = session("ao-html", "Design review", pages);
		for (const file of html.artifactFiles ?? []) {
			file.inlineUrl = `http://ao-inline-artifact.ao-html.localhost:3002/${file.name}`;
			file.rawUrl = `http://127.0.0.1:3002/${file.name}?raw=true`;
		}
		mocks.workspaces = [{ id: "ao", name: "AO", path: "/code/ao", sessions: [html] }];
		const fetch = vi.fn().mockImplementation(async () => new Response("<h1>Design review</h1>"));
		vi.stubGlobal("fetch", fetch);
		const { container } = renderView();

		await waitFor(() => expect(container.querySelectorAll("iframe")).toHaveLength(4));
		expect(screen.getAllByRole("listitem")).toHaveLength(6);
		expect(screen.getAllByTitle("No preview available")).toHaveLength(2);
		expect(fetch).toHaveBeenCalledTimes(4);
	});

	it.each(["missing", "network failure"])("shows an unavailable HTML preview for %s files after reopening", async (failure) => {
		const html = session("ao-html", "Design review", [{ name: "page.html", kind: "html", updatedAt: "2026-10-11T10:00:00Z" }]);
		html.artifactFiles![0].inlineUrl = "http://ao-inline-artifact.ao-html.localhost:3002/page.html";
		html.artifactFiles![0].rawUrl = "http://127.0.0.1:3002/page.html?raw=true";
		mocks.workspaces = [{ id: "ao", name: "AO", path: "/code/ao", sessions: [html] }];
		const fetch = vi.fn().mockResolvedValueOnce(new Response("<h1>Design review</h1>"));
		vi.stubGlobal("fetch", fetch);
		const { container, unmount } = renderView();
		await waitFor(() => expect(container.querySelector("iframe")).not.toBeNull());
		unmount();
		if (failure === "missing") fetch.mockResolvedValueOnce(new Response('{"code":"ARTIFACT_FILE_NOT_FOUND"}', { status: 404 }));
		else fetch.mockRejectedValueOnce(new TypeError("Failed to fetch"));
		const reopened = renderView();
		await waitFor(() => expect(within(screen.getByRole("region", { name: "AO" })).getByTitle("No preview available")).toBeInTheDocument());
		expect(reopened.container.querySelector("iframe")).toBeNull();
		expect(fetch).toHaveBeenCalledTimes(2);
	});

	it.each([
		{ name: "metrics.csv", content: 'Name,Value\n"Alpha, Inc",42\nBeta,7\n', text: "Alpha, Inc", type: "table" },
		{ name: "metrics.tsv", content: "Name\tValue\nAlpha\t42\n", text: "Alpha", type: "table" },
		{ name: "flow.mmd", content: "flowchart LR\n A --> B", text: "A --> B", type: "mermaid" },
		{ name: "flow.mermaid", content: "flowchart LR\n A --> B", text: "A --> B", type: "mermaid" },
	])("renders a $name thumbnail with the shared renderer", async ({ name, content, text, type }) => {
		const file = session("ao-format", "Preview formats", [{ name, kind: "file", updatedAt: "2026-10-11T10:00:00Z" }]);
		file.artifactFiles![0].rawUrl = `http://127.0.0.1:3002/${name}`;
		mocks.workspaces = [{ id: "ao", name: "AO", path: "/code/ao", sessions: [file] }];
		vi.stubGlobal("fetch", vi.fn().mockImplementation(async () => new Response(content)));
		const { container } = renderView();

		await waitFor(() => expect(container.textContent).toContain(text));
		expect(container.querySelector(type === "table" ? "table" : '[data-testid="mermaid-thumbnail"]')).not.toBeNull();
		expect(container.querySelector("[inert]")).not.toBeNull();
	});

	it("renders Mermaid fences inside Markdown thumbnails", async () => {
		const file = session("ao-diagram", "Architecture", [{ name: "architecture.md", updatedAt: "2026-10-11T10:00:00Z" }]);
		file.artifactFiles![0].rawUrl = "http://127.0.0.1:3002/architecture.md";
		mocks.workspaces = [{ id: "ao", name: "AO", path: "/code/ao", sessions: [file] }];
		vi.stubGlobal("fetch", vi.fn().mockImplementation(async () => new Response("```mermaid\nflowchart LR\n A --> B\n```")));
		const { container } = renderView();

		await waitFor(() => expect(container.querySelector('[data-testid="mermaid-thumbnail"]')).toHaveTextContent("A --> B"));
	});

	it("renders the first PDF page as a thumbnail with its file version", () => {
		const file = session("ao-pdf", "Report", [{ name: "report.pdf", kind: "file", updatedAt: "2026-10-11T10:00:00Z" }]);
		file.artifactFiles![0].rawUrl = "http://127.0.0.1:3002/report.pdf";
		mocks.workspaces = [{ id: "ao", name: "AO", path: "/code/ao", sessions: [file] }];
		const { container } = renderView();

		const preview = container.querySelector('[data-testid="pdf-thumbnail"]');
		expect(preview).toHaveAttribute("data-first-page-only", "true");
		expect(preview).toHaveAttribute("data-src", "http://127.0.0.1:3002/report.pdf?v=2026-10-11T10%3A00%3A00Z");
	});

	it("shows image previews and falls back to a placeholder when the image fails", () => {
		const image = session("ao-image", "Cover image", [{ name: "cover.png", kind: "file", updatedAt: "2026-10-11T10:00:00Z" }]);
		image.artifactFiles![0].rawUrl = "http://127.0.0.1:3002/cover.png";
		mocks.workspaces = [{ id: "ao", name: "AO", path: "/code/ao", sessions: [image] }];
		renderView();
		const project = within(screen.getByRole("region", { name: "AO" }));
		const preview = project.getByRole("button", { name: /^cover\.png/ }).closest("li")!.querySelector("img")!;
		expect(preview).toHaveAttribute("src", image.artifactFiles![0].rawUrl);
		fireEvent.error(preview);
		expect(project.getByRole("button", { name: /^cover\.png/ }).closest("li")!.querySelector("img")).toBeNull();
		expect(project.getByTitle("No preview available")).toBeInTheDocument();
	});

	it("renders Markdown content without interactive links or embedded HTML", async () => {
		const markdown = session("ao-doc", "Research notes", [{ name: "research.md", updatedAt: "2026-10-11T10:00:00Z" }]);
		markdown.artifactFiles![0].rawUrl = "http://127.0.0.1:3002/research.md";
		mocks.workspaces = [{ id: "ao", name: "AO", path: "/code/ao", sessions: [markdown] }];
		vi.stubGlobal("fetch", vi.fn().mockResolvedValue(new Response("# Research findings\n\n[Read more](https://example.com)\n\n<script>alert(1)</script>")));
		const { container } = renderView();
		await waitFor(() => expect(container.textContent).toContain("Research findings"));
		expect(container.querySelector("a, script")).toBeNull();
	});

	it("previews the artifact on this page before opening its source session", async () => {
		const user = userEvent.setup();
		renderView();

		await user.click(within(screen.getByRole("region", { name: "agent-orchestrator" })).getByRole("button", { name: /^artifacts-user-flow\.html/ }));
		const preview = within(screen.getByRole("dialog", { name: "artifacts-user-flow.html" }));
		expect(mocks.navigate).not.toHaveBeenCalled();
		expect(preview.getByText("agent-orchestrator · Design the Artifacts entry point")).toBeInTheDocument();
		await user.click(preview.getByRole("button", { name: "Open in session" }));

		expect(mocks.navigate).toHaveBeenCalledWith({ to: "/projects/$projectId/sessions/$sessionId", params: { projectId: "ao", sessionId: "ao-1" } });
		expect(useUiStore.getState().workspaceFileOpenRequest).toMatchObject({
			sessionId: "ao-1",
			path: "artifacts-user-flow.html",
			source: "artifact",
		});
	});

	it("opens a Scratchpad artifact on the standalone session route", async () => {
		const user = userEvent.setup();
		renderView();

		await user.click(within(screen.getByRole("region", { name: "Scratchpad" })).getByRole("button", { name: /^caching-options\.md/ }));
		expect(mocks.navigate).not.toHaveBeenCalled();
		await user.click(within(screen.getByRole("dialog", { name: "caching-options.md" })).getByRole("button", { name: "Open in session" }));

		expect(mocks.navigate).toHaveBeenCalledWith({ to: "/sessions/$sessionId", params: { sessionId: "standalone-1" } });
	});

	it.each(["report.pdf", "metrics.csv", "metrics.tsv", "flow.mmd", "flow.mermaid", "notes.md", "diagram.svg", "recording.mp4", "audio.mp3"])("opens %s in the shared file viewer", async (name) => {
		const file = session("ao-formats", "Preview formats", [{ name, kind: "file", updatedAt: "2026-10-11T10:00:00Z" }]);
		file.artifactFiles![0].rawUrl = `http://127.0.0.1:3002/${name}`;
		mocks.workspaces = [{ id: "ao", name: "AO", path: "/code/ao", sessions: [file] }];
		renderView();

		await userEvent.click(screen.getByRole("button", { name: new RegExp(`^${name}`) }));
		const preview = within(screen.getByRole("dialog", { name })).getByTestId("artifact-file-preview");
		expect(preview).toHaveAttribute("data-path", name);
		expect(preview).toHaveAttribute("data-raw-url", file.artifactFiles![0].rawUrl);
		expect(preview).toHaveAttribute("data-read-only", "true");
		expect(preview).not.toHaveAttribute("data-cloud", "true");
	});

	it("opens a session directly from the card arrow without opening a modal", async () => {
		const user = userEvent.setup();
		renderView();
		const project = within(screen.getByRole("region", { name: "agent-orchestrator" }));
		expect(project.queryByRole("button", { name: "Delete artifact: pricing.md" })).not.toBeInTheDocument();
		await user.click(project.getByRole("button", { name: "Open in session: pricing.md" }));
		expect(screen.queryByRole("dialog")).not.toBeInTheDocument();
		expect(mocks.navigate).toHaveBeenCalledWith({ to: "/projects/$projectId/sessions/$sessionId", params: { projectId: "ao", sessionId: "ao-2" } });
	});

	it("keeps pin and delete in the modal menu without duplicating Open in session there", async () => {
		const user = userEvent.setup();
		renderView();
		await user.click(within(screen.getByRole("region", { name: "agent-orchestrator" })).getByRole("button", { name: /^pricing\.md/ }));
		const modal = within(screen.getByRole("dialog", { name: "pricing.md" }));
		expect(modal.queryByRole("button", { name: "Pin artifact: pricing.md" })).not.toBeInTheDocument();
		expect(modal.getByRole("button", { name: "Open in session" })).toBeInTheDocument();
		await user.click(modal.getByRole("button", { name: "More actions: pricing.md" }));
		expect(screen.queryByRole("menuitem", { name: "Open in session" })).not.toBeInTheDocument();
		expect(screen.getByRole("menuitem", { name: "Delete artifact" })).toBeInTheDocument();
		await user.click(screen.getByRole("menuitem", { name: "Pin artifact" }));
		expect(useArtifactsViewStore.getState().pinnedArtifactIds).toHaveLength(1);
		expect(mocks.navigate).not.toHaveBeenCalled();
	});

	it("closes a preview with Escape and restores focus without navigating", async () => {
		const user = userEvent.setup();
		renderView();
		const trigger = within(screen.getByRole("region", { name: "agent-orchestrator" })).getByRole("button", { name: /^pricing\.md/ });
		await user.click(trigger);
		expect(screen.getByRole("dialog", { name: "pricing.md" })).toBeInTheDocument();
		await user.keyboard("{Escape}");
		await waitFor(() => expect(screen.queryByRole("dialog")).not.toBeInTheDocument());
		expect(mocks.navigate).not.toHaveBeenCalled();
		expect(trigger).toHaveFocus();
	});

	it("opens the same preview from list view without navigating", async () => {
		const user = userEvent.setup();
		renderView();
		await user.click(screen.getByRole("button", { name: "List view" }));
		await user.click(within(screen.getByRole("region", { name: "Recent" })).getByRole("button", { name: /^pricing\.md/ }));
		expect(screen.getByRole("dialog", { name: "pricing.md" })).toBeInTheDocument();
		expect(mocks.navigate).not.toHaveBeenCalled();
		await user.click(screen.getByRole("button", { name: "Close" }));
		expect(screen.queryByRole("dialog")).not.toBeInTheDocument();
	});

	it("opens a pinned artifact in a preview without navigating", async () => {
		const user = userEvent.setup();
		renderView();
		await user.click(within(screen.getByRole("region", { name: "agent-orchestrator" })).getByRole("button", { name: "Pin artifact: pricing.md" }));
		await user.click(within(screen.getByRole("region", { name: "Pinned" })).getByRole("button", { name: /^pricing\.md/ }));
		expect(screen.getByRole("dialog", { name: "pricing.md" })).toBeInTheDocument();
		expect(mocks.navigate).not.toHaveBeenCalled();
	});

	it("previews HTML on its isolated origin inside the modal", async () => {
		const user = userEvent.setup();
		const html = session("ao-html", "Design review", [{ name: "page.html", kind: "html", updatedAt: "2026-10-11T10:00:00Z" }]);
		html.artifactFiles![0].inlineUrl = "http://ao-inline-artifact.ao-html.localhost:3002/page.html";
		mocks.workspaces = [{ id: "ao", name: "AO", path: "/code/ao", sessions: [html] }];
		renderView();
		await user.click(within(screen.getByRole("region", { name: "AO" })).getByRole("button", { name: /^page\.html/ }));
		const frame = screen.getByRole("dialog", { name: "page.html" }).querySelector("iframe");
		expect(frame).toHaveAttribute("src", html.artifactFiles![0].inlineUrl);
		expect(frame).toHaveAttribute("sandbox", "allow-scripts allow-same-origin");
		expect(mocks.navigate).not.toHaveBeenCalled();
	});

	it("offers New artifact beside the filters with the same Scratchpad and project menu", async () => {
		const user = userEvent.setup();
		mocks.post.mockResolvedValue({ data: { session: { id: "standalone-7" } } });
		renderView();

		await user.click(screen.getByRole("button", { name: "New artifact" }));
		expect(screen.getAllByRole("menuitem").map((item) => item.textContent)).toEqual(["Scratchpad", "agent-orchestrator"]);
		await user.click(screen.getByRole("menuitem", { name: "Scratchpad" }));

		await waitFor(() => expect(mocks.navigate).toHaveBeenCalledWith({ to: "/sessions/$sessionId", params: { sessionId: "standalone-7" } }));
		expect(mocks.post).toHaveBeenCalledWith("/api/v1/sessions", {
			body: { kind: "worker", requestedOutput: "artifact", displayName: "New artifact", harness: "codex" },
		});
	});

	it("disables New artifact when no agent is ready", () => {
		mocks.agents = [{ ...codexReady, installation: { state: "not_installed", freshness: "fresh" } }];
		renderView();

		expect(screen.getByRole("button", { name: "New artifact" })).toBeDisabled();
	});

	it("shows one Create artifact action and no filters when nothing exists", async () => {
		const user = userEvent.setup();
		mocks.workspaces = withoutArtifacts;
		renderView();

		expect(screen.getByRole("heading", { name: "Create your first artifact" })).toBeInTheDocument();
		expect(screen.queryByRole("searchbox")).not.toBeInTheDocument();
		expect(screen.queryByRole("combobox")).not.toBeInTheDocument();

		await user.click(screen.getByRole("button", { name: "Create artifact" }));
		expect(screen.getAllByRole("menuitem").map((item) => item.textContent)).toEqual(["Scratchpad", "agent-orchestrator", "px0"]);
	});

	it("starts a Scratchpad artifact session without a project id and opens it", async () => {
		const user = userEvent.setup();
		mocks.workspaces = withoutArtifacts;
		mocks.post.mockResolvedValue({ data: { session: { id: "standalone-9" } } });
		renderView();

		await user.click(screen.getByRole("button", { name: "Create artifact" }));
		await user.click(screen.getByRole("menuitem", { name: "Scratchpad" }));

		await waitFor(() => expect(mocks.navigate).toHaveBeenCalledWith({ to: "/sessions/$sessionId", params: { sessionId: "standalone-9" } }));
		expect(mocks.post).toHaveBeenCalledWith("/api/v1/sessions", {
			body: { kind: "worker", requestedOutput: "artifact", displayName: "New artifact", harness: "codex" },
		});
		expect(mocks.capture).not.toHaveBeenCalledWith("ao.renderer.artifact_create_requested", expect.anything());
		expect(mocks.capture).toHaveBeenCalledWith("ao.renderer.artifact_create_succeeded", { scope: "standalone" });
	});

	it("starts a project artifact session with the project's worker agent", async () => {
		const user = userEvent.setup();
		mocks.workspaces = withoutArtifacts;
		mocks.get.mockResolvedValue({ data: { status: "ok", project: { agent: "codex", config: { worker: { agent: "claude-code" } } } } });
		mocks.post.mockResolvedValue({ data: { session: { id: "px0-3" } } });
		renderView();

		await user.click(screen.getByRole("button", { name: "Create artifact" }));
		await user.click(screen.getByRole("menuitem", { name: "px0" }));

		await waitFor(() => expect(mocks.navigate).toHaveBeenCalledWith({ to: "/projects/$projectId/sessions/$sessionId", params: { projectId: "px0", sessionId: "px0-3" } }));
		expect(mocks.post).toHaveBeenCalledWith("/api/v1/sessions", {
			body: { kind: "worker", requestedOutput: "artifact", displayName: "New artifact", projectId: "px0", harness: "claude-code" },
		});
		expect(mocks.capture).toHaveBeenCalledWith("ao.renderer.artifact_create_succeeded", { scope: "project" });
	});

	it("shows the daemon error and stays on the screen when the start fails", async () => {
		const user = userEvent.setup();
		mocks.workspaces = withoutArtifacts;
		mocks.post.mockResolvedValue({ error: { error: "conflict", code: "AGENT_NOT_READY", message: "Agent is not ready" } });
		renderView();

		await user.click(screen.getByRole("button", { name: "Create artifact" }));
		await user.click(screen.getByRole("menuitem", { name: "Scratchpad" }));

		await waitFor(() => expect(useUiStore.getState().globalToast).toMatchObject({ title: "Agent is not ready" }));
		expect(mocks.navigate).not.toHaveBeenCalled();
		expect(mocks.capture).toHaveBeenCalledWith("ao.renderer.artifact_create_failed", { scope: "standalone" });
		expect(screen.getByRole("button", { name: "Create artifact" })).toBeEnabled();
	});

	it("points to agent settings when no agent is ready", async () => {
		const user = userEvent.setup();
		mocks.workspaces = withoutArtifacts;
		mocks.agents = [{ ...codexReady, installation: { state: "not_installed", freshness: "fresh" } }];
		renderView();

		expect(screen.getByRole("heading", { name: "No agent is ready" })).toBeInTheDocument();
		expect(screen.queryByRole("button", { name: "Create artifact" })).not.toBeInTheDocument();
		await user.click(screen.getByRole("button", { name: "Open agent settings" }));
		expect(useUiStore.getState().settingsModal).toMatchObject({ scope: "global", section: "agents" });
	});
});
