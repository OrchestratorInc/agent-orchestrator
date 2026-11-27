import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, expect, it, vi } from "vitest";
import { useUiStore } from "../../stores/ui-store";
import { TooltipProvider } from "../ui/tooltip";
import { AgentAccountsPanel, type CliLoginControls } from "./AgentAccountsSection";

const { deleteMock, getMock, patchMock, postMock, putMock, terminalState } = vi.hoisted(() => ({
	deleteMock: vi.fn(), getMock: vi.fn(), patchMock: vi.fn(), postMock: vi.fn(), putMock: vi.fn(),
	terminalState: { value: undefined as ((state: "attached" | "exited" | "error") => void) | undefined },
}));

vi.mock("../../lib/api-client", () => ({
	apiClient: { DELETE: deleteMock, GET: getMock, PATCH: patchMock, POST: postMock, PUT: putMock },
	apiErrorMessage: (error: unknown) => error instanceof Error ? error.message : "request failed",
	apiErrorCode: (error: unknown) => (error as { code?: string } | undefined)?.code,
	apiErrorDetails: (error: unknown) => (error as { details?: Record<string, unknown> } | undefined)?.details,
}));

// The harness's sign-in panel follows an account's background sign-in here.
vi.mock("../../hooks/useAgentAuth", async (importOriginal) => ({
	...(await importOriginal<typeof import("../../hooks/useAgentAuth")>()),
	getAgentUILogin: vi.fn(), cancelAgentUILogin: vi.fn(), startAgentUILogin: vi.fn(),
}));

vi.mock("../TerminalPane", () => ({
	TerminalPane: ({ onTerminalStateChange }: { onTerminalStateChange?: (state: "attached" | "exited" | "error") => void }) => {
		terminalState.value = onTerminalStateChange;
		return <div data-testid="inline-terminal-body" />;
	},
}));

const device = { id: "device-claude", provider: "claude", device: true, default: true, signedIn: true, loggedIn: true, email: "a@example.com", kind: "subscription" };
const saved = { id: "acct-b", provider: "claude", device: false, default: false, signedIn: false, loggedIn: false, email: "b@example.com", kind: "subscription" };
const login = { operationId: "login-1", provider: "claude", shellTerminal: { handleId: "shellterm-1", title: "Sign in to Claude account", createdAt: "2026-10-18T10:00:00Z" } };

function renderPanel(cliLogin?: CliLoginControls) {
	const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } });
	return render(<QueryClientProvider client={queryClient}><TooltipProvider><AgentAccountsPanel provider="claude" cliLogin={cliLogin} /></TooltipProvider></QueryClientProvider>);
}

const findCliLoginRow = () => waitFor(() => {
	const row = document.querySelector<HTMLElement>('[data-agent-account="device-claude"]');
	if (!row) throw new Error("no CLI login row");
	return row;
});

beforeEach(() => {
	patchMock.mockReset();
	useUiStore.setState({ accountLogin: null });
	getMock.mockReset().mockImplementation((path: string) => Promise.resolve({
		data: path === "/api/v1/agents/accounts/usage" ? { days: 30, usage: [] } : { accounts: [device, saved] },
	}));
	postMock.mockReset().mockImplementation((path: string) => {
		if (path === "/api/v1/agents/accounts/{accountId}/login") return Promise.resolve({ data: login });
		if (path === "/api/v1/agents/accounts/login/{operationId}/verify") return Promise.resolve({ data: { status: "mismatch", account: saved } });
		return Promise.resolve({ data: {} });
	});
});

it("signs a signed-out saved account in again in its own folder", async () => {
	const user = userEvent.setup();
	renderPanel();
	await user.click(await screen.findByRole("button", { name: "Sign in again" }));

	expect(postMock).toHaveBeenCalledWith("/api/v1/agents/accounts/{accountId}/login", { params: { path: { accountId: "acct-b" } }, body: {} });
	expect(await screen.findByTestId("agent-account-login-terminal")).toBeInTheDocument();
	// Only the signed-out saved account offers it; it cannot be made default while signed out.
	expect(screen.getAllByRole("button", { name: "Sign in again" })).toHaveLength(1);
	expect(screen.queryByRole("button", { name: "Make default" })).not.toBeInTheDocument();

	await user.click(screen.getByRole("button", { name: "Check sign-in" }));
	await waitFor(() => expect(screen.getByRole("alert")).toHaveTextContent("This account is b@example.com. Sign in again with that login."));
});

it("names Windows Developer Mode, not Orchestrator's, when Windows refused an account folder's links", async () => {
	postMock.mockImplementation(() => Promise.resolve({ error: { code: "AGENT_ACCOUNT_UNSHARED", message: "server text", details: { windowsDeveloperMode: true } } }));
	const user = userEvent.setup();
	renderPanel();
	await user.click(await screen.findByRole("button", { name: "Sign in again" }));

	const alert = await screen.findByRole("alert");
	expect(alert).toHaveTextContent("Turn on Windows Developer Mode to use this account.");
	// A setting the user can change reads as a warning, not a failure.
	expect(alert).toHaveClass("text-warning");
	await user.hover(within(alert).getByRole("button", { name: "How to turn on Windows Developer Mode" }));
	expect(await screen.findByRole("tooltip")).toHaveTextContent("System > Advanced");
});

it("keeps any other failed account link an error without the Developer Mode help", async () => {
	postMock.mockImplementation(() => Promise.resolve({ error: { code: "AGENT_ACCOUNT_UNSHARED", message: "server text" } }));
	const user = userEvent.setup();
	renderPanel();
	await user.click(await screen.findByRole("button", { name: "Sign in again" }));

	const alert = await screen.findByRole("alert");
	expect(alert).toHaveClass("text-error");
	expect(alert).not.toHaveTextContent("Developer Mode");
	expect(within(alert).queryByRole("button", { name: "How to turn on Windows Developer Mode" })).not.toBeInTheDocument();
});

