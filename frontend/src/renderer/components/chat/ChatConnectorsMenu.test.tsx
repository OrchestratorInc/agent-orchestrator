import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { render, screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, describe, expect, it, vi } from "vitest";
import { apiClient } from "../../lib/api-client";
import * as hostClients from "../../lib/host-clients";
import { aoBridge } from "../../lib/bridge";
import { useUiStore } from "../../stores/ui-store";
import { ChatConnectorsMenu, connectorRowState } from "./ChatConnectorsMenu";

function renderMenu(servers: Parameters<typeof ChatConnectorsMenu>[0]["servers"] = []) {
	const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
	return render(
		<QueryClientProvider client={client}>
			<ChatConnectorsMenu sessionId="mer-1" agentName="Claude Code" servers={servers} />
		</QueryClientProvider>,
	);
}

const three = {
	data: {
		connectors: [
			{ key: "linear", name: "Linear", transport: "http", status: "connected", enabled: true, calls: 4, lastTool: "create_issue" },
			{ key: "sentry", name: "Sentry", transport: "http", auth: "header", status: "needs_signin", enabled: true, calls: 0 },
			{ key: "files", name: "files", transport: "stdio", status: "connected", enabled: true, calls: 0 },
		],
	},
};

it("does not offer this computer's connector settings in a chat on another machine", async () => {
	const remote = { GET: vi.fn(async () => ({ data: { connectors: [{ key: "linear", name: "Linear", transport: "http", status: "needs_signin", enabled: true, calls: 0 }] } })) };
	vi.spyOn(hostClients, "clientForSessionHost").mockReturnValue(remote as never);
	const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
	render(
		<QueryClientProvider client={client}>
			<ChatConnectorsMenu sessionId="mer-1" hostId="air" agentName="Claude Code" servers={[]} />
		</QueryClientProvider>,
	);
	await userEvent.setup().click(await screen.findByRole("button"));
	expect(await screen.findByText("Linear")).toBeInTheDocument();
	expect(screen.queryByRole("menuitem", { name: "Manage connectors" })).not.toBeInTheDocument();
	vi.restoreAllMocks();
});

