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
	daemon: { posts: {} as Record<string, Reply>, repo: "" },
	navigate: vi.fn(),
	cloud: { features: [] as string[], projects: [] as unknown[], createSession: vi.fn(), signedIn: true },
}));

vi.mock("../lib/host-clients", () => ({
	clientForSessionHost: () => ({
		GET: vi.fn(async () => ({ data: { project: { id: "app", repo: daemon.repo } } })),
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

function renderDialog() {
	useUiStore.setState({ moveSessionTarget: session });
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
	cloud.projects = [{ id: "cloud-proj", repositoryUrl: "https://github.com/Acme/App" }];
	daemon.repo = "git@github.com:acme/app.git";
	daemon.posts = {};
	useUiStore.setState({ moveSessionTarget: null });
});

it("moves the session to the AO Cloud project with the same repository", async () => {
	daemon.posts = {
		"/api/v1/sessions/{sessionId}/exit-agent": { data: {} },
		"/api/v1/sessions/{sessionId}/move/export": { data: { manifest: { branch: "feat/x", tip: "abc", harness: "claude-code" }, handoffNote: "note" } },
		"/api/v1/sessions/{sessionId}/move/complete": { data: {} },
	};
	cloud.createSession.mockResolvedValue({ session: { id: "cloud-9" } });
	renderDialog();
	const user = userEvent.setup();
	expect(await screen.findByText("Uncommitted files are pushed to the session branch on GitHub.")).toBeTruthy();
	await user.click(screen.getByRole("button", { name: "Move" }));
	await screen.findByText("The session now runs on AO Cloud.");
	expect(cloud.createSession).toHaveBeenCalledWith("org-1", expect.objectContaining({ projectId: "cloud-proj", branch: "feat/x" }), expect.anything());
	await user.click(screen.getByRole("button", { name: "Open session" }));
	expect(navigate).toHaveBeenCalledWith("cloud-proj", "cloud-9");
	expect(useUiStore.getState().moveSessionTarget).toBeNull();
});

it("says the session continues here when the push fails", async () => {
	daemon.posts = {
		"/api/v1/sessions/{sessionId}/exit-agent": { data: {} },
		"/api/v1/sessions/{sessionId}/move/export": { error: { code: "MOVE_REMOTE_AHEAD", message: "Origin's branch has commits this machine does not." } },
		"/api/v1/sessions/{sessionId}/resume-agent": { data: {} },
	};
	renderDialog();
	const user = userEvent.setup();
	await user.click(await screen.findByRole("button", { name: "Move" }));
	expect(await screen.findByText("Origin's branch has commits this machine does not.")).toBeTruthy();
	expect(screen.getByText("The session continues here, unchanged.")).toBeTruthy();
	expect(cloud.createSession).not.toHaveBeenCalled();
});

it.each([
	["AO Cloud cannot start a session on a branch", () => { cloud.features = []; }, "Needs a newer AO Cloud"],
	["no Cloud project has the repository", () => { daemon.repo = "https://github.com/acme/other"; }, "AO Cloud has no project for this repository."],
	["signed out of AO Cloud", () => { cloud.signedIn = false; }, "Sign in to AO Cloud to move this session."],
])("explains and disables Move when %s", async (_, setup, message) => {
	setup();
	renderDialog();
	expect(await screen.findByText(message)).toBeTruthy();
	await waitFor(() => expect(screen.getByRole("button", { name: "Move" }).hasAttribute("disabled")).toBe(true));
});