it("starts the sign-in a chat asked for, once", async () => {
	useUiStore.setState({ accountLogin: "acct-b" });
	const user = userEvent.setup();
	renderPanel();

	expect(await screen.findByTestId("agent-account-login-terminal")).toBeInTheDocument();
	expect(postMock.mock.calls.filter(([path]) => path === "/api/v1/agents/accounts/{accountId}/login")).toEqual([
		["/api/v1/agents/accounts/{accountId}/login", { params: { path: { accountId: "acct-b" } }, body: {} }],
	]);
	expect(useUiStore.getState().accountLogin).toBeNull();
	await user.click(screen.getByRole("button", { name: "Close sign-in" }));
});

it("signs this computer's login in through the CLI once it can, when a chat asks", async () => {
	useUiStore.setState({ accountLogin: "device-claude" });
	const onSignIn = vi.fn();
	const cliLogin = { disabled: true, canLogout: true, onSignIn, onLogout: vi.fn() };
	const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } });
	const panel = (controls: CliLoginControls) => <QueryClientProvider client={queryClient}><TooltipProvider><AgentAccountsPanel provider="claude" cliLogin={controls} /></TooltipProvider></QueryClientProvider>;
	const { rerender } = render(panel(cliLogin));
	await findCliLoginRow();
	expect(onSignIn).not.toHaveBeenCalled();

	rerender(panel({ ...cliLogin, disabled: false }));
	await waitFor(() => expect(onSignIn).toHaveBeenCalledTimes(1));
	expect(useUiStore.getState().accountLogin).toBeNull();
	expect(postMock).not.toHaveBeenCalledWith("/api/v1/agents/accounts/{accountId}/login", expect.anything());
});

it("does not check a closed sign-in, and the remove dialog starts without an old error", async () => {
	const user = userEvent.setup();
	renderPanel();
	await user.click(await screen.findByRole("button", { name: "Sign in again" }));
	const exited = terminalState.value;
	await user.click(await screen.findByRole("button", { name: "Close sign-in" }));
	// The closed terminal still reports its exit.
	exited?.("exited");
	await user.click(screen.getAllByRole("button", { name: "Remove account" })[0]);

	expect(postMock).not.toHaveBeenCalledWith("/api/v1/agents/accounts/login/{operationId}/verify", expect.anything());
	expect(await screen.findByRole("dialog")).not.toHaveTextContent("sign-in");
});

it("shows a removal in progress until it finishes", async () => {
	const user = userEvent.setup();
	let finish: (value: unknown) => void = () => undefined;
	deleteMock.mockReset().mockReturnValue(new Promise((resolve) => { finish = resolve; }));
	renderPanel();
	await user.click((await screen.findAllByRole("button", { name: "Remove account" }))[0]);
	const dialog = await screen.findByRole("dialog");
	await user.click(within(dialog).getByRole("button", { name: "Remove account" }));

	expect(within(dialog).getByRole("button", { name: "Removing…" })).toBeDisabled();
	finish({ data: { accounts: [device] } });
	await waitFor(() => expect(screen.queryByRole("dialog")).not.toBeInTheDocument());
});

it("checks a sign-in once when the CLI exits during a check", async () => {
	const user = userEvent.setup();
	let finish: (value: unknown) => void = () => undefined;
	postMock.mockImplementation((path: string) => {
		if (path === "/api/v1/agents/accounts/{accountId}/login") return Promise.resolve({ data: login });
		if (path === "/api/v1/agents/accounts/login/{operationId}/verify") return new Promise((resolve) => { finish = resolve; });
		return Promise.resolve({ data: {} });
	});
	renderPanel();
	await user.click(await screen.findByRole("button", { name: "Sign in again" }));
	await user.click(await screen.findByRole("button", { name: "Check sign-in" }));
	// The CLI exits while that check is still running.
	terminalState.value?.("exited");
	finish({ data: { status: "complete", account: { ...saved, signedIn: true, loggedIn: true } } });

	await waitFor(() => expect(screen.queryByTestId("agent-account-login-terminal")).not.toBeInTheDocument());
	expect(postMock.mock.calls.filter(([path]) => path === "/api/v1/agents/accounts/login/{operationId}/verify")).toHaveLength(1);
	expect(screen.queryByRole("alert")).not.toBeInTheDocument();
});

// A chat turn refused for its login sent the user here: the account that signs
// in takes them back to it.
it("returns to the chat turn waiting on a Claude login once its account signs in", async () => {
	const user = userEvent.setup();
	useUiStore.setState({
		settingsModal: { scope: "global", section: "harness", focusAgentId: "claude-code", startLogin: true },
		loginReturn: { sessionId: "s1", agentId: "claude-code", accountId: saved.id },
	});
	postMock.mockImplementation((path: string) => {
		if (path === "/api/v1/agents/accounts/{accountId}/login") return Promise.resolve({ data: login });
		if (path === "/api/v1/agents/accounts/login/{operationId}/verify") return Promise.resolve({ data: { status: "complete", account: { ...saved, signedIn: true } } });
		return Promise.resolve({ data: {} });
	});
	renderPanel();
	await user.click(await screen.findByRole("button", { name: "Sign in again" }));
	await user.click(await screen.findByRole("button", { name: "Check sign-in" }));

	await waitFor(() => expect(useUiStore.getState().loginReturn?.loggedInAt).toBeDefined());
	expect(useUiStore.getState().settingsModal).toBeNull();
	useUiStore.setState({ loginReturn: null });
});

