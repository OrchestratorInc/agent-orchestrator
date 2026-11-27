import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { apiClient } from "../../lib/api-client";
import { aoBridge } from "../../lib/bridge";
import * as hostClients from "../../lib/host-clients";
import type { ConversationItem } from "../../types/conversation";
import { ConnectorProposalDock, connectorAskIn, type ConnectorRestart } from "./ConnectorProposalDock";

function renderDock(props: { asked: boolean; waiting: boolean; hostId?: string; restart?: ConnectorRestart }) {
	const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
	return render(
		<QueryClientProvider client={client}>
			<ConnectorProposalDock sessionId="mer-1" {...props} />
		</QueryClientProvider>,
	);
}

const stamp = new Date(0).toISOString();
const sentry = {
	id: "p1", sessionId: "mer-1", key: "sentry", name: "sentry", transport: "http", url: "https://mcp.sentry.dev/mcp", args: [], envKeys: [], headerKeys: [],
	auth: "oauth", projectId: "api", reason: "To read the crash report.", state: "pending", createdAt: stamp,
};
const paper = {
	id: "p2", sessionId: "mer-1", key: "paper", name: "paper", transport: "stdio", command: "npx", args: ["-y", "paper-mcp"], envKeys: ["PAPER_KEY"], headerKeys: [],
	auth: "none", projectId: "api", state: "pending", createdAt: stamp,
};
const added = {
	id: "c1", key: "sentry", name: "sentry", transport: "http", url: "https://mcp.sentry.dev/mcp", args: [], envKeys: [], headerKeys: [],
	auth: "oauth", signedIn: false, enabled: true, status: "unchecked", tools: [], createdAt: stamp, updatedAt: stamp,
};

describe("connectorAskIn", () => {
	const row = (fields: Record<string, unknown>) => ({ kind: "activity", id: "a", sequence: 1, activityKind: "mcp_tool", status: "completed", summary: "", ...fields }) as unknown as ConversationItem;

	it("finds the request in the row shapes of Claude Code, Codex and other agents", () => {
		expect(connectorAskIn([row({ detail: { providerToolName: "mcp__ao__connector_propose" } })])).toEqual({ asked: true, waiting: false });
		expect(connectorAskIn([row({ summary: "Called ao/connector_propose", status: "running" })])).toEqual({ asked: true, waiting: true });
		expect(connectorAskIn([row({ activityKind: "tool", summary: "connector_propose", status: "pending" })])).toEqual({ asked: true, waiting: true });
	});

	it("finds no request in other rows", () => {
		expect(connectorAskIn([
			row({ detail: { providerToolName: "mcp__ao__connector_list" } }),
			row({ summary: "Called hf/hub_repo_search" }),
			{ kind: "message", id: "m", role: "user", text: "connector_propose" } as unknown as ConversationItem,
		])).toEqual({ asked: false, waiting: false });
	});
});

