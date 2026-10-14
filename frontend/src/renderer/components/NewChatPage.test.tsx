import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { act, render, waitFor } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import type { ChatWorkspaceProps } from "./chat/ChatWorkspace";
import { NewChatPage } from "./NewChatPage";

const h = vi.hoisted(() => ({
	navigate: vi.fn(),
	launch: vi.fn(),
	workspace: undefined as ChatWorkspaceProps | undefined,
}));

vi.mock("@tanstack/react-router", () => ({
	useNavigate: () => h.navigate,
	useRouter: () => ({ preloadRoute: async () => undefined, history: { canGoBack: () => false, back: vi.fn() } }),
}));
vi.mock("./ShellTopbar", () => ({ topbarDragStyle: undefined }));
vi.mock("./settings/MobileDevicesSection", () => ({ mobileDevicesQueryKey: ["mobile-devices"], fetchDevices: async () => [] }));
vi.mock("./chat/ChatWorkspace", () => ({
	ChatWorkspace: (props: ChatWorkspaceProps) => {
		h.workspace = props;
		return null;
	},
}));
vi.mock("./TaskComposer", () => ({
	useTaskLaunch: () => ({
		launch: h.launch,
		launchFallback: vi.fn(),
		submitMode: undefined,
		isCloudProject: false,
		canSubmit: true,
		chatModels: [{ id: "gpt-6", displayName: "GPT-6", default: true }],
		turnSettings: { model: "gpt-6", reasoningEffort: "high", approvalMode: "auto" },
		chooseTurnSettings: vi.fn(),
		agent: { value: "codex" },
		model: {},
		effort: {},
		showEffort: false,
		renderAgentControl: () => null,
		renderModelControl: () => null,
		renderEffortControl: () => null,
	}),
}));
vi.mock("../hooks/useWorkspaceQuery", () => ({
	useWorkspaceQuery: () => ({ data: [{ id: "proj-1", name: "Project", kind: "project", sessions: [] }] }),
	useRemoteWorkspaces: () => ({ data: [] }),
	workspaceQueryKeyForHost: () => ["workspaces"],
}));

function renderPage() {
	const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } });
	render(<QueryClientProvider client={queryClient}><NewChatPage projectId="proj-1" /></QueryClientProvider>);
	return queryClient;
}

beforeEach(() => {
	h.navigate.mockReset();
	h.launch.mockReset();
	h.workspace = undefined;
});

describe("NewChatPage", () => {
	it("creates the session on first send and hands its brief to the session chat", async () => {
		let resolve!: (sessionId: string) => void;
		h.launch.mockReturnValue(new Promise<string>((done) => { resolve = done; }));
		const queryClient = renderPage();
		expect(h.workspace?.draftSettings).toBeTruthy();

		let sent!: Promise<unknown>;
		act(() => { sent = Promise.resolve(h.workspace?.onSend?.("Fix the build", undefined, "cid-1")); });
		// While the session is created the brief is already the user's turn; only the
		// project picker leaves, and the agent, model, and effort hold still.
		expect(h.workspace?.localEchos).toEqual([expect.objectContaining({ clientMessageId: "cid-1", text: "Fix the build" })]);
		expect(h.workspace?.draftSettings).toEqual(expect.objectContaining({ props: expect.objectContaining({
			harness: "codex",
			settings: { model: "gpt-6", reasoningEffort: "high", approvalMode: "auto" },
		}) }));
		expect(h.workspace?.openingStartedAt).toEqual(expect.any(String));
		await waitFor(() => expect(h.launch).toHaveBeenCalledWith("Fix the build", { interfaceMode: undefined, clientRequestId: "cid-1" }));

		await act(async () => {
			resolve("sess-1");
			await sent;
		});
		expect(queryClient.getQueryData(["session-launch-settings", "sess-1"])).toEqual(expect.objectContaining({
			harness: "codex",
			settings: { model: "gpt-6", reasoningEffort: "high", approvalMode: "auto" },
		}));
		expect(queryClient.getQueryData(["session-opening-started", "sess-1"])).toEqual(expect.any(String));
		expect(queryClient.getQueryData(["conversation-local-echos"])).toEqual({
			"sess-1": [expect.objectContaining({ clientMessageId: "cid-1", text: "Fix the build", opening: true })],
		});
		await waitFor(() => expect(h.navigate).toHaveBeenCalledWith({
			to: "/projects/$projectId/sessions/$sessionId",
			params: { projectId: "proj-1", sessionId: "sess-1" },
			replace: true,
		}));
	});

	it("returns a refused brief to the composer and restores the selectors", async () => {
		h.launch.mockRejectedValue(Object.assign(new Error("Agent is not installed"), { name: "TaskCreateError", code: "AGENT_NOT_INSTALLED" }));
		renderPage();
		let outcome: unknown;
		await act(async () => {
			outcome = await Promise.resolve(h.workspace?.onSend?.("Fix the build", undefined, "cid-1")).catch((error: unknown) => error);
		});
		expect(outcome).toMatchObject({ message: "Agent is not installed", code: "TASK_CREATE_FAILED" });
		expect(h.workspace?.localEchos).toBeUndefined();
		expect(h.workspace?.draftSettings).toBeTruthy();
		expect(h.navigate).not.toHaveBeenCalled();
	});

	it("leaves a lost response uncertain so the composer retries with the same request id", async () => {
		h.launch.mockRejectedValue(new TypeError("Failed to fetch"));
		renderPage();
		let outcome: unknown;
		await act(async () => {
			outcome = await Promise.resolve(h.workspace?.onSend?.("Fix the build", undefined, "cid-1")).catch((error: unknown) => error);
		});
		expect(outcome).toBeInstanceOf(TypeError);
		expect(outcome).not.toHaveProperty("code");
		expect(h.navigate).not.toHaveBeenCalled();
	});
});