// The session runs on its own account; signing another one in does not fix it.
it("keeps the chat waiting when a different account signs in", async () => {
	const user = userEvent.setup();
	useUiStore.setState({
		settingsModal: { scope: "global", section: "harness", focusAgentId: "claude-code", startLogin: true },
		loginReturn: { sessionId: "s1", agentId: "claude-code", accountId: "the-sessions-account" },
	});
	postMock.mockImplementation((path: string) => {
		if (path === "/api/v1/agents/accounts/{accountId}/login") return Promise.resolve({ data: login });
		if (path === "/api/v1/agents/accounts/login/{operationId}/verify") return Promise.resolve({ data: { status: "complete", account: { ...saved, signedIn: true } } });
		return Promise.resolve({ data: {} });
	});
	renderPanel();
	await user.click(await screen.findByRole("button", { name: "Sign in again" }));
	await user.click(await screen.findByRole("button", { name: "Check sign-in" }));

	await waitFor(() => expect(postMock.mock.calls.some(([path]) => path === "/api/v1/agents/accounts/login/{operationId}/verify")).toBe(true));
	await waitFor(() => expect(screen.queryByTestId("agent-account-login-terminal")).not.toBeInTheDocument());
	expect(useUiStore.getState().loginReturn?.loggedInAt).toBeUndefined();
	expect(useUiStore.getState().settingsModal).not.toBeNull();
	useUiStore.setState({ loginReturn: null, settingsModal: null });
});

// The CLI's own login has no card of its own: its row signs it in again and
// out through the harness's login terminal, and has no folder to remove.
it("signs the CLI login in again and out from its own row", async () => {
	const user = userEvent.setup();
	const cliLogin = { disabled: false, canLogout: true, onSignIn: vi.fn(), onLogout: vi.fn() };
	renderPanel(cliLogin);
	const row = await findCliLoginRow();
	await user.click(within(row).getByRole("button", { name: "Sign in again" }));
	await user.click(within(row).getByRole("button", { name: "Log out" }));

	expect(cliLogin.onSignIn).toHaveBeenCalledTimes(1);
	expect(cliLogin.onLogout).toHaveBeenCalledTimes(1);
	expect(within(row).queryByRole("button", { name: "Remove account" })).not.toBeInTheDocument();
	expect(postMock).not.toHaveBeenCalledWith("/api/v1/agents/accounts/{accountId}/login", expect.anything());
});

it("offers only a sign-in on a signed-out CLI login, held while a login terminal is open", async () => {
	getMock.mockImplementation((path: string) => Promise.resolve({
		data: path === "/api/v1/agents/accounts/usage" ? { days: 30, usage: [] } : { accounts: [{ ...device, signedIn: false, loggedIn: false, default: false }, { ...saved, signedIn: true, loggedIn: true, default: true }] },
	}));
	renderPanel({ disabled: true, canLogout: true, onSignIn: vi.fn(), onLogout: vi.fn() });
	const row = await findCliLoginRow();
	expect(within(row).getByRole("button", { name: "Sign in again" })).toBeDisabled();
	expect(within(row).queryByRole("button", { name: "Log out" })).not.toBeInTheDocument();
});

// An API key launches the CLI but is not a subscription: the row says so
// instead of signed out, and the login can still be logged out.
it("shows an API-key CLI login as logged in without a subscription and keeps its Log out", async () => {
	const user = userEvent.setup();
	getMock.mockImplementation((path: string) => Promise.resolve({
		data: path === "/api/v1/agents/accounts/usage" ? { days: 30, usage: [] } : { accounts: [{ ...device, email: undefined, signedIn: false, loggedIn: true }, saved] },
	}));
	const cliLogin = { disabled: false, canLogout: true, onSignIn: vi.fn(), onLogout: vi.fn() };
	renderPanel(cliLogin);
	const row = await findCliLoginRow();
	expect(row).toHaveTextContent("Logged in without a subscription");
	expect(row).not.toHaveTextContent("Signed out");
	await user.click(within(row).getByRole("button", { name: "Log out" }));
	expect(cliLogin.onLogout).toHaveBeenCalledTimes(1);
});

function renderWithAccounts(accounts: unknown[]) {
	getMock.mockImplementation((path: string) => Promise.resolve({
		data: path === "/api/v1/agents/accounts/usage" ? { days: 30, usage: [] } : { accounts },
	}));
	renderPanel();
}

const findProblem = (id: string) => waitFor(() => {
	const note = document.querySelector<HTMLElement>(`[data-agent-account="${id}"] [data-account-problem]`);
	if (!note) throw new Error("no problem note");
	return note;
});

it("says a slow check plainly, keeps the old numbers' age, and puts the CLI's error under Details", async () => {
	const fetchedAt = new Date(Date.now() - 6 * 60_000).toISOString();
	renderWithAccounts([{ ...device, problem: "context deadline exceeded", problemKind: "slow", limits: { fetchedAt, windows: [{ id: "five_hour", label: "Session (5h)", usedPercent: 28 }] } }]);

	const note = await findProblem("device-claude");
	expect(note).toHaveTextContent("Updated 6 minutes ago · Claude Code took too long to report its limits. Trying again shortly.");
	expect(note).not.toHaveTextContent("context deadline exceeded");
	expect(note).toHaveClass("text-muted-foreground");
	expect(note.querySelector("svg")).toBeNull();
	// The numbers from the last good check stay.
	expect(screen.getByRole("progressbar", { name: /Session \(5h\)/ })).toBeInTheDocument();

	await userEvent.setup().hover(within(note).getByRole("button", { name: "Details" }));
	expect(await screen.findByRole("tooltip")).toHaveTextContent("context deadline exceeded");
});

it("words an older daemon's bare error as a check that will be tried again", async () => {
	renderWithAccounts([{ ...device, problem: "claude ended without answering get_usage" }]);

	const note = await findProblem("device-claude");
	expect(note).toHaveTextContent("Couldn't check limits right now. Trying again shortly.");
	expect(note).not.toHaveTextContent("get_usage");
});

