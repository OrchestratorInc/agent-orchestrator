import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { act, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { prepareChatComposerDelivery, writeChatComposerText } from "../lib/chat-drafts";
import type { ChatWorkspaceProps } from "./chat/ChatWorkspace";
import { NewChatPage } from "./NewChatPage";

const h = vi.hoisted(() => ({
	navigate: vi.fn(),
	launch: vi.fn(),
	launchFallback: vi.fn(),
	fallbackAction: undefined as string | undefined,
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
		return <>{props.draftNotice}</>;
	},
}));
vi.mock("./TaskComposer", () => ({
	useTaskLaunch: () => ({
		launch: h.launch,
		launchFallback: h.launchFallback,
		fallbackAction: h.fallbackAction,
		fallbackLabel: "Create as Terminal UI",
		submitMode: undefined,
		isCloudProject: false,
		canSubmit: true,
		launchSnapshot: () => ({ agent: "codex", model: "gpt-6", effort: "high", approvalMode: undefined, mode: undefined }),
		effectiveInterface: "chat",
		refreshModels: async () => undefined,
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

const draftScope = { sessionId: "new-chat:local", incarnation: "new-chat:local" };

function renderPage() {
	const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } });
	const page = (projectId: string) => <QueryClientProvider client={queryClient}><NewChatPage projectId={projectId} /></QueryClientProvider>;
	const { rerender } = render(page("proj-1"));
	return Object.assign(queryClient, { showProject: (projectId: string) => rerender(page(projectId)) });
}

beforeEach(() => {
	window.localStorage.removeItem("ao.newTask.pendingLaunch.v1");
	h.navigate.mockReset();
	h.launch.mockReset();
	h.launchFallback.mockReset();
	h.fallbackAction = undefined;
	h.workspace = undefined;
	window.localStorage.clear();
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
		await waitFor(() => expect(h.launch).toHaveBeenCalledWith("Fix the build", {
			clientRequestId: "cid-1",
			replay: { agent: "codex", model: "gpt-6", effort: "high", approvalMode: undefined, mode: undefined },
		}));

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

	it("takes attachments before a workspace exists and uploads them with the task", async () => {
		h.launch.mockResolvedValue("sess-1");
		renderPage();
		expect(h.workspace?.holdAttachments).toBe(true);
		const files = [{ mimeType: "image/png", data: "iVBORw==", name: "shot.png" }];

		await act(async () => { await h.workspace?.onSend?.("What is this?", files, "cid-1"); });

		expect(h.launch).toHaveBeenCalledWith("What is this?", expect.objectContaining({ clientRequestId: "cid-1", attachments: files }));
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

	it("becomes a fresh new chat when it moved to another project during the hand-off", async () => {
		let resolve!: (sessionId: string) => void;
		h.launch.mockReturnValue(new Promise<string>((done) => { resolve = done; }));
		const page = renderPage();
		let sent!: Promise<unknown>;
		act(() => { sent = Promise.resolve(h.workspace?.onSend?.("Fix the build", undefined, "cid-1")); });
		await waitFor(() => expect(h.launch).toHaveBeenCalled());
		// The session exists; before the page hands over, a new task opens for another project.
		await act(async () => {
			resolve("sess-1");
			page.showProject("proj-2");
			await sent;
		});
		await waitFor(() => expect(h.workspace?.localEchos).toBeUndefined());
		expect(h.navigate).not.toHaveBeenCalledWith(expect.objectContaining({ params: expect.objectContaining({ sessionId: "sess-1" }) }));
		expect(h.workspace?.busy).toBe(false);
	});

	it("starts the fallback once when its button is clicked twice", async () => {
		h.fallbackAction = "tui";
		h.launchFallback.mockReturnValue(new Promise<string>(() => undefined));
		writeChatComposerText(draftScope, "Fix the build");
		renderPage();
		const button = screen.getByRole("button", { name: "Create as Terminal UI" });
		act(() => {
			fireEvent.click(button);
			fireEvent.click(button);
		});
		expect(h.launchFallback).toHaveBeenCalledTimes(1);
	});

	it.each(["tui", "bypass-permissions"])("passes the current held files to the %s fallback and waits for preparation", async (fallback) => {
		h.fallbackAction = fallback;
		h.launchFallback.mockResolvedValue("fallback-session");
		writeChatComposerText(draftScope, "Read the attached file");
		renderPage();
		const files = [{ mimeType: "text/plain", data: btoa("hello"), name: "notes.txt" }];
		const removedFile = { mimeType: "image/png", data: "iVBORw==", name: "removed.png" };
		act(() => h.workspace?.onHeldAttachmentsChange?.([...files, removedFile], true));
		const button = screen.getByRole("button", { name: "Create as Terminal UI" });
		expect(button).toBeDisabled();

		act(() => h.workspace?.onHeldAttachmentsChange?.(files, false));
		await act(async () => fireEvent.click(button));
		expect(h.launchFallback).toHaveBeenCalledWith("Read the attached file", expect.any(String), files);
	});

	it("keeps a refused retry on its project and shows why", async () => {
		prepareChatComposerDelivery(draftScope, { kind: "send", clientMessageId: "cid-1", requestText: "Fix the build", composerText: "Fix the build", attachments: [] });
		h.launch.mockRejectedValueOnce(new TypeError("Failed to fetch"));
		renderPage();
		await act(async () => {
			await Promise.resolve(h.workspace?.onSend?.("Fix the build", undefined, "cid-1")).catch(() => undefined);
		});
		h.launch.mockRejectedValueOnce(Object.assign(new Error("Claude Code needs you to sign in again."), { name: "TaskCreateError", code: "AGENT_AUTH_REQUIRED" }));
		let outcome: unknown;
		await act(async () => {
			outcome = await Promise.resolve(h.workspace?.onSend?.("Fix the build", undefined, "cid-1")).catch((error: unknown) => error);
		});
		// The composer still holds the first attempt, whose outcome is unknown.
		expect(outcome).toMatchObject({ code: "AGENT_AUTH_REQUIRED" });
		expect(JSON.parse(window.localStorage.getItem("ao.newTask.pendingLaunch.v1") ?? "{}").local).toMatchObject({ clientMessageId: "cid-1", projectId: "proj-1" });
		expect(screen.getByRole("alert")).toHaveTextContent("Claude Code needs you to sign in again.");
	});
});
