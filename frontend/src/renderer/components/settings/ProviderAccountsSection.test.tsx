import { cleanup, render, screen, waitFor, within } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import userEvent from "@testing-library/user-event";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { ProviderAccountsSection } from "./ProviderAccountsSection";
import type { ProviderAccount, ProviderAccounts } from "../../hooks/useProviderAccounts";

const mock = vi.hoisted(() => ({ get: vi.fn(), post: vi.fn(), put: vi.fn(), patch: vi.fn(), remove: vi.fn(), open: vi.fn(), clipboard: vi.fn() }));
vi.mock("../../lib/api-client", () => ({ apiClient: { GET: mock.get, POST: mock.post, PUT: mock.put, PATCH: mock.patch, DELETE: mock.remove }, apiErrorMessage: (error: { message: string }) => error.message }));
vi.mock("../../lib/bridge", () => ({ aoBridge: { app: { openExternal: mock.open }, clipboard: { writeText: mock.clipboard } } }));
let inventory: ProviderAccounts;
function renderAccounts() {
	const cache = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } });
	render(<QueryClientProvider client={cache}><ProviderAccountsSection /></QueryClientProvider>);
	return cache;
}
type User = ReturnType<typeof userEvent.setup>;
// The page is a list and a detail: every account on the left, and the selected
// one on the right with each of its actions as a row.
const list = () => screen.getByRole("navigation", { name: "Accounts" });
const listItem = (account: ProviderAccount) => screen.getByTestId(`provider-account-${account.id}`);
const detail = () => screen.getByTestId("provider-account-detail");
async function open(user: User, account: ProviderAccount) {
	await user.click(listItem(account));
	await waitFor(() => expect(within(detail()).getByRole("heading", { name: account.displayName ?? "" })).toBeInTheDocument());
}
// Signing out or removing asks once, in the same row.
async function askRemoval(user: User, action: "Sign out" | "Remove") {
	await user.click(within(detail()).getByRole("button", { name: action }));
	return screen.getByRole("group", { name: "Confirm account change" });
}
const confirmButton = (group: HTMLElement) => within(group).getByRole("button", { name: /^(Sign out|Remove)$/ });
const loaded = () => screen.findByRole("heading", { name: alice.displayName! });
// Makes the daemon double behave: a default change moves the default, and a
// session switch moves that one session.
function followAccountChanges() {
	mock.put.mockImplementation(async (path: string, options: { params: { path: { accountId?: string; sessionId?: string } }; body?: { accountId?: string } }) => {
		if (path === "/api/v1/provider-accounts/{accountId}/primary") {
			const next = options.params.path.accountId;
			const provider = inventory.accounts.find(account => account.id === next)?.provider;
			inventory = { ...inventory, accounts: inventory.accounts.map(account => account.provider === provider ? { ...account, primary: account.id === next } : account) };
			return { data: inventory };
		}
		const sessionId = options.params.path.sessionId!;
		const target = options.body?.accountId;
		inventory = { ...inventory, accounts: inventory.accounts.map(account => ({ ...account, sessions: account.id === target ? [...account.sessions, sessionId] : account.sessions.filter(id => id !== sessionId) })) };
		return { data: { managed: true, provider: "codex", accountId: target } };
	});
}
const alice: ProviderAccount = { id: "a", provider: "codex", displayName: "Cedar Codex", email: "alice@example.test", signedIn: true, primary: true, sessions: ["session-a"] };
const bob: ProviderAccount = { id: "b", provider: "codex", displayName: "Maple Codex", email: "bob@example.test", signedIn: true, primary: false, sessions: ["session-b", "session-c"] };
const clara: ProviderAccount = { id: "c", provider: "claude", displayName: "Willow Claude", email: "clara@example.test", signedIn: true, primary: true, sessions: [] };
const waitingLogin = (extra: Record<string, unknown> = {}) => ({ data: { id: "login-1", provider: "codex", url: "https://provider.test/login", status: "waiting", accountId: "", ...extra } });
beforeEach(() => {
	vi.clearAllMocks();
	inventory = { accounts: [structuredClone(alice), structuredClone(bob), structuredClone(clara)], defaults: [{ provider: "codex", primaryId: "a", managed: true }, { provider: "claude", primaryId: "c", managed: true }], recoveryRequired: false };
	mock.get.mockImplementation(async (path: string) => path === "/api/v1/provider-accounts" ? { data: inventory } : waitingLogin());
	mock.post.mockResolvedValue({ data: inventory });
	mock.put.mockResolvedValue({ data: inventory });
	mock.patch.mockResolvedValue({ data: inventory });
	mock.remove.mockResolvedValue({ data: inventory });
	mock.open.mockResolvedValue(undefined);
	mock.clipboard.mockResolvedValue(undefined);
});
afterEach(cleanup);