it("warns when the folder is signed in as another login or the CLI is missing", async () => {
	renderWithAccounts([
		{ ...saved, signedIn: false, problem: "the folder is now signed in as c@example.com, not b@example.com", problemKind: "other_login" },
		{ ...device, problem: "claude: executable file not found in $PATH", problemKind: "cli_missing" },
	]);

	const other = await findProblem("acct-b");
	expect(other).toHaveTextContent("This account's folder is now signed in to a different login. Sign in again to use it.");
	expect(other).toHaveClass("text-warning");
	expect(other.querySelector("svg")).not.toBeNull();
	const missing = await findProblem("device-claude");
	expect(missing).toHaveTextContent("Claude Code isn't installed, so its limits can't be checked.");
	expect(missing).toHaveClass("text-warning");
});

it("lists a login the CLI and a saved account share once", async () => {
	const twin = { ...device, default: false, sameLoginAs: "acct-a" };
	const savedA = { id: "acct-a", provider: "claude", device: false, default: true, signedIn: true, email: "a@example.com" };
	getMock.mockImplementation((path: string) => Promise.resolve({
		data: path === "/api/v1/agents/accounts/usage"
			? { days: 30, usage: [{ accountId: "device-claude", inputTokens: 1000, outputTokens: 0 }, { accountId: "acct-a", inputTokens: 2000, outputTokens: 0 }] }
			: { accounts: [twin, savedA] },
	}));
	renderPanel({ disabled: false, canLogout: true, onSignIn: vi.fn(), onLogout: vi.fn() });

	const row = await waitFor(() => {
		const found = document.querySelector<HTMLElement>('[data-agent-account="acct-a"]');
		if (!found) throw new Error("no saved row");
		return found;
	});
	expect(document.querySelector('[data-agent-account="device-claude"]')).not.toBeInTheDocument();
	expect(screen.getAllByText("a@example.com")).toHaveLength(1);
	expect(screen.queryByText("Claude Code CLI login")).not.toBeInTheDocument();
	await waitFor(() => expect(within(row).getByText(/3K tokens in Orchestrator/)).toBeInTheDocument());
	expect(within(row).getByRole("button", { name: "Remove account" })).toBeInTheDocument();
	expect(within(row).queryByRole("button", { name: "Log out" })).not.toBeInTheDocument();
});

it("keeps the CLI's login as its own row while it is the default, so the saved account can be made the default", async () => {
	const user = userEvent.setup();
	const twin = { ...device, default: true, sameLoginAs: "acct-a" };
	const savedA = { id: "acct-a", provider: "claude", device: false, default: false, signedIn: true, email: "a@example.com" };
	getMock.mockImplementation((path: string) => Promise.resolve({
		data: path === "/api/v1/agents/accounts/usage" ? { days: 30, usage: [] } : { accounts: [twin, savedA, { ...saved, signedIn: true }] },
	}));
	renderPanel();

	const cliRow = await findCliLoginRow();
	await waitFor(() => expect(document.querySelector('[data-agent-account="acct-a"]')).toBeInTheDocument());
	const row = document.querySelector<HTMLElement>('[data-agent-account="acct-a"]')!;
	expect(cliRow).toHaveTextContent("a@example.com · Claude Code CLI login");
	expect(within(cliRow).getByText("Default for new tasks")).toBeInTheDocument();
	expect(screen.getAllByText("Default for new tasks")).toHaveLength(1);
	await user.click(within(row).getByRole("button", { name: "Make default" }));
	expect(postMock).toHaveBeenCalledWith("/api/v1/agents/accounts/{accountId}/default", { params: { path: { accountId: "acct-a" } } });
});

it("names each organization when one email has accounts in several", async () => {
	const acme = { id: "acct-a", provider: "claude", device: false, default: true, signedIn: true, email: "a@example.com", orgName: "Acme" };
	const personal = { id: "acct-p", provider: "claude", device: false, default: false, signedIn: true, email: "a@example.com", orgName: "Personal" };
	getMock.mockImplementation((path: string) => Promise.resolve({
		data: path === "/api/v1/agents/accounts/usage" ? { days: 30, usage: [] } : { accounts: [{ ...device, signedIn: false, loggedIn: false, default: false, email: undefined }, acme, personal] },
	}));
	renderPanel();

	await waitFor(() => expect(document.querySelector('[data-agent-account="acct-p"]')).toBeInTheDocument());
	expect(document.querySelector('[data-agent-account="acct-a"]')).toHaveTextContent("a@example.com · Acme");
	expect(document.querySelector('[data-agent-account="acct-p"]')).toHaveTextContent("a@example.com · Personal");
});

it("names the organization when signing in again lands in another one", async () => {
	const user = userEvent.setup();
	postMock.mockImplementation((path: string) => {
		if (path === "/api/v1/agents/accounts/{accountId}/login") return Promise.resolve({ data: login });
		if (path === "/api/v1/agents/accounts/login/{operationId}/verify") return Promise.resolve({ data: { status: "mismatch", account: { ...saved, orgName: "Acme" } } });
		return Promise.resolve({ data: {} });
	});
	renderPanel();
	await user.click(await screen.findByRole("button", { name: "Sign in again" }));
	await user.click(await screen.findByRole("button", { name: "Check sign-in" }));
	await waitFor(() => expect(screen.getByRole("alert")).toHaveTextContent("This account is b@example.com · Acme. Sign in again with that login."));
});

it("says an API key is not a subscription sign-in and keeps the sign-in open", async () => {
	const user = userEvent.setup();
	postMock.mockImplementation((path: string) => {
		if (path === "/api/v1/agents/accounts/{accountId}/login") return Promise.resolve({ data: login });
		if (path === "/api/v1/agents/accounts/login/{operationId}/verify") return Promise.resolve({ data: { status: "not_subscription" } });
		return Promise.resolve({ data: {} });
	});
	renderPanel();
	await user.click(await screen.findByRole("button", { name: "Sign in again" }));
	await user.click(await screen.findByRole("button", { name: "Check sign-in" }));
	await waitFor(() => expect(screen.getByRole("status")).toHaveTextContent("This is an API key, not a subscription. Sign in with your subscription account."));
	expect(screen.getByTestId("agent-account-login-terminal")).toBeInTheDocument();
	expect(screen.queryByRole("alert")).not.toBeInTheDocument();
});

