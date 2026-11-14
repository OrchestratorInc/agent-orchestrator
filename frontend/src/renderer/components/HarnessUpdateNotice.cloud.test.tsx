import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { cleanup, render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { CloudHarnessVersions } from "../hooks/useCloudHarnessVersions";
import { appI18n } from "../i18n";
import { apiClient } from "../lib/api-client";
import { useUiStore } from "../stores/ui-store";
import { agentReadiness } from "../test/agent-readiness-fixtures";
import { cloudHarnessEntry, cloudHarnessVersions } from "../test/cloud-harness-fixtures";
import { HarnessUpdateNotice } from "./HarnessUpdateNotice";
import { TooltipProvider } from "./ui/tooltip";

const cloud = vi.hoisted(() => ({ value: undefined as CloudHarnessVersions | undefined }));
vi.mock("../hooks/useCloudHarnessVersions", async (importOriginal) => ({
	...(await importOriginal<typeof import("../hooks/useCloudHarnessVersions")>()),
	useCloudHarnessVersions: () => cloud.value!,
}));

function mountNotice() {
	const client = new QueryClient({ defaultOptions: { queries: { retry: false, gcTime: 0 } } });
	return render(<QueryClientProvider client={client}><TooltipProvider><HarnessUpdateNotice enabled /></TooltipProvider></QueryClientProvider>);
}

/** Claude Code on this computer, behind or current. */
function mockLocal(behind: boolean) {
	const readiness = { agents: [agentReadiness("claude-code", "Claude Code")] };
	vi.spyOn(apiClient, "GET").mockImplementation(async (path) => {
		if (path === "/api/v1/agents/readiness") return { data: readiness } as never;
		if (path === "/api/v1/agents/installers") return { data: { agents: [{ agentId: "claude-code", documentationUrl: "https://code.claude.com/docs/en/setup", methods: [{ id: "official-installer", available: true, updateAvailable: true }] }] } } as never;
		if (path === "/api/v1/agents/install-jobs") return { data: { jobs: [] } } as never;
		if (path === "/api/v1/agents/auth-plans") return { data: { plans: [{ agentId: "claude-code", action: "login", available: true }] } } as never;
		if (path === "/api/v1/agents/{agent}/update-advisory") return { data: {
			agentId: "claude-code", status: behind ? "behind_latest" : "current", currentVersion: behind ? "2.1.291" : "2.1.322", latestVersion: "2.1.322",
			maintenanceMethod: "official-installer", source: "official-release", checkedAt: "2026-11-10T00:00:00Z",
		} } as never;
		return { data: undefined } as never;
	});
	vi.spyOn(apiClient, "POST").mockImplementation(async (path) => {
		if (path === "/api/v1/agents/{agent}/install") return { data: { target: "claude-code", status: "installing", startedAt: "2026-11-10T12:00:00Z" } } as never;
		return { data: readiness } as never;
	});
}

const cloudClaude = (editable: boolean) => cloudHarnessVersions({ editable, agents: [
	cloudHarnessEntry("claude-code", { current: "2.1.287", latest: "2.1.322", editable }),
	cloudHarnessEntry("codex", { current: "0.165.0", editable }),
] });

describe("AO Cloud in the startup update notice", () => {
	beforeEach(async () => {
		await appI18n.changeLanguage("en");
		localStorage.clear();
		useUiStore.setState({ settingsModal: null });
		cloud.value = undefined;
	});
	afterEach(() => { cleanup(); vi.restoreAllMocks(); });

	it("folds Cloud into the harness's entry, and one Update covers both", async () => {
		mockLocal(true);
		cloud.value = cloudClaude(true);
		mountNotice();
		const notice = await screen.findByRole("region", { name: "Agent updates available" });
		// One entry for the harness: no card of Cloud's own.
		const cards = within(notice).getAllByRole("article");
		expect(cards).toHaveLength(1);
		const card = cards[0]!;
		const targets = within(card).getByText("This computer and Cloud");
		await userEvent.hover(targets);
		expect(await screen.findByRole("tooltip")).toHaveTextContent("This computer: v2.1.291 → v2.1.322. Cloud: v2.1.287 → v2.1.322.");
		await userEvent.click(within(card).getByRole("button", { name: "Update" }));
		await waitFor(() => expect(cloud.value!.startUpdate).toHaveBeenCalledWith("claude-code"));
		await waitFor(() => expect(apiClient.POST).toHaveBeenCalledWith("/api/v1/agents/{agent}/install", expect.objectContaining({ params: { path: { agent: "claude-code" } } })));
	});

	it("shows an admin a Cloud-only update as the harness's entry", async () => {
		mockLocal(false);
		cloud.value = cloudClaude(true);
		mountNotice();
		const notice = await screen.findByRole("region", { name: "Agent updates available" });
		const card = within(notice).getByRole("article", { name: "Claude Code" });
		expect(card).toHaveTextContent("Cloud");
		expect(card).toHaveTextContent("v2.1.287");
		expect(card).toHaveTextContent("v2.1.322");
		await userEvent.click(within(card).getByRole("button", { name: "Update" }));
		await waitFor(() => expect(cloud.value!.startUpdate).toHaveBeenCalledWith("claude-code"));
		expect(apiClient.POST).not.toHaveBeenCalledWith("/api/v1/agents/{agent}/install", expect.anything());
	});

	it("never shows a Cloud update to someone who cannot make it", async () => {
		mockLocal(false);
		cloud.value = cloudClaude(false);
		mountNotice();
		await waitFor(() => expect(apiClient.GET).toHaveBeenCalledWith("/api/v1/agents/{agent}/update-advisory", expect.anything()));
		await new Promise((resolve) => setTimeout(resolve, 50));
		expect(screen.queryByRole("region", { name: "Agent updates available" })).toBeNull();

		cleanup();
		mockLocal(true);
		mountNotice();
		const notice = await screen.findByRole("region", { name: "Agent updates available" });
		expect(within(notice).queryByText("This computer and Cloud")).toBeNull();
		expect(within(notice).queryByText("Cloud")).toBeNull();
	});

	it("waits for Cloud's answer, so an entry never gains its Cloud target after it shows", async () => {
		mockLocal(true);
		cloud.value = cloudHarnessVersions(undefined);
		const view = mountNotice();
		await waitFor(() => expect(apiClient.GET).toHaveBeenCalledWith("/api/v1/agents/{agent}/update-advisory", expect.anything()));
		await new Promise((resolve) => setTimeout(resolve, 50));
		expect(screen.queryByRole("region", { name: "Agent updates available" })).toBeNull();
		cloud.value = cloudClaude(true);
		view.rerender(<QueryClientProvider client={new QueryClient()}><TooltipProvider><HarnessUpdateNotice enabled /></TooltipProvider></QueryClientProvider>);
		const notice = await screen.findByRole("region", { name: "Agent updates available" });
		expect(within(notice).getByText("This computer and Cloud")).toBeInTheDocument();
	});

	it("dismisses both places' updates with the entry", async () => {
		mockLocal(true);
		cloud.value = cloudClaude(true);
		mountNotice();
		const notice = await screen.findByRole("region", { name: "Agent updates available" });
		await userEvent.click(within(notice).getByRole("button", { name: "Dismiss Claude Code update" }));
		await waitFor(() => expect(screen.queryByRole("region", { name: "Agent updates available" })).toBeNull());
		const dismissed = JSON.parse(localStorage.getItem("ao.harness-update-dismissals.v1") ?? "[]") as string[];
		expect(dismissed).toEqual(expect.arrayContaining([
			JSON.stringify(["local", "claude-code", "2.1.322"]),
			JSON.stringify(["ao-cloud", "claude-code", "2.1.322"]),
		]));
	});
});
