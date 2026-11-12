import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { renderHook, waitFor } from "@testing-library/react";
import type { ReactNode } from "react";
import { describe, expect, it, vi } from "vitest";
import { CloudCpError, type CloudCpClient } from "../lib/cloud-cp";
import { cloudHarnessEntry } from "../test/cloud-harness-fixtures";
import { describeCloudHarness, useCloudHarnessVersions } from "./useCloudHarnessVersions";

const scope = vi.hoisted(() => ({ client: {} as Partial<CloudCpClient> }));
vi.mock("./useCloudGate", () => ({ useCloudGate: () => ({ cloudEnabled: true, localEnabled: true, client: "", loading: false }) }));
vi.mock("./useCloudOrg", () => ({ useCloudOrg: () => ({ org: { id: "org-1" }, isLoading: false, error: null, ready: true }) }));
vi.mock("./useCloudCp", () => ({ useCloudCp: () => ({ client: scope.client, ready: true, baseUrl: "https://cp.test", userId: "user-1" }) }));

function wrapper({ children }: { children: ReactNode }) {
	return <QueryClientProvider client={new QueryClient({ defaultOptions: { queries: { retry: false } } })}>{children}</QueryClientProvider>;
}

describe("useCloudHarnessVersions", () => {
	it("reads the organization's versions, and nothing from a control plane that predates them", async () => {
		const versions = { editable: true, agents: [cloudHarnessEntry("codex", { current: "0.165.0", latest: "0.167.0", editable: true })] };
		scope.client = { listOrgHarnessVersions: vi.fn(async () => versions) };
		const { result } = renderHook(() => useCloudHarnessVersions(), { wrapper });
		await waitFor(() => expect(result.current.data).toEqual(versions));
		expect(scope.client.listOrgHarnessVersions).toHaveBeenCalledWith("org-1", undefined, expect.anything());

		scope.client = { listOrgHarnessVersions: vi.fn(async () => { throw new CloudCpError("not found", { status: 404 }); }) };
		const older = renderHook(() => useCloudHarnessVersions(), { wrapper });
		await waitFor(() => expect(older.result.current.data).toBeNull());
		expect(older.result.current.isError).toBe(false);
	});

	it("updates to the newest release a click-time check finds, and only when the caller may", async () => {
		const stale = { editable: true, agents: [cloudHarnessEntry("claude-code", { current: "2.1.287", latest: "2.1.300", editable: true })] };
		const fresh = { editable: true, agents: [cloudHarnessEntry("claude-code", { current: "2.1.287", latest: "2.1.322", editable: true })] };
		const updated = cloudHarnessEntry("claude-code", { current: "2.1.322", tested: "2.1.287", editable: true });
		const list = vi.fn(async (_org: string, query?: { refresh?: boolean }) => query?.refresh ? fresh : stale);
		const install = vi.fn(async () => updated);
		scope.client = { listOrgHarnessVersions: list, installOrgHarnessVersion: install };
		const { result } = renderHook(() => useCloudHarnessVersions(), { wrapper });
		await waitFor(() => expect(result.current.data).toEqual(stale));
		await expect(result.current.startUpdate("claude-code")).resolves.toEqual({ entry: updated });
		expect(list).toHaveBeenLastCalledWith("org-1", { refresh: true });
		expect(install).toHaveBeenCalledWith("org-1", "claude-code", "2.1.322");
		await waitFor(() => expect(result.current.data?.agents[0]?.selectedVersion).toBe("2.1.322"));

		// A member's click finds nothing to do.
		const member = { editable: false, agents: [cloudHarnessEntry("claude-code", { current: "2.1.287", latest: "2.1.322", editable: false })] };
		scope.client = { listOrgHarnessVersions: vi.fn(async () => member), installOrgHarnessVersion: vi.fn() };
		const read = renderHook(() => useCloudHarnessVersions(), { wrapper });
		await waitFor(() => expect(read.result.current.data).toEqual(member));
		await expect(read.result.current.startUpdate("claude-code")).resolves.toEqual({});
		expect(scope.client.installOrgHarnessVersion).not.toHaveBeenCalled();
	});

	it("reports a refused update as an error", async () => {
		const versions = { editable: true, agents: [cloudHarnessEntry("codex", { current: "0.165.0", latest: "0.167.0", editable: true })] };
		scope.client = {
			listOrgHarnessVersions: vi.fn(async () => versions),
			installOrgHarnessVersion: vi.fn(async () => { throw new CloudCpError("The publisher has no such release.", { status: 422, code: "UNKNOWN_HARNESS_VERSION" }); }),
		};
		const { result } = renderHook(() => useCloudHarnessVersions(), { wrapper });
		await waitFor(() => expect(result.current.data).toEqual(versions));
		await expect(result.current.startUpdate("codex")).resolves.toEqual({ error: "The publisher has no such release." });
	});
});

describe("describeCloudHarness", () => {
	it("offers an update only to someone who may make it", () => {
		const admin = describeCloudHarness({ editable: true, agents: [cloudHarnessEntry("codex", { current: "0.165.0", latest: "0.167.0", editable: true })] }, "codex")!;
		expect(admin).toMatchObject({ currentVersion: "0.165.0", latestVersion: "0.167.0", updateExists: true, actionable: true, onDefault: true, failed: false });
		const member = describeCloudHarness({ editable: false, agents: [cloudHarnessEntry("codex", { current: "0.165.0", latest: "0.167.0", editable: false })] }, "codex")!;
		expect(member).toMatchObject({ updateExists: true, actionable: false });
		const current = describeCloudHarness({ editable: true, agents: [cloudHarnessEntry("codex", { current: "0.167.0", tested: "0.165.0", editable: true })] }, "codex")!;
		expect(current).toMatchObject({ updateExists: false, actionable: false, onDefault: false, defaultVersion: "0.165.0" });
		expect(describeCloudHarness({ editable: true, agents: [] }, "codex")).toBeUndefined();
		expect(describeCloudHarness(null, "codex")).toBeUndefined();
	});

	it("shows the AO-tested release once it has caught up with the organization's choice", () => {
		const superseded = { ...cloudHarnessEntry("claude-code", { current: "2.1.322", latest: "2.1.330", editable: true }), supersededVersion: "2.1.300" };
		expect(describeCloudHarness({ editable: true, agents: [superseded] }, "claude-code")).toMatchObject({
			currentVersion: "2.1.322", defaultVersion: "2.1.322", onDefault: true, updateExists: true, actionable: true,
		});
	});
});