describe("accounts list and detail", () => {
	it("lists every account by provider and shows the first one in detail", async () => {
		renderAccounts();
		await loaded();
		// The list is for finding an account: its name, and whether it is the default.
		expect(within(screen.getByTestId("provider-section-codex")).getByRole("heading", { name: "Codex" })).toBeInTheDocument();
		expect(within(screen.getByTestId("provider-section-claude")).getByRole("heading", { name: "Claude" })).toBeInTheDocument();
		expect(listItem(alice)).toHaveTextContent("Cedar CodexDefault");
		expect(listItem(bob)).toHaveTextContent("Maple Codex");
		expect(listItem(bob)).not.toHaveTextContent("Default");
		expect(listItem(clara)).toHaveTextContent("Willow ClaudeDefault");
		expect(listItem(alice)).toHaveAttribute("aria-current", "true");
		expect(listItem(bob)).not.toHaveAttribute("aria-current");
		// Each provider has its own way to add an account.
		expect(within(list()).getAllByRole("button", { name: "Add account" })).toHaveLength(2);
		// The detail belongs to the selected account and holds its actions as rows.
		expect(detail()).toHaveTextContent("New Codex sessionsStart on this account.");
		expect(detail()).toHaveTextContent("1 session is using this account.");
		expect(within(detail()).getByRole("button", { name: "Sign out" })).toBeInTheDocument();
		// Emails stay blurred until pointed at or focused.
		expect(within(detail()).getByText(alice.email)).toHaveClass("blur-sm");
		expect(within(detail()).getByText(alice.email)).toHaveAttribute("tabindex", "0");
	});
	it("shows the account that is clicked", async () => {
		const user = userEvent.setup();
		renderAccounts();
		await loaded();
		await open(user, bob);
		expect(listItem(bob)).toHaveAttribute("aria-current", "true");
		expect(listItem(alice)).not.toHaveAttribute("aria-current");
		expect(detail()).toHaveTextContent("2 sessions are using this account.");
		expect(detail()).toHaveTextContent("New Codex sessionsStart on Cedar Codex.");
		await open(user, clara);
		expect(detail()).toHaveTextContent("New Claude Code sessionsStart on this account.");
		expect(detail()).toHaveTextContent("No sessions are using this account.");
	});
	it("renames an account from its display name field", async () => {
		const user = userEvent.setup();
		const renamed = { ...inventory, accounts: inventory.accounts.map(account => account.id === "a" ? { ...account, displayName: "Work Codex" } : account) };
		mock.patch.mockImplementation(async (path: string) => path === "/api/v1/provider-accounts/{accountId}" ? { data: renamed } : { data: inventory });
		renderAccounts();
		await loaded();
		const input = within(detail()).getByRole("textbox", { name: "Account name" });
		// Escape puts the saved name back and changes nothing.
		await user.clear(input);
		await user.type(input, "Scratch{Escape}");
		expect(mock.patch).not.toHaveBeenCalled();
		const field = within(detail()).getByRole("textbox", { name: "Account name" });
		expect(field).toHaveValue("Cedar Codex");
		await user.clear(field);
		await user.type(field, "Work Codex{Enter}");
		expect(mock.patch).toHaveBeenCalledWith("/api/v1/provider-accounts/{accountId}", { params: { path: { accountId: "a" } }, body: { displayName: "Work Codex" } });
		await screen.findByText("Account name updated.");
		expect(listItem(alice)).toHaveTextContent("Work Codex");
	});
	it("marks every account that is this computer's own sign-in, in the list and on the account", async () => {
		inventory.accounts[0] = { ...alice, global: true };
		inventory.accounts[2] = { ...clara, global: true, signedIn: false };
		renderAccounts();
		await loaded();
		// More than one account can carry the mark, and it stays when one is signed out.
		expect(listItem(alice)).toHaveTextContent("Cedar CodexDefault·Global");
		expect(listItem(bob)).not.toHaveTextContent("Global");
		expect(listItem(clara)).toHaveTextContent("Global·Signed out");
		expect(within(detail()).getByTestId("provider-account-global")).toHaveAttribute("title", "Found in this machine's own sign-in");
		expect(within(detail()).getByTestId("provider-account-global")).toHaveTextContent("Global");
	});
	it("marks an API key imported from this computer as its own key, not a sign-in", async () => {
		inventory.accounts[0] = { ...alice, kind: "api_key", global: true };
		renderAccounts();
		await loaded();
		expect(listItem(alice)).toHaveTextContent("Default·Global·API key");
		expect(within(detail()).getByTestId("provider-account-global")).toHaveAttribute("title", "This machine's own API key");
	});
	it("re-checks accounts when the window regains focus or on request", async () => {
		const user = userEvent.setup();
		renderAccounts();
		await loaded();
		const refreshes = () => mock.get.mock.calls.filter(([, options]) => (options as { params?: { query?: { refresh?: boolean } } } | undefined)?.params?.query?.refresh).length;
		expect(refreshes()).toBe(0);
		window.dispatchEvent(new Event("focus"));
		await waitFor(() => expect(refreshes()).toBe(1));
		expect(mock.get).toHaveBeenCalledWith("/api/v1/provider-accounts", { params: { query: { refresh: true } } });
		expect(screen.getByText("Checked just now")).toBeInTheDocument();
		await user.click(screen.getByRole("button", { name: "Check again" }));
		await waitFor(() => expect(refreshes()).toBeGreaterThan(1));
	});
	it("renders recovery instructions without hiding the accounts", async () => {
		inventory.recoveryRequired = true;
		renderAccounts();
		expect(await screen.findByRole("alert")).toHaveTextContent("An account change needs recovery");
		expect(listItem(alice)).toBeInTheDocument();
	});
	it("shows a catalogue error without pretending that all accounts were signed out", async () => {
		mock.get.mockResolvedValue({ error: { message: "Account catalogue unavailable" } });
		renderAccounts();
		expect(await screen.findByRole("alert", {}, { timeout: 3000 })).toHaveTextContent("Account catalogue unavailable");
		expect(screen.queryByText(/No signed-in Codex account/)).toBeNull();
		expect(screen.queryByText(/No signed-in Claude account/)).toBeNull();
		expect(screen.queryByTestId("provider-account-a")).toBeNull();
		expect(mock.remove).not.toHaveBeenCalled();
		expect(mock.put).not.toHaveBeenCalled();
	});
	it("distinguishes native device use from an adopted provider waiting for login", async () => {
		inventory.accounts = [];
		inventory.defaults[1].managed = false;
		renderAccounts();
		await screen.findByText(/No signed-in Codex account/);
		expect(screen.getByText("Managed sessions need you to sign in again.")).toBeInTheDocument();
		await screen.findByText(/No signed-in Claude account/);
		expect(screen.getByText("Existing device sessions continue using their device account.")).toBeInTheDocument();
		// With nothing to select, the detail says how to start.
		expect(screen.getByText("No accounts yet")).toBeInTheDocument();
	});
});

describe("account usage", () => {
	it("gives each usage window its own row, named by its length, with unused resets", async () => {
		inventory.accounts[0].usage = { status: "available", plan: "pro", resetCredits: 2, windows: [{ durationSeconds: 18000, remainingFraction: 0.75, resetTime: "2030-01-01T00:00:00Z" }, { durationSeconds: 604800, remainingFraction: 0 }] };
		inventory.accounts[1].usage = { status: "available", plan: "plus", resetCredits: 0, windows: [{ durationSeconds: 604800, remainingFraction: 0.14 }] };
		const user = userEvent.setup();
		renderAccounts();
		await loaded();
		expect(screen.getByTestId("provider-account-usage-a")).toHaveTextContent(/^5-hourResets Jan 1.*75% left$/);
		expect(screen.getByTestId("provider-account-usage-a")).not.toHaveTextContent("GMT");
		expect(screen.getByTestId("provider-account-usage-a-1")).toHaveTextContent("WeeklyReached");
		expect(screen.getAllByRole("progressbar", { name: /alice@example.test/ })).toHaveLength(2);
		expect(detail()).toHaveTextContent("Codex·Pro");
		// Resets have their own group; one can only be used when the provider would accept it.
		expect(detail()).toHaveTextContent("Limit resetsUse one when a limit is reached.2 availableUse reset");
		expect(within(detail()).getByRole("button", { name: "Use reset" })).toBeDisabled();
		// The list carries one figure per account: the room left in its shortest window.
		expect(listItem(alice)).toHaveTextContent("Default·75% left");
		expect(listItem(bob)).toHaveTextContent("14% left");
		// A provider that reports only the weekly window still labels it by length.
		await open(user, bob);
		expect(screen.getByTestId("provider-account-usage-b")).toHaveTextContent("Weekly14% left");
		expect(screen.queryByTestId("provider-account-usage-b-1")).toBeNull();
		expect(detail()).not.toHaveTextContent("Limit resets");
	});
	it("names a scoped limit by what it covers, with its length beside the reset", async () => {
		inventory.accounts[0].usage = { status: "available", plan: "pro", windows: [
			{ durationSeconds: 18000, remainingFraction: 0.62 },
			{ durationSeconds: 604800, remainingFraction: 0.81 },
			{ scope: "code_review", durationSeconds: 604800, remainingFraction: 0.96, resetTime: "2030-01-01T00:00:00Z" },
			{ scope: "model", name: "GPT-5.3-Codex-Spark", durationSeconds: 18000, remainingFraction: 1 },
		], credits: { balance: "48067.0888145000" } };
		inventory.accounts[2].usage = { status: "available", plan: "max", planTier: "20x", windows: [{ durationSeconds: 18000, remainingFraction: 0.5 }, { scope: "model", name: "Opus", durationSeconds: 604800, remainingFraction: 0.34 }], extraUsage: { usedCents: 1820, limitCents: 5000 } };
		const user = userEvent.setup();
		renderAccounts();
		await loaded();
		expect(screen.getByTestId("provider-account-usage-a-2")).toHaveTextContent(/^Code reviewWeekly · Resets Jan 1.*96% left$/);
		expect(screen.getByTestId("provider-account-usage-a-3")).toHaveTextContent("GPT-5.3-Codex-Spark5-hour100% left");
		// The provider counts credits, not money, and to many decimals; they are shown whole, with their unit.
		expect(detail()).toHaveTextContent("CreditsUsed when plan limits run out.48,067 credits");
		// The list still shows the first general limit, not a scoped one.
		expect(listItem(alice)).toHaveTextContent("Default·62% left");
		await open(user, clara);
		expect(detail()).toHaveTextContent("Claude·Max 20x");
		expect(screen.getByTestId("provider-account-usage-c-1")).toHaveTextContent("OpusWeekly34% left");
		expect(detail()).toHaveTextContent("Extra usage$18.20 of $50.00 this month$31.80 left");
	});
	it("says when usage is unavailable or not reported", async () => {
		inventory.accounts[0].usage = { status: "available", windows: [{ durationSeconds: 18000, remainingFraction: 0 }] };
		inventory.accounts[1].usage = { status: "unavailable", message: "Usage unavailable" };
		inventory.accounts[2] = { ...clara, kind: "api_key", usage: { status: "unavailable" } };
		const user = userEvent.setup();
		renderAccounts();
		await loaded();
		expect(listItem(alice)).toHaveTextContent("Limit reached");
		await open(user, bob);
		expect(screen.getByTestId("provider-account-usage-b")).toHaveTextContent("Usage unavailable");
		await open(user, clara);
		expect(screen.getByTestId("provider-account-usage-c")).toHaveTextContent("Not reported for API keys");
		expect(listItem(clara)).toHaveTextContent("API key");
		expect(detail()).toHaveTextContent("Key label");
	});
});

