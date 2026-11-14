import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { act, renderHook, waitFor } from "@testing-library/react";
import type { ReactNode } from "react";
import { describe, expect, it, vi } from "vitest";
import type { CloudCpClient } from "../lib/cloud-cp";
import { CloudCpError } from "../lib/cloud-cp/errors";
import { useHostServesMediaVersions, useRestoreArtifactVersion } from "./useArtifactVersions";

const { hostGetMock } = vi.hoisted(() => ({ hostGetMock: vi.fn() }));
vi.mock("../lib/host-clients", async (importOriginal) => ({
	...(await importOriginal<typeof import("../lib/host-clients")>()),
	clientForSessionHost: () => ({ GET: hostGetMock }),
}));

function restoreWith(restoreSessionArtifactVersion: () => Promise<unknown>) {
	const queryClient = new QueryClient({ defaultOptions: { mutations: { retry: false } } });
	const wrapper = ({ children }: { children: ReactNode }) => <QueryClientProvider client={queryClient}>{children}</QueryClientProvider>;
	const client = { restoreSessionArtifactVersion } as unknown as CloudCpClient;
	return renderHook(() => useRestoreArtifactVersion({ cloud: { client, orgId: "org-1" }, path: "notes.md", sessionId: "s1" }), { wrapper });
}

describe("useRestoreArtifactVersion for a cloud file", () => {
	it("reads the control plane's refusal as view only", async () => {
		const { result } = restoreWith(vi.fn().mockRejectedValue(new CloudCpError("Only the person who started this session can change its files.", { status: 403, code: "artifact_not_owner" })));

		await act(async () => {
			await expect(result.current.mutateAsync({ version: 1 })).rejects.toThrow(/^View only\./);
		});
	});

	it("leaves any other failure as it is", async () => {
		const { result } = restoreWith(vi.fn().mockRejectedValue(new CloudCpError("The file changed.", { status: 409, code: "artifact_changed" })));

		await act(async () => {
			await expect(result.current.mutateAsync({ version: 1 })).rejects.toThrow("The file changed.");
		});
	});
});

describe("useHostServesMediaVersions", () => {
	function renderSupport(enabled = true) {
		const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } });
		const wrapper = ({ children }: { children: ReactNode }) => <QueryClientProvider client={queryClient}>{children}</QueryClientProvider>;
		return renderHook(() => useHostServesMediaVersions({ enabled, hostId: "remote-1", path: "chart.png", sessionId: "s1" }), { wrapper });
	}

	it("is true once the host's list says so", async () => {
		hostGetMock.mockResolvedValue({ data: { mediaVersions: true, path: "chart.png", versions: [] } });
		const { result } = renderSupport();
		await waitFor(() => expect(result.current).toBe(true));
	});

	it("stays false for an older daemon, which does not say so", async () => {
		hostGetMock.mockClear();
		hostGetMock.mockResolvedValue({ data: { path: "chart.png", versions: [] } });
		const { result } = renderSupport();
		await waitFor(() => expect(hostGetMock).toHaveBeenCalled());
		await act(async () => undefined);
		expect(result.current).toBe(false);
	});

	it("does not ask when it is not needed", () => {
		hostGetMock.mockClear();
		renderSupport(false);
		expect(hostGetMock).not.toHaveBeenCalled();
	});
});
