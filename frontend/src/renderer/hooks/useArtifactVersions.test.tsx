import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { act, renderHook } from "@testing-library/react";
import type { ReactNode } from "react";
import { describe, expect, it, vi } from "vitest";
import type { CloudCpClient } from "../lib/cloud-cp";
import { CloudCpError } from "../lib/cloud-cp/errors";
import { useRestoreArtifactVersion } from "./useArtifactVersions";

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
