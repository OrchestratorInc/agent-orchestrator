import { fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { appI18n } from "../../i18n";
import type { CloudCpBillingSummary } from "../../lib/cloud-cp/types";
import { CloudCpError } from "../../lib/cloud-cp/errors";
import { BillingSection, formatHours, formatRelative } from "./BillingSection";

const mocks = vi.hoisted(() => ({
	client: {
		getBilling: vi.fn(),
		createBillingCheckout: vi.fn(),
		createBillingPortal: vi.fn(),
		resetBillingUsage: vi.fn(),
	},
	openExternal: vi.fn(),
}));

vi.mock("../../hooks/useCloudGate", () => ({ useCloudGate: () => ({ cloudEnabled: true }) }));
vi.mock("../../lib/cloud-session", () => ({ useCloudSession: () => ({ status: "authenticated" }) }));
vi.mock("../../hooks/useCloudCp", () => ({
	useCloudCp: () => ({ client: mocks.client, ready: true, baseUrl: "https://cp.test" }),
}));
vi.mock("../../hooks/useCloudOrg", () => ({ useCloudOrg: () => ({ org: { id: "org-1" } }) }));
vi.mock("../../lib/bridge", () => ({ aoBridge: { app: { openExternal: mocks.openExternal } } }));

const starter = {
	plan: "starter", maxActiveSandboxes: 8, maxRunningSandboxes: 2, orchestratorSlots: 1, windowHours: 6,
	windowSessionHours: 8, weeklySessionHours: 20, manualResetsPerMonth: 1,
};
const pro = { ...starter, plan: "pro", maxActiveSandboxes: 16, maxRunningSandboxes: 4, windowSessionHours: 16, weeklySessionHours: 40 };
const plans = [
	{ name: "starter", id: "price_1", unitAmount: 2000, currency: "usd", interval: "month", limits: starter },
	{ name: "pro", id: "price_2", unitAmount: 4000, currency: "usd", interval: "month", limits: pro },
];

function subscribed(overrides: Partial<CloudCpBillingSummary> = {}): CloudCpBillingSummary {
	return {
		enabled: true, exempt: false, plan: "starter", subscriptionStatus: "active", entitled: true,
		currentPeriodEnd: "2026-11-10T09:00:00Z", limits: starter, plans,
		usage: {
			windowUsedMinutes: 252, windowLimitMinutes: 480, windowHours: 6,
			weeklyUsedMinutes: 780, weeklyLimitMinutes: 1200, weeklyResetsAt: "2026-10-12T09:00:00Z",
			manualResetsAllowed: 1, manualResetsUsed: 0, manualResetsRenewAt: "2026-11-10T09:00:00Z",
			activeOrchestrators: 1, activeWorkers: 2, runningOrchestrators: 1, runningWorkers: 1,
		},
		...overrides,
	};
}

function renderSection() {
	render(
		<QueryClientProvider client={new QueryClient({ defaultOptions: { queries: { retry: false } } })}>
			<BillingSection />
		</QueryClientProvider>,
	);
}

function card(plan: string): HTMLElement {
	const found = screen.getAllByTestId("plan-card").find((element) => element.dataset.plan === plan);
	if (!found) throw new Error(`no ${plan} card`);
	return found;
}

describe("BillingSection", () => {
	beforeEach(async () => {
		await appI18n.changeLanguage("en");
		vi.clearAllMocks();
	});

	it("shows Free, Starter and Pro with Starter recommended and Free current, and opens Checkout", async () => {
		mocks.client.getBilling.mockResolvedValue({ enabled: true, exempt: false, plan: "free", entitled: false, plans });
		mocks.client.createBillingCheckout.mockResolvedValue({ url: "https://checkout.stripe.test/cs_1" });
		renderSection();
		await screen.findByText("Starter");
		expect(screen.getAllByTestId("plan-card")).toHaveLength(3);
		expect(within(card("free")).getByText("Current")).toBeInTheDocument();
		expect(within(card("starter")).getByText("Recommended")).toBeInTheDocument();
		expect(within(card("starter")).queryByText("Current")).not.toBeInTheDocument();
		expect(within(card("starter")).getByText("$20 / month")).toBeInTheDocument();
		expect(within(card("starter")).getByText("2 sessions at once")).toBeInTheDocument();
		expect(within(card("pro")).getByText("16 sessions in total")).toBeInTheDocument();
		expect(within(card("pro")).getByText("16 h every 6 h")).toBeInTheDocument();
		expect(screen.queryByText("Usage")).not.toBeInTheDocument();
		fireEvent.click(within(card("starter")).getByRole("button", { name: "Upgrade" }));
		await waitFor(() => expect(mocks.openExternal).toHaveBeenCalledWith("https://checkout.stripe.test/cs_1"));
		expect(mocks.client.createBillingCheckout).toHaveBeenCalledWith("org-1", "starter");
	});

	it("marks the subscribed plan current and shows usage with session counts", async () => {
		mocks.client.getBilling.mockResolvedValue(subscribed());
		renderSection();
		await screen.findByText("Usage");
		expect(within(card("starter")).getByText("Current")).toBeInTheDocument();
		expect(within(card("free")).queryByText("Current")).not.toBeInTheDocument();
		expect(within(card("starter")).getByRole("button", { name: "Manage" })).toBeInTheDocument();
		expect(within(card("pro")).getByRole("button", { name: "Switch" })).toBeInTheDocument();
		expect(screen.getByText("6-hour usage")).toBeInTheDocument();
		expect(screen.getByText("4.2 of 8 h")).toBeInTheDocument();
		expect(screen.getByText(/^13 of 20 h · resets /)).toBeInTheDocument();
		expect(screen.getByText("2/2")).toBeInTheDocument();
		expect(screen.getByText("3/8")).toBeInTheDocument();
		expect(screen.getByText("Orchestrator 1/1 · Workers 1/1")).toBeInTheDocument();
	});

	it("switching plans goes through the Customer Portal", async () => {
		mocks.client.getBilling.mockResolvedValue(subscribed());
		mocks.client.createBillingPortal.mockResolvedValue({ url: "https://billing.stripe.test/p_1" });
		renderSection();
		await screen.findByText("Usage");
		fireEvent.click(within(card("pro")).getByRole("button", { name: "Switch" }));
		await waitFor(() => expect(mocks.openExternal).toHaveBeenCalledWith("https://billing.stripe.test/p_1"));
		expect(mocks.client.createBillingCheckout).not.toHaveBeenCalled();
	});

	it("confirms before spending a manual reset", async () => {
		mocks.client.getBilling.mockResolvedValue(subscribed());
		const after = subscribed();
		after.usage = { ...after.usage!, weeklyUsedMinutes: 0, manualResetsUsed: 1 };
		mocks.client.resetBillingUsage.mockResolvedValue(after);
		renderSection();
		fireEvent.click(await screen.findByRole("button", { name: "Reset now" }));
		expect(mocks.client.resetBillingUsage).not.toHaveBeenCalled();
		fireEvent.click(screen.getByRole("button", { name: "Reset" }));
		await waitFor(() => expect(mocks.client.resetBillingUsage).toHaveBeenCalledWith("org-1"));
		expect(await screen.findByText(/^0 of 20 h · resets /)).toBeInTheDocument();
	});

	it("explains when only admins may manage billing", async () => {
		mocks.client.getBilling.mockResolvedValue(subscribed());
		mocks.client.createBillingPortal.mockRejectedValue(new CloudCpError("forbidden", { status: 403, code: "forbidden" }));
		renderSection();
		await screen.findByText("Usage");
		fireEvent.click(within(card("starter")).getByRole("button", { name: "Manage" }));
		expect(await screen.findByRole("alert")).toHaveTextContent("Only organization admins can manage billing.");
	});

	it("says so plainly when billing is off", async () => {
		mocks.client.getBilling.mockResolvedValue({ enabled: false, exempt: false, plan: "", entitled: false, plans: [] });
		renderSection();
		expect(await screen.findByText("Billing isn't enabled on this server.")).toBeInTheDocument();
	});
});

describe("billing formatting", () => {
	it("formats session-minutes as hours", () => {
		expect(formatHours(252)).toBe("4.2");
		expect(formatHours(360)).toBe("6");
	});

	it("formats time until a reset", () => {
		const t = appI18n.t.bind(appI18n);
		const now = Date.parse("2026-10-10T12:00:00Z");
		expect(formatRelative("2026-10-10T13:20:00Z", t, now)).toBe("in 1 h 20 m");
		expect(formatRelative("2026-10-10T12:05:00Z", t, now)).toBe("in 5 m");
		expect(formatRelative("2026-10-10T11:00:00Z", t, now)).toBe("now");
	});
});
