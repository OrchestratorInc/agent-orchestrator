import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { MoveSessionDialog } from "./MoveSessionDialog";
import { useUiStore } from "../stores/ui-store";
import { CloudCpError } from "../lib/cloud-cp";
import { useUnfinishedMoveStore, useUnsettledMoveHereStore, useUnsettledMoveStore } from "../lib/move-finish";
import { TooltipProvider } from "./ui/tooltip";
import type { WorkspaceSession } from "../types/workspace";

type Reply = { data?: unknown; error?: unknown; response?: { status: number } };
type PostReply = Reply | (() => Reply);

const { daemon, navigate, cloud, uploadCloudTranscript } = vi.hoisted(() => ({
	daemon: {
		posts: {} as Record<string, PostReply>, repo: "", preview: {} as Reply, transcript: {} as Reply, targets: {} as Reply, agents: [] as unknown[],
		localProjects: [] as { id: string }[], postCalls: [] as { path: string; body: unknown }[], defaultBranch: "main",
	},
	uploadCloudTranscript: vi.fn(),
	navigate: vi.fn(),
	cloud: {
		features: [] as string[], projects: [] as unknown[], sessions: [] as unknown[], createSession: vi.fn(), signedIn: true, connections: [] as unknown[],
		previewSessionMove: vi.fn(), exportSessionMove: vi.fn(), getSessionMoveExport: vi.fn(), restartSessionAgent: vi.fn(), deleteSession: vi.fn(), createProject: vi.fn(),
	},
}));

vi.mock("../lib/host-clients", () => ({
	clientForSessionHost: () => ({
		GET: vi.fn(async (path: string) =>
			path === "/api/v1/sessions/{sessionId}/move/preview"
				? daemon.preview
				: path === "/api/v1/sessions/{sessionId}/move/transcript"
					? daemon.transcript
					: path === "/api/v1/sessions/move/targets"
						? daemon.targets
						: { data: { project: { id: "app", name: "app", defaultBranch: daemon.defaultBranch, repo: daemon.repo } } }),
		POST: vi.fn(async (path: string, init?: { body?: unknown }) => {
			daemon.postCalls.push({ path, body: init?.body });
			const reply = daemon.posts[path];
			return typeof reply === "function" ? reply() : reply ?? { error: { code: "UNEXPECTED", message: path } };
		}),
	}),
	labelForHost: () => undefined,
}));
vi.mock("../lib/navigate-to-session", () => ({ useNavigateToSession: () => navigate }));
vi.mock("../hooks/useCloudCp", () => ({
	useCloudCp: () => ({ client: cloud.signedIn ? cloud : null, baseUrl: "http://cp.test" }),
}));
vi.mock("../lib/cloud-attachments", () => ({ uploadCloudTranscript }));
vi.mock("../hooks/useCloudOrg", () => ({ useCloudOrg: () => ({ org: cloud.signedIn ? { id: "org-1" } : undefined }) }));
vi.mock("../hooks/useCloudOffering", () => ({ useCloudOffering: () => ({ features: cloud.features, chatHarnesses: ["claude-code"] }) }));
vi.mock("../hooks/useProviderConnections", () => ({ useProviderConnections: () => ({ data: cloud.connections, isPending: false }) }));
vi.mock("../hooks/useWorkspaceQuery", () => ({
	useCloudProjectsQuery: () => ({ data: cloud.projects, isPending: false }),
	useCloudSessionsQuery: () => ({ data: cloud.sessions, isPending: false }),
	workspaceQueryKeyForHost: (host?: string) => ["workspace", host ?? "local"],
	cloudSessionsQueryKey: ["cloud-sessions"],
	cloudProjectsQueryKey: ["cloud-projects"],
	workspaceQueryOptions: { queryKey: ["workspace", "local"], queryFn: async () => daemon.localProjects },
}));
vi.mock("../hooks/useAgentReadinessQuery", () => ({ useAgentReadinessQuery: () => ({ data: { agents: daemon.agents } }) }));

const session = {
	id: "app-1", title: "Fix login", workspaceId: "app", workspaceName: "app", provider: "claude-code", status: "working",
} as unknown as WorkspaceSession;

const moveReplies = {
	"/api/v1/sessions/{sessionId}/exit-agent": { data: {} },
	"/api/v1/sessions/{sessionId}/move/export": { data: { manifest: { branch: "feat/x", tip: "abc1234def", wip: true, harness: "claude-code" }, handoffNote: "note" } },
	"/api/v1/sessions/{sessionId}/move/complete": { data: {} },
};

function renderDialog(target: WorkspaceSession = session) {
	useUiStore.setState({ moveSessionTarget: target, globalToast: null });
	return render(
		<QueryClientProvider client={new QueryClient({ defaultOptions: { queries: { retry: false } } })}>
			<TooltipProvider>
				<MoveSessionDialog />
			</TooltipProvider>
		</QueryClientProvider>,
	);
}

beforeEach(() => {
	navigate.mockReset();
	cloud.createSession.mockReset();
	cloud.createProject.mockReset();
	for (const fn of [cloud.previewSessionMove, cloud.exportSessionMove, cloud.getSessionMoveExport, cloud.restartSessionAgent, cloud.deleteSession]) fn.mockReset();
	daemon.agents = [];
	daemon.localProjects = [];
	daemon.postCalls = [];
	uploadCloudTranscript.mockReset();
	daemon.transcript = { data: { agentSessionId: "aid-1", transcriptGzip: btoa("gz") } };
	cloud.signedIn = true;
	cloud.connections = [{ provider: "claude-code", label: "default", validationState: "valid" }];
	daemon.defaultBranch = "main";
	useUnsettledMoveStore.getState().set(":app-1", undefined);
	cloud.features = ["session.branch"];
	cloud.projects = [{ id: "cloud-proj", displayName: "app", repositoryUrl: "https://github.com/Acme/App" }];
	cloud.sessions = [];
	daemon.repo = "git@github.com:acme/app.git";
	daemon.posts = {};
	daemon.preview = { data: { branch: "feat/x", changes: [{ path: "README.md", status: "modified" }, { path: "login.go", status: "added" }] } };
	useUiStore.setState({ moveSessionTarget: null });
});

it("shows what moves, then opens the Cloud session and closes on success", async () => {
	daemon.posts = moveReplies;
	cloud.createSession.mockResolvedValue({ session: { id: "cloud-9" } });
	renderDialog();
	const user = userEvent.setup();
	expect(await screen.findByText("feat/x")).toBeTruthy();
	expect(screen.getByText("A login.go")).toBeTruthy();
	expect(screen.getByText("2")).toBeTruthy();
	expect(screen.getByText(/pushed to GitHub as one commit on feat\/x\. .* its worktree is removed, ignored files included\./)).toBeTruthy();
	await user.click(screen.getByRole("button", { name: "Move to Orchestrator Cloud" }));
	await waitFor(() => expect(navigate).toHaveBeenCalledWith("cloud-proj", "cloud-9"));
	expect(cloud.createSession).toHaveBeenCalledWith("org-1", expect.objectContaining({ projectId: "cloud-proj", branch: "feat/x" }), expect.anything());
	expect(useUiStore.getState().moveSessionTarget).toBeNull();
	expect(useUiStore.getState().globalToast?.title).toBe("Moved to Orchestrator Cloud");
});

