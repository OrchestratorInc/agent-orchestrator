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

	it("shows nothing before it knows which servers are Orchestrator's connectors", async () => {
		let answer: (value: unknown) => void = () => undefined;
		vi.spyOn(apiClient, "GET").mockReturnValue(new Promise((resolve) => { answer = resolve; }) as never);
		const { container } = renderMenu([{ name: "sentry", status: "needs-auth" }, { name: "github", status: "ready" }]);
		expect(container).toBeEmptyDOMElement();
		answer({ data: { connectors: [three.data.connectors[1]] } });
		const chip = await screen.findByRole("button", { name: "2 connectors in this chat" });
		await userEvent.setup().click(chip);
		expect(within(screen.getByRole("list", { name: "From Orchestrator" })).getByRole("listitem")).toHaveTextContent("Sentry");
		expect(within(screen.getByRole("list", { name: "From Claude Code's own settings" })).getByRole("listitem")).toHaveTextContent("githubLoaded");
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
		const signedIn = vi.fn();
		render(
			<QueryClientProvider client={client}>
				<ChatConnectorsMenu sessionId="mer-1" hostId="air" agentName="Codex" servers={[{ name: "sentry", status: "failed" }]} onSignedIn={signedIn} />
			</QueryClientProvider>,
		);
		await user.click(await screen.findByRole("button", { name: "1 connector in this chat" }));
		await user.click(screen.getByRole("button", { name: "Sign in to Sentry" }));
		expect(await screen.findByRole("heading", { name: "Sign in to Sentry" })).toBeInTheDocument();
		await vi.waitFor(() => expect(remote.POST).toHaveBeenCalledWith("/api/v1/connectors/{connectorId}/connect", { params: { path: { connectorId: "sentry" } } }));
		await user.type(await screen.findByLabelText("Page did not load after you approved? Paste its address."), "http://127.0.0.1:3011/oauth/mcp/callback?code=1");
		await user.click(screen.getByRole("button", { name: "Finish" }));
		expect(signedIn).not.toHaveBeenCalled();
		expect(await screen.findByText("Signed in.")).toBeInTheDocument();
		await user.click(await screen.findByRole("button", { name: "Done" }));
		expect(signedIn).toHaveBeenCalledWith({ key: "sentry", name: "Sentry" });
		expect(local).not.toHaveBeenCalled();
	});

	it("signs in to one of Codex's own servers through the agent, and reads the result from what the agent reports", async () => {
		const post = vi.fn(async (path: string) => (path.endsWith("/finish")
			? { data: undefined }
			: { data: { authorizationUrl: "https://tracker.example/authorize", callbackUrl: "http://127.0.0.1:52136/callback" } }));
		vi.spyOn(apiClient, "GET").mockResolvedValue({ data: { connectors: [] } } as never);
		vi.spyOn(apiClient, "POST").mockImplementation(post as never);
		const open = vi.spyOn(aoBridge.app, "openExternal").mockResolvedValue(undefined as never);
		const user = userEvent.setup();
		const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
		const signedIn = vi.fn();
		const menu = (status: string, ownSignIn: boolean) => (
			<QueryClientProvider client={client}>
				<ChatConnectorsMenu sessionId="mer-1" agentName="Codex" servers={[{ name: "tracker", status }, { name: "notes", status: "failed" }]} ownSignIn={ownSignIn} onSignedIn={signedIn} />
			</QueryClientProvider>
		);
		const view = render(menu("needs-auth", false));
		await user.click(await screen.findByRole("button", { name: "2 connectors in this chat" }));
		expect(screen.queryByRole("button", { name: "Sign in to tracker" })).not.toBeInTheDocument();
		expect(within(screen.getByRole("list", { name: "From Codex's own settings" })).getAllByRole("listitem").map((row) => row.textContent)).toEqual(["trackerNeeds sign-in", "notesFailed"]);
		await user.keyboard("{Escape}");
		expect(screen.getByRole("button", { name: "2 connectors in this chat" }).querySelector("svg")).not.toHaveClass("text-warning");
		view.rerender(menu("needs-auth", true));
		expect(screen.getByRole("button", { name: "2 connectors in this chat" }).querySelector("svg")).toHaveClass("text-warning");
		await user.click(screen.getByRole("button", { name: "2 connectors in this chat" }));
		expect(screen.queryByRole("button", { name: "Sign in to notes" })).not.toBeInTheDocument();
		await user.click(screen.getByRole("button", { name: "Sign in to tracker" }));
		expect(await screen.findByRole("heading", { name: "Sign in to tracker" })).toBeInTheDocument();
		await vi.waitFor(() => expect(post).toHaveBeenCalledWith("/api/v1/sessions/{sessionId}/conversation/mcp/sign-in", { params: { path: { sessionId: "mer-1" } }, body: { server: "tracker" } }));
		await vi.waitFor(() => expect(open).toHaveBeenCalledWith("https://tracker.example/authorize"));
		const address = await screen.findByLabelText("Page did not load after you approved? Paste its address.");
		expect(address).toHaveAttribute("placeholder", "http://127.0.0.1:52136/callback");
		expect(screen.queryByText("Signed in.")).not.toBeInTheDocument();
		view.rerender(
			<QueryClientProvider client={client}>
				<ChatConnectorsMenu sessionId="mer-1" agentName="Codex" servers={[{ name: "tracker", status: "needs-auth", error: "timed out waiting for OAuth callback", failureReason: "signInFailed" }]} ownSignIn />
			</QueryClientProvider>,
		);
		expect(await screen.findByRole("alert")).toHaveTextContent("timed out waiting for OAuth callback");
		await user.click(screen.getByRole("button", { name: "Start again" }));
		await vi.waitFor(() => expect(post).toHaveBeenCalledTimes(2));
		view.rerender(menu("needs-auth", true));
		expect(await screen.findByLabelText("Page did not load after you approved? Paste its address.")).toBeInTheDocument();
		expect(screen.queryByRole("alert")).not.toBeInTheDocument();
		view.rerender(menu("ready", true));
		expect(await screen.findByText("Signed in.")).toBeInTheDocument();
		await user.click(screen.getByRole("button", { name: "Done" }));
		expect(signedIn).not.toHaveBeenCalled();
	});

	it("finishes a sign-in to one of Codex's own servers from a pasted address", async () => {
		const post = vi.fn(async (path: string) => (path.endsWith("/finish") ? { data: undefined } : { data: { authorizationUrl: "https://tracker.example/authorize" } }));
		const remote = { GET: vi.fn(async () => ({ data: { connectors: [] } })), POST: post };
		vi.spyOn(hostClients, "clientForSessionHost").mockReturnValue(remote as never);
		vi.spyOn(aoBridge.app, "openExternal").mockResolvedValue(undefined as never);
		const user = userEvent.setup();
		const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
		render(
			<QueryClientProvider client={client}>
				<ChatConnectorsMenu sessionId="mer-1" hostId="air" agentName="Codex" servers={[{ name: "tracker", status: "needs-auth" }]} ownSignIn />
			</QueryClientProvider>,
		);
		await user.click(await screen.findByRole("button", { name: "1 connector in this chat" }));
		await user.click(screen.getByRole("button", { name: "Sign in to tracker" }));
		await user.type(await screen.findByLabelText("Page did not load after you approved? Paste its address."), "http://127.0.0.1:52136/callback?code=c1");
		await user.click(screen.getByRole("button", { name: "Finish" }));
		await vi.waitFor(() => expect(post).toHaveBeenCalledWith("/api/v1/sessions/{sessionId}/conversation/mcp/sign-in/finish", {
			params: { path: { sessionId: "mer-1" } },
			body: { server: "tracker", callbackUrl: "http://127.0.0.1:52136/callback?code=c1" },
		}));
		expect(await screen.findByText("Signed in.")).toBeInTheDocument();
	});

	it("says Signed in, not that no sign-in is needed, when the stored sign-in still works", async () => {
		vi.spyOn(apiClient, "GET").mockResolvedValue({ data: { connectors: [{ key: "sentry", name: "Sentry", transport: "http", auth: "oauth", status: "needs_signin", enabled: true, calls: 0 }] } } as never);
		vi.spyOn(apiClient, "POST").mockResolvedValue({ data: { connector: { signedIn: true } } } as never);
		const user = userEvent.setup();
		renderMenu();
		await user.click(await screen.findByRole("button", { name: "1 connector in this chat" }));
		await user.click(screen.getByRole("button", { name: "Sign in to Sentry" }));
		expect(await screen.findByText("Signed in.")).toBeInTheDocument();
		expect(screen.queryByText("This server needs no sign-in.")).not.toBeInTheDocument();
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
