import { cleanup, fireEvent, render, screen } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { AgentAccount } from "../hooks/useAgentAccounts";
import { useUiStore } from "../stores/ui-store";
import { SidebarSubscriptionAllowance } from "./SidebarSubscriptionAllowance";

const accountsState = vi.hoisted(() => ({ current: { data: undefined as AgentAccount[] | undefined, isLoading: true, error: null as unknown } }));

vi.mock("../hooks/useAgentAccounts", async (importOriginal) => ({
	...(await importOriginal<typeof import("../hooks/useAgentAccounts")>()),
	agentAccountProviderNames: { claude: "Claude Code", codex: "Codex" },
	useAgentAccounts: () => accountsState.current,
}));

describe("SidebarSubscriptionAllowance", () => {
	afterEach(cleanup);
	beforeEach(() => {
		accountsState.current = { data: undefined, isLoading: true, error: null };
		useUiStore.setState({ settingsModal: null });
	});

	it("shows a loading state while account limits are being read", () => {
		render(<SidebarSubscriptionAllowance />);
		expect(screen.getByRole("status")).toHaveTextContent("Loading");
	});

	it("shows remaining allowance from the tightest provider-reported window and its reset", () => {
		accountsState.current = {
			isLoading: false,
			error: null,
			data: [{
				id: "codex-device", provider: "codex", default: true, device: true, signedIn: true, loggedIn: true,
				limits: { fetchedAt: new Date().toISOString(), windows: [
					{ id: "session", label: "Session (5h)", usedPercent: 25, resetsAt: new Date(Date.now() + 2 * 60 * 60_000).toISOString() },
					{ id: "weekly", label: "Weekly", usedPercent: 10 },
				] },
			}, {
				id: "claude-device", provider: "claude", default: true, device: true, signedIn: true, loggedIn: true,
				limits: { fetchedAt: new Date().toISOString(), windows: [{ id: "weekly", label: "Weekly", usedPercent: 80 }] },
			}] };

		render(<SidebarSubscriptionAllowance />);
		const allowances = screen.getAllByTestId("sidebar-subscription-allowance");
		expect(allowances[0]).toHaveTextContent("Codex · Session (5h): 75% left · Resets in 2 hr 0 min");
		expect(allowances[1]).toHaveTextContent("Claude Code · Weekly: 20% left");
		fireEvent.click(screen.getByRole("button", { name: /Codex.*75% left/ }));
		expect(useUiStore.getState().settingsModal).toMatchObject({ scope: "global", section: "harness" });
	});

	it("shows provider-specific unavailable states when limits have no windows", () => {
		accountsState.current = {
			data: [
				{ id: "codex-device", provider: "codex", default: true, device: true, signedIn: true, loggedIn: true, limits: { fetchedAt: new Date().toISOString(), windows: [] } },
				{ id: "claude-device", provider: "claude", default: true, device: true, signedIn: true, loggedIn: true, limits: { fetchedAt: new Date().toISOString(), windows: [] } },
			],
			isLoading: false,
			error: null,
		};
		render(<SidebarSubscriptionAllowance />);
		expect(screen.getAllByTestId("sidebar-subscription-allowance").map((item) => item.textContent)).toEqual([
			"Codex subscription limits unavailable",
			"Claude Code subscription limits unavailable",
		]);
	});

	it("does not use a signed-in non-default account when the default account is signed out", () => {
		accountsState.current = {
			data: [
				{ id: "codex-default", provider: "codex", default: true, device: true, signedIn: false, loggedIn: false },
				{ id: "codex-secondary", provider: "codex", default: false, device: true, signedIn: true, loggedIn: true, limits: { fetchedAt: new Date().toISOString(), windows: [{ id: "weekly", label: "Weekly", usedPercent: 10 }] } },
			],
			isLoading: false,
			error: null,
		};
		render(<SidebarSubscriptionAllowance />);
		expect(screen.getByTestId("sidebar-subscription-allowance")).toHaveTextContent("Sign in to Codex to see allowance");
	});

	it("explains when no supported subscription account is configured", () => {
		accountsState.current = { data: [], isLoading: false, error: null };
		render(<SidebarSubscriptionAllowance />);
		expect(screen.getByTestId("sidebar-subscription-allowance")).toHaveTextContent("Allowance is available for Codex and Claude Code accounts");
	});

	it("does not invent allowance for an unsupported provider", () => {
		accountsState.current = {
			data: [{ id: "other", provider: "opencode", default: true, device: false, signedIn: true, loggedIn: true } as unknown as AgentAccount],
			isLoading: false,
			error: null,
		};
		render(<SidebarSubscriptionAllowance />);
		expect(screen.getByTestId("sidebar-subscription-allowance")).toHaveTextContent("Allowance is available for Codex and Claude Code accounts");
		expect(screen.queryByText(/OpenCode.*% left/)).not.toBeInTheDocument();
	});

	it("says limits are unavailable when the provider has not reported a window", () => {
		accountsState.current = { data: [], isLoading: false, error: new Error("offline") };
		render(<SidebarSubscriptionAllowance />);
		expect(screen.getByTestId("sidebar-subscription-allowance")).toHaveTextContent("Subscription limits unavailable");
	});
});