it.each([
	["no accounts", []],
	["one CLI account", [device]],
	["one saved account", [saved]],
	["an unsigned CLI placeholder and one saved account", [{ ...device, signedIn: false, loggedIn: false, email: undefined }, saved]],
	["an API key CLI login and one saved account", [{ ...device, signedIn: false }, saved]],
	["one account per provider", [device, { ...saved, provider: "codex" }]],
	["the CLI and saved copy of one login", [{ ...device, sameLoginAs: saved.id }, { ...saved, signedIn: true, loggedIn: true, email: device.email }]],
])("hides the usage-limit handoff with %s", async (_name, accounts) => {
	getMock.mockImplementation((path: string) => Promise.resolve({
		data: path === "/api/v1/agents/accounts/usage" ? { days: 30, usage: [] } : { accounts },
	}));
	renderPanel();
	await waitFor(() => expect(screen.queryByText("Loading accounts…")).not.toBeInTheDocument());

	expect(screen.queryByRole("switch", { name: "Move to another account at a usage limit" })).not.toBeInTheDocument();
});

it("shows the usage-limit handoff for two saved accounts with the CLI signed out", async () => {
	getMock.mockImplementation((path: string) => Promise.resolve({
		data: path === "/api/v1/agents/accounts/usage" ? { days: 30, usage: [] }
			: { accounts: [{ ...device, signedIn: false, loggedIn: false }, saved, { ...saved, id: "acct-c", email: "c@example.com" }] },
	}));
	renderPanel();

	expect(await screen.findByRole("switch", { name: "Move to another account at a usage limit" })).toBeInTheDocument();
});

it("hides the usage-limit handoff after removing the second account", async () => {
	const user = userEvent.setup();
	deleteMock.mockReset().mockResolvedValue({ data: { accounts: [device] } });
	renderPanel();
	const toggle = await screen.findByRole("switch", { name: "Move to another account at a usage limit" });
	await user.click(screen.getByRole("button", { name: "Remove account" }));
	await user.click(within(await screen.findByRole("dialog")).getByRole("button", { name: "Remove account" }));

	await waitFor(() => expect(toggle).not.toBeInTheDocument());
});

it("turns the usage-limit handoff on through the daemon and shows what it stored", async () => {
	const user = userEvent.setup();
	let stored = false;
	getMock.mockImplementation((path: string) => Promise.resolve({
		data: path === "/api/v1/settings" ? { accountHandoff: stored }
			: path === "/api/v1/agents/accounts/usage" ? { days: 30, usage: [] } : { accounts: [device, saved] },
	}));
	patchMock.mockReset().mockImplementation((_path: string, { body }: { body: { enabled: boolean } }) => {
		stored = body.enabled;
		return Promise.resolve({ data: { accountHandoff: stored } });
	});
	renderPanel();
	const toggle = await screen.findByRole("switch", { name: "Move to another account at a usage limit" });
	await waitFor(() => expect(toggle).toBeEnabled());
	expect(toggle).not.toBeChecked();

	await user.click(toggle);

	expect(patchMock).toHaveBeenCalledWith("/api/v1/settings/account-handoff", { body: { enabled: true } });
	await waitFor(() => expect(toggle).toBeChecked());
});

const claudeMethods = [
	{ id: "browser", label: "Claude subscription", secret: false },
	{ id: "console", label: "Anthropic Console", secret: false },
	{ id: "sso", label: "Organization SSO", secret: false },
	{ id: "setup_token", label: "Setup token", secret: true },
	{ id: "env_ANTHROPIC_API_KEY", label: "Anthropic API key", secret: true },
];
const keyAccount = { id: "acct-key", provider: "claude", device: false, default: false, signedIn: false, loggedIn: true, kind: "api_key", name: "Work", keyHint: "cdef" };

function renderPanelWithMethods(cliLogin?: CliLoginControls) {
	const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } });
	return render(<QueryClientProvider client={queryClient}><TooltipProvider><AgentAccountsPanel provider="claude" loginMethods={claudeMethods} cliLogin={cliLogin} /></TooltipProvider></QueryClientProvider>);
}

it("adds an account through the harness's own sign-in panel, offering every Claude method", async () => {
	const user = userEvent.setup();
	renderPanelWithMethods();
	await user.click(await screen.findByRole("button", { name: "Add account" }));

	for (const name of ["Claude subscription", "Anthropic Console", "Organization SSO", "Setup token", "Anthropic API key"]) {
		expect(screen.getByRole("button", { name })).toBeInTheDocument();
	}
	// The account panel has no terminal: the in-app sign-in is the way in.
	expect(screen.queryByTestId("agent-account-login-terminal")).not.toBeInTheDocument();
});