describe("changing the default", () => {
	it("changes the default at once, without a confirmation", async () => {
		followAccountChanges();
		inventory.accounts[0] = { ...alice, sessions: [] };
		const user = userEvent.setup();
		renderAccounts();
		await loaded();
		// The default account says so; there is nothing to press.
		expect(within(detail()).queryByRole("button", { name: "Make default" })).toBeNull();
		await open(user, bob);
		await user.click(within(detail()).getByRole("button", { name: "Make default" }));
		expect(mock.put).toHaveBeenCalledWith("/api/v1/provider-accounts/{accountId}/primary", { params: { path: { accountId: "b" } }, body: { moveExisting: false } });
		await waitFor(() => expect(listItem(bob)).toHaveTextContent("Default"));
		expect(listItem(alice)).not.toHaveTextContent("Default");
		expect(within(detail()).queryByRole("button", { name: "Make default" })).toBeNull();
		// The old default had no sessions, so nothing more is asked or said.
		expect(screen.queryByRole("group", { name: "Move sessions" })).toBeNull();
		expect(screen.queryByRole("status")).toBeNull();
		expect(mock.put).toHaveBeenCalledTimes(1);
	});
	it("keeps the accounts when the default change is refused", async () => {
		mock.put.mockResolvedValue({ error: { message: "Account is signed out" } });
		const user = userEvent.setup();
		renderAccounts();
		await loaded();
		await open(user, bob);
		await user.click(within(detail()).getByRole("button", { name: "Make default" }));
		expect(await screen.findByRole("status")).toHaveTextContent("Account is signed out");
		expect(listItem(alice)).toHaveTextContent("Default");
		expect(screen.queryByRole("group", { name: "Move sessions" })).toBeNull();
	});
	it("offers to move the old default's sessions and moves them one by one", async () => {
		followAccountChanges();
		inventory.accounts[0] = { ...alice, sessions: ["session-a", "session-d"] };
		const user = userEvent.setup();
		const cache = renderAccounts();
		const invalidate = vi.spyOn(cache, "invalidateQueries");
		await loaded();
		await open(user, bob);
		await user.click(within(detail()).getByRole("button", { name: "Make default" }));
		const offer = await screen.findByRole("group", { name: "Move sessions" });
		expect(offer).toHaveTextContent("2 sessions still use Cedar Codex");
		// Nothing has moved yet: only the default changed.
		expect(mock.put).toHaveBeenCalledTimes(1);
		await user.click(within(offer).getByRole("button", { name: "Move them here" }));
		expect(mock.put).toHaveBeenCalledWith("/api/v1/sessions/{sessionId}/provider-account", { params: { path: { sessionId: "session-a" } }, body: { accountId: "b" } });
		expect(mock.put).toHaveBeenCalledWith("/api/v1/sessions/{sessionId}/provider-account", { params: { path: { sessionId: "session-d" } }, body: { accountId: "b" } });
		expect(await screen.findByRole("status")).toHaveTextContent("2 sessions moved to Maple Codex.");
		expect(screen.queryByRole("group", { name: "Move sessions" })).toBeNull();
		expect(invalidate).toHaveBeenCalledWith({ queryKey: ["session-provider-account"] });
	});
	it("leaves the sessions where they are when the offer is dismissed", async () => {
		followAccountChanges();
		const user = userEvent.setup();
		renderAccounts();
		await loaded();
		await open(user, bob);
		await user.click(within(detail()).getByRole("button", { name: "Make default" }));
		const offer = await screen.findByRole("group", { name: "Move sessions" });
		expect(offer).toHaveTextContent("1 session still uses Cedar Codex");
		await user.click(within(offer).getByRole("button", { name: "Leave it there" }));
		expect(screen.queryByRole("group", { name: "Move sessions" })).toBeNull();
		expect(mock.put).toHaveBeenCalledTimes(1);
	});
	it("keeps the offer when a session cannot be moved", async () => {
		followAccountChanges();
		const user = userEvent.setup();
		renderAccounts();
		await loaded();
		await open(user, bob);
		await user.click(within(detail()).getByRole("button", { name: "Make default" }));
		const offer = await screen.findByRole("group", { name: "Move sessions" });
		mock.put.mockResolvedValue({ error: { message: "Account is signed out" } });
		await user.click(within(offer).getByRole("button", { name: "Move it here" }));
		expect(await screen.findByRole("status")).toHaveTextContent("Account is signed out");
		expect(screen.getByRole("group", { name: "Move sessions" })).toBeInTheDocument();
	});
	it("moves an account's sessions to another account at any time", async () => {
		followAccountChanges();
		const user = userEvent.setup();
		renderAccounts();
		await loaded();
		await user.click(within(detail()).getByRole("button", { name: "Move sessions" }));
		// Only other signed-in accounts of the same provider are offered.
		expect((await screen.findAllByRole("menuitem")).map(option => option.textContent)).toEqual([bob.displayName]);
		await user.click(screen.getByRole("menuitem", { name: bob.displayName! }));
		expect(mock.put).toHaveBeenCalledWith("/api/v1/sessions/{sessionId}/provider-account", { params: { path: { sessionId: "session-a" } }, body: { accountId: "b" } });
		expect(await screen.findByRole("status")).toHaveTextContent("1 session moved to Maple Codex.");
		// With no sessions left there is nothing to move.
		await waitFor(() => expect(within(detail()).queryByRole("button", { name: "Move sessions" })).toBeNull());
	});
});

describe("signed-out accounts", () => {
	it("shows an account whose sign-in is no longer accepted as signed out with one-click sign-in", async () => {
		inventory.accounts[1] = { ...bob, signInRequired: true, usage: { status: "available", plan: "pro", windows: [{ durationSeconds: 18000, remainingFraction: 0.5 }] } };
		mock.post.mockResolvedValue(waitingLogin({ accountId: "b" }));
		const user = userEvent.setup();
		renderAccounts();
		await loaded();
		expect(listItem(bob)).toHaveTextContent("Signed out");
		await open(user, bob);
		// The fix is the first thing on the page; usage and the default are not offered.
		expect(detail()).toHaveTextContent("This account is signed out2 sessions are waiting for it.");
		expect(screen.queryByTestId("provider-account-usage-b")).toBeNull();
		expect(within(detail()).queryByRole("button", { name: "Make default" })).toBeNull();
		expect(within(detail()).getByRole("button", { name: "Remove" })).toBeInTheDocument();
		expect(within(detail()).queryByRole("button", { name: "Sign out" })).toBeNull();
		await user.click(within(detail()).getByRole("button", { name: "Sign in again" }));
		await waitFor(() => expect(mock.post).toHaveBeenCalledWith("/api/v1/provider-accounts/login", { body: { provider: "codex", accountId: "b" } }));
		// The sign-in runs in place, on this account's page.
		expect(await within(detail()).findByText("Complete sign-in in your browser.")).toBeInTheDocument();
		expect(mock.open).not.toHaveBeenCalled();
		expect(within(detail()).getByRole("button", { name: "Open sign-in page" })).toBeEnabled();
	});
});

