import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import userEvent from "@testing-library/user-event";
import { act, cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { prepareChatComposerDelivery, readChatSessionDraft, writeChatComposerText } from "../lib/chat-drafts";
import { REPORT_PROBLEM_PROMPT } from "../lib/report-problem";
import type { ApprovalMode, ChatModel } from "../types/conversation";
import { rememberChatSettingsTemplate } from "../lib/chat-settings-template";
import type { PickerSource } from "./chat/AgentSwitchPicker";
import type { ChatWorkspaceProps } from "./chat/ChatWorkspace";
import { NewChatPage } from "./NewChatPage";
import { useUiStore } from "../stores/ui-store";
import { rememberStandaloneAgent } from "../lib/cloud-standalone-agents";
import { CLOUD_STANDALONE_WORKSPACE_ID, STANDALONE_WORKSPACE_ID } from "../types/workspace";

const h = vi.hoisted(() => ({
	navigate: vi.fn(),
	launch: vi.fn(),
	launchFallback: vi.fn(),
	fallbackAction: undefined as string | undefined,
	isCloudProject: false,
	error: undefined as string | undefined,
	status: undefined as string | undefined,
	errorAction: undefined as { label: string; onClick: () => void; input?: { label: string; value: string; onChange: (value: string) => void } } | undefined,
	replay: { agent: "codex", model: "gpt-6", effort: "high", approvalMode: undefined as string | undefined, mode: undefined as string | undefined },
	workspace: undefined as ChatWorkspaceProps | undefined,
	harness: "codex",
	chatModels: [{ id: "gpt-6", displayName: "GPT-6", default: true }] as ChatModel[],
	model: "gpt-6",
	approvalMode: "auto" as ApprovalMode,
	approvalModes: undefined as ApprovalMode[] | undefined,
	modelWarning: undefined as string | undefined,
	modelWarningRetries: true,
	offering: { ready: false, loaded: false, org: undefined as { id: string } | undefined, features: [] as string[], maxAttachmentBytes: undefined as number | undefined },
	workspaces: [] as Array<Record<string, unknown>>,
}));

vi.mock("@tanstack/react-router", () => ({
	useNavigate: () => h.navigate,
	useRouter: () => ({ preloadRoute: async () => undefined, history: { canGoBack: () => false, back: vi.fn() } }),
}));
// What the settings bar's agent and model picker is given; the picker is tested on its own.
const modelPicker = vi.hoisted(() => ({ current: undefined as PickerSource | undefined }));
vi.mock("./chat/AgentSwitchPicker", async (original) => ({
	...await original<typeof import("./chat/AgentSwitchPicker")>(),
	AgentModelPicker: ({ current }: { current: PickerSource }) => {
		modelPicker.current = current;
		return null;
	},
}));
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
		error: h.error,
		status: h.status,
		errorAction: h.errorAction,
		submitMode: undefined,
		isCloudProject: h.isCloudProject,
		canSubmit: true,
		catalogProjectId: "proj-1",
		launchSnapshot: () => ({ ...h.replay }),
		effectiveInterface: "chat",
		refreshModels: async () => undefined,
		modelWarning: h.modelWarning,
		modelWarningRetries: h.modelWarningRetries,
		chatModels: h.chatModels,
		turnSettings: { model: h.model, reasoningEffort: "high", approvalMode: h.approvalMode },
		approvalModes: h.approvalModes,
		chooseTurnSettings: vi.fn(),
		agent: { value: h.harness },
		model: {},
		effort: {},
		showEffort: false,
		renderAgentControl: () => null,
		renderModelControl: () => null,
		renderEffortControl: () => null,
	}),
}));
vi.mock("../hooks/useCloudOffering", () => ({ useCloudOffering: () => h.offering }));
vi.mock("../hooks/useWorkspaceQuery", () => ({
	useWorkspaceQuery: () => ({ data: h.workspaces }),
	useRemoteWorkspaces: () => ({ data: [] }),
	workspaceQueryKeyForHost: () => ["workspaces"],
}));

const draftScope = { sessionId: "new-chat:local", incarnation: "new-chat:local" };

