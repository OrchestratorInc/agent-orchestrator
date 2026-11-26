import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, describe, expect, it, vi } from "vitest";

const apiMocks = vi.hoisted(() => ({ get: vi.fn(), post: vi.fn(), patch: vi.fn() }));
const bridgeMocks = vi.hoisted(() => ({ browserCacheSize: vi.fn(), clearBrowserCache: vi.fn() }));

vi.mock("../../lib/api-client", () => ({
	apiClient: { GET: apiMocks.get, POST: apiMocks.post, PATCH: apiMocks.patch },
	apiErrorMessage: (error: unknown, fallback = "Request failed") =>
		typeof error === "object" && error && "message" in error ? String((error as { message: unknown }).message) : fallback,
}));

vi.mock("../../lib/bridge", () => ({ aoBridge: { storage: bridgeMocks } }));

import { formatStorageBytes, StorageSettingsSection } from "./StorageSettingsSection";

const GiB = 1024 ** 3;

function report(overrides: Record<string, unknown> = {}) {
	return {
		scannedAt: "2026-11-13T00:00:00Z",
		scanDurationMs: 100,
		scanning: false,
		cleaning: false,
		totalBytes: 3 * GiB,
		reclaimableBytes: 2 * GiB,
		autoReclaimableBytes: GiB,
		disk: { path: "/data", totalBytes: 100 * GiB, freeBytes: 10 * GiB, level: "low" },
		categories: [
			{ category: "worktrees", bytes: 2 * GiB, reclaimableBytes: 2 * GiB },
			{ category: "harnesses", bytes: GiB, reclaimableBytes: 0 },
			{ category: "logs", bytes: 0, reclaimableBytes: 0 },
		],
		worktrees: [{ path: "/w/p-1", sessionId: "p-1", state: "quiet", bytes: 1, dependencyBytes: 0, offloaded: true, offloadedBytes: GiB }],
		database: { fileBytes: 0, walBytes: 0, freeBytes: 0, tables: [], tablesKnown: false },
		plan: [],
		settings: {
			autoClean: true,
			offloadEnabled: true,
			offloadIdleHours: 48,
			offloadNoPrGraceHours: 2,
			offloadMergedIdleMinutes: 30,
			endedWorktreeDays: 7,
			backupDays: 14,
			logDays: 14,
			cleanDevProfiles: false,
			devProfileDays: 7,
			templateDays: 30,
		},
		autoCleanAllowed: true,
		copyOnWrite: false,
		...overrides,
	};
}

function renderSection() {
	const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
	return render(
		<QueryClientProvider client={client}>
			<StorageSettingsSection />
		</QueryClientProvider>,
	);
}

beforeEach(() => {
	apiMocks.get.mockReset();
	apiMocks.post.mockReset();
	apiMocks.patch.mockReset();
	bridgeMocks.browserCacheSize.mockReset().mockResolvedValue(0);
	bridgeMocks.clearBrowserCache.mockReset().mockResolvedValue(undefined);
	apiMocks.get.mockResolvedValue({ data: report(), error: undefined });
});