it("keeps the session here and offers a retry when the push fails", async () => {
	daemon.posts = {
		...moveReplies,
		"/api/v1/sessions/{sessionId}/move/export": { error: { code: "MOVE_REMOTE_AHEAD", message: "Origin's branch has commits this machine does not." } },
		"/api/v1/sessions/{sessionId}/resume-agent": { data: {} },
	};
	renderDialog();
	const user = userEvent.setup();
	await user.click(await screen.findByRole("button", { name: "Move to Orchestrator Cloud" }));
	expect(await screen.findByText("Origin's branch has commits this machine does not.")).toBeTruthy();
	expect(screen.getByText("The session continues here, unchanged.")).toBeTruthy();
	expect(screen.getByRole("button", { name: "Try again" })).toBeTruthy();
	expect(cloud.createSession).not.toHaveBeenCalled();
});

it("keeps the agent here stopped while Cloud does not answer, and Try again finishes the move", async () => {
	daemon.posts = { ...moveReplies, "/api/v1/sessions/{sessionId}/resume-agent": { data: {} } };
	const unreachable = new Error("Error invoking remote method 'cloudCp:request': TypeError: fetch failed");
	cloud.createSession.mockRejectedValueOnce(unreachable).mockRejectedValueOnce(unreachable).mockResolvedValueOnce({ session: { id: "cloud-9" } });
	renderDialog();
	const user = userEvent.setup();
	await user.click(await screen.findByRole("button", { name: "Move to Orchestrator Cloud" }));
	expect(await screen.findByText(/Orchestrator Cloud did not answer, so the session may already be starting there/)).toBeTruthy();
	// The push already happened, so the session is not as it was.
	expect(await screen.findByText("The uncommitted files are now commit abc1234 on feat/x, here and on GitHub.")).toBeTruthy();
	// Try again finishes the move, so nothing advises resuming the agent here.
	expect(screen.queryByText("The agent here is still stopped. Resume it from the session menu.")).toBeNull();
	expect(screen.queryByText("The session continues here, unchanged.")).toBeNull();
	expect(daemon.postCalls.map((call) => call.path)).not.toContain("/api/v1/sessions/{sessionId}/resume-agent");
	await user.click(screen.getByRole("button", { name: "Try again" }));
	await waitFor(() => expect(navigate).toHaveBeenCalledWith("cloud-proj", "cloud-9"));
	const [first, second, third] = cloud.createSession.mock.calls;
	expect(second).toEqual(first);
	expect(third).toEqual(first);
	expect(first[2]).toEqual({ idempotencyKey: expect.any(String) });
});

it.each([
	["the worktree cannot move", () => { daemon.preview = { error: { code: "MOVE_LFS", message: "The move includes Git LFS files, which cannot move yet." } }; }, "The move includes Git LFS files, which cannot move yet."],
	["AO Cloud cannot start a session on a branch", () => { cloud.features = []; }, "Needs a newer Orchestrator Cloud."],
	["no Cloud project has the repository and it is not on GitHub", () => { daemon.repo = "https://gitlab.com/acme/app"; }, "Orchestrator Cloud has no project for this repository."],
	["signed out of AO Cloud", () => { cloud.signedIn = false; }, "Sign in to Orchestrator Cloud to move this session."],
	["the project has no origin", () => { daemon.repo = ""; daemon.preview = { error: { code: "MOVE_NO_ORIGIN", message: "No origin." } }; }, "No origin."],
	["a live Cloud session is on the branch", () => { cloud.sessions = [{ id: "cloud-1", projectId: "cloud-proj", branch: "feat/x", isTerminated: false, displayName: "Fix login" }]; }, "The Cloud session Fix login is already on feat/x. Archive it first."],
	["the agent is not connected in AO Cloud", () => { cloud.connections = []; }, "Claude Code isn't connected in Orchestrator Cloud. Connect it in Settings, then move."],
	["the session is on the Cloud project's default branch", () => { cloud.projects = [{ id: "cloud-proj", displayName: "app", repositoryUrl: "https://github.com/Acme/App", defaultBranch: "feat/x" }]; }, "feat/x is the Cloud project's default branch. Only a session on its own branch can move."],
	["the repository's default branch is unknown", () => { daemon.repo = "git@github.com:acme/other.git"; daemon.defaultBranch = "auto"; }, "Orchestrator can't tell the repository's default branch, so it can't create the Cloud project. Set the project's default branch in its settings, then move."],
])("explains and disables Move when %s", async (_, setup, message) => {
	setup();
	renderDialog();
	expect(await screen.findByText(message)).toBeTruthy();
	await waitFor(() => expect(screen.getByRole("button", { name: "Move to Orchestrator Cloud" }).hasAttribute("disabled")).toBe(true));
});

it("creates the Cloud project on the default branch origin names when the project only knows \"auto\"", async () => {
	daemon.posts = moveReplies;
	daemon.repo = "git@github.com:acme/other.git";
	daemon.defaultBranch = "auto";
	daemon.preview = { data: { branch: "feat/x", defaultBranch: "trunk", changes: [] } };
	cloud.createProject.mockResolvedValue({ project: { id: "cloud-new" } });
	cloud.createSession.mockResolvedValue({ session: { id: "cloud-9" } });
	renderDialog();
	const user = userEvent.setup();
	await user.click(await screen.findByRole("button", { name: "Move to Orchestrator Cloud" }));
	await waitFor(() => expect(navigate).toHaveBeenCalledWith("cloud-new", "cloud-9"));
	expect(cloud.createProject.mock.calls[0][1]).toMatchObject({ defaultBranch: "trunk" });
});

it("does not say the session is unchanged when this computer's Orchestrator does not answer", async () => {
	daemon.posts = {
		...moveReplies,
		"/api/v1/sessions/{sessionId}/move/export": () => {
			throw new TypeError("fetch failed");
		},
	};
	renderDialog();
	const user = userEvent.setup();
	await user.click(await screen.findByRole("button", { name: "Move to Orchestrator Cloud" }));
	expect(await screen.findByText(/did not answer, so the outcome is unknown\. Check the session before trying again\./)).toBeTruthy();
	expect(screen.queryByText("The session continues here, unchanged.")).toBeNull();
	expect(screen.queryByText("The agent here is still stopped. Resume it from the session menu.")).toBeNull();
	expect(daemon.postCalls.map((call) => call.path)).not.toContain("/api/v1/sessions/{sessionId}/resume-agent");
	expect(cloud.createSession).not.toHaveBeenCalled();
});

