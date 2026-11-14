import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, expect, it, vi } from "vitest";
import { useUiStore } from "../../stores/ui-store";
import { TooltipProvider } from "../ui/tooltip";
import { AgentAccountsPanel, type CliLoginControls } from "./AgentAccountsSection";

const { deleteMock, getMock, patchMock, postMock, terminalState } = vi.hoisted(() => ({
	deleteMock: vi.fn(), getMock: vi.fn(), patchMock: vi.fn(), postMock: vi.fn(),
	terminalState: { value: undefined as ((state: "attached" | "exited" | "error") => void) | undefined },
}));

vi.mock("../../lib/api-client", () => ({
	apiClient: { DELETE: deleteMock, GET: getMock, PATCH: patchMock, POST: postMock },
	apiErrorMessage: (error: unknown) => error instanceof Error ? error.message : "request failed",
}));

vi.mock("../TerminalPane", () => ({
	TerminalPane: ({ onTerminalStateChange }: { onTerminalStateChange?: (state: "attached" | "exited" | "error") => void }) => {
		terminalState.value = onTerminalStateChange;
		return <div data-testid="inline-terminal-body" />;
	},
}));

const device = { id: "device-claude", provider: "claude", device: true, default: true, signedIn: true, loggedIn: true, email: "a@example.com" };
const saved = { id: "acct-b", provider: "claude", device: false, default: false, signedIn: false, loggedIn: false, email: "b@example.com" };
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

	expect(postMock).toHaveBeenCalledWith("/api/v1/agents/accounts/{accountId}/login", { params: { path: { accountId: "acct-b" } } });
	expect(await screen.findByTestId("agent-account-login-terminal")).toBeInTheDocument();
	// Only the signed-out saved account offers it; it cannot be made default while signed out.
	expect(screen.getAllByRole("button", { name: "Sign in again" })).toHaveLength(1);
	expect(screen.queryByRole("button", { name: "Make default" })).not.toBeInTheDocument();

	await user.click(screen.getByRole("button", { name: "Check sign-in" }));
	await waitFor(() => expect(screen.getByRole("alert")).toHaveTextContent("This account is b@example.com. Sign in again with that login."));
});

it("starts the sign-in a chat asked for, once", async () => {
	useUiStore.setState({ accountLogin: "acct-b" });
	const user = userEvent.setup();
	renderPanel();

	expect(await screen.findByTestId("agent-account-login-terminal")).toBeInTheDocument();
	expect(postMock.mock.calls.filter(([path]) => path === "/api/v1/agents/accounts/{accountId}/login")).toEqual([
		["/api/v1/agents/accounts/{accountId}/login", { params: { path: { accountId: "acct-b" } } }],
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