function renderPage() {
	const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } });
	const page = (projectId: string) => <QueryClientProvider client={queryClient}><NewChatPage projectId={projectId} /></QueryClientProvider>;
	const { rerender } = render(page("proj-1"));
	return Object.assign(queryClient, { showProject: (projectId: string) => rerender(page(projectId)), refresh: () => rerender(page("proj-1")) });
}

beforeEach(() => {
	window.localStorage.removeItem("ao.newTask.pendingLaunch.v1");
	h.navigate.mockReset();
	h.launch.mockReset();
	h.launchFallback.mockReset();
	h.fallbackAction = undefined;
	h.isCloudProject = false;
	h.error = undefined;
	h.status = undefined;
	h.errorAction = undefined;
	h.replay = { agent: "codex", model: "gpt-6", effort: "high", approvalMode: undefined, mode: undefined };
	h.workspace = undefined;
	h.harness = "codex";
	h.chatModels = [{ id: "gpt-6", displayName: "GPT-6", default: true }];
	h.model = "gpt-6";
	modelPicker.current = undefined;
	h.approvalMode = "auto";
	h.approvalModes = undefined;
	h.modelWarning = undefined;
	h.modelWarningRetries = true;
	h.offering = { ready: false, loaded: false, org: undefined, features: [], maxAttachmentBytes: undefined };
	h.workspaces = [{ id: "proj-1", name: "Project", kind: "project", sessions: [] }];
	window.localStorage.clear();
});

