import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { MoveSessionDialog } from "./MoveSessionDialog";
import { useUiStore } from "../stores/ui-store";
import { CloudCpError } from "../lib/cloud-cp";
import { useUnfinishedMoveStore } from "../lib/move-finish";
import { TooltipProvider } from "./ui/tooltip";
import type { WorkspaceSession } from "../types/workspace";

type Reply = { data?: unknown; error?: unknown };

const { daemon, navigate, cloud, uploadCloudTranscript } = vi.hoisted(() => ({
	daemon: { posts: {} as Record<string, Reply>, repo: "", preview: {} as Reply, transcript: {} as Reply, targets: {} as Reply, agents: [] as unknown[] },
	uploadCloudTranscript: vi.fn(),
	navigate: vi.fn(),
	cloud: {
		features: [] as string[], projects: [] as unknown[], sessions: [] as unknown[], createSession: vi.fn(), signedIn: true,
		previewSessionMove: vi.fn(), exportSessionMove: vi.fn(), restartSessionAgent: vi.fn(), deleteSession: vi.fn(),
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
						: { data: { project: { id: "app", repo: daemon.repo } } }),
		POST: vi.fn(async (path: string) => daemon.posts[path] ?? { error: { code: "UNEXPECTED", message: path } }),
	}),
	labelForHost: () => undefined,
}));
vi.mock("../lib/navigate-to-session", () => ({ useNavigateToSession: () => navigate }));
vi.mock("../hooks/useCloudCp", () => ({
	useCloudCp: () => ({ client: cloud.signedIn ? cloud : null, baseUrl: "http://cp.test" }),
}));
vi.mock("../lib/cloud-attachments", () => ({ uploadCloudTranscript }));
vi.mock("../hooks/useCloudOrg", () => ({ useCloudOrg: () => ({ org: cloud.signedIn ? { id: "org-1" } : undefined }) }));
vi.mock("../hooks/useCloudOffering", () => ({ useCloudOffering: () => ({ features: cloud.features }) }));
vi.mock("../hooks/useWorkspaceQuery", () => ({
	useCloudProjectsQuery: () => ({ data: cloud.projects, isPending: false }),
	useCloudSessionsQuery: () => ({ data: cloud.sessions, isPending: false }),
	workspaceQueryKeyForHost: (host?: string) => ["workspace", host ?? "local"],
	cloudSessionsQueryKey: ["cloud-sessions"],
	workspaceQueryOptions: { queryKey: ["workspaces"], queryFn: async () => [] },
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

function renderDialog() {
	useUiStore.setState({ moveSessionTarget: session, globalToast: null });
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
	for (const fn of [cloud.previewSessionMove, cloud.exportSessionMove, cloud.restartSessionAgent, cloud.deleteSession]) fn.mockReset();
	daemon.agents = [];
	uploadCloudTranscript.mockReset();
	daemon.transcript = { data: { agentSessionId: "aid-1", transcriptGzip: btoa("gz") } };
	cloud.signedIn = true;
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

it("tries again with the same move id and Cloud request after a lost response", async () => {
	daemon.posts = { ...moveReplies, "/api/v1/sessions/{sessionId}/resume-agent": { data: {} } };
	const unreachable = new Error("Error invoking remote method 'cloudCp:request': TypeError: fetch failed");
	cloud.createSession.mockRejectedValueOnce(unreachable).mockResolvedValueOnce({ session: { id: "cloud-9" } });
	renderDialog();
	const user = userEvent.setup();
	await user.click(await screen.findByRole("button", { name: "Move to Orchestrator Cloud" }));
	expect(await screen.findByText("Could not reach Orchestrator Cloud.")).toBeTruthy();
	// The push already happened, so the session is not as it was.
	expect(await screen.findByText("The uncommitted files are now commit abc1234 on feat/x, here and on GitHub.")).toBeTruthy();
	expect(screen.getByText("The agent here is running again.")).toBeTruthy();
	expect(screen.queryByText("The session continues here, unchanged.")).toBeNull();
	await user.click(screen.getByRole("button", { name: "Try again" }));
	await waitFor(() => expect(navigate).toHaveBeenCalledWith("cloud-proj", "cloud-9"));
	const [first, second] = cloud.createSession.mock.calls;
	expect(second).toEqual(first);
	expect(first[2]).toEqual({ idempotencyKey: expect.any(String) });
});

it.each([
	["the worktree cannot move", () => { daemon.preview = { error: { code: "MOVE_LFS", message: "The move includes Git LFS files, which cannot move yet." } }; }, "The move includes Git LFS files, which cannot move yet."],
	["AO Cloud cannot start a session on a branch", () => { cloud.features = []; }, "Needs a newer Orchestrator Cloud."],
	["no Cloud project has the repository", () => { daemon.repo = "https://github.com/acme/other"; }, "Orchestrator Cloud has no project for this repository."],
	["signed out of AO Cloud", () => { cloud.signedIn = false; }, "Sign in to Orchestrator Cloud to move this session."],
	["the project has no origin", () => { daemon.repo = ""; daemon.preview = { error: { code: "MOVE_NO_ORIGIN", message: "No origin." } }; }, "No origin."],
	["a live Cloud session is on the branch", () => { cloud.sessions = [{ id: "cloud-1", projectId: "cloud-proj", branch: "feat/x", isTerminated: false, displayName: "Fix login" }]; }, "The Cloud session Fix login is already on feat/x. Archive it first."],
])("explains and disables Move when %s", async (_, setup, message) => {
	setup();
	renderDialog();
	expect(await screen.findByText(message)).toBeTruthy();
	await waitFor(() => expect(screen.getByRole("button", { name: "Move to Orchestrator Cloud" }).hasAttribute("disabled")).toBe(true));
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
	const [baseUrl, orgId, importId, agentSessionId] = uploadCloudTranscript.mock.calls[0];
	expect([baseUrl, orgId, agentSessionId]).toEqual(["http://cp.test", "org-1", "aid-1"]);
	expect(cloud.createSession.mock.calls[0][1]).toMatchObject({ transcriptImportId: importId });
	expect(useUiStore.getState().globalToast?.body).toBeFalsy();
});

it("counts the subagents that move with the conversation", async () => {
	cloud.features = ["session.branch", "session.transcriptImport"];
	daemon.preview = { data: { branch: "feat/x", changes: [], transcript: { bytes: 2048, subagents: 2 } } };
	renderDialog();
	expect(await screen.findByText("Full transcript with 2 subagents, 2 KB")).toBeTruthy();
});

it("says the conversation moved as a note when its upload fails", async () => {
	daemon.posts = moveReplies;
	cloud.features = ["session.branch", "session.transcriptImport"];
	daemon.preview = { data: { branch: "feat/x", changes: [], transcript: { bytes: 2048 } } };
	uploadCloudTranscript.mockRejectedValue(new Error("Couldn’t reach Orchestrator Cloud."));
	cloud.createSession.mockResolvedValue({ session: { id: "cloud-9" } });
	renderDialog();
	const user = userEvent.setup();
	await user.click(await screen.findByRole("button", { name: "Move to Orchestrator Cloud" }));
	await waitFor(() => expect(navigate).toHaveBeenCalled());
	expect(cloud.createSession.mock.calls[0][1]).not.toHaveProperty("transcriptImportId");
	expect(useUiStore.getState().globalToast?.body).toBe("The conversation moved as a summary note.");
});

it.each([
	["an agent Cloud cannot resume", { bytes: 0, unavailable: "unsupported" }, "Summary note"],
	["a transcript over 64 MiB", { bytes: 70 << 20, unavailable: "too_large" }, "Summary note: the transcript is over 64 MiB"],
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

describe("moving a Cloud session to this computer", () => {
	const cloudSession = {
		id: "cloud-1", title: "Fix login", workspaceId: "cloud-proj", workspaceName: "app", provider: "claude-code", status: "working",
		cloud: { orgId: "org-1" }, activity: { state: "active" }, autoReviewEnabled: true,
	} as unknown as WorkspaceSession;
	const tip = "3f2a91c".padEnd(40, "0");
	const local = { projectId: "app", name: "app", path: "/Users/me/code/app" };

	function renderHere() {
		useUiStore.setState({ moveSessionTarget: cloudSession, globalToast: null });
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
		});
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
		["no project here has the repository", () => { daemon.targets = { data: { targets: [] } }; }, "No project on this computer has acme/app."],
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

	it("offers to add a project when none has the repository", async () => {
		daemon.targets = { data: { targets: [] } };
		const requestCreateProject = vi.fn();
		useUiStore.setState({ requestCreateProject });
		renderHere();
		const user = userEvent.setup();
		await user.click(await screen.findByRole("button", { name: "Add project…" }));
		expect(requestCreateProject).toHaveBeenCalled();
	});

	it("starts the Cloud agent again and names the pushed commit when this computer refuses", async () => {
		daemon.posts = { "/api/v1/sessions/move/import": { error: { code: "MOVE_LOCAL_AHEAD", message: "feat/login here has commits that Orchestrator Cloud's does not. Push them first." } } };
		renderHere();
		const user = userEvent.setup();
		await user.click(await screen.findByRole("button", { name: "Move to this computer" }));
		expect(await screen.findByText("feat/login here has commits that Orchestrator Cloud's does not. Push them first.")).toBeTruthy();
		expect(screen.getByText("The uncommitted files are now commit 3f2a91c on feat/login, in Orchestrator Cloud and on GitHub.")).toBeTruthy();
		expect(screen.getByText("The session continues in Orchestrator Cloud and its agent is running again.")).toBeTruthy();
		expect(cloud.restartSessionAgent).toHaveBeenCalledWith("org-1", "cloud-1");
		expect(cloud.deleteSession).not.toHaveBeenCalled();
	});

	it("keeps deleting the Cloud copy when that last step fails", async () => {
		useUnfinishedMoveStore.setState({ moves: [] });
		cloud.deleteSession.mockRejectedValue(new CloudCpError("busy", { status: 503 }));
		renderHere();
		const user = userEvent.setup();
		await user.click(await screen.findByRole("button", { name: "Move to this computer" }));
		expect(await screen.findByText(/Fix login runs on this computer\. .*Orchestrator keeps trying/)).toBeTruthy();
		expect(useUnfinishedMoveStore.getState().moves).toEqual([{ kind: "delete-cloud", orgId: "org-1", cloudSessionId: "cloud-1" }]);
		await user.click(screen.getByRole("button", { name: "Open session" }));
		expect(navigate).toHaveBeenCalledWith("app", "local-9");
	});
});