it("adds an Anthropic API key from the panel, checked by the daemon first", async () => {
	const user = userEvent.setup();
	postMock.mockImplementation((path: string) => {
		if (path !== "/api/v1/agents/accounts/api-keys") return Promise.resolve({ data: {} });
		return postMock.mock.calls.filter(([called]) => called === path).length === 1
			? Promise.resolve({ error: { code: "AGENT_ACCOUNT_KEY_REJECTED", message: "rejected" } })
			: Promise.resolve({ data: { accounts: [device, saved, keyAccount] } });
	});
	renderPanelWithMethods();
	await user.click(await screen.findByRole("button", { name: "Add account" }));
	await user.click(screen.getByRole("button", { name: "Anthropic API key" }));

	expect(screen.getByText(/billed per token to your API account/)).toBeInTheDocument();
	await user.type(screen.getByLabelText("Name"), "Work");
	await user.type(screen.getByLabelText("API key"), "sk-ant-api03-secret");
	await user.click(screen.getByRole("button", { name: "Save and verify" }));
	expect(await screen.findByText("The provider rejected this key.")).toBeInTheDocument();
	expect(postMock).toHaveBeenCalledWith("/api/v1/agents/accounts/api-keys", { body: { provider: "claude", name: "Work", apiKey: "sk-ant-api03-secret" } });

	// The refused key stays typed, so a retry needs no retyping.
	expect(screen.getByLabelText("API key")).toHaveValue("sk-ant-api03-secret");
	await user.click(screen.getByRole("button", { name: "Save and verify" }));
	await waitFor(() => expect(screen.queryByRole("button", { name: "Anthropic API key" })).not.toBeInTheDocument());
	const row = document.querySelector<HTMLElement>('[data-agent-account="acct-key"]');
	expect(row).toHaveTextContent("Key ending cdef · Billed per token");
	expect(row).not.toHaveTextContent("secret");
});

it("signs a subscription in through the CLI in the background and saves the account once it finishes", async () => {
	const user = userEvent.setup();
	const { getAgentUILogin } = await import("../../hooks/useAgentAuth");
	const running = { id: "auth-1", agentId: "claude-code", method: "browser", status: "running", expiresAt: "2026-11-17T10:00:00Z", loginUrl: "https://claude.ai/oauth" };
	vi.mocked(getAgentUILogin).mockReset()
		.mockResolvedValueOnce(running as never)
		.mockResolvedValue({ ...running, status: "succeeded", loginUrl: undefined } as never);
	postMock.mockImplementation((path: string) => {
		if (path === "/api/v1/agents/accounts/login") return Promise.resolve({ data: { operationId: "acct-op", provider: "claude", authOperationId: "auth-1" } });
		if (path === "/api/v1/agents/accounts/login/{operationId}/verify") return Promise.resolve({ data: { status: "complete", account: saved } });
		return Promise.resolve({ data: {} });
	});
	renderPanelWithMethods();
	await user.click(await screen.findByRole("button", { name: "Add account" }));
	await user.click(screen.getByRole("button", { name: "Claude subscription" }));

	expect(postMock).toHaveBeenCalledWith("/api/v1/agents/accounts/login", { body: { provider: "claude", method: "browser" } });
	await waitFor(() => expect(postMock).toHaveBeenCalledWith("/api/v1/agents/accounts/login/{operationId}/verify", { params: { path: { operationId: "acct-op" } } }), { timeout: 4000 });
	await waitFor(() => expect(screen.queryByRole("button", { name: "Claude subscription" })).not.toBeInTheDocument());
	expect(postMock).not.toHaveBeenCalledWith("/api/v1/agents/accounts/login/{operationId}/cancel", expect.anything());
});

it("shows an API key account with Replace key in the same panel instead of a sign-in", async () => {
	const user = userEvent.setup();
	getMock.mockImplementation((path: string) => Promise.resolve({
		data: path === "/api/v1/agents/accounts/usage" ? { days: 30, usage: [] } : { accounts: [device, keyAccount] },
	}));
	putMock.mockReset().mockResolvedValue({ data: { accounts: [device, { ...keyAccount, keyHint: "wxyz" }] } });
	renderPanelWithMethods();

	const row = await waitFor(() => {
		const found = document.querySelector<HTMLElement>('[data-agent-account="acct-key"]');
		if (!found) throw new Error("no API key row");
		return found;
	});
	expect(within(row).queryByRole("button", { name: "Sign in again" })).not.toBeInTheDocument();
	expect(within(row).getByRole("button", { name: "Make default" })).toBeEnabled();
	// Only subscriptions move at a usage limit, so one of each shows no handoff switch.
	expect(screen.queryByRole("switch", { name: "Move to another account at a usage limit" })).not.toBeInTheDocument();

	await user.click(within(row).getByRole("button", { name: "Replace key" }));
	// Only the key method, already open, and no name: the account keeps its own.
	expect(screen.queryByRole("button", { name: "Claude subscription" })).not.toBeInTheDocument();
	expect(screen.queryByLabelText("Name")).not.toBeInTheDocument();
	await user.type(screen.getByLabelText("API key"), "sk-ant-api03-new");
	await user.click(screen.getByRole("button", { name: "Save and verify" }));

	expect(putMock).toHaveBeenCalledWith("/api/v1/agents/accounts/{accountId}/api-key", { params: { path: { accountId: "acct-key" } }, body: { apiKey: "sk-ant-api03-new" } });
	await waitFor(() => expect(row).toHaveTextContent("Key ending wxyz"));
});

it("shows the Windows Developer Mode notice when the sign-in panel cannot link a new account's folder", async () => {
	const user = userEvent.setup();
	postMock.mockImplementation((path: string) => path === "/api/v1/agents/accounts/login"
		? Promise.resolve({ error: { code: "AGENT_ACCOUNT_UNSHARED", message: "server text", details: { windowsDeveloperMode: true } } })
		: Promise.resolve({ data: {} }));
	renderPanelWithMethods();
	await user.click(await screen.findByRole("button", { name: "Add account" }));
	await user.click(screen.getByRole("button", { name: "Claude subscription" }));

	// The panel closes; the card shows the localized warning and its help, as for a terminal sign-in.
	await waitFor(() => expect(screen.queryByRole("button", { name: "Claude subscription" })).not.toBeInTheDocument());
	const alert = screen.getByRole("alert");
	expect(alert).toHaveTextContent("Turn on Windows Developer Mode to use this account.");
	expect(alert).toHaveClass("text-warning");
	expect(within(alert).getByRole("button", { name: "How to turn on Windows Developer Mode" })).toBeInTheDocument();
});

