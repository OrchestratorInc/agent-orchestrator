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
		expect(allowances[0]).toHaveTextContent("Codex · Session (5h): 75% left · Resets in 2h 0m");
		expect(allowances[1]).toHaveTextContent("Claude Code · Weekly: 20% left");
		fireEvent.click(screen.getByRole("button", { name: /Codex.*75% left/ }));
		expect(useUiStore.getState().settingsModal).toMatchObject({ scope: "global", section: "harness" });
	});

	it("says limits are unavailable when the provider has not reported a window", () => {
		accountsState.current = { data: [], isLoading: false, error: new Error("offline") };
		render(<SidebarSubscriptionAllowance />);
		expect(screen.getByTestId("sidebar-subscription-allowance")).toHaveTextContent("Subscription limits unavailable");
	});
});