describe("ConnectorProposalDock", () => {
	afterEach(() => {
		vi.restoreAllMocks();
	});

	it("asks the daemon nothing in a chat where no agent asked for a connector", () => {
		const get = vi.spyOn(apiClient, "GET");
		const { container } = renderDock({ asked: false, waiting: false });
		expect(get).not.toHaveBeenCalled();
		expect(container).toBeEmptyDOMElement();
	});

	it("clears what the user typed when the agent replaces its request", async () => {
		let pending = [paper];
		vi.spyOn(apiClient, "GET").mockImplementation((async () => ({ data: { proposals: pending } })) as never);
		renderDock({ asked: true, waiting: true });
		fireEvent.click(await screen.findByRole("button", { name: "Review command" }));
		const field = screen.getByLabelText("Value for PAPER_KEY") as HTMLInputElement;
		fireEvent.change(field, { target: { value: "secret" } });
		expect(field.value).toBe("secret");
		expect(screen.getByRole("button", { name: "Approve" })).toBeEnabled();
		pending = [{ ...paper, id: "p3", args: ["-y", "other-mcp"] }];
		await waitFor(() => expect(screen.queryByRole("button", { name: "Approve" })).not.toBeInTheDocument(), { timeout: 5000 });
		expect(screen.queryByLabelText("Value for PAPER_KEY")).not.toBeInTheDocument();
		fireEvent.click(screen.getByRole("button", { name: "Review command" }));
		expect(screen.getByRole("group", { name: "Connector request" })).toHaveTextContent('["-y","other-mcp"]');
		expect((screen.getByLabelText("Value for PAPER_KEY") as HTMLInputElement).value).toBe("");
		expect(screen.getByRole("button", { name: "Approve" })).toBeDisabled();
	});

	it("asks and answers the machine that runs the chat", async () => {
		const local = vi.spyOn(apiClient, "GET");
		const remote = {
			GET: vi.fn(async () => ({ data: { proposals: [sentry] } })),
			POST: vi.fn(async () => ({ data: {} })),
		};
		const pick = vi.spyOn(hostClients, "clientForSessionHost").mockReturnValue(remote as never);
		renderDock({ asked: true, waiting: true, hostId: "air" });
		await screen.findByRole("group", { name: "Connector request" });
		fireEvent.click(screen.getByRole("button", { name: "Decline" }));
		await waitFor(() => expect(remote.POST).toHaveBeenCalledWith("/api/v1/connectors/proposals/{proposalId}/decline", { params: { path: { proposalId: "p1" } } }));
		expect(pick).toHaveBeenCalledWith("air");
		expect(local).not.toHaveBeenCalled();
	});

	it("shows what would be added, approves it for the chosen projects and starts the sign-in", async () => {
		let pending = [sentry];
		vi.spyOn(apiClient, "GET").mockImplementation((async () => ({ data: { proposals: pending } })) as never);
		vi.spyOn(aoBridge.app, "openExternal").mockResolvedValue(undefined as never);
		const post = vi.spyOn(apiClient, "POST").mockImplementation((async (path: string) => {
			if (path.endsWith("/approve")) {
				pending = [];
				return { data: { proposal: { ...sentry, state: "approved" }, connector: added } };
			}
			return { data: { authorizationUrl: "https://sentry.io/oauth/authorize", callbackUrl: "http://127.0.0.1:3011/oauth/mcp/callback", connector: added } };
		}) as never);
		renderDock({ asked: true, waiting: true });
		const dock = await screen.findByRole("group", { name: "Connector request" });
		expect(dock).toHaveTextContent("Add a connector?");
		expect(dock).toHaveTextContent("To read the crash report.");
		expect(dock).toHaveTextContent("mcp.sentry.dev/mcp");
		expect(dock).toHaveTextContent("Sign-in: Automatic");
		fireEvent.click(screen.getByRole("button", { name: "Approve" }));
		await waitFor(() => expect(post).toHaveBeenCalledWith("/api/v1/connectors/proposals/{proposalId}/approve", {
			params: { path: { proposalId: "p1" } },
			body: { projectId: "api", values: {} },
		}));
		expect(await screen.findByRole("heading", { name: "Sign in to sentry" })).toBeInTheDocument();
		await waitFor(() => expect(screen.queryByRole("group", { name: "Connector request" })).not.toBeInTheDocument());
	});

	it("offers a restart of the agent after an approval that needs no sign-in", async () => {
		let pending = [paper];
		vi.spyOn(apiClient, "GET").mockImplementation((async () => ({ data: { proposals: pending } })) as never);
		vi.spyOn(apiClient, "POST").mockImplementation((async () => {
			pending = [];
			return { data: { proposal: { ...paper, state: "approved" }, connector: { ...added, id: "c2", key: "paper", name: "paper", transport: "stdio", auth: "none" } } };
		}) as never);
		const offer = vi.fn();
		renderDock({ asked: true, waiting: true, restart: { busy: true, offer, restart: vi.fn() } });
		fireEvent.click(await screen.findByRole("button", { name: "Review command" }));
		fireEvent.change(screen.getByLabelText("Value for PAPER_KEY"), { target: { value: "typed" } });
		fireEvent.click(screen.getByRole("button", { name: "Approve" }));
		await waitFor(() => expect(offer).toHaveBeenCalledWith({ key: "paper", name: "paper" }));
	});

	it("restarts and continues from the notice only while the agent is not working, and can wait", async () => {
		const offer = vi.fn();
		const restart = vi.fn(async () => undefined);
		const busy = renderDock({ asked: false, waiting: false, restart: { ready: "paper", busy: true, offer, restart } });
		const notice = screen.getByRole("group", { name: "Connector ready" });
		expect(notice).toHaveTextContent("paper is ready");
		expect(notice).toHaveTextContent("The agent is working. You can restart it when it stops.");
		expect(screen.getByRole("button", { name: "Restart and continue" })).toBeDisabled();
		fireEvent.click(screen.getByRole("button", { name: "Later" }));
		expect(offer).toHaveBeenCalledWith();
		expect(restart).not.toHaveBeenCalled();
		busy.unmount();
		offer.mockClear();
		renderDock({ asked: false, waiting: false, restart: { ready: "paper", busy: false, offer, restart } });
		expect(screen.getByRole("group", { name: "Connector ready" })).toHaveTextContent("The agent restarts to get its tools, then continues the task.");
		fireEvent.click(screen.getByRole("button", { name: "Restart and continue" }));
		await waitFor(() => expect(offer).toHaveBeenCalledWith());
		expect(restart).toHaveBeenCalledTimes(1);
	});

	it("shows why a restart failed and keeps the notice", async () => {
		const offer = vi.fn();
		renderDock({ asked: false, waiting: false, restart: { ready: "paper", busy: false, offer, restart: async () => { throw new Error("Another app still has this chat open."); } } });
		fireEvent.click(screen.getByRole("button", { name: "Restart and continue" }));
		expect(await screen.findByRole("alert")).toHaveTextContent("Another app still has this chat open.");
		expect(offer).not.toHaveBeenCalled();
	});

	it("does not show the error of one request under the next request", async () => {
		let pending = [sentry, { ...paper, id: "p9" }];
		vi.spyOn(apiClient, "GET").mockImplementation((async () => ({ data: { proposals: pending } })) as never);
		vi.spyOn(apiClient, "POST").mockImplementation((async (path: string) => {
			if (path.endsWith("/approve")) return { error: { code: "CONNECTOR_LOOPBACK_REQUIRED", message: "Add this connector on the computer that runs the chat." } };
			pending = pending.slice(1);
			return { data: { proposal: { ...sentry, state: "declined" } } };
		}) as never);
		renderDock({ asked: true, waiting: false });
		fireEvent.click(await screen.findByRole("button", { name: "Approve" }));
		expect(await screen.findByRole("alert")).toHaveTextContent("Add this connector on the computer that runs the chat.");
		fireEvent.click(screen.getByRole("button", { name: "Decline" }));
		expect(await screen.findByRole("button", { name: "Review command" })).toBeInTheDocument();
		expect(screen.queryByRole("alert")).not.toBeInTheDocument();
	});

	it("takes each value from the user before a command can be approved, and can decline", async () => {
		let pending = [paper];
		vi.spyOn(apiClient, "GET").mockImplementation((async () => ({ data: { proposals: pending } })) as never);
		const post = vi.spyOn(apiClient, "POST").mockImplementation((async () => {
			pending = [];
			return { data: { proposal: { ...paper, state: "declined" } } };
		}) as never);
		renderDock({ asked: true, waiting: false });
		const dock = await screen.findByRole("group", { name: "Connector request" });
		expect(dock).toHaveTextContent("The agent asked for it in this chat.");
		expect(dock).not.toHaveTextContent("Sign-in:");
		expect(dock).not.toHaveTextContent("paper-mcp");
		expect(screen.queryByRole("button", { name: "Approve" })).not.toBeInTheDocument();
		expect(screen.queryByLabelText("Value for PAPER_KEY")).not.toBeInTheDocument();
		fireEvent.click(screen.getByRole("button", { name: "Review command" }));
		expect(dock).toHaveTextContent("Commandnpx");
		expect(dock).toHaveTextContent('Arguments (exact)["-y","paper-mcp"]');
		expect(dock).toHaveTextContent("Approval runs this command on the computer that runs this chat.");
		const approve = screen.getByRole("button", { name: "Approve" });
		expect(approve).toBeDisabled();
		fireEvent.change(screen.getByLabelText("Value for PAPER_KEY"), { target: { value: "typed" } });
		expect(approve).toBeEnabled();
		fireEvent.click(screen.getByRole("button", { name: "Decline" }));
		await waitFor(() => expect(post).toHaveBeenCalledWith("/api/v1/connectors/proposals/{proposalId}/decline", { params: { path: { proposalId: "p2" } } }));
		await waitFor(() => expect(screen.queryByRole("group", { name: "Connector request" })).not.toBeInTheDocument());
	});
});
