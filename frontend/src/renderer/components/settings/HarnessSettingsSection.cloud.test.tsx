import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { cleanup, fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { appI18n } from "../../i18n";
import type { CloudHarnessVersions } from "../../hooks/useCloudHarnessVersions";
import { apiClient } from "../../lib/api-client";
import { CloudCpError } from "../../lib/cloud-cp";
import { cloudHarnessEntry, cloudHarnessVersions } from "../../test/cloud-harness-fixtures";
import { TooltipProvider } from "../ui/tooltip";
import { HarnessSettingsSection } from "./HarnessSettingsSection";

// AO Cloud's harness versions, as the hook would return them for the test.
const cloud = vi.hoisted(() => ({ value: undefined as CloudHarnessVersions | undefined }));
vi.mock("../../hooks/useCloudHarnessVersions", async (importOriginal) => {
	const actual = await importOriginal<typeof import("../../hooks/useCloudHarnessVersions")>();
	return { ...actual, useCloudHarnessVersions: () => cloud.value! };
});
vi.mock("../../hooks/useCloudGate", () => ({ useCloudGate: () => ({ cloudEnabled: true, localEnabled: true, client: "", loading: false }) }));
vi.mock("../../hooks/useCloudOrg", () => ({ useCloudOrg: () => ({ org: { id: "org-1" }, isLoading: false, error: null, ready: true }) }));
// The signed-in person's saved credentials (GET /me/providers), as the hook would return them.
const providerConnections = vi.hoisted(() => ({ value: [] as Array<{ id: string; provider: string; label: string; validationState: string }> }));
vi.mock("../../hooks/useProviderConnections", async (importOriginal) => {
	const actual = await importOriginal<typeof import("../../hooks/useProviderConnections")>();
	return { ...actual, useProviderConnections: () => ({ data: providerConnections.value, isSuccess: true }) };
});
const deleteGitHubPAT = vi.hoisted(() => vi.fn(async (): Promise<void> => undefined));
vi.mock("../../hooks/useCloudCp", async (importOriginal) => {
	const actual = await importOriginal<typeof import("../../hooks/useCloudCp")>();
	return {
		...actual,
		useCloudCp: (...args: Parameters<typeof actual.useCloudCp>) => {
			const cp = actual.useCloudCp(...args);
			return { ...cp, client: { ...cp.client, deleteGitHubPAT } };
		},
	};
});

/** Codex and Claude Code installed here (Cursor is not), each with its local release status. */
function mockLocal(status: { codex: "current" | "behind_latest"; claude?: "current" | "behind_latest" }) {
	const readiness = { agents: ["claude-code", "codex", "cursor"].map((id) => ({
		id, label: id,
		installation: { state: id === "cursor" ? "not_installed" : "installed", freshness: "fresh", reason: "", reasonCode: "", attemptedAt: null, checkedAt: null },
		authentication: { state: "authorized", freshness: "fresh", reason: "", reasonCode: "", attemptedAt: null, checkedAt: null },
		effectiveReadiness: id === "cursor" ? "not_ready" : "ready",
		usageCount: 0,
	})) };
	const plans = { agents: ["claude-code", "codex", "cursor"].map((agentId) => ({
		agentId, available: true, automatic: true, method: "npm", documentationUrl: "https://example.test",
		methods: [{ id: "npm", label: "npm", available: true, recommended: true, reinstallAvailable: true, updateAvailable: true }],
	})) };
	vi.spyOn(apiClient, "GET").mockImplementation(async (path, options) => {
		if (path === "/api/v1/agents/readiness") return { data: readiness } as never;
		if (path === "/api/v1/agents/installers") return { data: plans } as never;
		if (path === "/api/v1/agents/install-jobs") return { data: { jobs: [] } } as never;
		if (path === "/api/v1/agents/auth-plans") return { data: { plans: [] } } as never;
		if (path === "/api/v1/agents/{agent}/update-advisory") {
			const agentId = (options as { params: { path: { agent: string } } }).params.path.agent;
			const behind = (agentId === "codex" ? status.codex : status.claude ?? "current") === "behind_latest";
			return { data: { agentId, status: behind ? "behind_latest" : "current", currentVersion: behind ? "0.160.0" : "0.167.0", latestVersion: "0.167.0", maintenanceMethod: "npm", checkedAt: "2026-11-10T00:00:00Z" } } as never;
		}
		return { data: undefined } as never;
	});
	vi.spyOn(apiClient, "POST").mockImplementation(async (path, options) => {
		if (path === "/api/v1/agents/refresh" || path === "/api/v1/agents/readiness/ensure") return { data: readiness } as never;
		if (path === "/api/v1/agents/{agent}/install") {
			const agent = (options as { params: { path: { agent: string } } }).params.path.agent;
			return { data: { target: agent, method: "npm", status: "installing", startedAt: "2026-11-10T12:00:00Z" } } as never;
		}
		return { data: undefined } as never;
	});
}

function renderSection() {
	const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
	return render(<QueryClientProvider client={client}><TooltipProvider><HarnessSettingsSection /></TooltipProvider></QueryClientProvider>);
}

async function openHarness(agentId: string): Promise<HTMLElement> {
	const row = await waitFor(() => {
		const element = document.querySelector<HTMLElement>(`button[data-agent="${agentId}"]`);
		if (!element) throw new Error("no row yet");
		return element;
	});
	fireEvent.click(row);
	return waitFor(() => {
		const page = document.querySelector<HTMLElement>(`div[data-agent="${agentId}"]`);
		if (!page) throw new Error("no page yet");
		return page;
	});
}

function cloudLine(page: HTMLElement): HTMLElement {
	const lines = page.querySelectorAll<HTMLElement>("[data-harness-cloud-line]");
	expect(lines).toHaveLength(1);
	return lines[0]!;
}

const local = (agent: string) => expect.objectContaining({ params: { path: { agent } } });

describe("AO Cloud on the Harness page", () => {
	beforeEach(async () => {
		await appI18n.changeLanguage("en");
		cloud.value = undefined;
	});
	afterEach(() => { cleanup(); vi.restoreAllMocks(); });

	it("offers one Update for a harness that needs one on this computer and on Cloud", async () => {
		mockLocal({ codex: "behind_latest" });
		cloud.value = cloudHarnessVersions({ editable: true, agents: [cloudHarnessEntry("codex", { current: "0.165.0", latest: "0.167.0", editable: true })] });
		renderSection();
		// One row for the harness, whose one mark covers both places.
		expect(document.querySelectorAll('button[data-agent="codex"]')).toHaveLength(1);
		const listRow = document.querySelector<HTMLElement>('button[data-agent="codex"]')!;
		expect(await within(listRow).findByRole("img", { name: "Updates available on this computer and Cloud" })).toBeInTheDocument();

		const page = await openHarness("codex");
		const line = cloudLine(page);
		expect(line).toHaveTextContent("Cloud");
		expect(line).toHaveTextContent("v0.165.0");
		expect(line).toHaveTextContent("v0.167.0 available");
		// The line is part of the installation row, not a card of its own, and has no button of its own.
		expect(within(line).queryByRole("button", { name: "Update" })).toBeNull();
		expect(screen.getAllByRole("heading", { name: "Installation" })).toHaveLength(1);
		const updates = within(page).getAllByRole("button", { name: "Update" });
		expect(updates).toHaveLength(1);
		// Which places it covers shows on hover.
		await userEvent.hover(updates[0]!);
		expect(await screen.findByRole("tooltip")).toHaveTextContent("Updates this computer and Cloud");

		await userEvent.click(updates[0]!);
		await waitFor(() => expect(cloud.value!.startUpdate).toHaveBeenCalledWith("codex"));
		await waitFor(() => expect(apiClient.POST).toHaveBeenCalledWith("/api/v1/agents/{agent}/install", local("codex")));
	});

	it("lets an admin update Cloud alone when only Cloud has an update", async () => {
		mockLocal({ codex: "current" });
		cloud.value = cloudHarnessVersions({ editable: true, agents: [cloudHarnessEntry("codex", { current: "0.165.0", latest: "0.167.0", editable: true })] });
		renderSection();
		const listRow = await waitFor(() => document.querySelector<HTMLElement>('button[data-agent="codex"]')!);
		expect(await within(listRow).findByRole("img", { name: "v0.167.0 available on Cloud" })).toBeInTheDocument();
		const page = await openHarness("codex");
		expect(cloudLine(page)).toHaveTextContent("v0.167.0 available");
		const update = await within(page).findByRole("button", { name: "Update" });
		await userEvent.hover(update);
		expect(await screen.findByRole("tooltip")).toHaveTextContent("Updates Cloud");
		await userEvent.click(update);
		await waitFor(() => expect(cloud.value!.startUpdate).toHaveBeenCalledWith("codex"));
		expect(apiClient.POST).not.toHaveBeenCalledWith("/api/v1/agents/{agent}/install", expect.anything());
	});

	it("shows a member Cloud's version read-only, with no update to make", async () => {
		mockLocal({ codex: "current" });
		cloud.value = cloudHarnessVersions({ editable: false, agents: [cloudHarnessEntry("codex", { current: "0.165.0", latest: "0.167.0", editable: false })] });
		renderSection();
		const listRow = await waitFor(() => document.querySelector<HTMLElement>('button[data-agent="codex"]')!);
		const page = await openHarness("codex");
		const line = cloudLine(page);
		expect(line).toHaveTextContent("v0.165.0");
		expect(line).not.toHaveTextContent("available");
		expect(within(page).queryByRole("button", { name: "Update" })).toBeNull();
		expect(within(listRow).queryByRole("img")).toBeNull();
		await userEvent.click(within(line).getByRole("button", { name: /Cloud/ }));
		expect(line).toHaveTextContent("Only organization owners and admins can change the Cloud version.");
		expect(within(line).queryByRole("button", { name: "Use AO-tested version" })).toBeNull();
	});

	it("keeps Cloud's line quiet when nothing needs an update", async () => {
		mockLocal({ codex: "current" });
		cloud.value = cloudHarnessVersions({ editable: true, agents: [cloudHarnessEntry("codex", { current: "0.165.0", editable: true })] });
		renderSection();
		const listRow = await waitFor(() => document.querySelector<HTMLElement>('button[data-agent="codex"]')!);
		const page = await openHarness("codex");
		const line = cloudLine(page);
		expect(line).toHaveTextContent("Cloudv0.165.0");
		expect(line).not.toHaveTextContent("available");
		expect(line).not.toHaveTextContent("failed");
		expect(within(line).getAllByRole("button")).toHaveLength(1); // only the details toggle
		expect(within(page).queryByRole("button", { name: "Update" })).toBeNull();
		expect(within(listRow).queryByRole("img")).toBeNull();
	});

	it("holds Cloud's line in place while its versions load", async () => {
		mockLocal({ codex: "current" });
		cloud.value = cloudHarnessVersions(undefined);
		const view = renderSection();
		const page = await openHarness("codex");
		const loading = cloudLine(page);
		expect(loading).toHaveTextContent("Checking…");
		cloud.value = cloudHarnessVersions({ editable: true, agents: [cloudHarnessEntry("codex", { current: "0.165.0", editable: true })] });
		view.rerender(<QueryClientProvider client={new QueryClient()}><TooltipProvider><HarnessSettingsSection /></TooltipProvider></QueryClientProvider>);
		await waitFor(() => expect(document.querySelector("[data-harness-cloud-line]")).toHaveTextContent("v0.165.0"));
		expect(document.querySelectorAll("[data-harness-cloud-line]")).toHaveLength(1);
	});

	it("shows a failed install on Cloud in the same row, with the way back to the AO-tested version", async () => {
		mockLocal({ codex: "current" });
		cloud.value = cloudHarnessVersions({ editable: true, agents: [cloudHarnessEntry("codex", {
			current: "0.167.0", tested: "0.165.0", editable: true,
			failed: { error: "the download did not match the registry's integrity", output: "npm ERR! E500" },
		})] });
		renderSection();
		const listRow = await waitFor(() => document.querySelector<HTMLElement>('button[data-agent="codex"]')!);
		await waitFor(() => expect(listRow.querySelector(".bg-error")).not.toBeNull());
		const page = await openHarness("codex");
		const line = cloudLine(page);
		expect(line).toHaveTextContent("Update failed");
		await userEvent.click(within(line).getByRole("button", { name: /Cloud/ }));
		expect(line).toHaveTextContent("A Cloud session couldn't install it and ran the AO-tested v0.165.0.");
		expect(line).toHaveTextContent("the download did not match the registry's integrity");
		expect(line).toHaveTextContent("npm ERR! E500");
		await userEvent.click(within(line).getByRole("button", { name: "Use AO-tested version" }));
		await waitFor(() => expect(cloud.value!.resetVersion).toHaveBeenCalledWith("codex"));
	});

	it("gives Cloud its own Update when the harness is not installed here", async () => {
		mockLocal({ codex: "current" });
		cloud.value = cloudHarnessVersions({ editable: true, agents: [cloudHarnessEntry("cursor", { current: "2026.08.11-e8db854", latest: "2026.11.05-86047df", editable: true })] });
		renderSection();
		const page = await openHarness("cursor");
		const line = cloudLine(page);
		await userEvent.click(within(line).getByRole("button", { name: "Update" }));
		await waitFor(() => expect(cloud.value!.startUpdate).toHaveBeenCalledWith("cursor"));
		expect(within(page).getByRole("button", { name: "Install" })).toBeInTheDocument();
	});
});

describe("Cloud harness tab", () => {
	beforeEach(async () => {
		await appI18n.changeLanguage("en");
		cloud.value = cloudHarnessVersions({ editable: true, agents: [] });
		providerConnections.value = [];
		deleteGitHubPAT.mockClear();
	});
	afterEach(() => cleanup());

	it("still lets a person remove a GitHub token they saved before, and offers nothing to save one", async () => {
		// A token saved before the GitHub App, still marked valid: the control plane keeps it
		// and can use it, so it must stay removable even though nothing saves one now.
		providerConnections.value = [{ id: "gh-1", provider: "github", label: "default", validationState: "valid" }];
		const { CloudHarnessContent } = await import("./harness/CloudHarnessContent");
		const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
		const view = render(<QueryClientProvider client={client}><TooltipProvider><CloudHarnessContent search="" /></TooltipProvider></QueryClientProvider>);
		const row = await waitFor(() => {
			const element = view.container.querySelector<HTMLElement>("[data-saved-github-token]");
			if (!element) throw new Error("no saved token row yet");
			return element;
		});
		expect(within(row).getByText("GitHub token")).toBeInTheDocument();
		expect(view.container.querySelector('input[type="password"]')).toBeNull();
		expect(screen.queryByRole("button", { name: "Save token" })).toBeNull();
		await userEvent.click(within(row).getByRole("button", { name: "Remove" }));
		await waitFor(() => expect(deleteGitHubPAT).toHaveBeenCalledTimes(1));
	});

	it("treats a GitHub token that is already gone as removed, and still reports any other failure", async () => {
		providerConnections.value = [{ id: "gh-1", provider: "github", label: "default", validationState: "valid" }];
		const { CloudHarnessContent } = await import("./harness/CloudHarnessContent");
		const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
		const invalidate = vi.spyOn(client, "invalidateQueries");
		const view = render(<QueryClientProvider client={client}><TooltipProvider><CloudHarnessContent search="" /></TooltipProvider></QueryClientProvider>);
		const row = await waitFor(() => {
			const element = view.container.querySelector<HTMLElement>("[data-saved-github-token]");
			if (!element) throw new Error("no saved token row yet");
			return element;
		});

		// Removed from another device first: the control plane answers 404 not_found.
		deleteGitHubPAT.mockRejectedValueOnce(new CloudCpError("not found", { status: 404, code: "not_found" }));
		await userEvent.click(within(row).getByRole("button", { name: "Remove" }));
		await waitFor(() => expect(invalidate).toHaveBeenCalledTimes(1));
		expect(within(row).queryByRole("alert")).toBeNull();

		// A real failure still says so, and the row is refetched to match the control plane.
		deleteGitHubPAT.mockRejectedValueOnce(new CloudCpError("unavailable", { status: 503, code: "unavailable" }));
		await userEvent.click(within(row).getByRole("button", { name: "Remove" }));
		expect(await within(row).findByRole("alert")).toHaveTextContent("Could not remove the GitHub token.");
		expect(invalidate).toHaveBeenCalledTimes(2);
	});

	it("offers no GitHub personal access token: Cloud reaches private repositories through the GitHub App", async () => {
		const { CloudHarnessContent } = await import("./harness/CloudHarnessContent");
		const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
		const view = render(<QueryClientProvider client={client}><TooltipProvider><CloudHarnessContent search="" /></TooltipProvider></QueryClientProvider>);
		// The Cloud agents are listed as before.
		expect(await screen.findByText("Codex")).toBeInTheDocument();
		expect(screen.queryByText("GitHub private repositories")).toBeNull();
		expect(screen.queryByText("GitHub token")).toBeNull();
		expect(screen.queryByRole("button", { name: "Save token" })).toBeNull();
		expect(view.container.querySelector('input[type="password"]')).toBeNull();
		expect(view.container.querySelector('[placeholder^="github_pat_"]')).toBeNull();
	});
});