it("says the outcome is unknown for a failure that is not a move error", async () => {
	// A reply without a manifest makes the move itself throw.
	daemon.posts = { ...moveReplies, "/api/v1/sessions/{sessionId}/move/export": { data: {} } };
	renderDialog();
	const user = userEvent.setup();
	await user.click(await screen.findByRole("button", { name: "Move to Orchestrator Cloud" }));
	expect(await screen.findByText("The outcome is unknown. Check the session before trying again.")).toBeTruthy();
	expect(screen.queryByText("The session continues here, unchanged.")).toBeNull();
});

it("finishes a move whose Cloud create went unanswered after the dialog was closed", async () => {
	daemon.posts = moveReplies;
	const unreachable = new Error("fetch failed");
	cloud.createSession.mockRejectedValueOnce(unreachable).mockRejectedValueOnce(unreachable).mockResolvedValueOnce({ session: { id: "cloud-9" } });
	const first = renderDialog();
	const user = userEvent.setup();
	await user.click(await screen.findByRole("button", { name: "Move to Orchestrator Cloud" }));
	expect(await screen.findByText(/Orchestrator Cloud did not answer/)).toBeTruthy();
	await user.click(screen.getByRole("button", { name: "Cancel" }));
	first.unmount();
	daemon.postCalls = [];

	renderDialog();
	expect(await screen.findByText("An earlier move may already have started this session in Orchestrator Cloud. Moving finishes that move.")).toBeTruthy();
	await user.click(screen.getByRole("button", { name: "Move to Orchestrator Cloud" }));
	await waitFor(() => expect(navigate).toHaveBeenCalledWith("cloud-proj", "cloud-9"));
	const [a, , c] = cloud.createSession.mock.calls;
	// The same idempotency key and body, so Cloud returns the session it may have made.
	expect(c).toEqual(a);
	expect(daemon.postCalls.map((call) => call.path)).toEqual(["/api/v1/sessions/{sessionId}/move/complete"]);
	expect(useUnsettledMoveStore.getState().moves).toEqual({});
});

it("creates the Cloud project when Cloud has none for the repository", async () => {
	daemon.posts = moveReplies;
	daemon.repo = "git@github.com:acme/other.git";
	cloud.createProject.mockResolvedValue({ project: { id: "cloud-new" } });
	cloud.createSession.mockResolvedValue({ session: { id: "cloud-9" } });
	renderDialog();
	const user = userEvent.setup();
	expect(await screen.findByText("New Cloud project")).toBeTruthy();
	await user.click(screen.getByRole("button", { name: "Move to Orchestrator Cloud" }));
	await waitFor(() => expect(navigate).toHaveBeenCalledWith("cloud-new", "cloud-9"));
	expect(cloud.createProject).toHaveBeenCalledWith(
		"org-1",
		{ displayName: "app", repositoryUrl: "https://github.com/acme/other", defaultBranch: "main" },
		{ idempotencyKey: expect.stringMatching(/-project$/) },
	);
	expect(cloud.createSession.mock.calls[0][1]).toMatchObject({ projectId: "cloud-new", branch: "feat/x" });
});

it("moves the conversation when Orchestrator Cloud can resume it", async () => {
	daemon.posts = moveReplies;
	cloud.features = ["session.branch", "session.transcriptImport"];
	daemon.preview = { data: { branch: "feat/x", changes: [], transcript: { bytes: 2048 } } };
	uploadCloudTranscript.mockResolvedValue(undefined);
	cloud.createSession.mockResolvedValue({ session: { id: "cloud-9" } });
	renderDialog();
	const user = userEvent.setup();
	expect(await screen.findByText("Full transcript, 2 KB")).toBeTruthy();
	await user.click(screen.getByRole("button", { name: "Move to Orchestrator Cloud" }));
	await waitFor(() => expect(navigate).toHaveBeenCalledWith("cloud-proj", "cloud-9"));
	const [baseUrl, orgId, importId, harness, agentSessionId] = uploadCloudTranscript.mock.calls[0];
	expect([baseUrl, orgId, harness, agentSessionId]).toEqual(["http://cp.test", "org-1", "claude-code", "aid-1"]);
	expect(cloud.createSession.mock.calls[0][1]).toMatchObject({ transcriptImportId: importId });
	expect(useUiStore.getState().globalToast?.body).toBeFalsy();
});

it("counts the subagents that move with the conversation", async () => {
	cloud.features = ["session.branch", "session.transcriptImport"];
	daemon.preview = { data: { branch: "feat/x", changes: [], transcript: { bytes: 2048, subagents: 2 } } };
	renderDialog();
	expect(await screen.findByText("Full transcript with 2 subagents, 2 KB")).toBeTruthy();
});

it("keeps the dialog open and says why when the conversation could not upload", async () => {
	daemon.posts = moveReplies;
	cloud.features = ["session.branch", "session.transcriptImport"];
	daemon.preview = { data: { branch: "feat/x", changes: [], transcript: { bytes: 2048 } } };
	uploadCloudTranscript.mockRejectedValue(new Error("Too many uploaded conversations are waiting to be resumed. Finish or cancel a move, or wait a day."));
	cloud.createSession.mockResolvedValue({ session: { id: "cloud-9" } });
	renderDialog();
	const user = userEvent.setup();
	await user.click(await screen.findByRole("button", { name: "Move to Orchestrator Cloud" }));
	expect(await screen.findByText(
		"The conversation did not move to Orchestrator Cloud (Too many uploaded conversations are waiting to be resumed. Finish or cancel a move, or wait a day.). The session there starts from a summary note; the full conversation stays on this computer.",
	)).toBeTruthy();
	expect(cloud.createSession.mock.calls[0][1]).not.toHaveProperty("transcriptImportId");
	expect(navigate).not.toHaveBeenCalled();
	await user.click(screen.getByRole("button", { name: "Open session" }));
	expect(navigate).toHaveBeenCalledWith("cloud-proj", "cloud-9");
});

it("warns before the move when the sidecar folder is over Cloud's upload limit", async () => {
	cloud.features = ["session.branch", "session.transcriptImport"];
	daemon.preview = { data: { branch: "feat/x", changes: [], transcript: { bytes: 2048, sidecarUnavailable: "too_large" } } };
	renderDialog();
	expect(await screen.findByText("Subagent transcripts and saved tool output are over Orchestrator Cloud's upload limit, so they stay on this computer.")).toBeTruthy();
});

it.each([
	["an agent Cloud cannot resume", { bytes: 0, unavailable: "unsupported" }, "Summary note"],
	["a conversation over Cloud's upload limit", { bytes: 70 << 20, unavailable: "too_large" }, "Summary note: the conversation is over Orchestrator Cloud's upload limit"],
])("shows a summary note for %s", async (_, transcript, text) => {
	cloud.features = ["session.branch", "session.transcriptImport"];
	daemon.preview = { data: { branch: "feat/x", changes: [], transcript } };
	renderDialog();
	expect(await screen.findByText(text)).toBeTruthy();
});

