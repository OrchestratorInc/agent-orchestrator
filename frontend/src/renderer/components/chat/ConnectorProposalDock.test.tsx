import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { apiClient } from "../../lib/api-client";
import { aoBridge } from "../../lib/bridge";
import * as hostClients from "../../lib/host-clients";
import { ConnectorProposalDock } from "./ConnectorProposalDock";

function renderDock(props: { asked: boolean; waiting: boolean; hostId?: string }) {
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
		const field = (await screen.findByLabelText("Value for PAPER_KEY")) as HTMLInputElement;
		fireEvent.change(field, { target: { value: "secret" } });
		expect(field.value).toBe("secret");
		pending = [{ ...paper, id: "p3", args: ["-y", "other-mcp"] }];
		await waitFor(() => expect(screen.getByRole("group", { name: "Connector request" })).toHaveTextContent("other-mcp"), { timeout: 5000 });
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

	it("takes each value from the user before a command can be approved, and can decline", async () => {
		let pending = [paper];
		vi.spyOn(apiClient, "GET").mockImplementation((async () => ({ data: { proposals: pending } })) as never);
		const post = vi.spyOn(apiClient, "POST").mockImplementation((async () => {
			pending = [];
			return { data: { proposal: { ...paper, state: "declined" } } };
		}) as never);
		renderDock({ asked: true, waiting: false });
		const dock = await screen.findByRole("group", { name: "Connector request" });
		expect(dock).toHaveTextContent("npx -y paper-mcp");
		expect(dock).toHaveTextContent("The agent asked for it in this chat.");
		expect(dock).not.toHaveTextContent("Sign-in:");
		const approve = screen.getByRole("button", { name: "Approve" });
		expect(approve).toBeDisabled();
		fireEvent.change(screen.getByLabelText("Value for PAPER_KEY"), { target: { value: "typed" } });
		expect(approve).toBeEnabled();
		fireEvent.click(screen.getByRole("button", { name: "Decline" }));
		await waitFor(() => expect(post).toHaveBeenCalledWith("/api/v1/connectors/proposals/{proposalId}/decline", { params: { path: { proposalId: "p2" } } }));
		await waitFor(() => expect(screen.queryByRole("group", { name: "Connector request" })).not.toBeInTheDocument());
	});
});