describe("account sign-out and removal", () => {
	it("offers the next signed-in account as the new default when removing the default", async () => {
		inventory.accounts[0] = { ...alice, signedIn: false };
		const user = userEvent.setup();
		renderAccounts();
		await loaded();
		const confirm = await askRemoval(user, "Remove");
		expect(confirm).toHaveTextContent("Remove Cedar Codex?Choose which account becomes the default.");
		expect(within(confirm).getByRole("button", { name: "Replacement default account" })).toHaveTextContent(bob.displayName!);
		expect(confirmButton(confirm)).toBeEnabled();
		await user.click(confirmButton(confirm));
		expect(mock.remove).toHaveBeenCalledWith("/api/v1/provider-accounts/{accountId}", { params: { path: { accountId: "a" } }, body: { replacementPrimaryId: "b" } });
		expect(await screen.findByRole("status")).toHaveTextContent("Removed Cedar Codex.");
	});
	it("signs out a secondary after saying where its sessions go", async () => {
		mock.post.mockResolvedValue({ data: { ...inventory, accounts: inventory.accounts.map(account => account.id === "b" ? { ...account, signedIn: false, sessions: [] } : account) } });
		const user = userEvent.setup();
		renderAccounts();
		await loaded();
		await open(user, bob);
		const confirm = await askRemoval(user, "Sign out");
		// One question and one fact; nothing about busy sessions or retrying.
		expect(confirm).toHaveTextContent("Sign out of Maple Codex?2 sessions move to Cedar Codex.");
		expect(confirm).not.toHaveTextContent(/busy|retry/i);
		expect(within(confirm).queryByRole("button", { name: "Replacement default account" })).toBeNull();
		await user.click(confirmButton(confirm));
		expect(mock.post).toHaveBeenCalledWith("/api/v1/provider-accounts/{accountId}/sign-out", { params: { path: { accountId: "b" } }, body: { replacementPrimaryId: undefined } });
		expect(mock.remove).not.toHaveBeenCalled();
		expect(await screen.findByRole("status")).toHaveTextContent("Signed out of Maple Codex.");
		// The account stays in the list, now as a signed-out one.
		expect(listItem(bob)).toHaveTextContent("Signed out");
		expect(within(detail()).getByRole("button", { name: "Sign in again" })).toBeInTheDocument();
	});
	it("asks only the question when the account has no sessions", async () => {
		inventory.accounts[1] = { ...bob, sessions: [] };
		const user = userEvent.setup();
		renderAccounts();
		await loaded();
		await open(user, bob);
		expect(await askRemoval(user, "Sign out")).toHaveTextContent(/^Sign out of Maple Codex\?CancelSign out$/);
	});
	it("says sessions wait for a sign-in when the last account goes", async () => {
		inventory.accounts = [{ ...structuredClone(clara), sessions: ["session-z"] }];
		const user = userEvent.setup();
		renderAccounts();
		await screen.findByRole("heading", { name: clara.displayName! });
		const confirm = await askRemoval(user, "Sign out");
		expect(confirm).toHaveTextContent("Sign out of Willow Claude?1 session waits until you sign in again.");
		expect(within(confirm).queryByRole("button", { name: "Replacement default account" })).toBeNull();
		expect(confirmButton(confirm)).toBeEnabled();
	});
	it("cancelling an account removal performs no destructive request", async () => {
		inventory.accounts[1] = { ...bob, signedIn: false };
		const user = userEvent.setup();
		renderAccounts();
		await loaded();
		await open(user, bob);
		const confirm = await askRemoval(user, "Remove");
		await user.click(within(confirm).getByRole("button", { name: "Cancel" }));
		expect(screen.queryByRole("group", { name: "Confirm account change" })).toBeNull();
		expect(mock.remove).not.toHaveBeenCalled();
	});
	it("keeps the confirmation and the account when removal is refused", async () => {
		mock.remove.mockResolvedValue({ error: { message: "Credential cleanup failed. Retry the operation." } });
		inventory.accounts[1] = { ...bob, signedIn: false };
		const user = userEvent.setup();
		renderAccounts();
		await loaded();
		await open(user, bob);
		await user.click(confirmButton(await askRemoval(user, "Remove")));
		expect(await screen.findByRole("status")).toHaveTextContent("Credential cleanup failed.");
		expect(screen.getByRole("group", { name: "Confirm account change" })).toBeInTheDocument();
		expect(listItem(bob)).toBeInTheDocument();
		expect(screen.queryByText(/^Removed /)).toBeNull();
		expect(mock.post).not.toHaveBeenCalled();
	});
	it("excludes signed-out and other-provider accounts from replacement choices", async () => {
		inventory.accounts.push({ id: "d", provider: "codex", displayName: "Aspen Codex", email: "signed-out@example.test", signedIn: false, primary: false, sessions: [] });
		const user = userEvent.setup();
		renderAccounts();
		await loaded();
		const confirm = await askRemoval(user, "Sign out");
		await user.click(within(confirm).getByRole("button", { name: "Replacement default account" }));
		// Only other signed-in accounts of the same provider can take over.
		expect((await screen.findAllByRole("menuitem")).map(option => option.textContent)).toEqual([bob.displayName]);
	});
	it("signs out a primary with the offered replacement, or another the user picks", async () => {
		inventory.accounts.push({ id: "e", provider: "codex", displayName: "Birch Codex", email: "erin@example.test", signedIn: true, primary: false, sessions: [], usage: { status: "available", windows: [{ durationSeconds: 18000, remainingFraction: 0.9 }] } });
		const user = userEvent.setup();
		renderAccounts();
		await loaded();
		const confirm = await askRemoval(user, "Sign out");
		// The next account in the list is offered, so the action is ready at once.
		expect(within(confirm).getByRole("button", { name: "Replacement default account" })).toHaveTextContent(bob.displayName!);
		expect(confirmButton(confirm)).toBeEnabled();
		// The chooser shows each account's room, like every other account menu.
		await user.click(within(confirm).getByRole("button", { name: "Replacement default account" }));
		const choice = await screen.findByRole("menuitem", { name: /Birch Codex/ });
		expect(choice).toHaveTextContent("90% left");
		await user.click(choice);
		expect(within(confirm).getByRole("button", { name: "Replacement default account" })).toHaveTextContent("Birch Codex");
		await user.click(confirmButton(confirm));
		expect(mock.post).toHaveBeenCalledWith("/api/v1/provider-accounts/{accountId}/sign-out", { params: { path: { accountId: "a" } }, body: { replacementPrimaryId: "e" } });
		expect(mock.remove).not.toHaveBeenCalled();
		expect(mock.put).not.toHaveBeenCalled();
		expect(await screen.findByRole("status")).toHaveTextContent("Signed out of Cedar Codex.");
	});
	it("prevents duplicate destructive requests while one is pending", async () => {
		let resolve!: (value: { data: ProviderAccounts }) => void;
		mock.remove.mockImplementation(() => new Promise<{ data: ProviderAccounts }>(done => { resolve = done; }));
		inventory.accounts[1] = { ...bob, signedIn: false };
		const user = userEvent.setup();
		renderAccounts();
		await loaded();
		await open(user, bob);
		const confirm = await askRemoval(user, "Remove");
		await user.click(confirmButton(confirm));
		expect(confirmButton(confirm)).toBeDisabled();
		await user.click(confirmButton(confirm));
		expect(mock.remove).toHaveBeenCalledTimes(1);
		// Adding an account is locked while the request is in flight.
		expect(screen.getAllByRole("button", { name: "Add account" }).every(button => (button as HTMLButtonElement).disabled)).toBe(true);
		resolve({ data: inventory });
		await waitFor(() => expect(screen.queryByRole("group", { name: "Confirm account change" })).toBeNull());
		expect(screen.getByRole("status")).toHaveTextContent("Removed Maple Codex.");
	});
});

