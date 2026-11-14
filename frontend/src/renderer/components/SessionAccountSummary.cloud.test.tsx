import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { appI18n } from "../i18n";
import type { WorkspaceSession } from "../types/workspace";
import { SessionAccountSummary } from "./SessionAccountSummary";

const cloud = vi.hoisted(() => ({
	listAgentAccounts: vi.fn(),
	getSessionAgentAccount: vi.fn(),
	switchSessionAgentAccount: vi.fn(),
	getSession: vi.fn(),
	getInterfaceTransition: vi.fn(),
}));
const setup = vi.hoisted(() => ({ agentAccounts: true, meUserId: "user-1" }));

vi.mock("../lib/preview-mode", () => ({ usesPreviewWorkspaceData: false }));
vi.mock("../hooks/useCloudCp", async (importOriginal) => ({
	...await importOriginal<typeof import("../hooks/useCloudCp")>(),
	useCloudCp: () => ({ client: cloud, ready: true, baseUrl: "https://cloud.example.test" }),
}));
vi.mock("../hooks/useCloudOffering", () => ({ useCloudOffering: () => ({ agentAccounts: setup.agentAccounts }) }));
vi.mock("../hooks/useCloudMeUserId", () => ({ useCloudMeUserId: () => setup.meUserId }));

const account = (id: string, email: string, validationState = "valid") => ({
	id, agent: "codex", name: "", email, credentialType: "auth_json", isDefault: id === "a-1", validationState,
	createdAt: "2026-11-01T00:00:00Z", updatedAt: "2026-11-01T00:00:00Z",
});

const session = (overrides: Partial<WorkspaceSession> = {}) => ({
	id: "s-1", provider: "codex", isTerminated: false, mode: "chat",
	cloud: { orgId: "org-1", createdByUserId: "user-1" }, ...overrides,
}) as WorkspaceSession;

function renderSummary(value: WorkspaceSession) {
	const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } });
	return render(<QueryClientProvider client={queryClient}><SessionAccountSummary session={value} /></QueryClientProvider>);
}

describe("SessionAccountSummary on a Cloud session", () => {
	beforeEach(async () => {
		await appI18n.changeLanguage("en");
		for (const mock of Object.values(cloud)) mock.mockReset();
		setup.agentAccounts = true;
		setup.meUserId = "user-1";
		cloud.listAgentAccounts.mockResolvedValue({ agentAccounts: [
			account("a-1", "work@example.com"), account("a-2", "me@example.com"), account("a-3", "old@example.com", "invalid"),
		] });
		cloud.getSessionAgentAccount.mockResolvedValue({ agent: "codex", accountId: "a-1" });
		cloud.getSession.mockResolvedValue({ session: { interfaceMode: "chat" } });
		cloud.getInterfaceTransition.mockResolvedValue({ supported: true, targetMode: "chat" });
		cloud.switchSessionAgentAccount.mockResolvedValue({ transition: { id: "t-1", phase: "requested" } });
	});

	it("moves its creator's session to another of their accounts", async () => {
		const user = userEvent.setup();
		renderSummary(session());
		const picker = await screen.findByRole("combobox", { name: "Account" });
		await waitFor(() => expect(picker).toHaveTextContent("work@example.com"));

		await user.click(picker);
		expect(screen.getByRole("option", { name: "old@example.com" })).toHaveAttribute("aria-disabled", "true");
		await user.click(screen.getByRole("option", { name: "me@example.com" }));
		expect(cloud.switchSessionAgentAccount).toHaveBeenCalledWith("org-1", "s-1", { accountId: "a-2" });
	});

	it("asks before stopping a working agent's turn", async () => {
		const user = userEvent.setup();
		renderSummary(session({ activity: { state: "active" } } as Partial<WorkspaceSession>));
		const picker = await screen.findByRole("combobox", { name: "Account" });
		await waitFor(() => expect(picker).toHaveTextContent("work@example.com"));
		await user.click(picker);
		await user.click(screen.getByRole("option", { name: "me@example.com" }));
		expect(cloud.switchSessionAgentAccount).not.toHaveBeenCalled();

		await user.click(await screen.findByRole("button", { name: "Stop and move" }));
		expect(cloud.switchSessionAgentAccount).toHaveBeenCalledWith("org-1", "s-1", { accountId: "a-2", policy: "interrupt" });
	});

	it("stops an idle terminal directly instead of draining it, and drains a working one", async () => {
		const user = userEvent.setup();
		cloud.getSession.mockResolvedValueOnce({ session: { activityState: "idle", status: "idle" } });
		renderSummary(session({ mode: "tui" } as Partial<WorkspaceSession>));
		const picker = await screen.findByRole("combobox", { name: "Account" });
		await waitFor(() => expect(picker).toHaveTextContent("work@example.com"));
		await user.click(picker);
		await user.click(screen.getByRole("option", { name: "me@example.com" }));
		await waitFor(() => expect(cloud.switchSessionAgentAccount).toHaveBeenCalledWith("org-1", "s-1", { accountId: "a-2", policy: "interrupt" }));

		cloud.getSession.mockResolvedValueOnce({ session: { activityState: "active", status: "working" } });
		await user.click(picker);
		await user.click(screen.getByRole("option", { name: "me@example.com" }));
		await waitFor(() => expect(cloud.switchSessionAgentAccount).toHaveBeenLastCalledWith("org-1", "s-1", { accountId: "a-2" }));
	});

	it("stays hidden for anyone but the creator, and without accounts", () => {
		const { unmount } = renderSummary(session({ cloud: { orgId: "org-1", createdByUserId: "someone-else" } }));
		expect(screen.queryByTestId("session-account-summary")).toBeNull();
		unmount();
		setup.agentAccounts = false;
		renderSummary(session());
		expect(screen.queryByTestId("session-account-summary")).toBeNull();
		expect(cloud.listAgentAccounts).not.toHaveBeenCalled();
		expect(cloud.getSessionAgentAccount).not.toHaveBeenCalled();
	});
});