it("does not show the last preview while it reloads on reopen", async () => {
	const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
	const ui = () => (
		<QueryClientProvider client={client}>
			<TooltipProvider>
				<MoveSessionDialog />
			</TooltipProvider>
		</QueryClientProvider>
	);
	useUiStore.setState({ moveSessionTarget: session, globalToast: null });
	const view = render(ui());
	expect(await screen.findByText("A login.go")).toBeTruthy();
	useUiStore.setState({ moveSessionTarget: null });
	await waitFor(() => expect(screen.queryByText("A login.go")).toBeNull());
	daemon.preview = new Promise<Reply>(() => {}) as unknown as Reply;
	useUiStore.setState({ moveSessionTarget: session });
	view.rerender(ui());
	expect(await screen.findByText("Checking what moves")).toBeTruthy();
	expect(screen.queryByText("A login.go")).toBeNull();
});

it("keeps recording the move on this computer when that last step fails", async () => {
	useUnfinishedMoveStore.setState({ moves: [] });
	daemon.posts = { ...moveReplies, "/api/v1/sessions/{sessionId}/move/complete": { error: { code: "INTERNAL", message: "database is locked" } } };
	cloud.createSession.mockResolvedValue({ session: { id: "cloud-9" } });
	renderDialog();
	const user = userEvent.setup();
	await user.click(await screen.findByRole("button", { name: "Move to Orchestrator Cloud" }));
	expect(await screen.findByText(/Orchestrator keeps trying/)).toBeTruthy();
	expect(useUnfinishedMoveStore.getState().moves).toEqual([
		expect.objectContaining({ kind: "complete", sessionId: "app-1", orgId: "org-1", cloudSessionId: "cloud-9" }),
	]);
});

it("lists every uncommitted file, marks the untracked ones, and says the whole conversation is uploaded", async () => {
	cloud.features = ["session.branch", "session.transcriptImport"];
	const changes = Array.from({ length: 7 }, (_, n) => ({ path: `src/file-${n}.go`, status: "modified" }));
	changes.push({ path: ".env.local", status: "added", untracked: true } as never);
	daemon.preview = { data: { branch: "feat/x", changes, transcript: { bytes: 2048 } } };
	renderDialog();
	expect(await screen.findByText("M src/file-6.go")).toBeTruthy();
	expect(screen.getByText(".env.local", { exact: false }).closest("li")?.textContent).toBe("?? .env.local");
	expect(screen.getByTitle("Untracked")).toBeTruthy();
	expect(screen.getByText("?? marks untracked files. They are committed and pushed too, so check them for secrets.")).toBeTruthy();
	expect(screen.getByText(/The whole conversation is uploaded to Orchestrator Cloud, including everything the agent read or printed/)).toBeTruthy();
	expect(screen.queryByText(/more$/)).toBeNull();
});

it("says which secret environment files stay on this computer and won't move", async () => {
	daemon.preview = { data: { branch: "feat/x", changes: [{ path: "README.md", status: "modified" }], withheld: [".env", "config/.env.local"] } };
	renderDialog();
	expect(await screen.findByText(/Secret environment files such as \.env are never pushed\. These stay on this computer/)).toBeTruthy();
	const list = screen.getByRole("list", { name: "Stays on this computer" });
	expect(Array.from(list.querySelectorAll("li")).map((li) => li.textContent)).toEqual([".env", "config/.env.local"]);
	// They are not among the files the move pushes.
	expect(screen.getByRole("list", { name: "Uncommitted files" }).textContent).not.toContain(".env");
	// The archive keeps a worktree that holds them, so the dialog must not say it is removed.
	expect(screen.getByText(/The session is then archived here\. Its worktree stays on this computer, with these files and any ignored files\./)).toBeTruthy();
	expect(screen.queryByText(/worktree is removed/)).toBeNull();
});

it("says the worktree stays when only secret environment files are left uncommitted", async () => {
	daemon.preview = { data: { branch: "feat/x", changes: [], withheld: [".env"] } };
	renderDialog();
	expect(await screen.findByText("feat/x is pushed to GitHub. The session is then archived here. Its worktree stays on this computer, with these files and any ignored files.")).toBeTruthy();
	expect(screen.queryByText(/worktree is removed/)).toBeNull();
});

it("says the worktree is removed when nothing stays on this computer", async () => {
	daemon.preview = { data: { branch: "feat/x", changes: [{ path: "README.md", status: "modified" }] } };
	renderDialog();
	expect(await screen.findByText(/The session is then archived here and its worktree is removed, ignored files included\./)).toBeTruthy();
});

it("saves the move before Cloud's create, so a relaunch finishes it under the same id", async () => {
	daemon.posts = moveReplies;
	// The app quits while the create is in flight.
	cloud.createSession.mockImplementationOnce(() => new Promise(() => {})).mockResolvedValueOnce({ session: { id: "cloud-9" } });
	const first = renderDialog();
	const user = userEvent.setup();
	await user.click(await screen.findByRole("button", { name: "Move to Orchestrator Cloud" }));
	await waitFor(() => expect(cloud.createSession).toHaveBeenCalledTimes(1));
	const saved = useUnsettledMoveStore.getState().moves[":app-1"];
	expect(saved).toMatchObject({ orgId: "org-1", request: cloud.createSession.mock.calls[0][1] });
	first.unmount();
	daemon.postCalls = [];

	renderDialog();
	expect(await screen.findByText("An earlier move may already have started this session in Orchestrator Cloud. Moving finishes that move.")).toBeTruthy();
	await user.click(screen.getByRole("button", { name: "Move to Orchestrator Cloud" }));
	await waitFor(() => expect(navigate).toHaveBeenCalledWith("cloud-proj", "cloud-9"));
	const [a, b] = cloud.createSession.mock.calls;
	expect(b).toEqual(a);
	expect(a[2]).toEqual({ idempotencyKey: saved?.moveId });
	expect(daemon.postCalls.map((call) => call.path)).toEqual(["/api/v1/sessions/{sessionId}/move/complete"]);
});

it("moves under the id of a move this computer recorded as pushed", async () => {
	daemon.posts = moveReplies;
	cloud.createSession.mockResolvedValue({ session: { id: "cloud-9" } });
	renderDialog({ ...session, moveStarted: "m-recorded" } as WorkspaceSession);
	const user = userEvent.setup();
	await user.click(await screen.findByRole("button", { name: "Move to Orchestrator Cloud" }));
	await waitFor(() => expect(navigate).toHaveBeenCalledWith("cloud-proj", "cloud-9"));
	expect(daemon.postCalls.find((call) => call.path === "/api/v1/sessions/{sessionId}/move/export")?.body).toMatchObject({ moveId: "m-recorded" });
	expect(cloud.createSession.mock.calls[0][2]).toEqual({ idempotencyKey: "m-recorded" });
});

