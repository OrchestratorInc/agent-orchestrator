import { vi } from "vitest";
import type { CloudHarnessVersions } from "../hooks/useCloudHarnessVersions";
import type { CloudCpHarnessVersion, CloudCpHarnessVersionsResponse, CloudCpManagedHarness } from "../lib/cloud-cp";

/** One harness as AO Cloud runs it: on `current`, with `latest` the newest release. */
export function cloudHarnessEntry(agentId: CloudCpManagedHarness, options: {
	current: string;
	latest?: string;
	tested?: string;
	editable: boolean;
	failed?: { error: string; output?: string };
}): CloudCpHarnessVersion {
	const tested = options.tested ?? options.current;
	const latest = options.latest ?? options.current;
	return {
		agentId,
		defaultVersion: tested,
		...(options.current !== tested ? { selectedVersion: options.current } : {}),
		plan: {
			agentId, automatic: true, available: true, documentationUrl: "", method: "ao-cloud",
			methods: [{
				id: "ao-cloud", label: "AO Cloud", available: true, recommended: true, reinstallAvailable: false, updateAvailable: options.editable,
				...(options.editable ? {} : { updateReason: "Only organization owners and admins can change the version AO Cloud runs." }),
			}],
		},
		advisory: {
			agentId, checkedAt: "2026-11-10T09:00:00Z", currentVersion: options.current, latestVersion: latest,
			maintenanceMethod: "ao-cloud", source: "ao-cloud", status: latest === options.current ? "current" : "behind_latest",
		},
		...(options.failed ? { job: { target: agentId, status: "failed" as const, method: "ao-cloud", ...options.failed } } : {}),
		...(agentId === "claude-code" ? { components: [{ tool: "claude-agent-acp", version: "0.70.0", defaultVersion: "0.70.0" }] } : {}),
	};
}

/** What useCloudHarnessVersions returns, with spies for its actions. */
export function cloudHarnessVersions(data: CloudCpHarnessVersionsResponse | null | undefined, overrides: Partial<CloudHarnessVersions> = {}): CloudHarnessVersions {
	return {
		expected: true,
		data,
		isPending: data === undefined,
		isError: false,
		startUpdate: vi.fn(async () => ({})),
		resetVersion: vi.fn(async () => ({})),
		...overrides,
	};
}