describe("StorageSettingsSection", () => {
	it("formats sizes in binary steps", () => {
		expect(formatStorageBytes(512, "en")).toBe("512 byte");
		expect(formatStorageBytes(1.5 * GiB, "en")).toBe("1.5 GB");
		expect(formatStorageBytes(20 * GiB, "en")).toBe("20 GB");
	});

	it("lists categories that hold data, the low-disk warning and offloaded sessions", async () => {
		renderSection();
		expect(await screen.findByText("Session worktrees")).toBeInTheDocument();
		expect(screen.getByText("Agent tools")).toBeInTheDocument();
		expect(screen.queryByText("Logs")).not.toBeInTheDocument();
		expect(screen.getByText("Disk space is low, so quiet sessions are offloaded sooner.")).toBeInTheDocument();
		expect(screen.getByText("Offloaded sessions")).toBeInTheDocument();
	});

	it("previews a cleanup and runs it only after confirmation", async () => {
		const user = userEvent.setup();
		apiMocks.post.mockImplementation(async (path: string, init: { body: { dryRun?: boolean } }) => {
			if (path !== "/api/v1/storage/clean") return { data: undefined, error: { message: `unexpected ${path}` } };
			return {
				data: {
					startedAt: "2026-11-13T00:00:00Z",
					running: !init.body.dryRun,
					dryRun: Boolean(init.body.dryRun),
					freedBytes: 2 * GiB,
					actions: [{ at: "2026-11-13T00:00:00Z", kind: "offload", target: "/w/p-1", reason: "quiet", bytes: 2 * GiB, dryRun: true, result: "planned" }],
				},
				error: undefined,
			};
		});
		renderSection();
		await screen.findByText("Session worktrees");
		const [categoryCleanUp] = screen.getAllByRole("button", { name: "Clean up" });
		await user.click(categoryCleanUp);

		expect(await screen.findByText("Free 2 GB?")).toBeInTheDocument();
		expect(apiMocks.post).toHaveBeenCalledTimes(1);
		expect(apiMocks.post.mock.calls[0][1]).toEqual({ body: { kinds: ["offload", "ended_worktrees", "orphan_worktrees"], dryRun: true } });

		await user.click(screen.getByRole("button", { name: "Free up space" }));
		await waitFor(() => expect(apiMocks.post).toHaveBeenCalledTimes(2));
		expect(apiMocks.post.mock.calls[1][1]).toEqual({ body: { kinds: ["offload", "ended_worktrees", "orphan_worktrees"], dryRun: false } });
	});

	it("says when there is nothing to clean", async () => {
		const user = userEvent.setup();
		apiMocks.post.mockResolvedValue({
			data: { startedAt: "2026-11-13T00:00:00Z", running: false, dryRun: true, freedBytes: 0, actions: [] },
			error: undefined,
		});
		renderSection();
		await screen.findByText("Session worktrees");
		await user.click(screen.getAllByRole("button", { name: "Clean up" })[0]);
		expect(await screen.findByText("Nothing to clean up right now")).toBeInTheDocument();
		expect(screen.queryByText("Free up space")).not.toBeInTheDocument();
	});

	it("saves one setting at a time", async () => {
		const user = userEvent.setup();
		apiMocks.patch.mockResolvedValue({ data: report().settings, error: undefined });
		renderSection();
		await user.click(await screen.findByRole("switch", { name: "Offload dependencies of quiet sessions" }));
		await waitFor(() => expect(apiMocks.patch).toHaveBeenCalledWith("/api/v1/storage/settings", { body: { offloadEnabled: false } }));
	});

	it("shows a failed cache clear and a failed measurement", async () => {
		const user = userEvent.setup();
		bridgeMocks.browserCacheSize.mockResolvedValue(512 * 1024 * 1024);
		bridgeMocks.clearBrowserCache.mockRejectedValue(new Error("cache is locked"));
		apiMocks.get.mockResolvedValue({
			data: report({
				scanError: "permission denied",
				categories: [{ category: "browser_data", bytes: GiB, reclaimableBytes: 0 }],
			}),
			error: undefined,
		});
		renderSection();
		expect(await screen.findByText("Measuring failed: permission denied")).toBeInTheDocument();
		await user.click(await screen.findByRole("button", { name: "Clear cache" }));
		expect(await screen.findByText("Could not clear the cache: cache is locked")).toBeInTheDocument();
	});

	it("explains a kill switch instead of offering automatic cleanup", async () => {
		apiMocks.get.mockResolvedValue({ data: report({ autoCleanAllowed: false }), error: undefined });
		renderSection();
		const toggle = await screen.findByRole("switch", { name: "Clean up automatically" });
		expect(toggle).toBeDisabled();
		expect(screen.getByText("Turned off on this computer by AO_STORAGE_AUTOCLEAN.")).toBeInTheDocument();
	});
});
