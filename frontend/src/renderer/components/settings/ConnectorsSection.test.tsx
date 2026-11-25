import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, describe, expect, it, vi } from "vitest";
import { connectorKeyFrom, joinCommandLine, splitCommandLine, type Connector } from "../../hooks/useConnectors";
import { apiClient } from "../../lib/api-client";
import { aoBridge } from "../../lib/bridge";
import { parseEnvLines } from "./ConnectorDialog";
import { ConnectorsSection, connectorTarget, needsSignIn } from "./ConnectorsSection";

vi.mock("../../hooks/useWorkspaceQuery", () => ({
	useWorkspaceQuery: () => ({ data: [{ id: "p1", name: "api" }, { id: "remote", name: "remote", hostId: "vm" }] }),
}));

const stamp = new Date(0).toISOString();
const base: Connector = {
	id: "c1", key: "linear", name: "Linear", transport: "http", url: "https://mcp.linear.app/mcp", args: [], envKeys: [], headerKeys: [],
	auth: "oauth", signedIn: true, enabled: true, status: "connected", tools: ["list_issues", "create_issue"], createdAt: stamp, updatedAt: stamp,
};
const list: Connector[] = [
	base,
	{ ...base, id: "c2", key: "sentry", name: "Sentry", url: "https://mcp.sentry.dev/mcp", signedIn: false, status: "needs_signin", tools: [] },
	{ ...base, id: "c3", key: "files", name: "files", transport: "stdio", url: undefined, command: "npx", args: ["-y", "files-mcp"], auth: "none", signedIn: false, status: "unavailable", statusDetail: { step: "start", error: "exec: npx: not found" }, tools: [] },
	{ ...base, id: "c4", key: "docs", name: "Docs", auth: "none", signedIn: false, enabled: false, status: "unchecked", tools: [] },
];

function renderSection() {
	const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
	return render(
		<QueryClientProvider client={client}>
			<ConnectorsSection />
		</QueryClientProvider>,
	);
}

function mockList(connectors: Connector[] = list, one?: () => Connector) {
	return vi.spyOn(apiClient, "GET").mockImplementation((async (path: string, init?: { params?: { path?: { connectorId?: string } } }) =>
		path === "/api/v1/connectors/import" ? { data: { candidates: [] } }
			: path === "/api/v1/connectors/{connectorId}"
				? { data: { connector: one?.() ?? connectors.find((connector) => connector.id === init?.params?.path?.connectorId) } }
				: { data: { connectors } }) as never);
}

