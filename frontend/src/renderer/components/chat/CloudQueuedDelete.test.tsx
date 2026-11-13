import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { renderChat as render } from "../../test/render-chat";
import { CloudCpError } from "../../lib/cloud-cp/errors";
import { CloudSessionChatSurface } from "./CloudSessionChatSurface";
import type { WorkspaceSession } from "../../types/workspace";

// The Cloud chat with its real ChatWorkspace and queue dock: what the person
// sees and clicks, against a mocked control plane.
const cloudMocks = vi.hoisted(() => ({
	listChatEvents: vi.fn(), sendSessionMessage: vi.fn(), cancelTurn: vi.fn(), steerTurn: vi.fn(),
	editQueuedMessage: vi.fn(), deleteQueuedMessage: vi.fn(), steerQueuedMessage: vi.fn(), decideChatApproval: vi.fn(),
	listChatModels: vi.fn(), listUserAgentModels: vi.fn(), resumeSession: vi.fn(), requestWorkspaceCheckout: vi.fn(),
}));
vi.mock("../../hooks/useCloudCp", () => ({ useCloudCp: () => ({ ready: true, client: cloudMocks, baseUrl: "", userId: "user-1" }) }));
vi.mock("../../hooks/useCloudOffering", () => ({ useCloudOffering: () => ({ features: ["queued-delete"] }) }));

const session = {
	id: "session-1", workspaceId: "project-1", workspaceName: "project", title: "Cloud session", provider: "codex",
	kind: "worker", mode: "chat", status: "working", runtimeConnected: true, updatedAt: "2026-09-22T00:00:00Z", prs: [],
	cloud: { orgId: "org-1" },
} as unknown as WorkspaceSession;

const at = (sequence: number, type: string, payload: Record<string, unknown>) =>
	({ sessionId: session.id, sequence, type, payload, createdAt: `2026-11-13T09:0${sequence}:00Z` });
// A working session with an orchestrator's message and then the person's own queued behind its turn.
const queued = [
	at(1, "chat.user_message", { text: "long job", turnId: "turn-run", authorUserId: "user-1" }),
	at(2, "chat.turn_started", { turnId: "turn-run", attempt: 1 }),
	at(3, "chat.user_message", { text: "report progress", turnId: "turn-orch", origin: "automation", senderLabel: "Orchestrator" }),
	at(4, "chat.user_message", { text: "mine, later", turnId: "turn-mine", authorUserId: "user-1" }),
];
const deleted = [...queued, at(5, "chat.turn_interrupted", { turnId: "turn-mine", attempt: 0, reason: "deleted" })];
const page = (events: unknown[]) => ({ events, hasMore: false, nextAfter: events.length, hasOlder: false, nextBefore: 1 });

function mount() {
	const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } });
	return render(<QueryClientProvider client={queryClient}><CloudSessionChatSurface session={session} /></QueryClientProvider>);
}
const row = () => screen.queryByTestId("queued-message-turn-mine");
const deleteButton = () => within(row()!).getByRole("button", { name: "Delete queued message" });

describe("Delete on a Cloud chat's queue", () => {
	beforeEach(() => {
		for (const mock of Object.values(cloudMocks)) mock.mockReset();
		cloudMocks.listChatModels.mockResolvedValue({ models: [] });
		cloudMocks.listUserAgentModels.mockResolvedValue({ models: [] });
	});

	// Delete reaches the control plane, holds the row (disabled) while it is in
	// flight, and the message is gone once the control plane recorded it, after
	// a remount too, since the chat reads the delete from its events.
	it("deletes the queued message on the control plane, and it stays gone after a remount", async () => {
		cloudMocks.listChatEvents.mockResolvedValue(page(queued));
		let answer: (value: unknown) => void = () => undefined;
		cloudMocks.deleteQueuedMessage.mockImplementation(() => new Promise((resolve) => { answer = resolve; }));
		const view = mount();
		await waitFor(() => expect(row()).toBeInTheDocument());
		await userEvent.click(deleteButton());
		expect(cloudMocks.deleteQueuedMessage).toHaveBeenCalledWith("org-1", session.id, "turn-mine");
		expect(cloudMocks.cancelTurn).not.toHaveBeenCalled();
		await waitFor(() => expect(deleteButton()).toBeDisabled());

		cloudMocks.listChatEvents.mockResolvedValue(page(deleted));
		answer({ ok: true });
		await waitFor(() => expect(row()).not.toBeInTheDocument());
		expect(screen.queryByText("mine, later")).not.toBeInTheDocument();

		view.unmount();
		mount();
		await waitFor(() => expect(screen.getByText("long job")).toBeInTheDocument());
		expect(row()).not.toBeInTheDocument();
		expect(screen.queryByText("mine, later")).not.toBeInTheDocument();
	});

	// A delete the control plane refuses, or cannot be asked, leaves the
	// message in the queue with why on its row.
	it.each([
		["the network", new TypeError("Failed to fetch"), "Failed to fetch"],
		["a steer in flight", new CloudCpError("This message is being steered into the running turn. Wait for the agent to take it or not.", { status: 409, code: "STEER_IN_PROGRESS" }),
			"This message is being steered into the running turn, so it can't be deleted now. Try again once the steer settles."],
		["a turn the agent took", new CloudCpError("This message is no longer queued.", { status: 409, code: "TURN_NOT_QUEUED" }),
			"This message is no longer queued, so it was not deleted. If the agent started on it, stop that turn instead."],
	])("keeps the row with the reason when %s refuses the delete", async (_name, error, shown) => {
		cloudMocks.listChatEvents.mockResolvedValue(page(queued));
		cloudMocks.deleteQueuedMessage.mockRejectedValue(error);
		mount();
		await waitFor(() => expect(row()).toBeInTheDocument());
		await userEvent.click(deleteButton());
		await waitFor(() => expect(within(row()!).getByText(shown)).toBeVisible());
		expect(within(row()!).getByText("mine, later")).toBeVisible();
		expect(deleteButton()).toBeEnabled();
	});
});
