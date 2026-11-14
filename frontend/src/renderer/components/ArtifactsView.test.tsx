import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { act, fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { useUiStore } from "../stores/ui-store";
import { useArtifactsViewStore } from "../stores/artifacts-view-store";
import { CLOUD_PROJECT_KIND, STANDALONE_WORKSPACE_ID, type WorkspaceSession, type WorkspaceSummary } from "../types/workspace";
import { ArtifactsView } from "./ArtifactsView";
import { TooltipProvider } from "./ui/tooltip";

// The signed-in person's control-plane user ID: only a cloud session's creator shares its files.
vi.mock("../hooks/useCloudMeUserId", () => ({ useCloudMeUserId: () => "me-1" }));

// Share is offered only where the control plane serves share links.
vi.mock("../hooks/useSettings", async (importOriginal) => {
	const actual = await importOriginal<typeof import("../hooks/useSettings")>();
	return {
		...actual,
		useSettings: (...args: Parameters<typeof actual.useSettings>) => {
			const result = actual.useSettings(...args);
			return { ...result, settings: { ...result.settings, cloudControlPlaneUrl: "https://cp.test" } as NonNullable<typeof result.settings> };
		},
	};
});
vi.mock("../hooks/useCloudOffering", async (importOriginal) => {
	const actual = await importOriginal<typeof import("../hooks/useCloudOffering")>();
	return {
		...actual,
		useCloudOffering: () => {
			const offering = actual.useCloudOffering();
			return mocks.cloudArtifacts === undefined ? offering : { ...offering, artifacts: mocks.cloudArtifacts };
		},
	};
});
vi.mock("./NotificationCenter", () => ({ NotificationCenter: () => <button type="button" aria-label="Notifications" /> }));

const mocks = vi.hoisted(() => ({
	workspaces: [] as WorkspaceSummary[],
	agents: [] as Array<Record<string, unknown>>,
	navigate: vi.fn(),
	post: vi.fn(),
	del: vi.fn(),
	get: vi.fn(),
	capture: vi.fn(),
	deleteCloudArtifact: vi.fn(),
	refreshCloudAccess: vi.fn(),
	artifactFileChanges: vi.fn(),
	hideShellTopbar: true,
	/** Overrides whether the control plane keeps artifacts; undefined keeps the real hook's answer. */
	cloudArtifacts: undefined as boolean | undefined,
	/** Who each published link opens for, as the link list says. */
	shareAccess: new Map<string, "restricted" | "anyone">(),
}));

vi.mock("./ShareArtifactPopover", async (importOriginal) => ({
	...await importOriginal<typeof import("./ShareArtifactPopover")>(),
	useReconciledShareLinks: () => mocks.shareAccess,
}));

// These tests are about the live preview a card falls back to; the cached image path has its own tests.
vi.mock("./ArtifactSnapshotThumbnail", async () => {
	const { ArtifactThumbnail } = await import("./ArtifactThumbnail");
	return { ArtifactSnapshotThumbnail: ArtifactThumbnail };
});
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
	useCloudArtifactAccess: (session: Pick<WorkspaceSession, "cloud">) => ({ data: session.cloud ? "https://cp.test/artifact-files/token/" : undefined, refetch: mocks.refreshCloudAccess }),
}));
vi.mock("../lib/cloud-cp/renderer-client", async (importOriginal) => ({
	...await importOriginal<typeof import("../lib/cloud-cp/renderer-client")>(),
	cloudCpFetch: async () => new Response("Cloud artifact thumbnail"),
}));
vi.mock("./CloudArtifactFrame", () => ({
	CloudArtifactFrame: ({ title, url }: { title: string; url: string }) => <iframe data-testid="cloud-artifact-preview" title={title} src={url} />,
}));
vi.mock("./ArtifactFileView", () => ({
	ArtifactFileView: ({ cloud, path, rawUrl, readOnly }: { cloud?: boolean; path: string; rawUrl?: string; readOnly?: boolean }) => <div data-testid="artifact-file-preview" data-cloud={cloud} data-path={path} data-raw-url={rawUrl} data-read-only={readOnly} />,
}));
vi.mock("../hooks/useArtifactFileChanges", () => ({ useArtifactFileChanges: mocks.artifactFileChanges }));
vi.mock("../hooks/useAgentReadinessQuery", () => ({ useAgentReadinessQuery: () => ({ data: { agents: mocks.agents } }) }));
vi.mock("../lib/api-client", async (importOriginal) => ({
	...await importOriginal<typeof import("../lib/api-client")>(),
	apiClient: { POST: mocks.post, GET: mocks.get, DELETE: mocks.del },
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

/** The one list every artifact is in, newest first, pinned ones on top. */
function artifactList() {
	return screen.getByRole("list", { name: "Artifacts" });
}

/** Search sits behind its icon until opened. */
async function openSearch(user: ReturnType<typeof userEvent.setup>) {
	await user.click(screen.getByRole("button", { name: "Search artifacts" }));
	return screen.getByRole("searchbox", { name: "Search artifacts" });
}

function renderView() {
	const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
	const tree = () => (
		<QueryClientProvider client={client}>
			<TooltipProvider>
				<ArtifactsView />
			</TooltipProvider>
		</QueryClientProvider>
	);
	const view = render(tree());
	/** Renders again, as a workspace refresh does. */
	const refresh = () => view.rerender(tree());
	return { ...view, refresh };
}


/** The open menu's items by accessible name; a project's letter tile is decoration, not part of it. */
function expectMenuItems(names: string[]) {
	const items = screen.getAllByRole("menuitem");
	expect(items).toHaveLength(names.length);
	items.forEach((item, index) => expect(item).toHaveAccessibleName(names[index]));
}

describe("ArtifactsView", () => {
	afterEach(() => vi.unstubAllGlobals());
	beforeEach(() => {
		mocks.shareAccess = new Map();
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
		mocks.cloudArtifacts = undefined;
		mocks.navigate.mockReset();
		mocks.post.mockReset();
		mocks.del.mockReset().mockResolvedValue({ data: undefined, error: undefined });
		mocks.get.mockReset();
		mocks.capture.mockReset();
		mocks.deleteCloudArtifact.mockReset().mockResolvedValue(undefined);
		mocks.artifactFileChanges.mockReset();
		useUiStore.setState({ settingsModal: null });
		useUiStore.getState().clearGlobalToast();
	});

	it("lists every artifact once, newest first, with its project and session, in no sections", () => {
		renderView();

		expect(screen.getByRole("heading", { level: 1, name: "Artifacts" })).not.toHaveClass("sr-only");
		expect(screen.queryAllByRole("region")).toHaveLength(0);
		expect(screen.queryByRole("heading", { level: 2 })).not.toBeInTheDocument();
		const gallery = within(artifactList());
		expect(gallery.getAllByRole("listitem").map((row) => row.textContent)).toEqual([
			expect.stringContaining("artifacts-user-flow.html"),
			expect.stringContaining("pricing.md"),
			expect.stringContaining("caching-options.md"),
		]);
		expect(gallery.getByText("agent-orchestrator · Design the Artifacts entry point")).toBeInTheDocument();
		expect(gallery.getByText("agent-orchestrator · Review pricing")).toBeInTheDocument();
		expect(gallery.getByText("Sessions · Compare caching options")).toBeInTheDocument();
		expect(screen.queryByRole("button", { name: /team/i })).not.toBeInTheDocument();
	});

	it("narrows the one list by project, then clears the filters", async () => {
		const user = userEvent.setup();
		renderView();
		await user.click(screen.getByRole("button", { name: "Filter" }));
		await user.click(screen.getByRole("menuitemcheckbox", { name: "Sessions" }));
		await user.keyboard("{Escape}");
		expect(within(artifactList()).getAllByRole("listitem")).toHaveLength(1);
		expect(within(artifactList()).getByText("Sessions · Compare caching options")).toBeInTheDocument();
		expect(screen.queryByRole("heading", { level: 2 })).not.toBeInTheDocument();
		expect(screen.getByRole("button", { name: "Filter" })).toHaveAttribute("data-active", "true");
		expect(screen.getByRole("button", { name: "Remove filter Project: Sessions" })).toBeInTheDocument();
		await user.type(await openSearch(user), "no such thing");
		await user.click(screen.getByRole("button", { name: "Clear filters" }));
		expect(screen.queryByRole("searchbox", { name: "Search artifacts" })).not.toBeInTheDocument();
		expect(within(artifactList()).getAllByRole("listitem")).toHaveLength(3);
		expect(screen.queryByRole("button", { name: /^Remove filter/ })).not.toBeInTheDocument();
		expect(screen.getByRole("button", { name: "Filter" })).not.toHaveAttribute("data-active");
	});

	it("filters by file type from the Filter menu and removes it from its chip", async () => {
		const user = userEvent.setup();
		mocks.workspaces = [{
			id: "ao",
			name: "agent-orchestrator",
			path: "/code/ao",
			sessions: [session("ao-1", "Launch", [
				{ name: "launch.html", kind: "html", updatedAt: "2026-10-11T10:00:00Z" },
				{ name: "hero.png", kind: "file", updatedAt: "2026-10-10T10:00:00Z" },
				{ name: "demo.mp4", kind: "file", updatedAt: "2026-10-09T10:00:00Z" },
			])],
		}];
		renderView();
		await user.click(screen.getByRole("button", { name: "Filter" }));
		expect(screen.getAllByRole("menuitemcheckbox").map((item) => item.textContent)).toEqual([
			"All projects", "agent-orchestrator", "All types", "All images", "All videos", "All documents", "Public", "Shared privately", "Not shared", "All",
		]);
		const names = () => within(artifactList()).getAllByRole("listitem").map((row) => row.textContent ?? "");
		// The menu stays open, so several types are picked in one go.
		await user.click(screen.getByRole("menuitemcheckbox", { name: "All videos" }));
		expect(screen.getByRole("menuitemcheckbox", { name: "All videos" })).toHaveAttribute("aria-checked", "true");
		expect(screen.getByRole("menuitemcheckbox", { name: "All types" })).toHaveAttribute("aria-checked", "false");
		await user.click(screen.getByRole("menuitemcheckbox", { name: "All documents" }));
		expect(names()).toEqual([expect.stringContaining("launch.html"), expect.stringContaining("demo.mp4")]);
		await user.click(screen.getByRole("menuitemcheckbox", { name: "All videos" }));
		expect(names()).toEqual([expect.stringContaining("launch.html")]);
		await user.keyboard("{Escape}");

		await user.click(screen.getByRole("button", { name: "Remove filter Type: All documents" }));
		expect(names()).toHaveLength(3);

		// All clears its filter.
		await user.click(screen.getByRole("button", { name: "Filter" }));
		await user.click(screen.getByRole("menuitemcheckbox", { name: "All images" }));
		await user.click(screen.getByRole("menuitemcheckbox", { name: "All types" }));
		expect(names()).toHaveLength(3);
		expect(screen.getByRole("menuitemcheckbox", { name: "All types" })).toHaveAttribute("aria-checked", "true");
	});

	it("filters by location, offering Cloud only where the control plane keeps artifacts", async () => {
		const user = userEvent.setup();
		mocks.cloudArtifacts = false;
		const { unmount } = renderView();
		const location = within(screen.getByRole("group", { name: "Artifact location" }));
		expect(location.getAllByRole("button").map((button) => button.textContent)).toEqual(["All", "On this machine"]);
		expect(location.getByRole("button", { name: "All" })).toHaveAttribute("aria-pressed", "true");
		unmount();

		mocks.cloudArtifacts = true;
		const cloud = session("cloud-1", "Cloud notes", [{ name: "cloud-notes.md", updatedAt: "2026-10-12T10:00:00Z" }]);
		cloud.cloud = { orgId: "org-1" };
		mocks.workspaces = [...withArtifacts, { id: "cloud-project", name: "Cloud project", path: "", kind: CLOUD_PROJECT_KIND, cloudOrgId: "org-1", sessions: [cloud] }];
		renderView();
		const withCloud = within(screen.getByRole("group", { name: "Artifact location" }));
		await user.click(withCloud.getByRole("button", { name: "Cloud" }));
		expect(withCloud.getByRole("button", { name: "Cloud" })).toHaveAttribute("aria-pressed", "true");
		expect(screen.getAllByRole("listitem")).toHaveLength(1);
		expect(screen.getByText("cloud-notes.md")).toBeInTheDocument();
		// The project menu lists only projects with artifacts in that location.
		await user.click(screen.getByRole("button", { name: "Filter" }));
		expect(screen.getAllByRole("menuitemcheckbox").slice(0, 2).map((item) => item.textContent)).toEqual(["All projects", "Cloud project"]);
		await user.keyboard("{Escape}");

		await user.click(withCloud.getByRole("button", { name: "On this machine" }));
		expect(screen.queryByText("cloud-notes.md")).not.toBeInTheDocument();
		expect(screen.getAllByText("pricing.md").length).toBeGreaterThan(0);
	});

	it("opens search from its icon and closes it with Escape, clearing the query", async () => {
		const user = userEvent.setup();
		renderView();
		const search = await openSearch(user);
		expect(search).toHaveFocus();
		await user.type(search, "pricing");
		expect(screen.queryByText("artifacts-user-flow.html")).not.toBeInTheDocument();
		await user.keyboard("{Escape}");
		expect(screen.queryByRole("searchbox")).not.toBeInTheDocument();
		expect(screen.getAllByRole("listitem")).toHaveLength(3);
	});

	it("searches by file name and session title, then clears filters", async () => {
		const user = userEvent.setup();
		renderView();

		await user.type(await openSearch(user), "pricing");
		expect(screen.getAllByText("pricing.md")).toHaveLength(1);
		expect(screen.queryByText("artifacts-user-flow.html")).not.toBeInTheDocument();
		expect(screen.queryByText("Sessions")).not.toBeInTheDocument();

		await user.clear(screen.getByRole("searchbox", { name: "Search artifacts" }));
		await user.type(screen.getByRole("searchbox", { name: "Search artifacts" }), "no such thing");
		expect(screen.getByText("No matches")).toBeInTheDocument();

		await user.click(screen.getByRole("button", { name: "Clear filters" }));
		expect(screen.queryByRole("searchbox", { name: "Search artifacts" })).not.toBeInTheDocument();
		expect(screen.getAllByRole("listitem")).toHaveLength(3);
	});

	it("searches project names and the translated Scratchpad label", async () => {
		const user = userEvent.setup();
		renderView();

		const search = await openSearch(user);
		await user.type(search, "agent-orchestrator");
		expect(within(artifactList()).getAllByRole("listitem")).toHaveLength(2);
		expect(screen.queryByText("caching-options.md")).not.toBeInTheDocument();
		await user.clear(search);
		await user.type(search, "Sessions");

		expect(screen.getAllByRole("listitem")).toHaveLength(1);
		expect(screen.getAllByText("caching-options.md")).toHaveLength(1);
		expect(screen.queryByText("pricing.md")).not.toBeInTheDocument();
	});

	it("names a single project's artifacts by project and session, with no heading, in either view", async () => {
		const user = userEvent.setup();
		mocks.workspaces = [withArtifacts[0]];
		// A section collapsed before sections were removed hides nothing.
		useArtifactsViewStore.setState({ collapsedSections: ["project:ao"] });
		renderView();
		expect(screen.queryByRole("heading", { level: 2 })).not.toBeInTheDocument();
		expect(screen.getAllByRole("listitem")).toHaveLength(2);
		expect(screen.getByText("agent-orchestrator · Review pricing")).toBeInTheDocument();
		await user.click(screen.getByRole("button", { name: "List view" }));
		expect(screen.getAllByRole("listitem")).toHaveLength(2);
		expect(screen.getByText("agent-orchestrator · Review pricing")).toBeInTheDocument();
	});

	it("selects artifacts one by one and deletes them together after confirming", async () => {
		const user = userEvent.setup();
		useArtifactsViewStore.setState({ pinnedArtifactIds: [JSON.stringify(["ao-2", "pricing.md"])] });
		renderView();

		await user.click(screen.getByRole("button", { name: "Select" }));
		expect(screen.getByText("0 selected")).toBeInTheDocument();
		expect(screen.getByRole("button", { name: "Delete" })).toBeDisabled();
		// Selecting hides each card's own actions and stops the card from opening a preview.
		expect(screen.queryByRole("button", { name: /^More actions/ })).not.toBeInTheDocument();
		expect(screen.queryByRole("button", { name: "New artifact" })).not.toBeInTheDocument();

		// The same file shows in Pinned, Recent and its project; one checkbox is checked in all of them.
		await user.click(screen.getAllByRole("checkbox", { name: "Select pricing.md" })[0]!);
		for (const box of screen.getAllByRole("checkbox", { name: "Select pricing.md" })) expect(box).toBeChecked();
		await user.click(screen.getAllByRole("checkbox", { name: "Select caching-options.md" })[0]!);
		expect(screen.getByText("2 selected")).toBeInTheDocument();
		expect(screen.queryByRole("dialog")).not.toBeInTheDocument();

		await user.click(screen.getByRole("button", { name: "Delete" }));
		const dialog = screen.getByRole("dialog", { name: "Delete 2 artifacts?" });
		expect(within(dialog).getByRole("list", { name: "Selected artifacts" })).toHaveTextContent("pricing.mdcaching-options.md");
		await user.click(within(dialog).getByRole("button", { name: "Delete 2 artifacts" }));

		await waitFor(() => expect(screen.queryByRole("dialog")).not.toBeInTheDocument());
		expect(mocks.del).toHaveBeenCalledTimes(2);
		expect(mocks.del).toHaveBeenCalledWith("/api/v1/sessions/{sessionId}/artifact-files", { params: { path: { sessionId: "ao-2" }, query: { path: "pricing.md" } } });
		expect(mocks.del).toHaveBeenCalledWith("/api/v1/sessions/{sessionId}/artifact-files", { params: { path: { sessionId: "standalone-1" }, query: { path: "caching-options.md" } } });
		expect(useArtifactsViewStore.getState().pinnedArtifactIds).toEqual([]);
		expect(screen.getByRole("button", { name: "Select" })).toBeInTheDocument();
		expect(screen.queryByRole("checkbox")).not.toBeInTheDocument();
	});

	it("selects every visible artifact, then leaves selection with Cancel or Escape", async () => {
		const user = userEvent.setup();
		useArtifactsViewStore.setState({ view: "list" });
		renderView();

		await user.click(screen.getByRole("button", { name: "Select" }));
		await user.click(screen.getByRole("button", { name: "Select all" }));
		expect(screen.getByText("3 selected")).toBeInTheDocument();
		await user.click(screen.getByRole("button", { name: "Deselect all" }));
		expect(screen.getByText("0 selected")).toBeInTheDocument();
		await user.click(screen.getByRole("button", { name: "Cancel" }));
		expect(screen.queryByRole("checkbox")).not.toBeInTheDocument();

		await user.click(screen.getByRole("button", { name: "Select" }));
		await user.click(screen.getAllByRole("checkbox", { name: "Select pricing.md" })[0]!);
		await user.keyboard("{Escape}");
		expect(screen.queryByRole("checkbox")).not.toBeInTheDocument();
		await user.click(screen.getByRole("button", { name: "Select" }));
		expect(screen.getByText("0 selected")).toBeInTheDocument();
	});

	it("drops a selected artifact deleted elsewhere, so a new file at its path starts unselected", async () => {
		const user = userEvent.setup();
		const { refresh } = renderView();

		await user.click(screen.getByRole("button", { name: "Select" }));
		await user.click(screen.getAllByRole("checkbox", { name: "Select pricing.md" })[0]!);
		await user.click(screen.getAllByRole("checkbox", { name: "Select caching-options.md" })[0]!);
		// The selection outlives the filters.
		const search = await openSearch(user);
		await user.type(search, "caching");
		await user.clear(search);
		expect(screen.getByText("2 selected")).toBeInTheDocument();

		const [project, scratchpad] = withArtifacts;
		mocks.workspaces = [{ ...project!, sessions: [project!.sessions[0]!, session("ao-2", "Review pricing", [])] }, scratchpad!];
		refresh();
		await waitFor(() => expect(screen.getByText("1 selected")).toBeInTheDocument());

		mocks.workspaces = withArtifacts;
		refresh();
		for (const box of screen.getAllByRole("checkbox", { name: "Select pricing.md" })) expect(box).not.toBeChecked();
		expect(screen.getByText("1 selected")).toBeInTheDocument();
		expect(screen.getAllByRole("checkbox", { name: "Select caching-options.md" })[0]).toBeChecked();
	});

	it.each(["gallery", "list"] as const)("describes each %s checkbox with its project, session and path", async (view) => {
		const user = userEvent.setup();
		useArtifactsViewStore.setState({ view });
		mocks.workspaces = [{
			id: "ao",
			name: "agent-orchestrator",
			path: "/code/ao",
			sessions: [
				session("ao-1", "Draft the report", [{ name: "report.md", updatedAt: "2026-10-11T10:00:00Z" }]),
				session("ao-2", "Review the report", [{ name: "report.md", updatedAt: "2026-10-10T10:00:00Z" }]),
			],
		}];
		renderView();

		await user.click(screen.getByRole("button", { name: "Select" }));
		const boxes = screen.getAllByRole("checkbox", { name: "Select report.md" });
		expect(boxes).toHaveLength(2);
		expect(boxes[0]).toHaveAccessibleDescription("agent-orchestrator · Draft the report · report.md");
		expect(boxes[1]).toHaveAccessibleDescription("agent-orchestrator · Review the report · report.md");
	});

	it("keeps the artifacts that failed to delete selected and says why", async () => {
		const user = userEvent.setup();
		mocks.del.mockImplementation(async (_path: string, { params }: { params: { path: { sessionId: string } } }) => (
			params.path.sessionId === "ao-2" ? { error: { error: { code: "internal", message: "disk is read-only" } } } : { data: undefined }
		));
		renderView();

		await user.click(screen.getByRole("button", { name: "Select" }));
		await user.click(screen.getAllByRole("checkbox", { name: "Select pricing.md" })[0]!);
		await user.click(screen.getAllByRole("checkbox", { name: "Select caching-options.md" })[0]!);
		await user.click(screen.getByRole("button", { name: "Delete" }));
		await user.click(within(screen.getByRole("dialog")).getByRole("button", { name: "Delete 2 artifacts" }));

		const dialog = await screen.findByRole("dialog", { name: "Delete 1 artifact?" });
		expect(within(dialog).getByText(/^Couldn't delete 1 of 2: /)).toBeInTheDocument();
		expect(within(dialog).getByRole("list", { name: "Selected artifacts" })).toHaveTextContent("pricing.md");
	});

	it("does not let another member's cloud artifacts be selected", async () => {
		const user = userEvent.setup();
		const mine = session("cloud-1", "My notes", [{ name: "mine.md", updatedAt: "2026-10-11T10:00:00Z" }]);
		mine.cloud = { orgId: "org-1", createdByUserId: "me-1" };
		const theirs = session("cloud-2", "Their notes", [{ name: "theirs.md", updatedAt: "2026-10-10T10:00:00Z" }]);
		theirs.cloud = { orgId: "org-1", createdByUserId: "someone-else" };
		mocks.workspaces = [{ id: "cloud-project", name: "Cloud project", path: "", kind: CLOUD_PROJECT_KIND, cloudOrgId: "org-1", sessions: [mine, theirs] }];
		renderView();

		await user.click(screen.getByRole("button", { name: "Select" }));
		expect(screen.getByRole("checkbox", { name: "Select theirs.md" })).toBeDisabled();
		await user.click(screen.getByRole("button", { name: "Select all" }));
		expect(screen.getByText("1 selected")).toBeInTheDocument();
		await user.click(screen.getByRole("button", { name: "Delete" }));
		await user.click(within(screen.getByRole("dialog")).getByRole("button", { name: "Delete 1 artifact" }));
		await waitFor(() => expect(mocks.deleteCloudArtifact).toHaveBeenCalledTimes(1));
		expect(mocks.deleteCloudArtifact).toHaveBeenCalledWith("org-1", "cloud-1", "mine.md");
	});

	it("does not let a bundle be selected, since deleting it would remove only one of its files", async () => {
		const user = userEvent.setup();
		mocks.workspaces = [{ id: "ao", name: "agent-orchestrator", path: "/code/ao", sessions: [
			session("ao-1", "Brand kit", [
				{ name: "brand.icon/mark.png", updatedAt: "2026-10-11T10:00:00Z", kind: "file" },
				{ name: "brand.icon/icon.json", updatedAt: "2026-10-11T10:00:00Z", kind: "file" },
				{ name: "notes.md", updatedAt: "2026-10-10T10:00:00Z" },
			]),
		] }];
		renderView();

		await user.click(screen.getByRole("button", { name: "Select" }));
		expect(screen.getByRole("checkbox", { name: "Select brand.icon" })).toBeDisabled();
		await user.click(screen.getByRole("button", { name: "Select all" }));
		expect(screen.getByText("1 selected")).toBeInTheDocument();
		await user.click(screen.getByRole("button", { name: "Delete" }));
		await user.click(within(screen.getByRole("dialog")).getByRole("button", { name: "Delete 1 artifact" }));
		await waitFor(() => expect(mocks.del).toHaveBeenCalledTimes(1));
		expect(mocks.del).toHaveBeenCalledWith("/api/v1/sessions/{sessionId}/artifact-files", { params: { path: { sessionId: "ao-1" }, query: { path: "notes.md" } } });
	});

	it("keeps search and the Filter menu while selecting, and Escape in search keeps the selection", async () => {
		const user = userEvent.setup();
		renderView();

		await user.click(screen.getByRole("button", { name: "Select" }));
		await user.click(screen.getAllByRole("checkbox", { name: "Select pricing.md" })[0]!);
		expect(screen.getByRole("button", { name: "Filter" })).toBeInTheDocument();
		expect(screen.queryByRole("button", { name: "List view" })).not.toBeInTheDocument();

		const search = await openSearch(user);
		await user.type(search, "caching");
		await user.click(screen.getByRole("button", { name: "Select all" }));
		expect(screen.getByText("2 selected")).toBeInTheDocument();
		await user.click(search);
		await user.keyboard("{Escape}");
		expect(screen.queryByRole("searchbox")).not.toBeInTheDocument();
		expect(screen.getByText("2 selected")).toBeInTheDocument();

		// Focus returns to the Search icon on the next frame; its tooltip takes the next Escape.
		await waitFor(() => expect(screen.getByRole("button", { name: "Search artifacts" })).toHaveFocus());
		await user.keyboard("{Escape}");
		expect(screen.getByText("2 selected")).toBeInTheDocument();
		await user.keyboard("{Escape}");
		expect(screen.queryByRole("checkbox")).not.toBeInTheDocument();
	});

	it("keeps gallery cloud actions on the cloud session, showing its files in a folder under ~/.ao", async () => {
		const cloud = session("cloud-1", "Cloud notes", [{ name: "notes.md", updatedAt: "2026-10-11T10:00:00Z" }]);
		cloud.cloud = { orgId: "org-1" };
		mocks.workspaces = [{ id: "cloud-project", name: "Cloud project", path: "", kind: CLOUD_PROJECT_KIND, cloudOrgId: "org-1", sessions: [cloud] }];
		renderView();
		await userEvent.click(screen.getByRole("button", { name: "More actions: notes.md" }));
		expect(screen.getAllByRole("menuitem", { name: /^Show in / })).toHaveLength(1);
		expect(screen.queryByRole("menuitem", { name: "Show in Terminal" })).not.toBeInTheDocument();
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
		cloud.cloud = { orgId: "org-1", createdByUserId: "me-1" };
		mocks.workspaces = [{ id: "cloud-project", name: "Cloud project", path: "", kind: CLOUD_PROJECT_KIND, cloudOrgId: "org-1", sessions: [cloud] }];
		renderView();
		await userEvent.click(screen.getByRole("button", { name: new RegExp(`^${name}`) }));
		const dialog = screen.getByRole("dialog", { name });
		// A cloud session's file is shared as a local one is.
		expect(within(dialog).getByRole("button", { name: `Publish: ${name}` })).toBeInTheDocument();
		if (kind === "html") {
			expect(within(dialog).getByTestId("cloud-artifact-preview")).toHaveAttribute("src", `https://cp.test/artifact-files/token/${name}?v=2026-10-11T10%3A00%3A00Z`);
		} else {
			const preview = within(dialog).getByTestId("artifact-file-preview");
			expect(preview).toHaveAttribute("data-cloud", "true");
			expect(preview).toHaveAttribute("data-read-only", "true");
			expect(preview).toHaveAttribute("data-raw-url", `https://cp.test/artifact-files/token/${name}?v=2026-10-11T10%3A00%3A00Z`);
		}
	});

	it("offers no Share on another member's cloud session file", async () => {
		const cloud = session("cloud-1", "Cloud notes", [{ name: "notes.md", kind: "markdown", updatedAt: "2026-10-11T10:00:00Z" }]);
		cloud.cloud = { orgId: "org-1", createdByUserId: "someone-else" };
		mocks.workspaces = [{ id: "cloud-project", name: "Cloud project", path: "", kind: CLOUD_PROJECT_KIND, cloudOrgId: "org-1", sessions: [cloud] }];
		renderView();
		await userEvent.click(screen.getByRole("button", { name: /^notes\.md/ }));
		const dialog = await screen.findByRole("dialog", { name: "notes.md" });
		await new Promise((resolve) => setTimeout(resolve, 50));
		expect(within(dialog).queryByRole("button", { name: "Publish: notes.md" })).toBeNull();
	});

	it("offers Publish on a Scratchpad file, which belongs to the Scratchpad workspace", async () => {
		renderView();
		await userEvent.click(within(artifactList()).getByRole("button", { name: /^caching-options\.md/ }));
		const dialog = screen.getByRole("dialog", { name: "caching-options.md" });
		expect(await within(dialog).findByRole("button", { name: "Publish: caching-options.md" })).toBeInTheDocument();
	});

	it("marks a published artifact with the globe, and offers Copy link and Unpublish instead of Publish", async () => {
		const user = userEvent.setup();
		const published = session("ao-2", "Review pricing", [{ name: "pricing.md", updatedAt: "2026-10-10T10:00:00Z" }]);
		published.artifactFiles![0].shareUrl = "https://share.test/pricing";
		mocks.workspaces = [{ ...withArtifacts[0], sessions: [withArtifacts[0].sessions[0], published] }, withArtifacts[1]];
		useArtifactsViewStore.setState({ view: "list" });
		renderView();
		const row = (name: string) => within(artifactList()).getByRole("button", { name: new RegExp(`^${name.replace(".", "\\.")}`) }).closest("li")!;
		expect(within(row("pricing.md")).getByRole("img", { name: "Published" })).toBeInTheDocument();
		expect(within(row("caching-options.md")).queryByRole("img", { name: "Published" })).not.toBeInTheDocument();

		await user.click(within(row("pricing.md")).getByRole("button", { name: "More actions: pricing.md" }));
		expect(await screen.findByRole("menuitem", { name: "Copy link" })).toBeInTheDocument();
		expect(screen.getByRole("menuitem", { name: "Unpublish" })).toBeInTheDocument();
		expect(screen.queryByRole("menuitem", { name: "Publish" })).not.toBeInTheDocument();
		await user.keyboard("{Escape}");

		await user.click(within(row("caching-options.md")).getByRole("button", { name: "More actions: caching-options.md" }));
		expect(await screen.findByRole("menuitem", { name: "Publish" })).toBeInTheDocument();
		expect(screen.queryByRole("menuitem", { name: "Unpublish" })).not.toBeInTheDocument();
		expect(screen.queryByRole("menuitem", { name: "Copy link" })).not.toBeInTheDocument();
	});

	it.each(["list", "gallery"] as const)("names the globe Published on hover in %s view", async (view) => {
		const published = session("ao-2", "Review pricing", [{ name: "pricing.md", updatedAt: "2026-10-10T10:00:00Z" }]);
		published.artifactFiles![0].shareUrl = "https://share.test/pricing";
		mocks.workspaces = [{ ...withArtifacts[0], sessions: [published] }];
		useArtifactsViewStore.setState({ view });
		renderView();
		const globe = within(artifactList()).getByRole("img", { name: "Published" });
		fireEvent.pointerMove(globe, { pointerType: "mouse" });
		expect(await screen.findByRole("tooltip")).toHaveTextContent("Published");
	});

	it.each(["list", "gallery"] as const)("marks a private link with an amber link, not the globe, in %s view", async (view) => {
		const published = session("ao-2", "Review pricing", [
			{ name: "pricing.md", updatedAt: "2026-10-10T10:00:00Z" },
			{ name: "roadmap.md", updatedAt: "2026-10-09T10:00:00Z" },
		]);
		published.artifactFiles![0].shareUrl = "https://share.test/pricing";
		published.artifactFiles![1].shareUrl = "https://share.test/roadmap";
		mocks.shareAccess = new Map([["https://share.test/pricing", "restricted"], ["https://share.test/roadmap", "anyone"]]);
		mocks.workspaces = [{ ...withArtifacts[0], sessions: [published] }];
		useArtifactsViewStore.setState({ view });
		renderView();
		const item = (name: string) => within(artifactList()).getByRole("button", { name: new RegExp(`^${name.replace(".", "\\.")}`) }).closest("li")!;
		const shared = within(item("pricing.md")).getByRole("img", { name: "Shared privately" });
		expect(shared).toHaveClass("text-status-in-review");
		expect(shared.querySelector("svg")).toHaveClass("icon-link");
		expect(within(item("pricing.md")).queryByRole("img", { name: "Published" })).toBeNull();
		expect(within(item("roadmap.md")).getByRole("img", { name: "Published" })).toHaveClass("text-success");
		expect(within(item("roadmap.md")).getByRole("img", { name: "Published" }).querySelector("svg")).toHaveClass("icon-globe");
		fireEvent.pointerMove(shared, { pointerType: "mouse" });
		expect(await screen.findByRole("tooltip")).toHaveTextContent("Shared privately");
	});

	it("marks a published artifact with a muted link until the link list says who it opens for", async () => {
		const published = session("ao-2", "Review pricing", [{ name: "pricing.md", updatedAt: "2026-10-10T10:00:00Z" }]);
		published.artifactFiles![0].shareUrl = "https://share.test/pricing";
		mocks.workspaces = [{ ...withArtifacts[0], sessions: [published] }];
		useArtifactsViewStore.setState({ view: "list" });
		renderView();
		const mark = within(artifactList()).getByRole("img", { name: "Published" });
		expect(mark).toHaveAttribute("data-artifact-share-mark", "published");
		expect(mark).toHaveClass("text-muted-foreground");
		expect(mark).not.toHaveClass("text-success");
	});

	it("filters to privately shared artifacts", async () => {
		const user = userEvent.setup();
		const published = session("ao-2", "Review pricing", [
			{ name: "pricing.md", updatedAt: "2026-10-10T10:00:00Z" },
			{ name: "roadmap.md", updatedAt: "2026-10-09T10:00:00Z" },
		]);
		published.artifactFiles![0].shareUrl = "https://share.test/pricing";
		published.artifactFiles![1].shareUrl = "https://share.test/roadmap";
		mocks.shareAccess = new Map([["https://share.test/pricing", "restricted"], ["https://share.test/roadmap", "anyone"]]);
		mocks.workspaces = [{ ...withArtifacts[0], sessions: [withArtifacts[0].sessions[0], published] }, withArtifacts[1]];
		renderView();
		const rows = () => within(artifactList()).getAllByRole("listitem").map((row) => row.textContent ?? "");

		await user.click(screen.getByRole("button", { name: "Filter" }));
		await user.click(screen.getByRole("menuitemcheckbox", { name: "Shared privately" }));
		expect(rows()).toEqual([expect.stringContaining("pricing.md")]);
		await user.click(screen.getByRole("menuitemcheckbox", { name: "Public" }));
		expect(rows()).toEqual([expect.stringContaining("pricing.md"), expect.stringContaining("roadmap.md")]);
	});

	it("filters to public artifacts, or ones not shared, and removes the filter from its chip", async () => {
		const user = userEvent.setup();
		const published = session("ao-2", "Review pricing", [{ name: "pricing.md", updatedAt: "2026-10-10T10:00:00Z" }]);
		published.artifactFiles![0].shareUrl = "https://share.test/pricing";
		mocks.shareAccess = new Map([["https://share.test/pricing", "anyone"]]);
		mocks.workspaces = [{ ...withArtifacts[0], sessions: [withArtifacts[0].sessions[0], published] }, withArtifacts[1]];
		renderView();
		const rows = () => within(artifactList()).getAllByRole("listitem").map((row) => row.textContent ?? "");

		await user.click(screen.getByRole("button", { name: "Filter" }));
		await user.click(screen.getByRole("menuitemcheckbox", { name: "Public" }));
		expect(rows()).toEqual([expect.stringContaining("pricing.md")]);
		expect(screen.getByRole("button", { name: "Filter" })).toHaveAttribute("data-active", "true");
		await user.click(screen.getByRole("menuitemcheckbox", { name: "Public" }));
		await user.click(screen.getByRole("menuitemcheckbox", { name: "Not shared" }));
		expect(rows()).toEqual([expect.stringContaining("artifacts-user-flow.html"), expect.stringContaining("caching-options.md")]);
		// Filters narrow together: unshared artifacts of the Scratchpad.
		await user.click(screen.getByRole("menuitemcheckbox", { name: "Sessions" }));
		expect(rows()).toEqual([expect.stringContaining("caching-options.md")]);
		await user.keyboard("{Escape}");
		expect(screen.getByRole("button", { name: "Remove filter Project: Sessions" })).toBeInTheDocument();

		await user.click(screen.getByRole("button", { name: "Remove filter Sharing: Not shared" }));
		await user.click(screen.getByRole("button", { name: "Remove filter Project: Sessions" }));
		expect(rows()).toHaveLength(3);
		expect(screen.getByRole("button", { name: "Filter" })).not.toHaveAttribute("data-active");
	});

	it.each(["{Enter}", "[Space]"])("opens a card's menu with %s before the pointer has visited the card, and Escape returns focus", async (key) => {
		const user = userEvent.setup();
		renderView();
		const project = within(artifactList());
		// The card draws stand-in buttons until focus or the pointer reaches it; focus moves to the menu's own.
		act(() => project.getByRole("button", { name: "More actions: pricing.md" }).focus());
		expect(project.getByRole("button", { name: "More actions: pricing.md" })).toHaveFocus();
		await user.keyboard(key);
		expect(await screen.findByRole("menuitem", { name: "Delete artifact" })).toBeInTheDocument();
		await user.keyboard("{Escape}");
		await waitFor(() => expect(screen.queryByRole("menuitem", { name: "Delete artifact" })).not.toBeInTheDocument());
		expect(project.getByRole("button", { name: "More actions: pricing.md" })).toHaveFocus();
	});

	it("starts a pin tooltip from the one pointer move that reached the card", async () => {
		renderView();
		const project = within(artifactList());
		// The pointer comes to rest on Pin: that move lands on the stand-in, before the tooltip trigger exists.
		fireEvent.pointerMove(project.getByRole("button", { name: "Pin artifact: pricing.md" }), { pointerType: "mouse" });
		expect(screen.queryByRole("tooltip")).not.toBeInTheDocument();
		expect(await screen.findByRole("tooltip")).toHaveTextContent("Pin artifact");
	});

	it("pins from a first tap on a card the pointer has not moved over", async () => {
		renderView();
		const project = within(artifactList());
		const pin = project.getByRole("button", { name: "Pin artifact: pricing.md" });
		// A browser sends the click only to the button pressed, and only while it is still in the page.
		fireEvent.pointerDown(pin, { button: 0, pointerType: "touch", isPrimary: true });
		act(() => pin.focus());
		await act(async () => {});
		fireEvent.pointerUp(pin, { button: 0, pointerType: "touch", isPrimary: true });
		expect(pin).toBeInTheDocument();
		fireEvent.click(pin);
		expect(within(artifactList()).getByText("pricing.md")).toBeInTheDocument();
		// A pressed tooltip trigger stays quiet when focus lands on it, as it does without the stand-in.
		expect(screen.queryByRole("tooltip")).not.toBeInTheDocument();
	});

	it("moves a pinned artifact to the top of the list, marked, and remembers the pin on return", async () => {
		const user = userEvent.setup();
		const names = () => within(artifactList()).getAllByRole("listitem").map((row) => row.textContent ?? "");
		const { unmount } = renderView();
		await user.click(screen.getByRole("button", { name: "List view" }));
		await user.click(within(artifactList()).getByRole("button", { name: "Pin artifact: pricing.md" }));
		expect(names()).toEqual([expect.stringContaining("pricing.md"), expect.stringContaining("artifacts-user-flow.html"), expect.stringContaining("caching-options.md")]);
		expect(within(artifactList()).getAllByRole("listitem")).toHaveLength(3);
		expect(within(within(artifactList()).getAllByRole("listitem")[0]).getByRole("img", { name: "Pinned" })).toBeInTheDocument();
		expect(within(artifactList()).getByRole("button", { name: "Unpin artifact: pricing.md" })).toHaveAttribute("aria-pressed", "true");
		expect(JSON.parse(window.localStorage.getItem("ao.artifacts.view") ?? "{}").pinnedArtifactIds).toHaveLength(1);
		unmount();
		renderView();
		await user.click(within(artifactList()).getByRole("button", { name: "Unpin artifact: pricing.md" }));
		expect(names()).toEqual([expect.stringContaining("artifacts-user-flow.html"), expect.stringContaining("pricing.md"), expect.stringContaining("caching-options.md")]);
		expect(screen.queryByRole("img", { name: "Pinned" })).not.toBeInTheDocument();
	});

	it("remembers list view when returning", async () => {
		const user = userEvent.setup();
		const { unmount } = renderView();
		await user.click(screen.getByRole("button", { name: "List view" }));
		unmount();
		renderView();
		expect(screen.getByRole("button", { name: "List view" })).toHaveAttribute("aria-pressed", "true");
		expect(JSON.parse(window.localStorage.getItem("ao.artifacts.view") ?? "{}")).toMatchObject({ view: "list" });
	});

	it("shows a placeholder for text artifacts without a usable URL", () => {
		renderView();
		const project = within(artifactList());
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
		expect(within(artifactList()).getByTitle("No preview available")).toBeInTheDocument();
	});

	it("asks again about a page whose check failed for a passing reason, but keeps a good answer", async () => {
		const html = session("ao-html", "Design review", [{ name: "page.html", kind: "html", updatedAt: "2026-10-11T10:00:00Z" }]);
		html.artifactFiles![0].inlineUrl = "http://ao-inline-artifact.ao-html.localhost:3002/page.html";
		html.artifactFiles![0].rawUrl = "http://127.0.0.1:3002/page.html?raw=true";
		mocks.workspaces = [{ id: "ao", name: "AO", path: "/code/ao", sessions: [html] }];
		const fetch = vi.fn()
			.mockResolvedValueOnce(new Response(null, { status: 503 }))
			.mockResolvedValue(new Response("<h1>Design review</h1>"));
		vi.stubGlobal("fetch", fetch);
		// One query cache across mounts, as the app keeps while a card scrolls away and back.
		const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
		const tree = (mount: string) => (
			<QueryClientProvider client={client}>
				<TooltipProvider>
					<ArtifactsView key={mount} />
				</TooltipProvider>
			</QueryClientProvider>
		);
		const view = render(tree("first"));
		await waitFor(() => expect(within(artifactList()).getByTitle("No preview available")).toBeInTheDocument());
		expect(view.container.querySelector("iframe")).toBeNull();
		// The daemon was briefly unavailable: the next mount asks again and frames the page.
		view.rerender(tree("second"));
		await waitFor(() => expect(view.container.querySelector("iframe")).not.toBeNull());
		expect(fetch).toHaveBeenCalledTimes(2);
		// A good answer is kept for this version of the file.
		view.rerender(tree("third"));
		await waitFor(() => expect(view.container.querySelector("iframe")).not.toBeNull());
		expect(fetch).toHaveBeenCalledTimes(2);
	});

	it("draws a pinned page once, at the top, with one live thumbnail", async () => {
		const html = session("ao-html", "Design review", [{ name: "page.html", kind: "html", updatedAt: "2026-10-11T10:00:00Z" }]);
		html.artifactFiles![0].inlineUrl = "http://ao-inline-artifact.ao-html.localhost:3002/page.html";
		html.artifactFiles![0].rawUrl = "http://127.0.0.1:3002/page.html?raw=true";
		mocks.workspaces = [{ id: "ao", name: "AO", path: "/code/ao", sessions: [html] }, withArtifacts[1]];
		useArtifactsViewStore.setState({ pinnedArtifactIds: [JSON.stringify(["ao-html", "page.html"])] });
		const fetch = vi.fn().mockImplementation(async () => new Response("<h1>Design review</h1>"));
		vi.stubGlobal("fetch", fetch);
		const { container } = renderView();

		await waitFor(() => expect(container.querySelectorAll("iframe")).toHaveLength(1));
		const rows = within(artifactList()).getAllByRole("listitem");
		expect(rows).toHaveLength(2);
		expect(rows[0].querySelector("iframe")).not.toBeNull();
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

	it("caps cloud HTML thumbnails at four, the pinned page first", async () => {
		const pages: Parameters<typeof session>[2] = Array.from({ length: 6 }, (_, index) => ({ name: `page-${index}.html`, kind: "html", updatedAt: "2026-10-11T10:00:00Z" }));
		const cloud = session("cloud-html", "Cloud design review", pages);
		cloud.cloud = { orgId: "org-1" };
		mocks.workspaces = [{ id: "cloud", name: "Cloud", path: "", kind: CLOUD_PROJECT_KIND, sessions: [cloud] }, withArtifacts[1]];
		useArtifactsViewStore.setState({ pinnedArtifactIds: [JSON.stringify(["cloud-html", "page-0.html"])] });
		const { container } = renderView();

		await waitFor(() => expect(container.querySelectorAll("iframe")).toHaveLength(4));
		const rows = within(artifactList()).getAllByRole("listitem");
		expect(rows).toHaveLength(7);
		expect(rows[0].textContent).toContain("page-0.html");
		expect(rows[0].querySelector("iframe")).not.toBeNull();
		for (const frame of container.querySelectorAll("iframe")) {
			expect(frame.src).toMatch(/^https:\/\/cp.test\/artifact-files\/token\/page-\d.html\?v=/);
		}
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
		await waitFor(() => expect(within(artifactList()).getByTitle("No preview available")).toBeInTheDocument());
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

	it("shows image previews and falls back to a placeholder when the image fails", async () => {
		const image = session("ao-image", "Cover image", [{ name: "cover.png", kind: "file", updatedAt: "2026-10-11T10:00:00Z" }]);
		image.artifactFiles![0].rawUrl = "http://127.0.0.1:3002/cover.png";
		mocks.workspaces = [{ id: "ao", name: "AO", path: "/code/ao", sessions: [image] }];
		// No small copy can be made here, so the card shows the original.
		vi.stubGlobal("fetch", vi.fn().mockRejectedValue(new Error("offline")));
		renderView();
		const project = within(artifactList());
		const card = project.getByRole("button", { name: /^cover\.png/ }).closest("li")!;
		await waitFor(() => expect(card.querySelector("img")).not.toBeNull());
		const preview = card.querySelector("img")!;
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

	it("lists an orchestrator's artifacts and opens them in the orchestrator session", async () => {
		const user = userEvent.setup();
		mocks.workspaces = [{
			id: "ao",
			name: "agent-orchestrator",
			path: "/code/ao",
			sessions: [
				{ ...session("ao-48", "ao-48", [{ name: "weekly-status.html", updatedAt: "2026-10-12T10:00:00Z", kind: "html" }]), kind: "orchestrator" },
				session("ao-1", "Design the Artifacts entry point", [{ name: "artifacts-user-flow.html", updatedAt: "2026-10-11T10:00:00Z", kind: "html" }]),
			],
		}, withArtifacts[1]];
		renderView();

		const project = within(artifactList());
		expect(project.getAllByRole("listitem").map((row) => row.textContent)).toEqual([
			expect.stringContaining("weekly-status.html"),
			expect.stringContaining("artifacts-user-flow.html"),
			expect.stringContaining("caching-options.md"),
		]);
		// Named like every other artifact: its project, then its session.
		expect(project.getByText("agent-orchestrator · Orchestrator")).toBeInTheDocument();
		await user.click(project.getByRole("button", { name: /^weekly-status\.html/ }));
		await user.click(within(screen.getByRole("dialog", { name: "weekly-status.html" })).getByRole("button", { name: "Open in session" }));

		expect(mocks.navigate).toHaveBeenCalledWith({ to: "/projects/$projectId/sessions/$sessionId", params: { projectId: "ao", sessionId: "ao-48" } });
	});

	it("previews the artifact on this page before opening its source session", async () => {
		const user = userEvent.setup();
		renderView();

		await user.click(within(artifactList()).getByRole("button", { name: /^artifacts-user-flow\.html/ }));
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

	it("reloads an open local page when its agent rewrites it", async () => {
		const user = userEvent.setup();
		const page = session("ao-html", "Design review", [{ name: "page.html", kind: "html", updatedAt: "2026-10-11T10:00:00Z" }]);
		page.artifactFiles![0].inlineUrl = "http://ao-inline-artifact.ao-html.localhost:3002/page.html";
		page.artifactFiles![0].rawUrl = "http://127.0.0.1:3002/page.html?raw=true";
		vi.stubGlobal("fetch", vi.fn(async () => new Response("<h1>Design review</h1>")));
		mocks.workspaces = [{ id: "ao", name: "AO", path: "/code/ao", sessions: [page] }];
		const { refresh } = renderView();

		await user.click(within(artifactList()).getByRole("button", { name: /^page\.html/ }));
		const dialog = screen.getByRole("dialog", { name: "page.html" });
		const opened = dialog.querySelector("iframe");
		expect(opened).toHaveAttribute("src", page.artifactFiles![0].inlineUrl);
		expect(mocks.artifactFileChanges).toHaveBeenLastCalledWith({ id: "ao-html", cloud: undefined }, true);

		const rewritten = structuredClone(page);
		rewritten.artifactFiles![0].updatedAt = "2026-10-11T10:05:00Z";
		mocks.workspaces = [{ id: "ao", name: "AO", path: "/code/ao", sessions: [rewritten] }];
		refresh();

		const reloaded = screen.getByRole("dialog", { name: "page.html" }).querySelector("iframe");
		expect(reloaded).toHaveAttribute("src", page.artifactFiles![0].inlineUrl);
		expect(reloaded).not.toBe(opened);
	});

	it("follows a cloud preview's changes through its cloud session", async () => {
		const user = userEvent.setup();
		const cloud = session("cloud-1", "Cloud notes", [{ name: "notes.md", updatedAt: "2026-10-11T10:00:00Z" }]);
		cloud.cloud = { orgId: "org-1" } as WorkspaceSession["cloud"];
		mocks.workspaces = [{ id: "cloud", name: "Cloud", path: "", kind: CLOUD_PROJECT_KIND, sessions: [cloud] }];
		renderView();

		await user.click(within(artifactList()).getByRole("button", { name: /^notes\.md/ }));
		expect(screen.getByRole("dialog", { name: "notes.md" })).toBeInTheDocument();
		expect(mocks.artifactFileChanges).toHaveBeenLastCalledWith({ id: "cloud-1", cloud: { orgId: "org-1" } }, true);
	});

	it("opens a Scratchpad artifact on the standalone session route", async () => {
		const user = userEvent.setup();
		renderView();

		await user.click(within(artifactList()).getByRole("button", { name: /^caching-options\.md/ }));
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
		const project = within(artifactList());
		expect(project.queryByRole("button", { name: "Delete artifact: pricing.md" })).not.toBeInTheDocument();
		await user.click(project.getByRole("button", { name: "Open in session: pricing.md" }));
		expect(screen.queryByRole("dialog")).not.toBeInTheDocument();
		expect(mocks.navigate).toHaveBeenCalledWith({ to: "/projects/$projectId/sessions/$sessionId", params: { projectId: "ao", sessionId: "ao-2" } });
	});

	it("keeps pin and delete in the modal menu without duplicating Open in session there", async () => {
		const user = userEvent.setup();
		renderView();
		await user.click(within(artifactList()).getByRole("button", { name: /^pricing\.md/ }));
		const modal = within(screen.getByRole("dialog", { name: "pricing.md" }));
		expect(modal.queryByRole("button", { name: "Pin artifact: pricing.md" })).not.toBeInTheDocument();
		expect(modal.getByRole("button", { name: "Open in session" })).toBeInTheDocument();
		expect(await modal.findByRole("button", { name: "Publish: pricing.md" })).toBeInTheDocument();
		await user.click(modal.getByRole("button", { name: "More actions: pricing.md" }));
		expect(screen.getByRole("menuitem", { name: "Publish" })).toBeInTheDocument();
		expect(screen.queryByRole("menuitem", { name: "Open in session" })).not.toBeInTheDocument();
		expect(screen.getByRole("menuitem", { name: "Delete artifact" })).toBeInTheDocument();
		await user.click(screen.getByRole("menuitem", { name: "Pin artifact" }));
		expect(useArtifactsViewStore.getState().pinnedArtifactIds).toHaveLength(1);
		expect(mocks.navigate).not.toHaveBeenCalled();
	});

	it("closes a preview with Escape and restores focus without navigating", async () => {
		const user = userEvent.setup();
		renderView();
		const trigger = within(artifactList()).getByRole("button", { name: /^pricing\.md/ });
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
		await user.click(within(artifactList()).getByRole("button", { name: /^pricing\.md/ }));
		expect(screen.getByRole("dialog", { name: "pricing.md" })).toBeInTheDocument();
		expect(mocks.navigate).not.toHaveBeenCalled();
		await user.click(screen.getByRole("button", { name: "Close" }));
		expect(screen.queryByRole("dialog")).not.toBeInTheDocument();
	});

	it("opens a pinned artifact in a preview without navigating", async () => {
		const user = userEvent.setup();
		renderView();
		await user.click(within(artifactList()).getByRole("button", { name: "Pin artifact: pricing.md" }));
		await user.click(within(artifactList()).getByRole("button", { name: /^pricing\.md/ }));
		expect(screen.getByRole("dialog", { name: "pricing.md" })).toBeInTheDocument();
		expect(mocks.navigate).not.toHaveBeenCalled();
	});

	it("previews HTML on its isolated origin inside the modal", async () => {
		const user = userEvent.setup();
		const html = session("ao-html", "Design review", [{ name: "page.html", kind: "html", updatedAt: "2026-10-11T10:00:00Z" }]);
		html.artifactFiles![0].inlineUrl = "http://ao-inline-artifact.ao-html.localhost:3002/page.html";
		mocks.workspaces = [{ id: "ao", name: "AO", path: "/code/ao", sessions: [html] }];
		renderView();
		await user.click(within(artifactList()).getByRole("button", { name: /^page\.html/ }));
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
		expectMenuItems(["Sessions", "agent-orchestrator"]);
		await user.click(screen.getByRole("menuitem", { name: "Sessions" }));

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
		expectMenuItems(["Sessions", "agent-orchestrator", "px0"]);
	});

	it("gives each project the sidebar's icon in the New artifact menu", async () => {
		const user = userEvent.setup();
		renderView();
		// The letter tile is decoration beside the project's name, as in the sidebar.
		await user.click(screen.getByRole("button", { name: "New artifact" }));
		const item = screen.getByRole("menuitem", { name: "agent-orchestrator" });
		expect(item.querySelector('[aria-hidden="true"]')?.textContent).toBe("A");
		// Scratchpad keeps its file icon: it is not a project.
		expect(screen.getByRole("menuitem", { name: "Sessions" }).textContent).toBe("Sessions");
	});

	it("starts a Scratchpad artifact session without a project id and opens it", async () => {
		const user = userEvent.setup();
		mocks.workspaces = withoutArtifacts;
		mocks.post.mockResolvedValue({ data: { session: { id: "standalone-9" } } });
		renderView();

		await user.click(screen.getByRole("button", { name: "Create artifact" }));
		await user.click(screen.getByRole("menuitem", { name: "Sessions" }));

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
		await user.click(screen.getByRole("menuitem", { name: "Sessions" }));

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
		expect(useUiStore.getState().settingsModal).toMatchObject({ scope: "global", section: "harness" });
	});
});