describe("ConnectorsSection", () => {
	afterEach(() => {
		vi.restoreAllMocks();
	});

	it("shows each connector with its target and a truthful state", async () => {
		mockList();
		renderSection();
		const row = (key: string) => document.querySelector(`[data-connector="${key}"]`) as HTMLElement;
		expect(await screen.findByText("Linear")).toBeInTheDocument();
		expect(row("linear")).toHaveTextContent("mcp.linear.app/mcp · Connected · 2 tools");
		expect(row("sentry")).toHaveTextContent("Needs sign-in");
		expect(row("files")).toHaveTextContent("npx -y files-mcp · The command did not start");
		expect(row("files").querySelector("[title]")).toHaveAttribute("title", "exec: npx: not found");
		expect(row("docs")).toHaveTextContent("Off");
		expect(row("linear").closest(".settings-grouped-rows")).not.toBeNull();
		expect(screen.getAllByRole("button", { name: "Sign in" })).toHaveLength(1);
		expect(row("sentry")).toContainElement(screen.getByRole("button", { name: "Sign in" }));
	});

	it("says so when there is no connector", async () => {
		mockList([]);
		renderSection();
		expect(await screen.findByText("No connectors yet.")).toBeInTheDocument();
	});

	it("turns a connector off for new sessions", async () => {
		mockList();
		const patch = vi.spyOn(apiClient, "PATCH").mockResolvedValue({ data: { connector: { ...base, enabled: false } } } as never);
		renderSection();
		fireEvent.click(await screen.findByRole("switch", { name: "Give Linear to new sessions" }));
		await waitFor(() => expect(patch).toHaveBeenCalledWith("/api/v1/connectors/{connectorId}", { params: { path: { connectorId: "c1" } }, body: { enabled: false } }));
	});

	it("opens the sign-in page and finishes from a pasted address", async () => {
		mockList();
		const open = vi.spyOn(aoBridge.app, "openExternal").mockResolvedValue(undefined as never);
		const post = vi.spyOn(apiClient, "POST").mockImplementation((async (path: string) =>
			path.endsWith("/connect")
				? { data: { authorizationUrl: "https://auth.example/authorize?state=s1", callbackUrl: "http://127.0.0.1:3011/oauth/mcp/callback", connector: list[1] } }
				: { data: { connector: { ...list[1], signedIn: true, status: "connected" } } }) as never);
		renderSection();
		fireEvent.click(await screen.findByRole("button", { name: "Sign in" }));
		expect(await screen.findByRole("heading", { name: "Sign in to Sentry" })).toBeInTheDocument();
		await waitFor(() => expect(open).toHaveBeenCalledWith("https://auth.example/authorize?state=s1"));
		expect(screen.getByText("Approve the sign-in in your browser.")).toBeInTheDocument();
		const field = screen.getByLabelText("Page did not load after you approved? Paste its address.");
		expect(field).toHaveAttribute("placeholder", "http://127.0.0.1:3011/oauth/mcp/callback");
		fireEvent.change(field, { target: { value: " http://127.0.0.1:3011/oauth/mcp/callback?code=c1&state=s1 " } });
		fireEvent.click(screen.getByRole("button", { name: "Finish" }));
		await waitFor(() => expect(post).toHaveBeenCalledWith("/api/v1/connectors/{connectorId}/connect/finish", {
			params: { path: { connectorId: "c2" } },
			body: { callbackUrl: "http://127.0.0.1:3011/oauth/mcp/callback?code=c1&state=s1" },
		}));
		expect(await screen.findByText("Signed in.")).toBeInTheDocument();
	});

	it("waits for the server's approval of a refused sign-in and completes by itself", async () => {
		const refused: Connector = { ...base, id: "c5", key: "notion", name: "Notion", signedIn: true, status: "needs_signin", tools: [] };
		let approved = false;
		mockList([refused], () => ({ ...refused, signedIn: approved, status: approved ? "connected" : "needs_signin" }));
		vi.spyOn(aoBridge.app, "openExternal").mockResolvedValue(undefined as never);
		vi.spyOn(apiClient, "POST").mockResolvedValue({
			data: { authorizationUrl: "https://notion.example/authorize", callbackUrl: "http://127.0.0.1:3011/oauth/mcp/callback", connector: { ...refused, signedIn: false } },
		} as never);
		renderSection();
		fireEvent.click(await screen.findByRole("button", { name: "Sign in" }));
		expect(await screen.findByText("Approve the sign-in in your browser.")).toBeInTheDocument();
		expect(screen.queryByText("Signed in.")).not.toBeInTheDocument();
		approved = true;
		expect(await screen.findByText("Signed in.", undefined, { timeout: 5000 })).toBeInTheDocument();
	});

	it("stops waiting and offers a new start when the sign-in fails in the browser", async () => {
		const sentry = list[1] as Connector;
		let failed = true;
		mockList(list, () => (failed ? { ...sentry, statusDetail: { step: "sign-in", error: "access_denied" } } : sentry));
		vi.spyOn(aoBridge.app, "openExternal").mockResolvedValue(undefined as never);
		const post = vi.spyOn(apiClient, "POST").mockResolvedValue({
			data: { authorizationUrl: "https://auth.example/authorize", callbackUrl: "http://127.0.0.1:3011/oauth/mcp/callback", connector: sentry },
		} as never);
		renderSection();
		fireEvent.click(await screen.findByRole("button", { name: "Sign in" }));
		expect(await screen.findByRole("alert")).toHaveTextContent("access_denied");
		expect(screen.queryByText("Approve the sign-in in your browser.")).not.toBeInTheDocument();
		failed = false;
		fireEvent.click(screen.getByRole("button", { name: "Start again" }));
		expect(await screen.findByText("Approve the sign-in in your browser.")).toBeInTheDocument();
		expect(post).toHaveBeenCalledTimes(2);
	});

	it("adds a server from the catalog and starts its sign-in", async () => {
		mockList([]);
		vi.spyOn(aoBridge.app, "openExternal").mockResolvedValue(undefined as never);
		const created = { ...base, id: "c9", signedIn: false, status: "unchecked" as const, tools: [] };
		const post = vi.spyOn(apiClient, "POST").mockImplementation((async (path: string) =>
			path === "/api/v1/connectors"
				? { data: { connector: created } }
				: { data: { authorizationUrl: "https://linear.app/oauth/authorize", callbackUrl: "http://127.0.0.1:3011/oauth/mcp/callback", connector: created } }) as never);
		renderSection();
		fireEvent.click(await screen.findByRole("button", { name: "Add Linear" }));
		const dialog = await screen.findByRole("dialog");
		expect(screen.getByLabelText("URL")).toHaveValue("https://mcp.linear.app/mcp");
		expect(screen.getByLabelText("Name")).toHaveValue("linear");
		fireEvent.click(dialog.querySelector('button[type="submit"]') as HTMLButtonElement);
		await waitFor(() => expect(post).toHaveBeenCalledWith("/api/v1/connectors", {
			body: { key: "linear", name: "Linear", projectId: "", presetId: "linear", transport: "http", url: "https://mcp.linear.app/mcp", auth: "oauth" },
		}));
		expect(await screen.findByRole("heading", { name: "Sign in to Linear" })).toBeInTheDocument();
	});

	it("offers the catalog on shelves, hides what is added, and searches both lists", async () => {
		mockList();
		renderSection();
		await screen.findByText("Linear");
		const shelf = (name: string) => within(screen.getByRole("list", { name })).getAllByRole("listitem").map((row) => row.textContent);
		expect(shelf("Popular")).toEqual(["NotionPages and databasesAdd", "SupabasePostgres, projects and Edge FunctionsAdd", "VercelProjects and deploymentsAdd", "StripeCustomers, payments and Stripe docsAdd"]);
		expect(shelf("Developer tools")).toEqual(["PostHogProduct analytics and feature flagsAdd", "NeonServerless Postgres projectsAdd", "CContext7Current docs for librariesAdd"]);
		expect(shelf("Productivity")).toHaveLength(3);
		expect(document.querySelector('[data-connector-preset="notion"] svg[fill="currentColor"] path')).not.toBeNull();
		expect(document.querySelector('[data-connector="linear"] svg[fill="currentColor"] path')).not.toBeNull();
		expect(document.querySelector('[data-connector="files"] svg[fill="currentColor"]')).toBeNull();
		expect(document.querySelector('[data-connector-preset="monday"] svg[fill="currentColor"]')).toBeNull();
		expect(document.querySelector('[data-connector-preset="monday"]')).toHaveTextContent("Mmonday.com");

		fireEvent.change(screen.getByRole("textbox", { name: "Search connectors" }), { target: { value: "postgres" } });
		expect(shelf("Popular")).toEqual(["SupabasePostgres, projects and Edge FunctionsAdd"]);
		expect(shelf("Developer tools")).toEqual(["NeonServerless Postgres projectsAdd"]);
		expect(screen.queryByRole("list", { name: "Productivity" })).not.toBeInTheDocument();
		expect(document.querySelector('[data-connector="linear"]')).toBeNull();
		expect(screen.getByText("No connector matches your search.")).toBeInTheDocument();

		fireEvent.change(screen.getByRole("textbox", { name: "Search connectors" }), { target: { value: "sentry" } });
		expect(document.querySelector('[data-connector="sentry"]')).not.toBeNull();
		expect(screen.queryByRole("list", { name: "Developer tools" })).not.toBeInTheDocument();
	});

	it("keeps what an edit does not show: the other headers, the command's exact words", async () => {
		const twoHeaders: Connector = { ...base, id: "c7", key: "docs", name: "Docs", auth: "header", signedIn: false, headerKeys: ["Authorization", "X-Org"], status: "needs_signin", tools: [] };
		const command: Connector = { ...list[2], id: "c8", key: "win", name: "win", command: "/Applications/My App/bin/server", args: ["--dir", "C:\\Users\\me\\notes", ""], status: "connected", statusDetail: undefined };
		mockList([twoHeaders, command]);
		const patch = vi.spyOn(apiClient, "PATCH").mockResolvedValue({ data: { connector: twoHeaders } } as never);
		vi.spyOn(apiClient, "POST").mockResolvedValue({ data: { connector: twoHeaders } } as never);
		const user = userEvent.setup();
		renderSection();
		await screen.findByText("Docs");
		const row = document.querySelector('[data-connector="docs"]') as HTMLElement;
		expect(row).toHaveTextContent("The server refused the sign-in");
		expect(screen.queryByRole("button", { name: "Sign in" })).not.toBeInTheDocument();

		await user.click(screen.getByRole("button", { name: "Actions for Docs" }));
		await user.click(await screen.findByRole("menuitem", { name: "Edit" }));
		let dialog = await screen.findByRole("dialog");
		fireEvent.click(dialog.querySelector('button[type="submit"]') as HTMLButtonElement);
		await waitFor(() => expect(patch).toHaveBeenCalledWith("/api/v1/connectors/{connectorId}", expect.objectContaining({
			body: expect.objectContaining({ headers: { Authorization: "", "X-Org": "" }, url: "https://mcp.linear.app/mcp" }),
		})));
		await waitFor(() => expect(screen.queryByRole("dialog")).not.toBeInTheDocument());

		await user.click(screen.getByRole("button", { name: "Actions for win" }));
		await user.click(await screen.findByRole("menuitem", { name: "Edit" }));
		dialog = await screen.findByRole("dialog");
		fireEvent.click(dialog.querySelector('button[type="submit"]') as HTMLButtonElement);
		await waitFor(() => expect(patch).toHaveBeenLastCalledWith("/api/v1/connectors/{connectorId}", expect.objectContaining({
			params: { path: { connectorId: "c8" } },
			body: expect.objectContaining({ command: "/Applications/My App/bin/server", args: ["--dir", "C:\\Users\\me\\notes", ""] }),
		})));
	});

	it("adds a local command with its environment values", async () => {
		mockList([]);
		const created = { ...list[2], status: "unchecked" as const, statusDetail: undefined };
		const post = vi.spyOn(apiClient, "POST").mockResolvedValue({ data: { connector: created } } as never);
		const user = userEvent.setup();
		renderSection();
		fireEvent.click(await screen.findByRole("button", { name: "Add connector" }));
		const dialog = await screen.findByRole("dialog");
		await user.click(screen.getByRole("tab", { name: "Local command" }));
		const submit = dialog.querySelector('button[type="submit"]') as HTMLButtonElement;
		fireEvent.change(screen.getByLabelText("Command"), { target: { value: 'npx -y files-mcp "/my notes"' } });
		fireEvent.change(screen.getByLabelText("Environment values"), { target: { value: "TOKEN" } });
		expect(screen.getByRole("alert")).toHaveTextContent("Each line needs NAME=value.");
		expect(submit).toBeDisabled();
		fireEvent.change(screen.getByLabelText("Environment values"), { target: { value: "TOKEN=a=b\n" } });
		fireEvent.change(screen.getByLabelText("Name"), { target: { value: "files" } });
		fireEvent.click(submit);
		await waitFor(() => expect(post).toHaveBeenCalledWith("/api/v1/connectors", {
			body: { key: "files", name: "files", projectId: "", transport: "stdio", command: "npx", args: ["-y", "files-mcp", "/my notes"], env: { TOKEN: "a=b" } },
		}));
	});
});