it("cancels an account's pending sign-in when the panel goes away mid sign-in", async () => {
	const user = userEvent.setup();
	const { getAgentUILogin } = await import("../../hooks/useAgentAuth");
	vi.mocked(getAgentUILogin).mockReset().mockResolvedValue({ id: "auth-1", agentId: "claude-code", method: "browser", status: "running", expiresAt: "2026-11-17T10:00:00Z" } as never);
	postMock.mockImplementation((path: string) => path === "/api/v1/agents/accounts/login"
		? Promise.resolve({ data: { operationId: "acct-op", provider: "claude", authOperationId: "auth-1" } })
		: Promise.resolve({ data: {} }));
	const { unmount } = renderPanelWithMethods();
	await user.click(await screen.findByRole("button", { name: "Add account" }));
	await user.click(screen.getByRole("button", { name: "Claude subscription" }));
	await waitFor(() => expect(postMock).toHaveBeenCalledWith("/api/v1/agents/accounts/login", expect.anything()));

	unmount();
	await waitFor(() => expect(postMock).toHaveBeenCalledWith("/api/v1/agents/accounts/login/{operationId}/cancel", { params: { path: { operationId: "acct-op" } } }));
});

it("cancels an account sign-in that starts after the panel went away", async () => {
	const user = userEvent.setup();
	const { getAgentUILogin } = await import("../../hooks/useAgentAuth");
	vi.mocked(getAgentUILogin).mockReset().mockResolvedValue({ id: "auth-1", agentId: "claude-code", method: "browser", status: "running", expiresAt: "2026-11-17T10:00:00Z" } as never);
	let resolveStart: (value: unknown) => void = () => undefined;
	postMock.mockImplementation((path: string) => path === "/api/v1/agents/accounts/login"
		? new Promise((resolve) => { resolveStart = resolve; })
		: Promise.resolve({ data: {} }));
	const { unmount } = renderPanelWithMethods();
	await user.click(await screen.findByRole("button", { name: "Add account" }));
	await user.click(screen.getByRole("button", { name: "Claude subscription" }));
	await waitFor(() => expect(postMock).toHaveBeenCalledWith("/api/v1/agents/accounts/login", expect.anything()));

	unmount();
	resolveStart({ data: { operationId: "acct-op", provider: "claude", authOperationId: "auth-1" } });
	await waitFor(() => expect(postMock).toHaveBeenCalledWith("/api/v1/agents/accounts/login/{operationId}/cancel", { params: { path: { operationId: "acct-op" } } }));
});

it("keeps the computer's own sign-in waiting while an account signs in", async () => {
	const user = userEvent.setup();
	const onSignIn = vi.fn();
	renderPanelWithMethods({ disabled: false, canLogout: true, onSignIn, onLogout: vi.fn() });
	const row = await findCliLoginRow();
	expect(within(row).getByRole("button", { name: "Sign in again" })).toBeEnabled();

	await user.click(screen.getByRole("button", { name: "Add account" }));
	expect(within(row).getByRole("button", { name: "Sign in again" })).toBeDisabled();
});

it("adds an account in a terminal on a daemon from before sign-in methods", async () => {
	const user = userEvent.setup();
	const oldDevice = { ...device, kind: undefined };
	const oldSaved = { ...saved, kind: undefined };
	getMock.mockImplementation((path: string) => Promise.resolve({
		data: path === "/api/v1/agents/accounts/usage" ? { days: 30, usage: [] } : { accounts: [oldDevice, oldSaved] },
	}));
	postMock.mockImplementation((path: string) => Promise.resolve({ data: path === "/api/v1/agents/accounts/login" ? login : {} }));
	renderPanelWithMethods();
	await user.click(await screen.findByRole("button", { name: "Add account" }));

	// Its older request takes no method, so the panel never opens.
	expect(postMock).toHaveBeenCalledWith("/api/v1/agents/accounts/login", { body: { provider: "claude" } });
	expect(screen.queryByRole("button", { name: "Claude subscription" })).not.toBeInTheDocument();
});

it("waits for the sign-in methods before replacing a key", async () => {
	getMock.mockImplementation((path: string) => Promise.resolve({
		data: path === "/api/v1/agents/accounts/usage" ? { days: 30, usage: [] } : { accounts: [device, keyAccount] },
	}));
	renderPanel();
	await waitFor(() => expect(document.querySelector('[data-agent-account="acct-key"]')).not.toBeNull());
	const row = document.querySelector<HTMLElement>('[data-agent-account="acct-key"]')!;

	// Without the panel's methods, a key has no terminal sign-in to fall back to.
	expect(within(row).getByRole("button", { name: "Replace key" })).toBeDisabled();
	expect(postMock).not.toHaveBeenCalled();
});

function renderForEdit(accounts: unknown[]) {
	getMock.mockImplementation((path: string) => Promise.resolve({
		data: path === "/api/v1/agents/accounts/usage" ? { days: 30, usage: [] } : { accounts },
	}));
	renderPanel();
}
const rowOf = async (id: string) => {
	await waitFor(() => expect(document.querySelector(`[data-agent-account="${id}"]`)).not.toBeNull());
	return document.querySelector<HTMLElement>(`[data-agent-account="${id}"]`)!;
};

it("offers Edit on every account, this computer's login included", async () => {
	renderForEdit([device, saved]);
	expect(within(await rowOf("acct-b")).getByRole("button", { name: "Edit" })).toBeInTheDocument();
	expect(within(await rowOf("device-claude")).getByRole("button", { name: "Edit" })).toBeInTheDocument();
});

it("gives two accounts whose emails start alike different letters", async () => {
	renderForEdit([
		{ ...device, email: "ronish.rohan@gmail.com", color: "blue" },
		{ ...saved, email: "ronish@untrivial.ai", color: "violet" },
	]);
	expect(within(await rowOf("device-claude")).getByRole("img", { name: "ronish.rohan@gmail.com" })).toHaveTextContent("RO");
	expect(within(await rowOf("acct-b")).getByRole("img", { name: "ronish@untrivial.ai" })).toHaveTextContent("RU");
});

