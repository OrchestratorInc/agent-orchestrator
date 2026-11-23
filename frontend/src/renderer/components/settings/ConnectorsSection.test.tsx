import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, describe, expect, it, vi } from "vitest";
import { connectorKeyFrom, splitCommandLine, type Connector } from "../../hooks/useConnectors";
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

function mockList(connectors: Connector[] = list) {
	return vi.spyOn(apiClient, "GET").mockImplementation((async (path: string) =>
		path === "/api/v1/connectors/import" ? { data: { candidates: [] } } : { data: { connectors } }) as never);
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
		// Only the connector that needs it offers Sign in.
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

	it("adds a server from the catalog and starts its sign-in", async () => {
		mockList([]);
		vi.spyOn(aoBridge.app, "openExternal").mockResolvedValue(undefined as never);
		const created = { ...base, id: "c9", signedIn: false, status: "unchecked" as const, tools: [] };
		const post = vi.spyOn(apiClient, "POST").mockImplementation((async (path: string) =>
			path === "/api/v1/connectors"
				? { data: { connector: created } }
				: { data: { authorizationUrl: "https://linear.app/oauth/authorize", callbackUrl: "http://127.0.0.1:3011/oauth/mcp/callback", connector: created } }) as never);
		renderSection();
		fireEvent.click(await screen.findByRole("button", { name: "Add connector" }));
		const dialog = await screen.findByRole("dialog");
		fireEvent.click(screen.getByRole("button", { name: "Linear" }));
		expect(screen.getByLabelText("URL")).toHaveValue("https://mcp.linear.app/mcp");
		expect(screen.getByLabelText("Name")).toHaveValue("linear");
		fireEvent.click(dialog.querySelector('button[type="submit"]') as HTMLButtonElement);
		await waitFor(() => expect(post).toHaveBeenCalledWith("/api/v1/connectors", {
			body: { key: "linear", name: "Linear", projectId: null, presetId: "linear", transport: "http", url: "https://mcp.linear.app/mcp", auth: "oauth" },
		}));
		expect(await screen.findByRole("heading", { name: "Sign in to Linear" })).toBeInTheDocument();
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
			body: { key: "files", name: "files", projectId: null, transport: "stdio", command: "npx", args: ["-y", "files-mcp", "/my notes"], env: { TOKEN: "a=b" } },
		}));
	});
});

describe("connector helpers", () => {
	it("splits a command line with quotes", () => {
		expect(splitCommandLine(`npx -y "my server" --dir '/a b' c\\ d ""`)).toEqual(["npx", "-y", "my server", "--dir", "/a b", "c d", ""]);
		expect(splitCommandLine("   ")).toEqual([]);
	});

	it("derives a key and reads environment lines", () => {
		expect(connectorKeyFrom("  My Server (EU) ")).toBe("my-server-eu");
		expect(connectorKeyFrom("a".repeat(40))).toHaveLength(32);
		expect(parseEnvLines("A=1\n\nB = x=y")).toEqual({ A: "1", B: " x=y" });
		expect(parseEnvLines("=1")).toBeNull();
	});

	it("names a target and knows when sign-in is needed", () => {
		expect(connectorTarget(base)).toBe("mcp.linear.app/mcp");
		expect(needsSignIn(base)).toBe(false);
		expect(needsSignIn({ ...base, signedIn: false, status: "unchecked" })).toBe(true);
		expect(needsSignIn({ ...base, auth: "none", signedIn: false, status: "connected" })).toBe(false);
		expect(needsSignIn({ ...base, auth: "header", signedIn: false, status: "needs_signin" })).toBe(true);
	});
});