describe("connector helpers", () => {
	it("keeps the backslashes of a Windows path", () => {
		expect(splitCommandLine("C:\\Tools\\mcp\\server.exe --port 1")).toEqual(["C:\\Tools\\mcp\\server.exe", "--port", "1"]);
		expect(splitCommandLine("my\\ server a\\\\b")).toEqual(["my server", "a\\b"]);
	});

	it("splits a command line with quotes", () => {
		expect(splitCommandLine(`npx -y "my server" --dir '/a b' c\\ d ""`)).toEqual(["npx", "-y", "my server", "--dir", "/a b", "c d", ""]);
		expect(splitCommandLine("   ")).toEqual([]);
		for (const words of [
			["npx", "-y", "server"],
			["/Applications/My App/bin/server", "--dir", "C:\\Users\\me\\notes", ""],
			["node", "-e", 'console.log("a b")', `{"a":1}`, "it's", 'say "hi"\\'],
		]) {
			expect(splitCommandLine(joinCommandLine(words))).toEqual(words);
		}
	});

	it("makes no key with two underscores together", () => {
		expect(connectorKeyFrom("My__Docs  Server")).toBe("my_docs-server");
	});

	it("derives a key and reads environment lines", () => {
		expect(connectorKeyFrom("  My Server (EU) ")).toBe("my-server-eu");
		expect(connectorKeyFrom("a".repeat(40))).toHaveLength(32);
		expect(connectorKeyFrom("_internal")).toBe("internal");
		expect(connectorKeyFrom("--_x_")).toBe("x_");
		expect(parseEnvLines("A=1\n\nB = x=y")).toEqual({ A: "1", B: " x=y" });
		expect(parseEnvLines("=1")).toBeNull();
	});

	it("names a target and knows when sign-in is needed", () => {
		expect(connectorTarget(base)).toBe("mcp.linear.app/mcp");
		expect(needsSignIn(base)).toBe(false);
		expect(needsSignIn({ ...base, signedIn: false, status: "unchecked" })).toBe(true);
		expect(needsSignIn({ ...base, auth: "none", signedIn: false, status: "connected" })).toBe(false);
		expect(needsSignIn({ ...base, auth: "header", signedIn: false, status: "needs_signin" })).toBe(false);
		expect(needsSignIn({ ...base, signedIn: true, status: "needs_signin" })).toBe(true);
		expect(needsSignIn({ ...base, signedIn: false, status: "unavailable" })).toBe(false);
	});
});