describe("adding an account", () => {
	async function startAdding(user: User, index = 0) {
		await loaded();
		await user.click(screen.getAllByRole("button", { name: "Add account" })[index]);
	}
	it("takes over the detail with the sign-in methods, and keeps the list", async () => {
		const user = userEvent.setup();
		renderAccounts();
		await startAdding(user);
		const group = screen.getByRole("group", { name: "codex sign-in methods" });
		expect(within(group).getByRole("heading", { name: "Add a Codex account" })).toBeInTheDocument();
		expect(within(group).getByRole("button", { name: "Browser" })).toBeInTheDocument();
		expect(within(group).getByRole("button", { name: "Device code" })).toBeInTheDocument();
		expect(within(group).getByRole("button", { name: "API key" })).toBeInTheDocument();
		expect(within(group).getByLabelText("Import JSON")).toBeInTheDocument();
		// The list stays visible, with a place for the account being added.
		expect(listItem(alice)).not.toHaveAttribute("aria-current");
		expect(within(screen.getByTestId("provider-section-codex")).getByText("New Codex account")).toBeInTheDocument();
		expect(screen.queryByTestId("provider-account-detail")).toBeNull();
		await user.click(within(group).getByRole("button", { name: "Close" }));
		expect(screen.queryByRole("group", { name: "codex sign-in methods" })).toBeNull();
		expect(detail()).toBeInTheDocument();
	});
	it("does not offer device login for Claude", async () => {
		const user = userEvent.setup();
		renderAccounts();
		await startAdding(user, 1);
		const group = screen.getByRole("group", { name: "claude sign-in methods" });
		expect(within(group).queryByRole("button", { name: "Device code" })).toBeNull();
		expect(within(group).getByRole("button", { name: "Browser" })).toBeInTheDocument();
	});
	it("keeps the provider link in the panel until the user opens it", async () => {
		mock.post.mockResolvedValue(waitingLogin());
		const user = userEvent.setup();
		renderAccounts();
		await startAdding(user);
		await user.click(screen.getByRole("button", { name: "Browser" }));
		expect(mock.post).toHaveBeenCalledWith("/api/v1/provider-accounts/login", { body: { provider: "codex" } });
		const group = screen.getByRole("group", { name: "codex sign-in methods" });
		expect(await within(group).findByText("Complete sign-in in your browser.")).toBeInTheDocument();
		expect(screen.getAllByRole("button", { name: "Add account" }).every(b => (b as HTMLButtonElement).disabled)).toBe(true);
		expect(within(screen.getByTestId("provider-section-codex")).getByText("Signing in")).toBeInTheDocument();
		expect(mock.open).not.toHaveBeenCalled();
		await user.click(within(group).getByRole("button", { name: "Copy link" }));
		expect(mock.clipboard).toHaveBeenCalledWith("https://provider.test/login");
		await user.click(screen.getByRole("button", { name: "Open sign-in page" }));
		expect(mock.open).toHaveBeenCalledTimes(1);
		await user.click(screen.getByRole("button", { name: "Cancel sign-in" }));
		expect(mock.remove).toHaveBeenCalledWith("/api/v1/provider-accounts/login/{loginId}", { params: { path: { loginId: "login-1" } } });
		await waitFor(() => expect(screen.queryByText("Complete sign-in in your browser.")).toBeNull());
	});
	it("lets another account be viewed while a sign-in is waiting", async () => {
		mock.post.mockResolvedValue(waitingLogin());
		const user = userEvent.setup();
		renderAccounts();
		await startAdding(user);
		await user.click(screen.getByRole("button", { name: "Browser" }));
		await screen.findByText("Complete sign-in in your browser.");
		await open(user, bob);
		// The waiting sign-in keeps its place in the list and can be returned to.
		const placeholder = within(screen.getByTestId("provider-section-codex")).getByText("New Codex account");
		await user.click(placeholder);
		expect(await screen.findByText("Complete sign-in in your browser.")).toBeInTheDocument();
		expect(mock.post).toHaveBeenCalledTimes(1);
	});
	it("reports a callback-port conflict and keeps Add account available", async () => {
		mock.post.mockResolvedValue({ error: { message: "Login callback port is in use; retry later" } });
		const user = userEvent.setup();
		renderAccounts();
		await startAdding(user, 1);
		await user.click(screen.getByRole("button", { name: "Browser" }));
		expect(await screen.findByRole("status")).toHaveTextContent("Login callback port is in use");
		expect(mock.open).not.toHaveBeenCalled();
		expect(screen.getAllByRole("button", { name: "Add account" })[1]).toBeEnabled();
	});
	it("keeps the login link available if the external browser fails to open", async () => {
		mock.post.mockResolvedValue(waitingLogin());
		mock.open.mockRejectedValue(new Error("Browser could not be opened"));
		const user = userEvent.setup();
		renderAccounts();
		await startAdding(user);
		await user.click(screen.getByRole("button", { name: "Browser" }));
		await user.click(await screen.findByRole("button", { name: "Open sign-in page" }));
		expect(await screen.findByRole("status")).toHaveTextContent("Browser could not be opened");
		expect(screen.getByRole("button", { name: "Open sign-in page" })).toBeEnabled();
	});
	it("starts Codex device login and keeps its code visible", async () => {
		mock.post.mockResolvedValue({ data: { id: "device-1", provider: "codex", mode: "device", code: "ABCD-EFGH", url: "https://provider.test/device", status: "waiting", accountId: "" } });
		const user = userEvent.setup();
		renderAccounts();
		await startAdding(user);
		await user.click(screen.getByRole("button", { name: "Device code" }));
		expect(mock.post).toHaveBeenCalledWith("/api/v1/provider-accounts/login", { body: { provider: "codex", mode: "device" } });
		const group = screen.getByRole("group", { name: "codex sign-in methods" });
		expect(await within(group).findByText(/ABCD-EFGH/)).toBeInTheDocument();
		expect(group).toHaveTextContent("Enter this code on the Codex sign-in page.");
		await user.click(within(group).getByRole("button", { name: "Copy code" }));
		expect(mock.clipboard).toHaveBeenCalledWith("ABCD-EFGH");
		await user.click(within(group).getByRole("button", { name: "Copy link" }));
		expect(mock.clipboard).toHaveBeenCalledWith("https://provider.test/device");
		expect(mock.open).not.toHaveBeenCalled();
		await user.click(within(group).getByRole("button", { name: "Open sign-in page" }));
		expect(mock.open).toHaveBeenCalledWith("https://provider.test/device");
	});
	it("sends the API key and base URL through AO without rendering the key", async () => {
		mock.post.mockResolvedValue({ data: { id: "key-1", provider: "codex", mode: "api_key", status: "waiting", accountId: "" } });
		const user = userEvent.setup();
		renderAccounts();
		await startAdding(user);
		await user.click(screen.getByRole("button", { name: "API key" }));
		// Each box names itself and shows what a real value looks like.
		const key = screen.getByPlaceholderText("API key (sk-…)");
		await user.type(key, "secret-api-key");
		await user.type(screen.getByPlaceholderText("Base URL (https://api.openai.com/v1)"), "https://api.example.test");
		expect(screen.getByRole("textbox", { name: "Display label (optional)" })).toHaveValue("");
		// The key is hidden until the eye is pressed, and can be hidden again.
		expect(key).toHaveAttribute("type", "password");
		await user.click(screen.getByRole("button", { name: "Show API key" }));
		expect(key).toHaveAttribute("type", "text");
		await user.click(screen.getByRole("button", { name: "Hide API key" }));
		expect(key).toHaveAttribute("type", "password");
		await user.click(screen.getByRole("button", { name: "Add" }));
		expect(mock.post).toHaveBeenCalledWith("/api/v1/provider-accounts/login", { body: { provider: "codex", mode: "api_key", apiKey: "secret-api-key", baseUrl: "https://api.example.test" } });
		expect(screen.queryByText("secret-api-key")).toBeNull();
	});
	it("opens the API key form under its own row and closes it from the same row", async () => {
		const user = userEvent.setup();
		renderAccounts();
		await startAdding(user, 1);
		const group = screen.getByRole("group", { name: "claude sign-in methods" });
		const row = within(group).getByRole("button", { name: "API key" });
		expect(row).toHaveAttribute("aria-expanded", "false");
		await user.click(row);
		expect(row).toHaveAttribute("aria-expanded", "true");
		// The other methods stay where they are; the form sits under its row.
		expect(within(group).getByRole("button", { name: "Browser" })).toBeEnabled();
		expect(within(group).getByPlaceholderText("API key (sk-ant-…)")).toBeInTheDocument();
		expect(within(group).getByPlaceholderText("Base URL (https://api.anthropic.com)")).toBeInTheDocument();
		expect(within(group).getByRole("button", { name: "Add" })).toBeDisabled();
		await user.click(row);
		expect(row).toHaveAttribute("aria-expanded", "false");
		expect(within(group).queryByRole("button", { name: "Add" })).toBeNull();
		expect(mock.post).not.toHaveBeenCalled();
	});
	it("reads a JSON file and sends its contents to the selected provider", async () => {
		mock.post.mockResolvedValue({ data: { id: "import-1", provider: "codex", mode: "import", status: "waiting", accountId: "" } });
		const user = userEvent.setup();
		renderAccounts();
		await startAdding(user);
		const file = new File([JSON.stringify({ type: "codex", email: "imported@example.test", access_token: "secret" })], "codex.json", { type: "application/json" });
		await user.upload(within(screen.getByRole("group", { name: "codex sign-in methods" })).getByLabelText("Import JSON"), file);
		expect(mock.post).toHaveBeenCalledWith("/api/v1/provider-accounts/login", { body: { provider: "codex", mode: "import", credentialJson: JSON.stringify({ type: "codex", email: "imported@example.test", access_token: "secret" }) } });
	});
});