describe("ChatConnectorsMenu", () => {
	afterEach(() => {
		vi.restoreAllMocks();
		useUiStore.setState({ settingsModal: null });
	});

	it("shows nothing for a chat without connectors", async () => {
		const get = vi.spyOn(apiClient, "GET").mockResolvedValue({ data: { connectors: [] } } as never);
		const { container } = renderMenu();
		await vi.waitFor(() => expect(get).toHaveBeenCalledWith("/api/v1/sessions/{sessionId}/connectors", { params: { path: { sessionId: "mer-1" } } }));
		expect(container).toBeEmptyDOMElement();
	});

	it("lists each connector with its calls and its state, and opens Settings", async () => {
		vi.spyOn(apiClient, "GET").mockResolvedValue(three as never);
		const user = userEvent.setup();
		renderMenu([{ name: "linear", status: "ready" }, { name: "files", status: "failed", error: "spawn npx ENOENT" }, { name: "ao", status: "ready" }]);
		const chip = await screen.findByRole("button", { name: "3 connectors in this chat" });
		expect(chip).toHaveTextContent("3");
		await user.click(chip);
		const rows = screen.getAllByRole("listitem");
		expect(rows).toHaveLength(3);
		expect(rows[0]).toHaveTextContent("Linear4 calls · create_issueLoaded");
		expect(rows[1]).toHaveTextContent("SentryNo calls yetNeeds sign-in");
		expect(rows[2]).toHaveTextContent("filesLocal commandFailed");
		expect(screen.getByText("Failed")).toHaveAttribute("title", "spawn npx ENOENT");
		await user.click(screen.getByRole("menuitem", { name: "Manage connectors" }));
		expect(useUiStore.getState().settingsModal).toMatchObject({ scope: "global", section: "connectors" });
	});

	it("counts and lists the agent's own servers in a second group", async () => {
		vi.spyOn(apiClient, "GET").mockResolvedValue({ data: { connectors: three.data.connectors.slice(0, 2) } } as never);
		const user = userEvent.setup();
		renderMenu([
			{ name: "ao", status: "ready" },
			{ name: "linear", status: "ready" },
			{ name: "github", status: "ready" },
			{ name: "claude.ai Notion", status: "needs-auth" },
			{ name: "plugin:docs:docs", status: "failed", error: "spawn docs ENOENT" },
			{ name: "paused", status: "disabled" },
		]);
		const chip = await screen.findByRole("button", { name: "5 connectors in this chat" });
		expect(chip).toHaveTextContent("5");
		await user.click(chip);
		const ours = within(screen.getByRole("list", { name: "From Orchestrator" })).getAllByRole("listitem");
		expect(ours.map((row) => row.textContent)).toEqual(["Linear4 calls · create_issueLoaded", "SentryNo calls yetNeeds sign-in"]);
		const own = within(screen.getByRole("list", { name: "From Claude Code's own settings" })).getAllByRole("listitem");
		expect(own.map((row) => row.textContent)).toEqual(["githubLoaded", "claude.ai NotionNeeds sign-in", "plugin:docs:docsFailed"]);
		expect(screen.getByText("Failed")).toHaveAttribute("title", "spawn docs ENOENT");
		expect(screen.getByText("From Orchestrator").parentElement).toHaveTextContent("From Orchestrator2");
		expect(screen.getByText("From Claude Code's own settings").parentElement).toHaveTextContent("From Claude Code's own settings3");
	});

	it("shows the agent's own servers in a chat with no connector from Orchestrator", async () => {
		vi.spyOn(apiClient, "GET").mockResolvedValue({ data: { connectors: [] } } as never);
		const user = userEvent.setup();
		renderMenu([{ name: "ao", status: "ready" }, { name: "github", status: "starting" }]);
		await user.click(await screen.findByRole("button", { name: "1 connector in this chat" }));
		expect(screen.queryByText("From Orchestrator")).not.toBeInTheDocument();
		expect(within(screen.getByRole("list", { name: "From Claude Code's own settings" })).getByRole("listitem")).toHaveTextContent("githubStarting");
	});

	it("signs in to a connector from the chat, on the machine that runs the chat", async () => {
		const local = vi.spyOn(apiClient, "POST");
		const remote = {
			GET: vi.fn(async () => ({ data: { connectors: [{ key: "sentry", name: "Sentry", transport: "http", auth: "oauth", status: "needs_signin", enabled: true, calls: 0 }] } })),
			POST: vi.fn(async () => ({ data: { authorizationUrl: "https://sentry.example/authorize", callbackUrl: "http://127.0.0.1:3011/oauth/mcp/callback", connector: {} } })),
		};
		vi.spyOn(hostClients, "clientForSessionHost").mockReturnValue(remote as never);
		vi.spyOn(aoBridge.app, "openExternal").mockResolvedValue(undefined as never);
		const user = userEvent.setup();
		const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
		render(
			<QueryClientProvider client={client}>
				<ChatConnectorsMenu sessionId="mer-1" hostId="air" agentName="Codex" servers={[]} />
			</QueryClientProvider>,
		);
		await user.click(await screen.findByRole("button", { name: "1 connector in this chat" }));
		await user.click(screen.getByRole("button", { name: "Sign in to Sentry" }));
		expect(await screen.findByRole("heading", { name: "Sign in to Sentry" })).toBeInTheDocument();
		await vi.waitFor(() => expect(remote.POST).toHaveBeenCalledWith("/api/v1/connectors/{connectorId}/connect", { params: { path: { connectorId: "sentry" } } }));
		expect(await screen.findByLabelText("Page did not load after you approved? Paste its address.")).toBeInTheDocument();
		expect(local).not.toHaveBeenCalled();
	});

	it("does not claim an agent loaded a server it has not reported", () => {
		const on = { enabled: true, status: "connected" as const };
		expect(connectorRowState(on)).toBe("added");
		expect(connectorRowState(on, { status: "ready" })).toBe("loaded");
		expect(connectorRowState(on, { status: "starting" })).toBe("starting");
		expect(connectorRowState(on, { status: "cancelled" })).toBe("failed");
		expect(connectorRowState({ enabled: true, status: "needs_signin" }, { status: "ready" })).toBe("needsSignIn");
		expect(connectorRowState({ enabled: false, status: "connected" }, { status: "ready" })).toBe("off");
	});
});