describe("NewChatPage", () => {
	it("moves off a Cloud standalone agent's project once it knows it: a shared one to the Scratchpad, the person's own to a new agent", () => {
		h.workspaces = [
			{ id: "proj-1", name: "Project", kind: "project", sessions: [] },
			{ id: "agent-shared", name: "Their agent", kind: "cloud", standaloneAgent: true, shared: { source: "link" }, sessions: [] },
			{ id: "agent-1", name: "My agent", kind: "cloud", standaloneAgent: true, sessions: [] },
		];
		const page = renderPage();
		expect(h.navigate).not.toHaveBeenCalled();
		page.showProject("agent-shared");
		expect(h.navigate).toHaveBeenLastCalledWith({ to: "/new", search: { project: undefined, host: undefined }, replace: true });
		page.showProject("agent-1");
		expect(h.navigate).toHaveBeenLastCalledWith({ to: "/new", search: { project: CLOUD_STANDALONE_WORKSPACE_ID, host: undefined }, replace: true });
	});

	it("offers Retry beside a model notice only when reading the list again can clear it", () => {
		h.modelWarning = "Couldn't load Cloud's models for Codex.";
		renderPage();
		expect(screen.getByText("Couldn't load Cloud's models for Codex.")).toBeInTheDocument();
		expect(screen.getByRole("button", { name: "Retry" })).toBeInTheDocument();
		cleanup();
		h.modelWarning = "This project's model, gpt-x, isn't available to your Codex account on Cloud, so its tasks use the default. Change it in Project settings.";
		h.modelWarningRetries = false;
		renderPage();
		expect(screen.getByText(/isn't available to your Codex account on Cloud/)).toBeInTheDocument();
		expect(screen.queryByRole("button", { name: "Retry" })).toBeNull();
	});
	it("shows a confirmed branch choice as status before the next send", () => {
		h.status = "Branch fix/login selected for your next task.";
		renderPage();
		expect(screen.getByRole("status").textContent).toContain("fix/login");
		expect(screen.queryByRole("alert")).toBeNull();
	});
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

	it("starts a cloud project's task on the page, with its approval mode and without attachments", async () => {
		h.isCloudProject = true;
		h.launch.mockResolvedValue("cloud-sess-1");
		const queryClient = renderPage();
		const queryClientEchos = () => queryClient.getQueryData(["conversation-local-echos"]);
		// No hand-over to the old dialog: the page stays and offers the same composer.
		expect(h.navigate).not.toHaveBeenCalled();
		expect(h.workspace?.holdAttachments).toBe(false);
		expect(h.workspace?.skills).toEqual([]);
		// The settings bar (inside the draft's fragment) offers the approval mode.
		expect(h.workspace?.draftSettings).toEqual(expect.objectContaining({ props: { children: expect.objectContaining({ props: expect.objectContaining({ showApprovalMode: true }) }) } }));
		await act(async () => { await h.workspace?.onSend?.("Fix the build", undefined, "cid-1"); });
		expect(h.launch).toHaveBeenCalledWith("Fix the build", expect.objectContaining({ clientRequestId: "cid-1" }));
		// A cloud session opens on its own surface, so no local echo is seeded for it.
		expect(queryClientEchos()).toBeUndefined();
		await waitFor(() => expect(h.navigate).toHaveBeenCalledWith(expect.objectContaining({ params: { projectId: "proj-1", sessionId: "cloud-sess-1" } })));
	});

	it("offers Cloud sessions beside the local one where Cloud starts standalone agents", async () => {
		const user = userEvent.setup();
		// The project picker sits in the composer's tray.
		const openPicker = async () => {
			cleanup();
			render(<QueryClientProvider client={new QueryClient()}><NewChatPage projectId={STANDALONE_WORKSPACE_ID} /></QueryClientProvider>);
			const tray = h.workspace?.composerTray;
			cleanup();
			render(<QueryClientProvider client={new QueryClient()}>{tray}</QueryClientProvider>);
			await user.click(screen.getByRole("button", { name: "Project: Sessions" }));
		};
		await openPicker();
		expect(await screen.findByRole("menuitem", { name: "Sessions" })).toBeInTheDocument();
		expect(screen.queryByRole("menuitem", { name: "Cloud sessions" })).toBeNull();

		h.offering = { ready: true, loaded: true, org: { id: "org-1" }, features: ["share", "standalone_agents"], maxAttachmentBytes: undefined };
		await openPicker();
		await user.click(await screen.findByRole("menuitem", { name: "Cloud sessions" }));
		expect(h.navigate).toHaveBeenCalledWith(expect.objectContaining({ to: "/new", search: expect.objectContaining({ project: CLOUD_STANDALONE_WORKSPACE_ID }) }));
	});

	// A Cloud standalone agent starts without files: its own project comes
	// with its session, so a start the files did not reach would leave it behind.
	it("offers files on a Cloud project's task where the control plane takes them, and not on a Cloud standalone agent", () => {
		h.isCloudProject = true;
		h.offering = { ready: true, loaded: true, org: { id: "org-1" }, features: ["attachments", "standalone_agents"], maxAttachmentBytes: 25 * 1024 * 1024 };
		renderPage();
		expect(h.workspace?.holdAttachments).toBe(true);
		expect(h.workspace?.cloudAttachments).toEqual({ maxBytes: 25 * 1024 * 1024 });
		expect(h.workspace?.attachmentsRefusal).toBeUndefined();
		cleanup();
		render(<QueryClientProvider client={new QueryClient()}><NewChatPage projectId={CLOUD_STANDALONE_WORKSPACE_ID} /></QueryClientProvider>);
		expect(h.workspace?.holdAttachments).toBe(false);
		expect(h.workspace?.cloudAttachments).toBeUndefined();
		// A draft that brought files from another target keeps them, with why this one takes none.
		expect(h.workspace?.attachmentsRefusal).toBe("Files can't start a standalone agent. Remove them to continue.");
	});

	// A send whose answer was lost keeps the files it went with until its
	// outcome is known, even if the target stopped taking files meanwhile:
	// its retry carries them under the same request, and only a new send
	// meets the refusal.
	it("keeps a pending send's files held, without the refusal, until its outcome is known", async () => {
		h.isCloudProject = true;
		h.offering = { ready: true, loaded: true, org: { id: "org-1" }, features: [], maxAttachmentBytes: undefined };
		const page = renderPage();
		expect(h.workspace?.holdAttachments).toBe(false);
		expect(h.workspace?.attachmentsRefusal).toBe("Files can't start a task in this Cloud project yet. Remove them to continue.");
		prepareChatComposerDelivery(draftScope, { kind: "send", clientMessageId: "cid-1", requestText: "Read this", composerText: "Read this", attachments: [] });
		h.launch.mockRejectedValue(new TypeError("Failed to fetch"));
		await act(async () => {
			await Promise.resolve(h.workspace?.onSend?.("Read this", [{ mimeType: "text/plain", data: "aGk=" }], "cid-1")).catch(() => undefined);
		});
		page.refresh();
		expect(h.workspace?.holdAttachments).toBe(true);
		expect(h.workspace?.attachmentsRefusal).toBeUndefined();
	});

	it("opens a new Cloud standalone agent on the project the control plane made for it", async () => {
		h.isCloudProject = true;
		h.launch.mockImplementation(async () => {
			rememberStandaloneAgent("agent-sess-1", "agent-1");
			return "agent-sess-1";
		});
		render(<QueryClientProvider client={new QueryClient()}><NewChatPage projectId={CLOUD_STANDALONE_WORKSPACE_ID} /></QueryClientProvider>);
		await act(async () => { await h.workspace?.onSend?.("Fix the build", undefined, "cid-1"); });
		await waitFor(() => expect(h.navigate).toHaveBeenCalledWith(expect.objectContaining({
			to: "/projects/$projectId/sessions/$sessionId",
			params: { projectId: "agent-1", sessionId: "agent-sess-1" },
		})));
	});

	it("takes attachments before a workspace exists and uploads them with the task", async () => {
		h.launch.mockResolvedValue("sess-1");
		renderPage();
		expect(h.workspace?.holdAttachments).toBe(true);
		const files = [{ mimeType: "image/png", data: "iVBORw==", name: "shot.png" }];

		await act(async () => { await h.workspace?.onSend?.("What is this?", files, "cid-1"); });

		expect(h.launch).toHaveBeenCalledWith("What is this?", expect.objectContaining({ clientRequestId: "cid-1", attachments: files }));
	});

	it("takes the session's top bar from the send until the page returns the brief or closes", async () => {
		useUiStore.setState({ newChatSending: false });
		let refuse!: (error: unknown) => void;
		h.launch.mockReturnValueOnce(new Promise<string>((_, fail) => { refuse = fail; }));
		renderPage();
		expect(useUiStore.getState().newChatSending).toBe(false);

		let sent!: Promise<unknown>;
		act(() => { sent = Promise.resolve(h.workspace?.onSend?.("Fix the build", undefined, "cid-1")).catch(() => undefined); });
		// Set before the first message paints, so it lands where the session shows it.
		expect(useUiStore.getState().newChatSending).toBe(true);
		await waitFor(() => expect(h.launch).toHaveBeenCalled());
		await act(async () => {
			refuse(Object.assign(new Error("Agent is not installed"), { name: "TaskCreateError", code: "AGENT_NOT_INSTALLED" }));
			await sent;
		});
		expect(useUiStore.getState().newChatSending).toBe(false);

		// Leaving mid-send gives the bar back too.
		h.launch.mockReturnValueOnce(new Promise<string>(() => undefined));
		act(() => { void Promise.resolve(h.workspace?.onSend?.("Fix the build", undefined, "cid-2")).catch(() => undefined); });
		expect(useUiStore.getState().newChatSending).toBe(true);
		cleanup();
		expect(useUiStore.getState().newChatSending).toBe(false);
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

	it("clears a typed model refusal so the next send uses the reviewed choice", async () => {
		h.launch.mockRejectedValueOnce(Object.assign(new Error("Review the agent default and start again"), { name: "TaskCreateError", code: "UNSUPPORTED_MODEL" }))
			.mockResolvedValueOnce("sess-2");
		renderPage();
		let refusal: unknown;
		await act(async () => {
			refusal = await Promise.resolve(h.workspace?.onSend?.("Fix the build", undefined, "cid-1")).catch((error: unknown) => error);
		});
		expect(refusal).toMatchObject({ code: "TASK_CREATE_FAILED" });
		expect(JSON.parse(window.localStorage.getItem("ao.newTask.pendingLaunch.v1") ?? "{}")).toEqual({});
		h.replay = { ...h.replay, model: "agent-default" };
		await act(async () => { await h.workspace?.onSend?.("Fix the build", undefined, "cid-2"); });
		expect(h.launch).toHaveBeenNthCalledWith(2, "Fix the build", expect.objectContaining({
			clientRequestId: "cid-2",
			replay: expect.objectContaining({ model: "agent-default" }),
		}));
	});

	it("shows the new branch input after a primary send conflict", async () => {
		h.launch.mockRejectedValue(new Error("Branch is in use"));
		const page = renderPage();
		await act(async () => {
			await Promise.resolve(h.workspace?.onSend?.("Fix the build", undefined, "cid-1")).catch(() => undefined);
		});
		const chooseBranch = vi.fn();
		h.error = "That branch is open in another worktree.";
		h.errorAction = {
			label: "Choose new branch",
			onClick: chooseBranch,
			input: {
				label: "New branch name",
				value: "",
				onChange: (value) => {
					if (h.errorAction?.input) h.errorAction.input.value = value;
					page.refresh();
				},
			},
		};
		act(() => page.refresh());
		expect(screen.getByRole("alert")).toHaveTextContent(h.error);
		const button = screen.getByRole("button", { name: "Choose new branch" });
		expect(button).toBeDisabled();
		fireEvent.change(screen.getByRole("textbox", { name: "New branch name" }), { target: { value: "fix/new-branch" } });
		expect(button).toBeEnabled();
		fireEvent.click(button);
		expect(chooseBranch).toHaveBeenCalledOnce();
	});

	it("shows the install action after a primary send reports a missing agent", async () => {
		h.launch.mockRejectedValue(new Error("Agent binary not found"));
		const page = renderPage();
		await act(async () => {
			await Promise.resolve(h.workspace?.onSend?.("Fix the build", undefined, "cid-1")).catch(() => undefined);
		});
		const openSettings = vi.fn();
		h.error = "This agent is not installed.";
		h.errorAction = { label: "Open Agent Harnesses", onClick: openSettings };
		act(() => page.refresh());
		expect(screen.getByRole("alert")).toHaveTextContent(h.error);
		fireEvent.click(screen.getByRole("button", { name: "Open Agent Harnesses" }));
		expect(openSettings).toHaveBeenCalledOnce();
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

	// A Cloud create's error carries the control plane's HTTP status.
	const createError = (message: string, status: number, code?: string) =>
		Object.assign(new Error(message), { name: "TaskCreateError", status, ...(code ? { code } : {}) });

	it.each([
		{ status: 500, code: "internal_error" },
		{ status: 502, code: undefined },
		{ status: 503, code: "unavailable" },
		{ status: 504, code: undefined },
		{ status: 429, code: "rate_limited" },
		{ status: 429, code: undefined },
	])("leaves a $status ($code) answer to a create uncertain, so the composer retries it with the same request id", async ({ status, code }) => {
		h.isCloudProject = true;
		// The composer holds the send while it is in flight, as it does in the app.
		prepareChatComposerDelivery(draftScope, { kind: "send", clientMessageId: "cid-1", requestText: "Fix the build", composerText: "Fix the build", attachments: [] });
		h.launch.mockRejectedValue(createError("The control plane could not finish the request.", status, code));
		renderPage();
		let outcome: unknown;
		await act(async () => {
			outcome = await Promise.resolve(h.workspace?.onSend?.("Fix the build", undefined, "cid-1")).catch((error: unknown) => error);
		});
		// The error itself, not a final refusal: the composer keeps the send and offers "Retry message safely".
		expect(outcome).toMatchObject({ status });
		expect(outcome).not.toMatchObject({ code: "TASK_CREATE_FAILED" });
		expect(JSON.parse(window.localStorage.getItem("ao.newTask.pendingLaunch.v1") ?? "{}").local).toMatchObject({ clientMessageId: "cid-1" });
		expect(h.navigate).not.toHaveBeenCalled();
	});

	it.each([
		{ status: 409, code: "WORKER_SLOTS_FULL", message: "All your plan's running sessions are in use." },
		{ status: 402, code: "PLAN_REQUIRED", message: "Cloud sessions need a plan." },
		{ status: 429, code: "WEEKLY_LIMIT_REACHED", message: "You have used this week's Cloud hours." },
		{ status: 422, code: "validation_error", message: "The brief is too long." },
	])("keeps a $status $code refusal final, with its own message", async ({ status, code, message }) => {
		h.isCloudProject = true;
		h.launch.mockRejectedValue(createError(message, status, code));
		renderPage();
		let outcome: unknown;
		await act(async () => {
			outcome = await Promise.resolve(h.workspace?.onSend?.("Fix the build", undefined, "cid-1")).catch((error: unknown) => error);
		});
		expect(outcome).toMatchObject({ message, code: "TASK_CREATE_FAILED" });
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

describe("New chat opened from Report a problem", () => {
	const saveSend = (projectId: string) => window.localStorage.setItem("ao.newTask.pendingLaunch.v1", JSON.stringify({
		local: { clientMessageId: "cid-1", projectId, replay: {}, settings: {} },
	}));
	const renderReport = (projectId: string, report: boolean) => {
		const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } });
		const page = (id: string) => <QueryClientProvider client={queryClient}><NewChatPage projectId={id} report={report} /></QueryClientProvider>;
		const { rerender } = render(page(projectId));
		return { queryClient, refresh: () => rerender(page(projectId)) };
	};
	const picker = (queryClient: QueryClient) => {
		render(<QueryClientProvider client={queryClient}>{h.workspace?.composerTray}</QueryClientProvider>);
		return screen.getByRole("button", { name: /^Project: / });
	};
	beforeEach(() => prepareChatComposerDelivery(draftScope, { kind: "send", clientMessageId: "cid-1", requestText: "Fix the build", composerText: "Fix the build", attachments: [] }));

	it.each([
		{ report: true, heading: "What went wrong?" },
		{ report: false, heading: undefined },
	])("overrides the welcome heading only for report mode (report: $report)", ({ report, heading }) => {
		renderReport(STANDALONE_WORKSPACE_ID, report);
		expect(h.workspace?.welcomeHeading).toBe(heading);
	});

	it("stays on Scratchpad with the project picker locked", () => {
		const { queryClient } = renderReport(STANDALONE_WORKSPACE_ID, true);
		expect(picker(queryClient)).toBeDisabled();
		expect(h.navigate).not.toHaveBeenCalled();
	});

	it("leaves the project picker open on a normal new chat", () => {
		const { queryClient } = renderReport(STANDALONE_WORKSPACE_ID, false);
		expect(picker(queryClient)).toBeEnabled();
	});

	it("follows a saved send to its project, still as a report", () => {
		saveSend("proj-1");
		renderReport(STANDALONE_WORKSPACE_ID, true);
		expect(h.navigate).toHaveBeenCalledWith({ to: "/new", search: { project: "proj-1", host: undefined, report: true }, replace: true });
	});

	it("waits for a saved send on its project, then returns to Scratchpad", () => {
		saveSend("proj-1");
		const { refresh } = renderReport("proj-1", true);
		expect(h.navigate).not.toHaveBeenCalled();
		window.localStorage.removeItem("ao.newTask.pendingLaunch.v1");
		refresh();
		expect(h.navigate).toHaveBeenCalledWith({ to: "/new", search: { host: undefined, report: true }, replace: true });
	});

	it("leaves a normal new chat on its project", () => {
		renderReport("proj-1", false);
		expect(h.navigate).not.toHaveBeenCalled();
	});

	it.each([
		{ report: false, left: "keep this" },
		{ report: true, left: `${REPORT_PROBLEM_PROMPT}\n\nkeep this` },
	])("takes the report prompt out only for a normal new chat (report: $report)", ({ report, left }) => {
		window.localStorage.clear();
		writeChatComposerText(draftScope, `${REPORT_PROBLEM_PROMPT}\n\nkeep this`);
		renderReport(STANDALONE_WORKSPACE_ID, report);
		expect(readChatSessionDraft(draftScope).composer.text).toBe(left);
	});
});

describe("New Chat permission menu capabilities", () => {
	it.each([false, true])("offers only Kimi's default with saved template %s", async (saved) => {
		h.harness = "kimi";
		h.approvalMode = "default";
		h.approvalModes = ["default"];
		if (saved) rememberChatSettingsTemplate("kimi", undefined, { options: [
			{ id: "model", name: "Model", category: "model", type: "select", currentValue: "gpt-6", choices: [{ value: "gpt-6", name: "GPT-6" }] },
			{ id: "mode", name: "Mode", category: "mode", type: "select", currentValue: "auto", choices: [
				{ value: "default", name: "Default", permissionMode: "default" },
				{ value: "auto", name: "Auto", permissionMode: "auto" },
				{ value: "bypass", name: "Bypass", permissionMode: "bypass-permissions" },
			] },
		] });
		const queryClient = renderPage();
		render(<QueryClientProvider client={queryClient}>{h.workspace?.draftSettings}</QueryClientProvider>);
		await userEvent.click(screen.getByRole("button", { name: saved ? "Mode" : "Approval policy for the next turn" }));
		expect(screen.getAllByRole("menuitemradio")).toHaveLength(1);
		expect(screen.getByRole("menuitemradio")).toHaveTextContent("Use agent permissions");
		expect(screen.queryByRole("menuitemradio", { name: /Auto-approve/ })).toBeNull();
		expect(screen.queryByRole("menuitemradio", { name: /Bypass/ })).toBeNull();
	});
	it("does not advertise generic permissions before capability discovery", () => {
		h.harness = "kimi";
		h.approvalModes = [];
		const queryClient = renderPage();
		render(<QueryClientProvider client={queryClient}>{h.workspace?.draftSettings}</QueryClientProvider>);
		expect(screen.queryByRole("button", { name: "Approval policy for the next turn" })).toBeNull();
		expect(screen.queryByText("Auto-approve")).toBeNull();
	});
});

describe("New Chat model menu", () => {
	// This computer's Claude Code defaults to Fable, so its saved bar describes its
	// Default row as Fable 5.2 beside a Fable 5.2 row of its own.
	const rememberLocalClaudeBar = () => rememberChatSettingsTemplate("claude-code", undefined, { options: [
		{ id: "model", name: "Model", category: "model", type: "select", currentValue: "default", choices: [
			{ value: "default", name: "Default (recommended)", description: "Fable 5.2" },
			{ value: "sonnet", name: "Sonnet 5.5" },
			{ value: "fable", name: "Fable 5.2" },
			{ value: "opus", name: "Opus 5.5" },
		] },
	] });
	const catalog: ChatModel[] = [
		{ id: "sonnet", displayName: "Sonnet 5.5", default: true },
		{ id: "fable", displayName: "Fable 5.2", default: false },
		{ id: "opus", displayName: "Opus 5.5", default: false },
	];

	it("lists Cloud's own models for a Cloud task, not this computer's saved Claude Code bar", () => {
		h.harness = "claude-code";
		h.isCloudProject = true;
		h.chatModels = catalog;
		h.model = "sonnet";
		rememberLocalClaudeBar();
		const queryClient = renderPage();
		// Drawn from Cloud's catalog, as the session's own bar is, not from this computer's agent.
		expect(h.workspace?.draftSettings).toEqual(expect.objectContaining({ props: { children: expect.objectContaining({
			props: expect.objectContaining({ models: catalog, configOptions: undefined }),
		}) } }));
		render(<QueryClientProvider client={queryClient}>{h.workspace?.draftSettings}</QueryClientProvider>);
		expect(modelPicker.current?.models.map((model) => [model.id, model.label])).toEqual([
			["sonnet", "Sonnet 5.5"], ["fable", "Fable 5.2"], ["opus", "Opus 5.5"],
		]);
		expect(modelPicker.current?.modelId).toBe("sonnet");
	});

	it("keeps the model this computer's agent defaults to in a local task's menu", () => {
		h.harness = "claude-code";
		h.chatModels = catalog;
		h.model = "sonnet";
		rememberLocalClaudeBar();
		const queryClient = renderPage();
		render(<QueryClientProvider client={queryClient}>{h.workspace?.draftSettings}</QueryClientProvider>);
		// Drawn from the agent's saved controls, which a new task can start with.
		expect(h.workspace?.draftSettings).toEqual(expect.objectContaining({ props: { children: expect.objectContaining({
			props: expect.objectContaining({ configOptions: [expect.objectContaining({ id: "model", currentValue: "sonnet" })] }),
		}) } }));
		expect(modelPicker.current?.models.map((model) => [model.id, model.label])).toEqual([
			["sonnet", "Sonnet 5.5"], ["fable", "Fable 5.2"], ["opus", "Opus 5.5"],
		]);
	});
});