describe("pending login continuity", () => {
	async function startBrowserLogin(user: User, index = 0) {
		await loaded();
		await user.click(screen.getAllByRole("button", { name: "Add account" })[index]);
		await user.click(screen.getByRole("button", { name: "Browser" }));
		await screen.findByText("Complete sign-in in your browser.");
	}
	it("resumes the same browser login after leaving and returning to settings", async () => {
		const cache = new QueryClient({ defaultOptions: { queries: { retry: false, gcTime: Infinity } } });
		const user = userEvent.setup();
		mock.post.mockResolvedValue(waitingLogin());
		const first = render(<QueryClientProvider client={cache}><ProviderAccountsSection /></QueryClientProvider>);
		await startBrowserLogin(user);
		expect(mock.open).not.toHaveBeenCalled();
		expect(mock.post).toHaveBeenCalledTimes(1);
		first.unmount();
		render(<QueryClientProvider client={cache}><ProviderAccountsSection /></QueryClientProvider>);
		expect(screen.getByText("Complete sign-in in your browser.")).toBeInTheDocument();
		expect(screen.getByRole("button", { name: "Open sign-in page" })).toBeEnabled();
		expect(screen.getAllByRole("button", { name: "Add account" }).every(button => (button as HTMLButtonElement).disabled)).toBe(true);
		await user.click(screen.getByRole("button", { name: "Open sign-in page" }));
		expect(mock.open).toHaveBeenCalledTimes(1);
		expect(mock.post).toHaveBeenCalledTimes(1);
		await user.click(screen.getByRole("button", { name: "Cancel sign-in" }));
		await waitFor(() => expect(screen.queryByText("Complete sign-in in your browser.")).toBeNull());
		expect(cache.getQueryData(["provider-account-login"])).toBeNull();
	});
	it("records successful polling, shows the accounts again and clears waiting controls", async () => {
		const cache = renderAccounts();
		const user = userEvent.setup();
		mock.post.mockResolvedValue(waitingLogin({ provider: "claude" }));
		mock.get.mockImplementation(async (path: string) => path === "/api/v1/provider-accounts"
			? { data: inventory }
			: { data: { id: "login-1", provider: "claude", url: "https://provider.test/login", status: "complete", accountId: "c" } });
		await startBrowserLogin(user, 1);
		await waitFor(() => expect(screen.getByRole("status")).toHaveTextContent("Account signed in."), { timeout: 3000 });
		expect(screen.queryByRole("button", { name: "Open sign-in page" })).toBeNull();
		expect(screen.getAllByRole("button", { name: "Add account" }).every(button => !(button as HTMLButtonElement).disabled)).toBe(true);
		// The account that was signed in is the one on show.
		expect(within(detail()).getByRole("heading", { name: clara.displayName! })).toBeInTheDocument();
		expect(cache.getQueryData(["provider-account-login"])).toMatchObject({ status: "complete", accountId: "c" });
		expect(mock.get).toHaveBeenCalledWith("/api/v1/provider-accounts/login/{loginId}", { params: { path: { loginId: "login-1" } } });
		expect(mock.remove).not.toHaveBeenCalled();
	});
	it("does not announce a successful login after the provider reports failure", async () => {
		const user = userEvent.setup();
		mock.post.mockResolvedValue(waitingLogin());
		mock.get.mockImplementation(async (path: string) => path === "/api/v1/provider-accounts"
			? { data: inventory }
			: { data: { id: "login-1", provider: "codex", url: "https://provider.test/login", status: "failed", accountId: "" } });
		renderAccounts();
		await startBrowserLogin(user);
		await waitFor(() => expect(screen.getByRole("status")).toHaveTextContent("Sign-in failed. Please sign in again."), { timeout: 3000 });
		expect(screen.queryByText(/Account signed in/)).toBeNull();
		expect(screen.queryByRole("button", { name: "Open sign-in page" })).toBeNull();
		expect(listItem(alice)).toBeInTheDocument();
		expect(mock.put).not.toHaveBeenCalled();
		expect(mock.remove).not.toHaveBeenCalled();
	});
	it("preserves a pending attempt if cancellation is refused", async () => {
		const user = userEvent.setup();
		mock.post.mockResolvedValue(waitingLogin());
		mock.remove.mockResolvedValue({ error: { message: "Unable to cancel login. Try again." } });
		const cache = renderAccounts();
		await startBrowserLogin(user);
		await user.click(screen.getByRole("button", { name: "Cancel sign-in" }));
		expect(await screen.findByRole("status")).toHaveTextContent("Unable to cancel login. Try again.");
		expect(screen.getByText("Complete sign-in in your browser.")).toBeInTheDocument();
		expect(cache.getQueryData(["provider-account-login"])).toMatchObject({ id: "login-1", status: "waiting" });
		expect(screen.getByRole("button", { name: "Open sign-in page" })).toBeEnabled();
		expect(mock.post).toHaveBeenCalledTimes(1);
	});
	it("retains the current attempt and its controls after a poll transport failure", async () => {
		const user = userEvent.setup();
		mock.post.mockResolvedValue(waitingLogin());
		mock.get.mockImplementation(async (path: string) => path === "/api/v1/provider-accounts"
			? { data: inventory }
			: { error: { message: "AO is reconnecting" } });
		const cache = renderAccounts();
		await startBrowserLogin(user);
		await waitFor(() => expect(screen.getByRole("status")).toHaveTextContent("AO is reconnecting"), { timeout: 3000 });
		expect(cache.getQueryData(["provider-account-login"])).toMatchObject({ status: "waiting" });
		expect(screen.getByRole("button", { name: "Cancel sign-in" })).toBeEnabled();
		expect(mock.post).toHaveBeenCalledTimes(1);
		expect(mock.remove).not.toHaveBeenCalled();
	});
	it("releases an unknown attempt and permits a fresh sign-in without manual cancellation", async () => {
		const cache = new QueryClient({ defaultOptions: { queries: { retry: false } } });
		cache.setQueryData(["provider-account-login"], { id: "old-attempt", provider: "codex", url: "https://provider.test/old", status: "waiting", accountId: "" });
		mock.get.mockImplementation(async (path: string) => path === "/api/v1/provider-accounts"
			? { data: inventory }
			: { error: { code: "PROVIDER_LOGIN_NOT_FOUND", message: "Login attempt not found" } });
		render(<QueryClientProvider client={cache}><ProviderAccountsSection /></QueryClientProvider>);
		await waitFor(() => expect(screen.getByRole("status")).toHaveTextContent("Sign-in failed. Please sign in again."), { timeout: 3000 });
		expect(screen.queryByRole("button", { name: "Open sign-in page" })).toBeNull();
		expect(cache.getQueryData(["provider-account-login"])).toMatchObject({ id: "old-attempt", status: "failed" });
		expect(screen.getAllByRole("button", { name: "Add account" }).every(button => !(button as HTMLButtonElement).disabled)).toBe(true);
		expect(mock.remove).not.toHaveBeenCalled();
		expect(mock.open).not.toHaveBeenCalled();
		mock.post.mockResolvedValue(waitingLogin({ id: "new-attempt", url: "https://provider.test/new" }));
		const user = userEvent.setup();
		await user.click(screen.getAllByRole("button", { name: "Add account" })[0]);
		await user.click(screen.getByRole("button", { name: "Browser" }));
		expect(mock.open).not.toHaveBeenCalled();
		await waitFor(() => expect(cache.getQueryData(["provider-account-login"])).toMatchObject({ id: "new-attempt", status: "waiting" }));
		expect(listItem(alice)).toBeInTheDocument();
		expect(mock.put).not.toHaveBeenCalled();
	});
});