describe("moving a Cloud session to this computer", () => {
	const cloudSession = {
		id: "cloud-1", title: "Fix login", workspaceId: "cloud-proj", workspaceName: "app", provider: "claude-code", status: "working",
		cloud: { orgId: "org-1" }, activity: { state: "active" }, autoReviewEnabled: true,
	} as unknown as WorkspaceSession;
	const tip = "3f2a91c".padEnd(40, "0");
	const local = { projectId: "app", name: "app", path: "/Users/me/code/app" };

	function renderHere(target: WorkspaceSession = cloudSession) {
		useUiStore.setState({ moveSessionTarget: target, globalToast: null });
		return render(
			<QueryClientProvider client={new QueryClient({ defaultOptions: { queries: { retry: false } } })}>
				<TooltipProvider>
					<MoveSessionDialog />
				</TooltipProvider>
			</QueryClientProvider>,
		);
	}

	beforeEach(() => {
		cloud.previewSessionMove.mockResolvedValue({
			branch: "feat/login",
			changes: [{ path: "README.md", status: "modified" }, { path: "docs/old-login.md", status: "deleted" }],
			transcript: { bytes: 524288, subagents: 2 },
		});
		cloud.exportSessionMove.mockResolvedValue({
			manifest: { sourceSessionId: "cloud-1", sourceLabel: "AO Cloud", repoKey: "github.com/acme/app", branch: "feat/login", tip, wip: true, harness: "claude-code" },
			handoffNote: "moved",
			transcript: { agentSessionId: "conv-1", projectDir: "/p", transcriptGzip: "H4sI" },
		});
		useUnsettledMoveHereStore.setState({ moves: {} });
		cloud.restartSessionAgent.mockResolvedValue({ requested: true });
		cloud.deleteSession.mockResolvedValue({ session: { id: "cloud-1", desiredState: "deleted" } });
		daemon.targets = { data: { targets: [local] } };
		daemon.posts = { "/api/v1/sessions/move/import": { data: { session: { id: "local-9" } } } };
	});

	it("shows what moves, then opens the session here and deletes the Cloud one", async () => {
		renderHere();
		const user = userEvent.setup();
		expect(await screen.findByText("feat/login")).toBeTruthy();
		expect(screen.getByText("~/code/app")).toBeTruthy();
		expect(screen.getByText("Full transcript with 2 subagents, 512 KB")).toBeTruthy();
		expect(screen.getByText("Working. Moving stops it first.")).toBeTruthy();
		expect(screen.getByText("D docs/old-login.md")).toBeTruthy();
		expect(screen.getByText(/The Cloud session is then deleted, ignored files such as \.env included\./)).toBeTruthy();
		await user.click(screen.getByRole("button", { name: "Move to this computer" }));
		await waitFor(() => expect(navigate).toHaveBeenCalledWith("app", "local-9"));
		expect(cloud.deleteSession).toHaveBeenCalledWith("org-1", "cloud-1");
		expect(useUiStore.getState().globalToast).toMatchObject({ title: "Moved to this computer", body: "Fix login continues in app." });
	});

	it.each([
		["the branch is checked out here", () => {
			daemon.targets = { data: { targets: [{ ...local, refusal: { code: "MOVE_BRANCH_CHECKED_OUT", message: "feat/login is checked out here by Fix login (old). Archive that session first." } }] } };
		}, "feat/login is checked out here by Fix login (old). Archive that session first."],
		["the agent is not set up here", () => { daemon.agents = [{ id: "claude-code", label: "Claude Code", effectiveReadiness: "not_ready" }]; }, "Claude Code isn't set up on this computer. Set it up in Settings, then move."],
		["the sandbox cannot be reached", () => { cloud.previewSessionMove.mockRejectedValue(new CloudCpError("worker offline", { status: 503 })); }, "Orchestrator Cloud can't reach this session's sandbox. Try again in a moment."],
	])("explains why it cannot move when %s", async (_, setup, message) => {
		setup();
		renderHere();
		expect(await screen.findByText(message)).toBeTruthy();
		expect(screen.queryByRole("button", { name: "Move to this computer" })?.hasAttribute("disabled") ?? true).toBe(true);
	});

	it("clones the repository when no project here has it, then moves into it", async () => {
		daemon.targets = { data: { targets: [] } };
		daemon.posts = {
			"/api/v1/projects/clone": { data: { project: { id: "app-new" } } },
			"/api/v1/sessions/move/import": { data: { session: { id: "local-9" } } },
		};
		renderHere();
		const user = userEvent.setup();
		expect(await screen.findByText("New clone in ~/ao/projects")).toBeTruthy();
		await user.click(screen.getByRole("button", { name: "Move to this computer" }));
		await waitFor(() => expect(navigate).toHaveBeenCalledWith("app-new", "local-9"));
		expect(cloud.exportSessionMove).toHaveBeenCalled();
	});

	it("moves into the cloned project on Try again after a later step fails", async () => {
		daemon.targets = { data: { targets: [] } };
		daemon.posts = {
			// The clone adds a project, so the workspace list changes under the dialog.
			"/api/v1/projects/clone": () => {
				daemon.localProjects = [{ id: "app-new" }];
				return { data: { project: { id: "app-new" } } };
			},
			"/api/v1/sessions/move/import": { data: { session: { id: "local-9" } } },
		};
		cloud.exportSessionMove.mockRejectedValueOnce(new CloudCpError("push rejected", { status: 409 }));
		renderHere();
		const user = userEvent.setup();
		await user.click(await screen.findByRole("button", { name: "Move to this computer" }));
		const retry = await screen.findByRole("button", { name: "Try again" });
		await waitFor(() => expect(retry.hasAttribute("disabled")).toBe(false));
		await user.click(retry);
		await waitFor(() => expect(navigate).toHaveBeenCalledWith("app-new", "local-9"));
		expect(daemon.postCalls.filter((call) => call.path === "/api/v1/projects/clone")).toHaveLength(1);
		expect(daemon.postCalls.find((call) => call.path === "/api/v1/sessions/move/import")?.body).toMatchObject({ projectId: "app-new" });
	});

	it("explains and disables Move when AO Cloud cannot push a session's branch", async () => {
		cloud.features = [];
		renderHere();
		expect(await screen.findByText("Needs a newer Orchestrator Cloud.")).toBeTruthy();
		await waitFor(() => expect(screen.getByRole("button", { name: "Move to this computer" }).hasAttribute("disabled")).toBe(true));
		expect(cloud.exportSessionMove).not.toHaveBeenCalled();
	});

	it("says nothing moved when the clone fails", async () => {
		daemon.targets = { data: { targets: [] } };
		daemon.posts = { "/api/v1/projects/clone": { error: { code: "CLONE_EMPTY_REPOSITORY", message: "Orchestrator needs a repository with at least one commit." } } };
		renderHere();
		const user = userEvent.setup();
		await user.click(await screen.findByRole("button", { name: "Move to this computer" }));
		expect(await screen.findByText("Orchestrator needs a repository with at least one commit.")).toBeTruthy();
		expect(screen.getByText("Clone acme/app into ~/ao/projects")).toBeTruthy();
		expect(screen.getByText("Nothing moved. The session stays in Orchestrator Cloud as it was.")).toBeTruthy();
		expect(cloud.exportSessionMove).not.toHaveBeenCalled();
	});

	it("starts the Cloud agent again and names the pushed commit when this computer refuses", async () => {
		daemon.posts = { "/api/v1/sessions/move/import": { error: { code: "MOVE_LOCAL_AHEAD", message: "feat/login here has commits that Orchestrator Cloud's does not. Push them first." }, response: { status: 409 } } };
		renderHere();
		const user = userEvent.setup();
		await user.click(await screen.findByRole("button", { name: "Move to this computer" }));
		expect(await screen.findByText("feat/login here has commits that Orchestrator Cloud's does not. Push them first.")).toBeTruthy();
		expect(screen.getByText("The uncommitted files are now commit 3f2a91c on feat/login, in Orchestrator Cloud and on GitHub.")).toBeTruthy();
		expect(screen.getByText("The session continues in Orchestrator Cloud and its agent is running again.")).toBeTruthy();
		expect(cloud.restartSessionAgent).toHaveBeenCalledWith("org-1", "cloud-1");
		expect(cloud.deleteSession).not.toHaveBeenCalled();
	});

	it("says a session in Chat carries on in Cloud and takes messages again when the move fails", async () => {
		// Cloud answers agent/restart for a session in Chat with requested: false.
		cloud.restartSessionAgent.mockResolvedValue({ requested: false });
		daemon.posts = { "/api/v1/sessions/move/import": { error: { code: "MOVE_LOCAL_AHEAD", message: "feat/login here has commits that Orchestrator Cloud's does not. Push them first." }, response: { status: 409 } } };
		renderHere({ ...cloudSession, mode: "chat" } as WorkspaceSession);
		const user = userEvent.setup();
		await user.click(await screen.findByRole("button", { name: "Move to this computer" }));
		expect(await screen.findByText("feat/login here has commits that Orchestrator Cloud's does not. Push them first.")).toBeTruthy();
		expect(screen.getByText("The session continues in Orchestrator Cloud, and its Chat takes messages again.")).toBeTruthy();
		expect(screen.queryByText("The session continues in Orchestrator Cloud and its agent is running again.")).toBeNull();
		expect(screen.queryByText("The session stays in Orchestrator Cloud, but its agent is stopped. Restart it from the session.")).toBeNull();
		expect(cloud.restartSessionAgent).toHaveBeenCalledWith("org-1", "cloud-1");
	});

	it("keeps the Cloud session and says so when part of the conversation did not move", async () => {
		daemon.posts = { "/api/v1/sessions/move/import": { data: { session: { id: "local-9" }, conversationIncomplete: true } } };
		renderHere();
		const user = userEvent.setup();
		await user.click(await screen.findByRole("button", { name: "Move to this computer" }));
		await waitFor(() => expect(navigate).toHaveBeenCalledWith("app", "local-9"));
		expect(cloud.deleteSession).not.toHaveBeenCalled();
		expect(useUiStore.getState().globalToast?.body).toBe(
			"Fix login continues in app. The Cloud session is kept, with its agent stopped, because part of its conversation did not move here.",
		);
	});

	it("keeps deleting the Cloud copy when that last step fails", async () => {
		useUnfinishedMoveStore.setState({ moves: [] });
		cloud.deleteSession.mockRejectedValue(new CloudCpError("busy", { status: 503 }));
		renderHere();
		const user = userEvent.setup();
		await user.click(await screen.findByRole("button", { name: "Move to this computer" }));
		expect(await screen.findByText(/Fix login runs on this computer\. .*Orchestrator keeps trying/)).toBeTruthy();
		expect(useUnfinishedMoveStore.getState().moves).toEqual([expect.objectContaining({ kind: "delete-cloud", orgId: "org-1", cloudSessionId: "cloud-1" })]);
		await user.click(screen.getByRole("button", { name: "Open session" }));
		expect(navigate).toHaveBeenCalledWith("app", "local-9");
	});
	it("marks Cloud's untracked files, which the move commits and pushes too", async () => {
		cloud.previewSessionMove.mockResolvedValue({
			branch: "feat/login",
			changes: [{ path: "README.md", status: "modified" }, { path: "scratch/creds.json", status: "added", untracked: true }],
			transcript: { bytes: 524288, subagents: 2 },
		});
		renderHere();
		expect((await screen.findByText("scratch/creds.json", { exact: false })).closest("li")?.textContent).toBe("?? scratch/creds.json");
		expect(screen.getByText("?? marks untracked files. They are committed and pushed too, so check them for secrets.")).toBeTruthy();
	});

	it("tries again after Cloud's export timed out, and moves", async () => {
		cloud.exportSessionMove.mockRejectedValueOnce(new CloudCpError("The worker did not answer in time.", { status: 504, code: "WORKER_TIMEOUT" }));
		renderHere();
		const user = userEvent.setup();
		await user.click(await screen.findByRole("button", { name: "Move to this computer" }));
		expect(await screen.findByText("The worker did not answer in time.")).toBeTruthy();
		expect(cloud.restartSessionAgent).toHaveBeenCalledTimes(1);
		await user.click(screen.getByRole("button", { name: "Try again" }));
		await waitFor(() => expect(navigate).toHaveBeenCalledWith("app", "local-9"));
		expect(cloud.exportSessionMove).toHaveBeenCalledTimes(2);
		expect(daemon.postCalls.filter((call) => call.path === "/api/v1/sessions/move/import")).toHaveLength(1);
	});

	it("says Orchestrator Cloud is still exporting, and keeps the export's id for Try again", async () => {
		cloud.exportSessionMove.mockResolvedValue({ status: "running", moveId: "exp-1", branchMayBePushed: true });
		cloud.getSessionMoveExport.mockReturnValue(new Promise(() => {}));
		renderHere();
		const user = userEvent.setup();
		await user.click(await screen.findByRole("button", { name: "Move to this computer" }));
		expect(await screen.findByText("Orchestrator Cloud is still stopping the agent and pushing feat/login. This can take a few minutes.")).toBeTruthy();
		expect(Object.values(useUnsettledMoveHereStore.getState().moves)).toEqual([expect.objectContaining({ exportMoveId: "exp-1" })]);
	});

	it("names the branch Orchestrator Cloud may have pushed when its export timed out", async () => {
		cloud.exportSessionMove.mockRejectedValue(new CloudCpError("AO Cloud started this move but did not finish it in time.", {
			status: 504, code: "MOVE_EXPORT_TIMED_OUT", details: { moveId: "exp-1", branchMayBePushed: true },
		}));
		renderHere();
		const user = userEvent.setup();
		await user.click(await screen.findByRole("button", { name: "Move to this computer" }));
		expect(await screen.findByText(/^feat\/login may already be pushed to GitHub, with the session's uncommitted work as a WIP commit\. Nothing moved to this computer\./)).toBeTruthy();
		expect(cloud.restartSessionAgent).toHaveBeenCalledTimes(1);
		expect(screen.getByRole("button", { name: "Try again" })).toBeTruthy();
	});

	it("keeps Cloud's agent stopped on Try again after an unanswered import, and remembers that past a relaunch", async () => {
		let imports = 0;
		daemon.posts = {
			"/api/v1/sessions/move/import": () => {
				imports++;
				// The first import got no answer; Try again's replay fails with a server error.
				if (imports === 1) throw new Error("connection reset");
				return { error: { code: "INTERNAL", message: "replay failed" }, response: { status: 500 } };
			},
		};
		renderHere();
		const user = userEvent.setup();
		await user.click(await screen.findByRole("button", { name: "Move to this computer" }));
		await screen.findByRole("button", { name: "Try again" });
		expect(Object.values(useUnsettledMoveHereStore.getState().moves)).toEqual([expect.objectContaining({ importUnsettled: true, projectId: "app" })]);
		await user.click(screen.getByRole("button", { name: "Try again" }));
		await waitFor(() => expect(imports).toBe(2));
		await screen.findByRole("button", { name: "Try again" });
		expect(cloud.restartSessionAgent).not.toHaveBeenCalled();
		expect(cloud.deleteSession).not.toHaveBeenCalled();
		expect(Object.values(useUnsettledMoveHereStore.getState().moves)).toEqual([expect.objectContaining({ importUnsettled: true })]);
	});

	it("saves the import as unsettled before it sends it", async () => {
		const savedWhenSent: unknown[] = [];
		daemon.posts = {
			"/api/v1/sessions/move/import": () => {
				savedWhenSent.push(...Object.values(useUnsettledMoveHereStore.getState().moves));
				return { data: { session: { id: "local-9" } } };
			},
		};
		renderHere();
		const user = userEvent.setup();
		await user.click(await screen.findByRole("button", { name: "Move to this computer" }));
		await waitFor(() => expect(navigate).toHaveBeenCalledWith("app", "local-9"));
		expect(savedWhenSent).toEqual([expect.objectContaining({ projectId: "app", importUnsettled: true })]);
		// A definitive answer settles it.
		expect(useUnsettledMoveHereStore.getState().moves).toEqual({});
	});

	it("keeps Cloud's agent stopped after a crash mid-import, when Cloud's next export fails", async () => {
		// The renderer went away after sending the import: the saved move names the
		// project but, as an older build saved it, no uncertainty flag.
		useUnsettledMoveHereStore.getState().set("org-1:cloud-1", { moveId: "m-earlier", projectId: "app" });
		cloud.exportSessionMove.mockRejectedValue(new CloudCpError("bad gateway", { status: 502, code: "WORKER_REQUEST_FAILED" }));
		renderHere();
		const user = userEvent.setup();
		await user.click(await screen.findByRole("button", { name: "Move to this computer" }));
		await screen.findByRole("button", { name: "Try again" });
		expect(cloud.restartSessionAgent).not.toHaveBeenCalled();
		expect(daemon.postCalls.filter((call) => call.path === "/api/v1/sessions/move/import")).toHaveLength(0);
		expect(Object.values(useUnsettledMoveHereStore.getState().moves)).toEqual([expect.objectContaining({ moveId: "m-earlier", projectId: "app" })]);
	});

	it("waits on the export an earlier attempt left running, under its moveId", async () => {
		useUnsettledMoveHereStore.getState().set("org-1:cloud-1", { moveId: "m-earlier", exportMoveId: "exp-1" });
		renderHere();
		const user = userEvent.setup();
		await user.click(await screen.findByRole("button", { name: "Move to this computer" }));
		await waitFor(() => expect(navigate).toHaveBeenCalledWith("app", "local-9"));
		expect(cloud.exportSessionMove).toHaveBeenCalledWith("org-1", "cloud-1", { moveId: "exp-1" });
		expect(daemon.postCalls.find((call) => call.path === "/api/v1/sessions/move/import")?.body).toMatchObject({ moveId: "m-earlier" });
		expect(useUnsettledMoveHereStore.getState().moves).toEqual({});
	});

	it("says plainly that the session's Cloud worker is too old to move, before and on export", async () => {
		cloud.previewSessionMove.mockRejectedValue(new CloudCpError("worker too old (CP text)", { status: 409, code: "MOVE_WORKER_TOO_OLD" }));
		const first = renderHere();
		expect(await screen.findByText("This session's Orchestrator Cloud worker is too old to move it. Restart the session to update it, then move it.")).toBeTruthy();
		first.unmount();

		cloud.previewSessionMove.mockResolvedValue({ branch: "feat/login", changes: [] });
		cloud.exportSessionMove.mockRejectedValue(new CloudCpError("worker too old (CP text)", { status: 409, code: "MOVE_WORKER_TOO_OLD" }));
		renderHere();
		const user = userEvent.setup();
		await user.click(await screen.findByRole("button", { name: "Move to this computer" }));
		expect(await screen.findByText("This session's Orchestrator Cloud worker is too old to move it. Restart the session to update it, then move it.")).toBeTruthy();
		expect(screen.queryByText("worker too old (CP text)")).toBeNull();
	});

	it("says the Cloud agent did not stop, with nothing pushed, and offers Try again", async () => {
		cloud.exportSessionMove.mockRejectedValueOnce(new CloudCpError("agent did not stop (CP text)", { status: 409, code: "MOVE_AGENT_STILL_RUNNING" }));
		renderHere();
		const user = userEvent.setup();
		await user.click(await screen.findByRole("button", { name: "Move to this computer" }));
		expect(await screen.findByText(
			"Orchestrator Cloud could not confirm the agent had stopped: some of its processes were still running, so nothing was committed or pushed. Try again.",
		)).toBeTruthy();
		expect(screen.queryByText("agent did not stop (CP text)")).toBeNull();
		expect(daemon.postCalls.filter((call) => call.path === "/api/v1/sessions/move/import")).toHaveLength(0);
		expect(cloud.deleteSession).not.toHaveBeenCalled();
		// Trying again is safe: it moves.
		await user.click(screen.getByRole("button", { name: "Try again" }));
		await waitFor(() => expect(navigate).toHaveBeenCalledWith("app", "local-9"));
	});

	it("says messages are still queued on Cloud, with nothing moved, and offers Try again once they are handled", async () => {
		cloud.exportSessionMove.mockRejectedValueOnce(new CloudCpError("turns queued (CP text)", { status: 409, code: "MOVE_TURNS_QUEUED" }));
		renderHere();
		const user = userEvent.setup();
		await user.click(await screen.findByRole("button", { name: "Move to this computer" }));
		expect(await screen.findByText(
			"Messages are still queued for this session in Orchestrator Cloud. Let them run or remove them, then move it.",
		)).toBeTruthy();
		expect(screen.queryByText("turns queued (CP text)")).toBeNull();
		// Refused before anything stopped: the session is as it was, and no restart is asked.
		expect(screen.getByText("Nothing moved. The session stays in Orchestrator Cloud as it was.")).toBeTruthy();
		expect(cloud.restartSessionAgent).not.toHaveBeenCalled();
		expect(daemon.postCalls.filter((call) => call.path === "/api/v1/sessions/move/import")).toHaveLength(0);
		expect(cloud.deleteSession).not.toHaveBeenCalled();
		// Once the queued messages ran or were removed, trying again moves it.
		await user.click(screen.getByRole("button", { name: "Try again" }));
		await waitFor(() => expect(navigate).toHaveBeenCalledWith("app", "local-9"));
	});

	it("says a switch is in progress, in the app's words, without restarting the Cloud agent mid-switch", async () => {
		cloud.exportSessionMove.mockRejectedValueOnce(new CloudCpError("switch in progress (CP text)", { status: 409, code: "INTERFACE_TRANSITION_IN_PROGRESS" }));
		renderHere();
		const user = userEvent.setup();
		await user.click(await screen.findByRole("button", { name: "Move to this computer" }));
		expect(await screen.findByText("A switch is in progress for this session. Try again when it finishes.")).toBeTruthy();
		expect(screen.queryByText("switch in progress (CP text)")).toBeNull();
		expect(screen.getByText("Nothing moved. The session stays in Orchestrator Cloud as it was.")).toBeTruthy();
		expect(cloud.restartSessionAgent).not.toHaveBeenCalled();
		expect(daemon.postCalls.filter((call) => call.path === "/api/v1/sessions/move/import")).toHaveLength(0);
		await user.click(screen.getByRole("button", { name: "Try again" }));
		await waitFor(() => expect(navigate).toHaveBeenCalledWith("app", "local-9"));
	});

	it("says the move is still running when Cloud will not restart its agent, and Try again finishes that move", async () => {
		// The export's answer is lost, and the restart is refused: the export runs after all.
		cloud.exportSessionMove.mockRejectedValueOnce(new CloudCpError("fetch failed", { status: 0 }));
		cloud.restartSessionAgent.mockRejectedValueOnce(new CloudCpError("move in progress (CP text)", { status: 409, code: "MOVE_IN_PROGRESS" }));
		renderHere();
		const user = userEvent.setup();
		await user.click(await screen.findByRole("button", { name: "Move to this computer" }));
		expect(await screen.findByText(
			"This session is moving to another computer right now. Restart its agent once the move has finished or failed.",
		)).toBeTruthy();
		expect(screen.queryByText("move in progress (CP text)")).toBeNull();
		// Not the line that says to restart it from the session now.
		expect(screen.queryByText("The session stays in Orchestrator Cloud, but its agent is stopped. Restart it from the session.")).toBeNull();
		expect(cloud.deleteSession).not.toHaveBeenCalled();
		// Asked again, Cloud answers with the move that holds the session.
		cloud.exportSessionMove.mockResolvedValueOnce({ ...(await cloud.exportSessionMove.getMockImplementation()?.()), moveId: "exp-1" });
		await user.click(screen.getByRole("button", { name: "Try again" }));
		await waitFor(() => expect(navigate).toHaveBeenCalledWith("app", "local-9"));
		expect(cloud.restartSessionAgent).toHaveBeenCalledTimes(1);
		expect(cloud.deleteSession).toHaveBeenCalledWith("org-1", "cloud-1", { moveId: "exp-1" });
	});

	it("keeps the Cloud session when it was used again after the move, and says so", async () => {
		useUnfinishedMoveStore.setState({ moves: [] });
		cloud.exportSessionMove.mockResolvedValue({ ...(await cloud.exportSessionMove.getMockImplementation()?.()), moveId: "exp-1" });
		cloud.deleteSession.mockRejectedValue(new CloudCpError("The session was used after the move.", { status: 409, code: "MOVE_COPY_IN_USE" }));
		renderHere();
		const user = userEvent.setup();
		await user.click(await screen.findByRole("button", { name: "Move to this computer" }));
		await waitFor(() => expect(navigate).toHaveBeenCalledWith("app", "local-9"));
		expect(cloud.deleteSession).toHaveBeenCalledWith("org-1", "cloud-1", { moveId: "exp-1" });
		expect(useUiStore.getState().globalToast?.body).toContain("The Cloud session was used again after the move, so Orchestrator kept it.");
		expect(useUnfinishedMoveStore.getState().moves.filter((move) => move.kind === "delete-cloud")).toEqual([]);
	});

	it("says Cloud could not save the conversation, keeping its session and restarting the agent", async () => {
		cloud.exportSessionMove.mockRejectedValue(new CloudCpError("The session's branch was pushed, but its latest conversation could not be saved to AO Cloud, so the move stopped. Try again.", {
			status: 409, code: "MOVE_TRANSCRIPT_NOT_CAPTURED",
		}));
		renderHere();
		const user = userEvent.setup();
		await user.click(await screen.findByRole("button", { name: "Move to this computer" }));
		expect(await screen.findByText("Orchestrator Cloud could not save the session's latest conversation, so nothing moved here. The Cloud session is kept and its agent is running again.")).toBeTruthy();
		expect(screen.getByText(/^feat\/login may already be pushed to GitHub/)).toBeTruthy();
		expect(cloud.restartSessionAgent).toHaveBeenCalledTimes(1);
		expect(cloud.deleteSession).not.toHaveBeenCalled();
	});

	it("keeps the Cloud session when its export leaves out the conversation the preview listed", async () => {
		cloud.exportSessionMove.mockResolvedValue({
			manifest: { sourceSessionId: "cloud-1", sourceLabel: "AO Cloud", repoKey: "github.com/acme/app", branch: "feat/login", tip, wip: true, harness: "claude-code" },
			handoffNote: "moved",
		});
		renderHere();
		const user = userEvent.setup();
		await user.click(await screen.findByRole("button", { name: "Move to this computer" }));
		await waitFor(() => expect(navigate).toHaveBeenCalledWith("app", "local-9"));
		expect(cloud.deleteSession).not.toHaveBeenCalled();
		expect(useUiStore.getState().globalToast?.body).toContain("The Cloud session is kept");
	});

	it("finishes a move here after a relaunch under the same id, though its session now holds the branch", async () => {
		daemon.posts = { "/api/v1/sessions/move/import": () => { throw new Error("fetch failed"); } };
		const first = renderHere();
		const user = userEvent.setup();
		await user.click(await screen.findByRole("button", { name: "Move to this computer" }));
		expect(await screen.findByText(/may already be starting here/)).toBeTruthy();
		const firstImport = daemon.postCalls.find((call) => call.path === "/api/v1/sessions/move/import")?.body as { moveId: string; projectId: string };
		first.unmount();

		// The session the first import started holds the branch here now.
		daemon.targets = { data: { targets: [{ ...local, refusal: { code: "MOVE_BRANCH_CHECKED_OUT", message: "feat/login is checked out here by Fix login." } }] } };
		daemon.posts = { "/api/v1/sessions/move/import": { data: { session: { id: "local-9" } } } };
		daemon.postCalls = [];
		renderHere();
		expect(await screen.findByText("An earlier move may already have started this session on this computer. Moving finishes that move.")).toBeTruthy();
		await user.click(screen.getByRole("button", { name: "Move to this computer" }));
		await waitFor(() => expect(navigate).toHaveBeenCalledWith("app", "local-9"));
		expect(daemon.postCalls[0]?.body).toMatchObject({ moveId: firstImport.moveId, projectId: firstImport.projectId });
		expect(cloud.deleteSession).toHaveBeenCalledWith("org-1", "cloud-1");
		expect(useUnsettledMoveHereStore.getState().moves).toEqual({});
	});
});
