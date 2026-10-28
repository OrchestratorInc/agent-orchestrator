import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { act, render, screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import type { ReactNode } from "react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { useArtifactsViewStore } from "../stores/artifacts-view-store";
import { aoBridge } from "../lib/bridge";
import type { SessionArtifact, WorkspaceSession } from "../types/workspace";
import { SessionArtifactsView } from "./SessionArtifactsView";
import { TooltipProvider } from "./ui/tooltip";

const { cloudAccessMock, deleteArtifactMock, deleteCloudArtifactMock, openPreviewMock, sendSessionMessageMock } = vi.hoisted(() => ({
	cloudAccessMock: vi.fn(),
	deleteCloudArtifactMock: vi.fn(),
	sendSessionMessageMock: vi.fn(),
	deleteArtifactMock: vi.fn(),
	openPreviewMock: vi.fn(),
}));

vi.mock("../hooks/useSessionWorkspaceFiles", () => ({
	CloudArtifactAccessError: class extends Error {},
	CLOUD_TEXT_ARTIFACT: /\.(md|markdown|txt|html?)$/i,
	deleteSessionArtifactFile: deleteArtifactMock,
	sessionArtifactFileQueryOptions: (sessionId: string, path: string) => ({
		queryKey: ["artifact-file", sessionId, path],
		queryFn: async () => (path.endsWith(".png") ? { binary: true, content: "" } : { content: `contents of ${path}`, editable: true, fileFingerprint: "fingerprint" }),
	}),
}));
vi.mock("../hooks/useCloudCp", () => ({
	useCloudCp: () => ({
		client: { sendSessionMessage: sendSessionMessageMock, deleteSessionArtifact: deleteCloudArtifactMock },
		ready: true,
		baseUrl: "https://cp.test",
		userId: "u-1",
	}),
}));
vi.mock("./ArtifactFileView", () => ({
	ArtifactFileView: ({ cloud, feedbackRequestKey, initialEditing, path, rawUrl, sendMessage }: { cloud?: boolean; feedbackRequestKey?: number; initialEditing?: boolean; path: string; rawUrl?: string; sendMessage?: (message: string) => Promise<void> }) => (
		<div data-editing={initialEditing ? "true" : "false"} data-cloud={cloud ? "true" : ""} data-feedback-key={feedbackRequestKey ?? ""} data-raw-url={rawUrl ?? ""} data-testid="artifact-view">
			{`viewer:${path}`}
			{sendMessage ? <button type="button" onClick={() => void sendMessage("looks good")}>send feedback</button> : null}
		</div>
	),
}));
vi.mock("./CloudArtifactFrame", () => ({
	CloudArtifactFrame: ({ url, onAccessRefused, onNavigate }: { url: string; onAccessRefused?: () => void; onNavigate?: (path: string) => void }) => (
		<div data-testid="cloud-frame">
			{url}
			<button type="button" onClick={onAccessRefused}>refuse</button>
			<button type="button" onClick={() => onNavigate?.("docs/notes.md")}>open notes</button>
			<button type="button" onClick={() => onNavigate?.("assets/logo.png")}>open asset</button>
		</div>
	),
}));
vi.mock("../hooks/useCloudArtifacts", async (importOriginal) => ({
	...(await importOriginal<typeof import("../hooks/useCloudArtifacts")>()),
	useCloudArtifactAccess: cloudAccessMock,
}));

const artifacts: SessionArtifact[] = [
	{ kind: "markdown", name: "notes.md", path: "notes.md", size: 12, updatedAt: "2026-09-22T00:00:00Z" },
	{ kind: "html", name: "report.html", path: "report.html", previewUrl: "http://artifact.localhost/report.html", inlineUrl: "http://ao-inline-artifact.sess-1.localhost/report.html", size: 20, updatedAt: "2026-09-23T00:00:00Z" },
	{ kind: "file", name: "plan.md", path: "reports/plan.md", size: 20, updatedAt: "2026-09-21T00:00:00Z" },
];

function session(artifactFiles: SessionArtifact[] = artifacts): WorkspaceSession {
	return { id: "sess-1", artifactFiles } as WorkspaceSession;
}

function renderView(ui: ReactNode) {
	const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
	const wrap = (node: ReactNode) => (
		<QueryClientProvider client={client}>
			<TooltipProvider>{node}</TooltipProvider>
		</QueryClientProvider>
	);
	const view = render(wrap(ui));
	return { ...view, rerender: (next: ReactNode) => view.rerender(wrap(next)) };
}

const CLOUD_ROOT = "https://cp.test/api/cloud/v1/artifact-files/token/";

function cloudSession(): WorkspaceSession {
	return {
		id: "cloud-1",
		cloud: { orgId: "org-1" },
		artifactFiles: [
			{ kind: "html", name: "report.html", path: "report.html", size: 20, updatedAt: "2026-09-23T00:00:00Z" },
			{ kind: "markdown", name: "notes.md", path: "docs/notes.md", size: 12, updatedAt: "2026-09-22T00:00:00Z" },
		],
	} as WorkspaceSession;
}

beforeEach(() => {
	vi.stubGlobal("IntersectionObserver", class {
		constructor(private callback: (entries: { isIntersecting: boolean }[]) => void) {}
		observe() { this.callback([{ isIntersecting: true }]); }
		disconnect() {}
	});
});
afterEach(() => vi.unstubAllGlobals());

describe("SessionArtifactsView for a cloud session", () => {
	beforeEach(() => {
		openPreviewMock.mockReset();
		cloudAccessMock.mockReset().mockReturnValue({ data: CLOUD_ROOT, isError: false, refetch: vi.fn() });
		sendSessionMessageMock.mockReset().mockResolvedValue({});
	});

	it("sends feedback on an html artifact to the worker through the control plane", async () => {
		renderView(<SessionArtifactsView session={cloudSession()} />);
		await userEvent.click(screen.getByRole("button", { name: /^report\.html/ }));
		await userEvent.click(screen.getByRole("button", { name: "Add feedback" }));
		await userEvent.type(screen.getByRole("textbox"), "make the chart wider");
		await userEvent.keyboard("{Meta>}{Enter}{/Meta}");

		expect(sendSessionMessageMock).toHaveBeenCalledWith("org-1", "cloud-1", { text: expect.stringContaining("make the chart wider") });
		expect(sendSessionMessageMock.mock.calls[0][2].text).toContain("report.html");
	});

	it("opens html feedback when it was requested", async () => {
		renderView(<SessionArtifactsView request={{ feedback: true, key: 3, path: "report.html" }} session={cloudSession()} />);

		expect(await screen.findByRole("textbox")).toBeInTheDocument();
	});

	it("sends feedback on a markdown artifact to the worker through the control plane", async () => {
		renderView(<SessionArtifactsView session={cloudSession()} />);
		await userEvent.click(screen.getByRole("button", { name: /^notes\.md/ }));
		await userEvent.click(screen.getByRole("button", { name: "send feedback" }));

		expect(sendSessionMessageMock).toHaveBeenCalledWith("org-1", "cloud-1", { text: "looks good" });
	});

	it("frames an html artifact from its access URL instead of the Browser panel", async () => {
		renderView(<SessionArtifactsView session={cloudSession()} />);
		await userEvent.click(screen.getByRole("button", { name: /^report\.html/ }));

		expect(screen.getByTestId("cloud-frame")).toHaveTextContent(`${CLOUD_ROOT}report.html?v=2026-09-23T00%3A00%3A00Z`);
		expect(openPreviewMock).not.toHaveBeenCalled();
	});

	it("reloads an open html artifact when the file changes", async () => {
		const { rerender } = renderView(<SessionArtifactsView session={cloudSession()} />);
		await userEvent.click(screen.getByRole("button", { name: /^report\.html/ }));

		const changed = cloudSession();
		changed.artifactFiles = changed.artifactFiles!.map((file) => (file.path === "report.html" ? { ...file, updatedAt: "2026-09-24T00:00:00Z" } : file));
		rerender(<SessionArtifactsView session={changed} />);

		expect(screen.getByTestId("cloud-frame")).toHaveTextContent(`${CLOUD_ROOT}report.html?v=2026-09-24T00%3A00%3A00Z`);
	});

	it("keeps an open file's URL while the access prefix is refreshed", async () => {
		const { rerender } = renderView(<SessionArtifactsView session={cloudSession()} />);
		await userEvent.click(screen.getByRole("button", { name: /^report\.html/ }));
		cloudAccessMock.mockReturnValue({ data: "https://cp.test/api/cloud/v1/artifact-files/fresh/", isError: false, refetch: vi.fn() });
		rerender(<SessionArtifactsView session={cloudSession()} />);

		expect(screen.getByTestId("cloud-frame")).toHaveTextContent(`${CLOUD_ROOT}report.html`);
	});

	it("takes a fresh access prefix after the open file's link was refused", async () => {
		const refetch = vi.fn();
		cloudAccessMock.mockReturnValue({ data: CLOUD_ROOT, isError: false, refetch });
		const { rerender } = renderView(<SessionArtifactsView session={cloudSession()} />);
		await userEvent.click(screen.getByRole("button", { name: /^report\.html/ }));
		await userEvent.click(screen.getByRole("button", { name: "refuse" }));
		expect(refetch).toHaveBeenCalled();
		const fresh = "https://cp.test/api/cloud/v1/artifact-files/fresh/";
		cloudAccessMock.mockReturnValue({ data: fresh, isError: false, refetch });
		rerender(<SessionArtifactsView session={cloudSession()} />);

		expect(screen.getByTestId("cloud-frame")).toHaveTextContent(`${fresh}report.html`);
	});

	it("offers a retry when the access prefix cannot be fetched", async () => {
		const refetch = vi.fn();
		cloudAccessMock.mockReturnValue({ data: undefined, isError: true, refetch });
		renderView(<SessionArtifactsView session={cloudSession()} />);
		await userEvent.click(screen.getByRole("button", { name: /^report\.html/ }));
		await userEvent.click(screen.getByRole("button", { name: /retry/i }));

		expect(refetch).toHaveBeenCalled();
	});

	it("opens a markdown artifact in the viewer through the cloud fetch", async () => {
		renderView(<SessionArtifactsView session={cloudSession()} />);
		await userEvent.click(screen.getByRole("button", { name: /^notes\.md/ }));

		const viewer = screen.getByTestId("artifact-view");
		expect(viewer).toHaveAttribute("data-cloud", "true");
		expect(viewer).toHaveAttribute("data-raw-url", `${CLOUD_ROOT}docs/notes.md?v=2026-09-22T00%3A00%3A00Z`);
	});

	it("says when the files cannot be reached", async () => {
		cloudAccessMock.mockReturnValue({ data: undefined, isError: true, refetch: vi.fn() });
		renderView(<SessionArtifactsView session={cloudSession()} />);
		await userEvent.click(screen.getByRole("button", { name: /^report\.html/ }));

		expect(screen.getByText("Couldn’t load this session’s artifacts. Try again.")).toBeInTheDocument();
	});

	it("names the file a framed page opened in the header", async () => {
		renderView(<SessionArtifactsView session={cloudSession()} />);
		await userEvent.click(screen.getByRole("button", { name: /^report\.html/ }));
		await userEvent.click(screen.getByRole("button", { name: "open asset" }));
		expect(screen.getByRole("button", { name: "report.html" })).toBeInTheDocument();
		await userEvent.click(screen.getByRole("button", { name: "open notes" }));
		expect(screen.getByRole("button", { name: "notes.md" })).toBeInTheDocument();
	});

	it("downloads a cloud file from the session's current access link", async () => {
		const fetchFile = vi.spyOn(aoBridge.cloudArtifact, "fetch").mockResolvedValue(new Uint8Array([1, 2]));
		const save = vi.spyOn(aoBridge.chatImage, "save").mockResolvedValue(true);
		const session = cloudSession();
		session.artifactFiles = [{ kind: "file", name: "report.pdf", path: "report.pdf", size: 2, updatedAt: "2026-09-24T00:00:00Z" }];
		renderView(<SessionArtifactsView session={session} />);
		await userEvent.click(screen.getByRole("button", { name: /report\.pdf/ }));
		await userEvent.click(screen.getByRole("button", { name: "report.pdf" }));
		await userEvent.click(await screen.findByRole("menuitem", { name: "Download" }));

		expect(fetchFile).toHaveBeenCalledWith(`${CLOUD_ROOT}report.pdf?v=2026-09-24T00%3A00%3A00Z`);
		expect(save).toHaveBeenCalledWith({ name: "report.pdf", bytes: new Uint8Array([1, 2]) });
		fetchFile.mockRestore();
		save.mockRestore();
	});

	it("offers no local folder actions", async () => {
		renderView(<SessionArtifactsView session={cloudSession()} />);
		expect(screen.queryAllByRole("button", { name: /^Open in / })).toHaveLength(0);
		await userEvent.click(screen.getByRole("button", { name: /^notes\.md/ }));
		await userEvent.click(screen.getByRole("button", { name: "notes.md" }));
		expect(screen.queryByRole("menuitem", { name: /^Show in / })).toBeNull();
	});

	it("deletes a cloud file from cloud storage and the sandbox after confirming", async () => {
		deleteCloudArtifactMock.mockReset().mockResolvedValue(undefined);
		useArtifactsViewStore.setState({ pinnedArtifactIds: [JSON.stringify(["cloud-1", "docs/notes.md"])] });
		renderView(<SessionArtifactsView session={cloudSession()} />);
		await userEvent.click(screen.getByRole("button", { name: /^notes\.md/ }));
		await userEvent.click(screen.getByRole("button", { name: "notes.md" }));
		await userEvent.click(screen.getByRole("menuitem", { name: "Delete artifact" }));
		const dialog = await screen.findByRole("dialog");
		expect(dialog).toHaveTextContent("from the session's cloud storage and from its sandbox");
		expect(deleteCloudArtifactMock).not.toHaveBeenCalled();
		await userEvent.click(within(dialog).getByRole("button", { name: "Delete artifact" }));

		await vi.waitFor(() => expect(deleteCloudArtifactMock).toHaveBeenCalledWith("org-1", "cloud-1", "docs/notes.md"));
		await vi.waitFor(() => expect(useArtifactsViewStore.getState().pinnedArtifactIds).toEqual([]));
		expect(deleteArtifactMock).not.toHaveBeenCalled();
	});
});

describe("SessionArtifactsView", () => {
	beforeEach(() => {
		cloudAccessMock.mockReset().mockReturnValue({ data: undefined, isError: false, refetch: vi.fn() });
		openPreviewMock.mockReset();
		deleteArtifactMock.mockReset().mockResolvedValue(undefined);
	});

	it("lists every artifact newest first", () => {
		renderView(<SessionArtifactsView session={session()} />);

		expect(within(screen.getByRole("list")).getAllByRole("listitem").map((item) => within(item).getAllByRole("button")[0].textContent)).toEqual([
			expect.stringContaining("report.html"),
			expect.stringContaining("notes.md"),
			expect.stringContaining("plan.md"),
		]);
	});

	it("caps the sidebar gallery at four live HTML previews", async () => {
		const pages: SessionArtifact[] = Array.from({ length: 6 }, (_, index) => ({
			kind: "html",
			name: `page-${index}.html`,
			path: `page-${index}.html`,
			inlineUrl: `http://ao-inline-artifact.sess-1.localhost/page-${index}.html`,
			rawUrl: `http://127.0.0.1:3002/page-${index}.html?raw=true`,
			size: 20,
			updatedAt: "2026-09-23T00:00:00Z",
		}));
		const fetch = vi.fn().mockImplementation(async () => new Response("<h1>Preview</h1>"));
		vi.stubGlobal("fetch", fetch);
		const { container } = renderView(<SessionArtifactsView session={session(pages)} />);

		await vi.waitFor(() => expect(container.querySelectorAll("iframe")).toHaveLength(4));
		expect(screen.getAllByRole("listitem")).toHaveLength(6);
		expect(screen.getAllByTitle("No preview available")).toHaveLength(2);
		expect(fetch).toHaveBeenCalledTimes(4);
	});

	it("shows an empty state when the session has no artifacts", () => {
		renderView(<SessionArtifactsView session={session([])} />);

		expect(screen.getByText("No artifacts in this session yet.")).toBeInTheDocument();
		expect(screen.queryByRole("button", { name: /^Open in (?!session)/ })).not.toBeInTheDocument();
	});

	it("opens the artifact folder in the file manager and the terminal", async () => {
		const openArtifacts = vi.spyOn(aoBridge.editorHandoff, "openArtifacts");
		renderView(<SessionArtifactsView session={session()} />);

		const [fileManager, terminal] = screen.getAllByRole("button", { name: /^Open in (?!session)/ });
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

		await userEvent.click(screen.getAllByRole("button", { name: /^Open in (?!session)/ })[0]);

		expect(await screen.findByRole("alert")).toHaveTextContent("This session has no artifacts folder yet.");
		openArtifacts.mockRestore();
	});

	it("clears a failed open after a few seconds", async () => {
		vi.useFakeTimers({ shouldAdvanceTime: true });
		const openArtifacts = vi.spyOn(aoBridge.editorHandoff, "openArtifacts").mockRejectedValue(new Error("Could not open Terminal."));
		try {
			renderView(<SessionArtifactsView request={{ key: 1, path: "notes.md" }} session={session()} />);

			await userEvent.click(screen.getByRole("button", { name: "notes.md" }));
			await userEvent.click(screen.getByRole("menuitem", { name: "Show in Terminal" }));
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

		expect(screen.queryByRole("button", { name: /^Open in (?!session)/ })).not.toBeInTheDocument();
	});

	it("opens HTML in the artifact viewer and can switch to its source", async () => {
		renderView(<SessionArtifactsView session={session()} />);
		await userEvent.click(screen.getByRole("button", { name: /^report\.html/ }));
		expect(screen.queryByRole("dialog")).not.toBeInTheDocument();
		expect(document.querySelector("iframe")).toHaveAttribute("src", "http://ao-inline-artifact.sess-1.localhost/report.html");
		expect(document.querySelector("iframe")).toHaveAttribute("sandbox", "allow-scripts allow-same-origin");
		expect(screen.getByTestId("artifact-view").parentElement).toHaveClass("hidden");
		await userEvent.click(screen.getByRole("button", { name: "View source" }));
		expect(screen.getByTestId("artifact-view").parentElement).not.toHaveClass("hidden");
		expect(document.querySelector("iframe")).toBeNull();
		await userEvent.click(screen.getByRole("button", { name: "Rich preview" }));
		expect(document.querySelector("iframe")).not.toBeNull();
	});

	it("never frames an HTML artifact from the daemon's own origin", () => {
		const html: SessionArtifact = { kind: "html", name: "report.html", path: "report.html", inlineUrl: "http://127.0.0.1:3002/report.html", size: 20, updatedAt: "2026-09-23T00:00:00Z" };
		renderView(<SessionArtifactsView request={{ key: 1, path: "report.html" }} session={session([html])} />);
		expect(document.querySelector("iframe")).toBeNull();
		expect(screen.getByTestId("artifact-view")).toHaveTextContent("viewer:report.html");
	});

	it("opens a file artifact in the viewer and returns to the list", async () => {
		renderView(<SessionArtifactsView session={session()} />);

		await userEvent.click(screen.getByRole("button", { name: /^plan\.md/ }));
		expect(screen.getByTestId("artifact-view")).toHaveTextContent("viewer:reports/plan.md");

		await userEvent.click(screen.getByRole("button", { name: "Back to artifacts" }));
		expect(screen.queryByTestId("artifact-view")).not.toBeInTheDocument();
		expect(screen.getByRole("button", { name: /^notes\.md/ })).toBeInTheDocument();
	});

	it("opens a file card directly in the artifact viewer", async () => {
		renderView(<SessionArtifactsView session={session()} />);
		await userEvent.click(screen.getByRole("button", { name: /^notes\.md/ }));
		expect(screen.getByTestId("artifact-view")).toHaveTextContent("viewer:notes.md");
		expect(screen.queryByRole("dialog")).not.toBeInTheDocument();
	});

	it("edits a card in the artifact viewer without pin or open-in-session controls", async () => {
		renderView(<SessionArtifactsView session={session()} />);
		expect(screen.queryByRole("button", { name: /^Pin artifact:/ })).not.toBeInTheDocument();
		expect(screen.queryByRole("button", { name: /^Open in session:/ })).not.toBeInTheDocument();
		await userEvent.click(screen.getByRole("button", { name: "Edit file: notes.md" }));
		expect(await screen.findByTestId("artifact-view")).toHaveTextContent("viewer:notes.md");
		await vi.waitFor(() => expect(screen.getByTestId("artifact-view")).toHaveAttribute("data-editing", "true"));
	});

	it("limits the card menu to showing the file in the file manager", async () => {
		const openArtifacts = vi.spyOn(aoBridge.editorHandoff, "openArtifacts");
		renderView(<SessionArtifactsView session={session()} />);
		await userEvent.click(screen.getByRole("button", { name: "More actions: notes.md" }));
		const items = screen.getAllByRole("menuitem");
		expect(items).toHaveLength(1);
		expect(items[0]).toHaveTextContent(/^Show in /);
		await userEvent.click(items[0]);
		expect(openArtifacts).toHaveBeenCalledWith({ sessionId: "sess-1", targetId: "file-manager", path: "notes.md" });
		openArtifacts.mockRestore();
	});

	it("confirms a card deletion before removing the artifact", async () => {
		renderView(<SessionArtifactsView session={session()} />);
		await userEvent.click(screen.getByRole("button", { name: "Delete artifact: notes.md" }));
		expect(deleteArtifactMock).not.toHaveBeenCalled();
		const dialog = screen.getByRole("dialog");
		await userEvent.click(within(dialog).getByRole("button", { name: "Delete artifact" }));
		await vi.waitFor(() => expect(deleteArtifactMock).toHaveBeenCalledWith({ path: "notes.md", sessionId: "sess-1", hostId: undefined }));
	});

	it("keeps cloud cards read-only without local file manager actions", () => {
		renderView(<SessionArtifactsView session={{ ...session(), cloud: { orgId: "org-1" } } as WorkspaceSession} />);
		expect(screen.queryByRole("button", { name: /^Edit file:/ })).not.toBeInTheDocument();
		expect(screen.queryByRole("button", { name: /^Delete artifact:/ })).not.toBeInTheDocument();
		expect(screen.queryByRole("button", { name: /^More actions:/ })).not.toBeInTheDocument();
	});

	it("offers version history for a text file but not for a binary one", async () => {
		const image: SessionArtifact = { kind: "file", name: "chart.png", path: "chart.png", size: 30, updatedAt: "2026-09-20T00:00:00Z" };
		renderView(<SessionArtifactsView session={session([...artifacts, image])} />);

		await userEvent.click(screen.getByRole("button", { name: /^plan\.md/ }));
		expect(screen.queryByRole("button", { name: "Version history" })).not.toBeInTheDocument();
		await userEvent.click(screen.getByRole("button", { name: /^plan\.md/ }));
		expect(await screen.findByRole("menuitem", { name: "Version history" })).toBeInTheDocument();
		await userEvent.keyboard("{Escape}");

		await userEvent.click(screen.getByRole("button", { name: "Back to artifacts" }));
		await userEvent.click(screen.getByRole("button", { name: /^chart\.png/ }));
		await userEvent.click(screen.getByRole("button", { name: /chart\.png/ }));
		expect(screen.queryByRole("menuitem", { name: "Version history" })).not.toBeInTheDocument();
		expect(screen.queryByRole("menuitem", { name: "Copy contents" })).not.toBeInTheDocument();
	});

	it("shows the open artifact full screen, keeping the same viewer, until Escape", async () => {
		renderView(<SessionArtifactsView session={session()} />);
		await userEvent.click(screen.getByRole("button", { name: /^notes\.md/ }));
		const viewer = screen.getByTestId("artifact-view");

		await userEvent.click(screen.getByRole("button", { name: "Maximize" }));

		const overlay = screen.getByRole("dialog", { name: "notes.md" });
		expect(overlay.parentElement).toBe(document.body);
		expect(within(overlay).getByTestId("artifact-view")).toBe(viewer);
		expect(overlay).toHaveFocus();
		expect(within(overlay).getByRole("button", { name: "Return to panel" })).toBeInTheDocument();

		await userEvent.keyboard("{Escape}");

		expect(screen.queryByRole("dialog")).not.toBeInTheDocument();
		expect(screen.getByTestId("artifact-view")).toBe(viewer);
		expect(screen.getByRole("button", { name: "Maximize" })).toBeInTheDocument();
	});

	it("covers the whole app window without taking the window itself full screen", async () => {
		const action = vi.spyOn(aoBridge.menu, "action").mockResolvedValue(undefined);
		renderView(<SessionArtifactsView session={session()} />);
		await userEvent.click(screen.getByRole("button", { name: /^notes\.md/ }));

		await userEvent.click(screen.getByRole("button", { name: "Maximize" }));

		expect(screen.getByRole("dialog", { name: "notes.md" })).toHaveClass("fixed", "inset-0");
		await userEvent.click(screen.getByRole("button", { name: "Return to panel" }));
		expect(action).not.toHaveBeenCalled();
		action.mockRestore();
	});

	it("leaves full screen when going back to the list", async () => {
		renderView(<SessionArtifactsView session={session()} />);
		await userEvent.click(screen.getByRole("button", { name: /^notes\.md/ }));
		await userEvent.click(screen.getByRole("button", { name: "Maximize" }));

		await userEvent.click(within(screen.getByRole("dialog")).getByRole("button", { name: "Back to artifacts" }));

		expect(screen.queryByRole("dialog")).not.toBeInTheDocument();
		expect(screen.getByRole("list")).toBeInTheDocument();
	});

	it("names the open artifact once, as a menu that reveals it in the file manager and the terminal", async () => {
		const openArtifacts = vi.spyOn(aoBridge.editorHandoff, "openArtifacts");
		renderView(<SessionArtifactsView request={{ key: 1, path: "reports/plan.md" }} session={session()} />);

		expect(screen.getByRole("button", { name: "Back to artifacts" })).toBeInTheDocument();
		expect(screen.getAllByText("plan.md")).toHaveLength(1);
		await userEvent.click(screen.getByRole("button", { name: "plan.md" }));
		const items = screen.getAllByRole("menuitem").map((item) => item.textContent);
		expect(items).toEqual(["Version history", expect.stringMatching(/^Show in /), expect.stringMatching(/^Show in /), "Copy as Markdown", "Delete artifact"]);
		await userEvent.click(screen.getByRole("menuitem", { name: /^Show in (?!Terminal)/ }));
		await userEvent.click(screen.getByRole("button", { name: "plan.md" }));
		await userEvent.click(screen.getByRole("menuitem", { name: "Show in Terminal" }));

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

	it.each([
		{ location: "local", hostId: undefined, cloud: false, path: "chart.png", separators: 1, actions: [expect.stringMatching(/^Show in /), expect.stringMatching(/^Show in /), "Delete artifact"] },
		{ location: "remote", hostId: "remote-1", cloud: false, path: "chart.png", separators: 0, actions: ["Delete artifact"] },
		{ location: "cloud", hostId: undefined, cloud: true, path: "chart.png", separators: 1, actions: ["Download", "Delete artifact"] },
		{ location: "local", hostId: undefined, cloud: false, path: "notes.md", separators: 2, actions: [expect.stringMatching(/^Show in /), expect.stringMatching(/^Show in /), "Copy as Markdown", "Delete artifact"] },
		{ location: "remote", hostId: "remote-1", cloud: false, path: "notes.md", separators: 1, actions: ["Copy as Markdown", "Delete artifact"] },
		{ location: "cloud", hostId: undefined, cloud: true, path: "notes.md", separators: 1, actions: ["Copy as Markdown", "Download", "Delete artifact"] },
	])("separates only populated action groups for $location $path", async ({ hostId, cloud, path, separators, actions }) => {
		const artifact: SessionArtifact = { kind: "file", name: path, path, size: 2, updatedAt: "2026-09-24T00:00:00Z" };
		const currentSession = { ...session([artifact]), ...(cloud ? { cloud: { orgId: "org-1" } } : {}) } as WorkspaceSession;
		renderView(<SessionArtifactsView hostId={hostId} request={{ key: 1, path }} session={currentSession} />);

		await userEvent.click(screen.getByRole("button", { name: path }));
		const menu = screen.getByRole("menu");
		const hasHistory = !cloud && path.endsWith(".md");
		expect(within(menu).getAllByRole("menuitem").map((item) => item.textContent)).toEqual(hasHistory ? ["Version history", ...actions] : actions);
		expect(within(menu).queryAllByRole("separator")).toHaveLength(separators + (hasHistory ? 1 : 0));
		const children = Array.from(menu.children);
		expect(children[0]).not.toHaveAttribute("role", "separator");
		expect(children.at(-1)).not.toHaveAttribute("role", "separator");
		for (let index = 1; index < children.length; index += 1) {
			if (children[index].getAttribute("role") === "separator") expect(children[index - 1]).not.toHaveAttribute("role", "separator");
		}
	});

	it("copies other artifact contents on a remote host without local reveal actions", async () => {
		const writeText = vi.spyOn(aoBridge.clipboard, "writeText").mockResolvedValue(undefined);
		const other: SessionArtifact = { kind: "file", name: "data.json", path: "data.json", size: 2, updatedAt: "2026-09-24T00:00:00Z" };
		renderView(<SessionArtifactsView hostId="remote-1" request={{ key: 1, path: "data.json" }} session={session([other])} />);

		await userEvent.click(screen.getByRole("button", { name: "data.json" }));
		expect(screen.getAllByRole("menuitem").map((item) => item.textContent)).toEqual(["Version history", "Copy contents", "Delete artifact"]);
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

	it("offers copy, download and delete for a cloud session", async () => {
		renderView(<SessionArtifactsView request={{ key: 1, path: "notes.md" }} session={{ ...session(), cloud: { orgId: "org-1" } } as WorkspaceSession} />);

		await userEvent.click(screen.getByRole("button", { name: "notes.md" }));
		expect(screen.getAllByRole("menuitem").map((item) => item.textContent)).toEqual(["Copy as Markdown", "Download", "Delete artifact"]);
	});

	it("opens a requested artifact and passes its feedback request through", () => {
		renderView(<SessionArtifactsView request={{ feedback: true, key: 7, path: "notes.md" }} session={session()} />);

		const viewer = screen.getByTestId("artifact-view");
		expect(viewer).toHaveTextContent("viewer:notes.md");
		expect(viewer).toHaveAttribute("data-feedback-key", "7");
	});
});