describe("account resets", () => {
	const limited = () => ({ status: "available" as const, plan: "pro", resetCredits: 2, resetUsable: true, resets: [{ left: 1, total: 1, expiresAt: "2030-01-21T00:00:00Z" }, { label: "Launch bonus", left: 2, total: 3, expiresAt: "2030-02-04T00:00:00Z" }], windows: [{ durationSeconds: 18000, remainingFraction: 0 }] });
	const resetCalls = () => mock.post.mock.calls.filter(([path]) => path === "/api/v1/provider-accounts/{accountId}/reset");
	it("lists each reset with its expiry and asks once before spending one", async () => {
		inventory.accounts[0].usage = limited();
		mock.post.mockImplementation(async (path: string) => path.endsWith("/reset") ? { data: { outcome: "reset" } } : { data: inventory });
		const user = userEvent.setup();
		renderAccounts();
		await loaded();
		expect(detail()).toHaveTextContent("Limit resetsClears the usage limits.2 availableUse reset");
		expect(detail()).toHaveTextContent("Reset 1Expires Jan 21, 2030");
		expect(detail()).toHaveTextContent("Launch bonus · 2 of 3 leftExpires Feb 4, 2030");
		await user.click(within(detail()).getByRole("button", { name: "Use reset" }));
		const ask = screen.getByRole("group", { name: "Use reset" });
		expect(ask).toHaveTextContent("Use a reset on Cedar Codex?Clears the usage limits. This can't be undone.");
		expect(resetCalls()).toHaveLength(0);
		await user.click(within(ask).getByRole("button", { name: "Use reset" }));
		expect(await screen.findByText("Limits reset on Cedar Codex.")).toBeInTheDocument();
		expect(resetCalls()).toEqual([["/api/v1/provider-accounts/{accountId}/reset", { params: { path: { accountId: "a" } } }]]);
		// Usage is read again straight away, so the bars show the new room.
		expect(mock.get).toHaveBeenCalledWith("/api/v1/provider-accounts", { params: { query: { refresh: true } } });
		expect(screen.queryByRole("group", { name: "Use reset" })).toBeNull();
	});
	it("can be cancelled without spending anything", async () => {
		inventory.accounts[0].usage = limited();
		const user = userEvent.setup();
		renderAccounts();
		await loaded();
		await user.click(within(detail()).getByRole("button", { name: "Use reset" }));
		await user.click(within(screen.getByRole("group", { name: "Use reset" })).getByRole("button", { name: "Cancel" }));
		expect(screen.queryByRole("group", { name: "Use reset" })).toBeNull();
		expect(resetCalls()).toHaveLength(0);
	});
	it("does not try again when the provider never confirms", async () => {
		inventory.accounts[0].usage = limited();
		mock.post.mockImplementation(async (path: string) => path.endsWith("/reset") ? { data: { outcome: "unknown" } } : { data: inventory });
		const user = userEvent.setup();
		renderAccounts();
		await loaded();
		await user.click(within(detail()).getByRole("button", { name: "Use reset" }));
		await user.click(within(screen.getByRole("group", { name: "Use reset" })).getByRole("button", { name: "Use reset" }));
		expect(await screen.findByText("The provider didn't confirm the reset. Check usage before trying again.")).toBeInTheDocument();
		expect(resetCalls()).toHaveLength(1);
	});
	it("says when the next reset is allowed and shows no resets as none", async () => {
		inventory.accounts[0].usage = { ...limited(), resetUsable: false, resetBlockedUntil: "2099-01-01T00:00:00Z" };
		inventory.accounts[1].usage = { status: "available", resetCredits: 0, windows: [{ durationSeconds: 18000, remainingFraction: 0.5 }] };
		const user = userEvent.setup();
		renderAccounts();
		await loaded();
		expect(detail()).toHaveTextContent(/Limit resetsNext reset after Jan 1.*2 availableUse reset/);
		expect(within(detail()).getByRole("button", { name: "Use reset" })).toBeDisabled();
		await open(user, bob);
		expect(detail()).toHaveTextContent("No resets available");
		expect(within(detail()).queryByRole("button", { name: "Use reset" })).toBeNull();
	});
});

describe("a paused account", () => {
	it("says so in the list and in its limits, and can be resumed", async () => {
		inventory.accounts[0].usage = { status: "available", pausedUntil: "2099-01-01T00:00:00Z", pausedReason: "quota", windows: [{ durationSeconds: 18000, remainingFraction: 0.14 }] };
		const user = userEvent.setup();
		renderAccounts();
		await loaded();
		expect(listItem(alice)).toHaveTextContent("Default·Paused·14% left");
		expect(detail()).toHaveTextContent(/Paused until Jan 1.*The provider rate-limited this account\.Resume now/);
		await user.click(within(detail()).getByRole("button", { name: "Resume now" }));
		expect(await screen.findByText("Cedar Codex resumed.")).toBeInTheDocument();
		expect(mock.post).toHaveBeenCalledWith("/api/v1/provider-accounts/{accountId}/resume", { params: { path: { accountId: "a" } } });
	});
	it("shows nothing once the pause is over", async () => {
		inventory.accounts[0].usage = { status: "available", pausedUntil: "2001-01-01T00:00:00Z", windows: [{ durationSeconds: 18000, remainingFraction: 0.14 }] };
		renderAccounts();
		await loaded();
		expect(listItem(alice)).not.toHaveTextContent("Paused");
		expect(within(detail()).queryByRole("button", { name: "Resume now" })).toBeNull();
	});
});