it("shows an account's name before its email, with a badge once there are two accounts", async () => {
	renderForEdit([device, { ...saved, name: "Cristian Account", color: "teal" }]);
	const row = await rowOf("acct-b");
	expect(row).toHaveTextContent("Cristian Account · b@example.com");
	expect(within(row).getByRole("img", { name: "Cristian Account" })).toHaveTextContent("CA");
});

it("saves a new name and color with PATCH and refreshes the accounts", async () => {
	const user = userEvent.setup();
	renderForEdit([device, saved]);
	patchMock.mockResolvedValue({ data: { ...saved, name: "Work", color: "blue" } });
	await user.click(within(await rowOf("acct-b")).getByRole("button", { name: "Edit" }));
	const dialog = await screen.findByRole("dialog");
	await user.type(within(dialog).getByLabelText("Name"), "Work");
	await user.click(within(dialog).getByRole("radio", { name: "Blue" }));
	expect(within(dialog).getByRole("radio", { name: "Blue" })).toBeChecked();
	getMock.mockClear();
	await user.click(within(dialog).getByRole("button", { name: "Save" }));
	await waitFor(() => expect(patchMock).toHaveBeenCalledWith("/api/v1/agents/accounts/{accountId}", { params: { path: { accountId: "acct-b" } }, body: { name: "Work", color: "blue" } }));
	await waitFor(() => expect(screen.queryByRole("dialog")).not.toBeInTheDocument());
	expect(getMock).toHaveBeenCalled();
});

it("disables a color another account uses, names that account, and offers no None", async () => {
	const user = userEvent.setup();
	renderForEdit([{ ...device, color: "blue" }, { ...saved, name: "Work", color: "teal" }]);
	await user.click(within(await rowOf("acct-b")).getByRole("button", { name: "Edit" }));
	const dialog = await screen.findByRole("dialog");
	expect(within(dialog).getByRole("radio", { name: "Teal" })).toBeChecked();
	expect(within(dialog).getByRole("radio", { name: "Blue, used by A · a@example.com" })).toBeDisabled();
	expect(within(dialog).queryByRole("radio", { name: "None" })).not.toBeInTheDocument();
	expect(within(dialog).getAllByRole("radio")).toHaveLength(8);
});

it("renames this computer's own login", async () => {
	const user = userEvent.setup();
	renderForEdit([{ ...device, color: "blue" }, { ...saved, color: "violet" }]);
	patchMock.mockResolvedValue({ data: { ...device, name: "Personal", color: "blue" } });
	await user.click(within(await rowOf("device-claude")).getByRole("button", { name: "Edit" }));
	const dialog = await screen.findByRole("dialog");
	await user.type(within(dialog).getByLabelText("Name"), "Personal");
	await user.click(within(dialog).getByRole("button", { name: "Save" }));
	await waitFor(() => expect(patchMock).toHaveBeenCalledWith("/api/v1/agents/accounts/{accountId}", { params: { path: { accountId: "device-claude" } }, body: { name: "Personal", color: "blue" } }));
});

it("does not save on the Enter that confirms an IME composition", async () => {
	const user = userEvent.setup();
	renderForEdit([device, saved]);
	patchMock.mockResolvedValue({ data: { ...saved, name: "仕事" } });
	await user.click(within(await rowOf("acct-b")).getByRole("button", { name: "Edit" }));
	const dialog = await screen.findByRole("dialog");
	const input = within(dialog).getByLabelText("Name");
	fireEvent.change(input, { target: { value: "仕事" } });
	fireEvent.keyDown(input, { key: "Enter", isComposing: true });
	fireEvent.keyDown(input, { key: "Enter", keyCode: 229 });
	expect(patchMock).not.toHaveBeenCalled();
	fireEvent.keyDown(input, { key: "Enter" });
	await waitFor(() => expect(patchMock).toHaveBeenCalledWith("/api/v1/agents/accounts/{accountId}", { params: { path: { accountId: "acct-b" } }, body: { name: "仕事", color: "" } }));
});

it("shows the API error inside the edit dialog and keeps it open", async () => {
	const user = userEvent.setup();
	renderForEdit([device, saved]);
	patchMock.mockResolvedValue({ error: { code: "AGENT_ACCOUNT_NOT_FOUND", message: "That account is gone" } });
	await user.click(within(await rowOf("acct-b")).getByRole("button", { name: "Edit" }));
	const dialog = await screen.findByRole("dialog");
	await user.type(within(dialog).getByLabelText("Name"), "Work");
	await user.click(within(dialog).getByRole("button", { name: "Save" }));
	expect(await within(dialog).findByRole("alert")).toBeInTheDocument();
	expect(screen.getByRole("dialog")).toBeInTheDocument();
});

it("sends the chosen color when adding an API key account", async () => {
	const user = userEvent.setup();
	postMock.mockImplementation((path: string) => Promise.resolve({ data: path === "/api/v1/agents/accounts/api-keys" ? { accounts: [device, saved, keyAccount] } : {} }));
	renderPanelWithMethods();
	await user.click(await screen.findByRole("button", { name: "Add account" }));
	await user.click(screen.getByRole("button", { name: "Anthropic API key" }));
	await user.type(screen.getByLabelText("Name"), "Work");
	await user.click(screen.getByRole("radio", { name: "Violet" }));
	await user.type(screen.getByLabelText("API key"), "sk-ant-api03-secret");
	await user.click(screen.getByRole("button", { name: "Save and verify" }));
	await waitFor(() => expect(postMock).toHaveBeenCalledWith("/api/v1/agents/accounts/api-keys", { body: { provider: "claude", name: "Work", apiKey: "sk-ant-api03-secret", color: "violet" } }));
});
