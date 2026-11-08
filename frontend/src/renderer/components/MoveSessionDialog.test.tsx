import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, expect, it, vi } from "vitest";
import { MoveSessionDialog } from "./MoveSessionDialog";
import { useUiStore } from "../stores/ui-store";
import { TooltipProvider } from "./ui/tooltip";
import type { WorkspaceSession } from "../types/workspace";

type Reply = { data?: unknown; error?: unknown };

const { daemon, navigate, cloud } = vi.hoisted(() => ({
	daemon: { posts: {} as Record<string, Reply>, repo: "", preview: {} as Reply },
	navigate: vi.fn(),
	cloud: { features: [] as string[], projects: [] as unknown[], createSession: vi.fn(), signedIn: true },
}));

vi.mock("../lib/host-clients", () => ({
	clientForSessionHost: () => ({
		GET: vi.fn(async (path: string) =>
			path === "/api/v1/sessions/{sessionId}/move/preview" ? daemon.preview : { data: { project: { id: "app", repo: daemon.repo } } }),
		POST: vi.fn(async (path: string) => daemon.posts[path] ?? { error: { code: "UNEXPECTED", message: path } }),
	}),
	labelForHost: () => undefined,
}));
vi.mock("../lib/navigate-to-session", () => ({ useNavigateToSession: () => navigate }));
vi.mock("../hooks/useCloudCp", () => ({ useCloudCp: () => ({ client: cloud.signedIn ? { createSession: cloud.createSession } : null }) }));
vi.mock("../hooks/useCloudOrg", () => ({ useCloudOrg: () => ({ org: cloud.signedIn ? { id: "org-1" } : undefined }) }));
vi.mock("../hooks/useCloudOffering", () => ({ useCloudOffering: () => ({ features: cloud.features }) }));
vi.mock("../hooks/useWorkspaceQuery", () => ({
	useCloudProjectsQuery: () => ({ data: cloud.projects, isPending: false }),
	workspaceQueryKeyForHost: (host?: string) => ["workspace", host ?? "local"],
	cloudSessionsQueryKey: ["cloud-sessions"],
}));

const session = {
	id: "app-1", title: "Fix login", workspaceId: "app", workspaceName: "app", provider: "claude-code", status: "working",
} as unknown as WorkspaceSession;

const moveReplies = {
	"/api/v1/sessions/{sessionId}/exit-agent": { data: {} },
	"/api/v1/sessions/{sessionId}/move/export": { data: { manifest: { branch: "feat/x", tip: "abc", harness: "claude-code" }, handoffNote: "note" } },
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
	cloud.signedIn = true;
	cloud.features = ["session.branch"];
	cloud.projects = [{ id: "cloud-proj", displayName: "app", repositoryUrl: "https://github.com/Acme/App" }];
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
	expect(screen.getByText("login.go")).toBeTruthy();
	expect(screen.getByText("2")).toBeTruthy();
	expect(screen.getByText(/pushed to GitHub as one commit on feat\/x/)).toBeTruthy();
	await user.click(screen.getByRole("button", { name: "Move to AO Cloud" }));
	await waitFor(() => expect(navigate).toHaveBeenCalledWith("cloud-proj", "cloud-9"));
	expect(cloud.createSession).toHaveBeenCalledWith("org-1", expect.objectContaining({ projectId: "cloud-proj", branch: "feat/x" }), expect.anything());
	expect(useUiStore.getState().moveSessionTarget).toBeNull();
	expect(useUiStore.getState().globalToast?.title).toBe("Moved to AO Cloud");
});

it("keeps the session here and offers a retry when the push fails", async () => {
	daemon.posts = {
		...moveReplies,
		"/api/v1/sessions/{sessionId}/move/export": { error: { code: "MOVE_REMOTE_AHEAD", message: "Origin's branch has commits this machine does not." } },
		"/api/v1/sessions/{sessionId}/resume-agent": { data: {} },
	};
	renderDialog();
	const user = userEvent.setup();
	await user.click(await screen.findByRole("button", { name: "Move to AO Cloud" }));
	expect(await screen.findByText("Origin's branch has commits this machine does not.")).toBeTruthy();
	expect(screen.getByText("The session continues here, unchanged.")).toBeTruthy();
	expect(screen.getByRole("button", { name: "Try again" })).toBeTruthy();
	expect(cloud.createSession).not.toHaveBeenCalled();
});

it("tries again with the same move id and Cloud request after a lost response", async () => {
	daemon.posts = { ...moveReplies, "/api/v1/sessions/{sessionId}/resume-agent": { data: {} } };
	cloud.createSession.mockRejectedValueOnce(new Error("network error")).mockResolvedValueOnce({ session: { id: "cloud-9" } });
	renderDialog();
	const user = userEvent.setup();
	await user.click(await screen.findByRole("button", { name: "Move to AO Cloud" }));
	await user.click(await screen.findByRole("button", { name: "Try again" }));
	await waitFor(() => expect(navigate).toHaveBeenCalledWith("cloud-proj", "cloud-9"));
	const [first, second] = cloud.createSession.mock.calls;
	expect(second).toEqual(first);
	expect(first[2]).toEqual({ idempotencyKey: expect.any(String) });
});

it.each([
	["the worktree cannot move", () => { daemon.preview = { error: { code: "MOVE_LFS", message: "The move includes Git LFS files, which cannot move yet." } }; }, "The move includes Git LFS files, which cannot move yet."],
	["AO Cloud cannot start a session on a branch", () => { cloud.features = []; }, "Needs a newer AO Cloud"],
	["no Cloud project has the repository", () => { daemon.repo = "https://github.com/acme/other"; }, "AO Cloud has no project for this repository."],
	["signed out of AO Cloud", () => { cloud.signedIn = false; }, "Sign in to AO Cloud to move this session."],
])("explains and disables Move when %s", async (_, setup, message) => {
	setup();
	renderDialog();
	expect(await screen.findByText(message)).toBeTruthy();
	await waitFor(() => expect(screen.getByRole("button", { name: "Move to AO Cloud" }).hasAttribute("disabled")).toBe(true));
});