describe("an account whose sign-in has stopped renewing", () => {
	it("says so in the list and above its limits while the account still works", async () => {
		inventory.accounts[0].usage = { status: "available", signInEnding: true, signInEndsAt: "2099-01-01T00:00:00Z", windows: [{ durationSeconds: 18000, remainingFraction: 0.14 }] };
		renderAccounts();
		await loaded();
		expect(listItem(alice)).toHaveTextContent("Default·Sign-in ending·14% left");
		const warning = screen.getByTestId("provider-account-sign-in-ending-a");
		expect(warning).toHaveTextContent(/Sign-in is not renewingWorks until Jan 1.*Sign in again to keep using it\.Sign in again/);
		// The account is still signed in: its limits stay on the page.
		expect(detail()).toHaveTextContent("14% left");
		expect(within(warning).getByRole("button", { name: "Sign in again" })).toBeEnabled();
	});
	it("signs in again in place, for that account", async () => {
		inventory.accounts[0].usage = { status: "available", signInEnding: true, windows: [{ durationSeconds: 18000, remainingFraction: 0.14 }] };
		mock.post.mockResolvedValue(waitingLogin({ accountId: "a" }));
		const user = userEvent.setup();
		renderAccounts();
		await loaded();
		await user.click(within(screen.getByTestId("provider-account-sign-in-ending-a")).getByRole("button", { name: "Sign in again" }));
		await waitFor(() => expect(mock.post).toHaveBeenCalledWith("/api/v1/provider-accounts/login", { body: { provider: "codex", accountId: "a" } }));
		expect(await within(screen.getByTestId("provider-account-sign-in-ending-a")).findByText("Complete sign-in in your browser.")).toBeInTheDocument();
	});
	it("gives no date when the helper does not know one", async () => {
		inventory.accounts[0].usage = { status: "available", signInEnding: true, windows: [] };
		renderAccounts();
		await loaded();
		expect(screen.getByTestId("provider-account-sign-in-ending-a")).toHaveTextContent("It will stop working. Sign in again to keep using it.");
	});
	it("shows nothing for a healthy account", async () => {
		renderAccounts();
		await loaded();
		expect(screen.queryByTestId("provider-account-sign-in-ending-a")).toBeNull();
		expect(listItem(alice)).not.toHaveTextContent("Sign-in ending");
	});
});

describe("plan and activity", () => {
	const quiet = Array.from({ length: 18 }, () => ({ succeeded: 0, failed: 0 }));
	it("puts the plan facts and recent requests beside the limits", async () => {
		inventory.accounts[2].usage = { status: "available", plan: "team", organization: "Acme", renewsAt: "2030-11-14T00:00:00Z", addedAt: "2030-09-03T00:00:00Z", refreshedAt: new Date(Date.now() - 12 * 60_000).toISOString(), requests: [...quiet, { succeeded: 9, failed: 0 }, { succeeded: 4, failed: 3 }], windows: [{ durationSeconds: 18000, remainingFraction: 0.5 }] };
		const user = userEvent.setup();
		renderAccounts();
		await loaded();
		await open(user, clara);
		const aside = within(detail()).getByRole("complementary");
		expect(aside).toHaveTextContent("PlanTeam");
		expect(aside).toHaveTextContent("RenewsNov 14, 2030");
		expect(aside).toHaveTextContent("OrganizationAcme");
		expect(aside).toHaveTextContent("AddedSep 3, 2030");
		expect(aside).toHaveTextContent("Sign-in refreshed12 minutes ago");
		expect(aside).toHaveTextContent("RequestsLast 3 hours163 failed");
		await user.click(within(aside).getByRole("button", { name: "Refresh sign-in" }));
		expect(await screen.findByText("Sign-in refreshed.")).toBeInTheDocument();
		expect(mock.post).toHaveBeenCalledWith("/api/v1/provider-accounts/{accountId}/refresh-sign-in", { params: { path: { accountId: "c" } } });
	});
	it("shows an API key's activity even though it has no limits", async () => {
		inventory.accounts[2] = { ...clara, kind: "api_key", usage: { status: "unavailable", addedAt: "2030-09-28T00:00:00Z", requests: [...quiet, { succeeded: 2, failed: 0 }, { succeeded: 1, failed: 0 }] } };
		const user = userEvent.setup();
		renderAccounts();
		await loaded();
		await open(user, clara);
		expect(screen.getByTestId("provider-account-usage-c")).toHaveTextContent("Not reported for API keys");
		const aside = within(detail()).getByRole("complementary");
		expect(aside).toHaveTextContent("AddedSep 28, 2030");
		expect(aside).toHaveTextContent("RequestsLast 3 hours3None failed");
		expect(within(aside).queryByRole("button", { name: "Refresh sign-in" })).toBeNull();
	});
	it("counts requests even before any, and adds the provider's token tally", async () => {
		const today = new Date().toISOString().slice(0, 10);
		inventory.accounts[0].usage = { status: "available", plan: "pro", windows: [{ durationSeconds: 18000, remainingFraction: 0.5 }], requests: Array.from({ length: 20 }, () => ({ succeeded: 0, failed: 0 })), tokens: { latestDay: today, latestDayTokens: 1_240_000, lifetime: 482_000_000, peakDaily: 9_400_000, longestTurnSeconds: 4_320, currentStreakDays: 6, longestStreakDays: 23 } };
		inventory.accounts[1].usage = { status: "available", windows: [{ durationSeconds: 18000, remainingFraction: 0.5 }], tokens: { latestDay: "2030-01-05", latestDayTokens: 3_800, lifetime: 96_000_000 } };
		const user = userEvent.setup();
		renderAccounts();
		await loaded();
		const aside = within(detail()).getByRole("complementary");
		// A quiet account says so in figures; there is no graph.
		expect(aside).toHaveTextContent("RequestsLast 3 hours0None failed");
		expect(within(aside).queryByRole("img")).toBeNull();
		expect(aside).toHaveTextContent("Tokens today1.2M");
		expect(aside).toHaveTextContent("Lifetime tokens482M");
		expect(aside).toHaveTextContent("Peak day9.4M");
		expect(aside).toHaveTextContent("Longest turn1 hr 12 min");
		expect(aside).toHaveTextContent("Current streak6 days");
		expect(aside).toHaveTextContent("Longest streak23 days");
		// The refresh stays within reach when the helper cannot say when the last one was.
		expect(aside).toHaveTextContent("Sign-in refreshedUnknown");
		expect(within(aside).getByRole("button", { name: "Refresh sign-in" })).toBeEnabled();
		// A tally whose latest day is not today names the day; missing figures are left out.
		await open(user, bob);
		const other = within(detail()).getByRole("complementary");
		expect(other).toHaveTextContent("Tokens on Jan 5, 20303.8K");
		expect(other).toHaveTextContent("Lifetime tokens96M");
		expect(other).not.toHaveTextContent("Peak day");
		expect(other).not.toHaveTextContent("Requests");
		// With nothing from the helper there is no sign-in row to act on.
		expect(within(other).queryByRole("button", { name: "Refresh sign-in" })).toBeNull();
	});
	it("keeps one column when there is nothing to put beside the limits", async () => {
		inventory.accounts[0].usage = { status: "available", windows: [{ durationSeconds: 18000, remainingFraction: 0.5 }] };
		renderAccounts();
		await loaded();
		expect(within(detail()).queryByRole("complementary")).toBeNull();
	});
});
