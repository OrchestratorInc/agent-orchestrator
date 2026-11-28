import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { renderHook, waitFor } from "@testing-library/react";
import type { ReactNode } from "react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { CloudCpError, type CloudCpClient } from "../lib/cloud-cp";
import { useSharedArtifacts } from "./useSharedArtifacts";

const scope = vi.hoisted(() => ({ client: {} as Partial<CloudCpClient> }));
vi.mock("./useCloudCp", () => ({ useCloudCp: () => ({ client: scope.client, ready: true, baseUrl: "https://cp.test", userId: "user-1" }) }));

function wrapper({ children }: { children: ReactNode }) {
	return <QueryClientProvider client={new QueryClient({ defaultOptions: { queries: { retry: false } } })}>{children}</QueryClientProvider>;
}

afterEach(() => vi.useRealTimers());

describe("useSharedArtifacts", () => {
	it("is empty, not an error, where the control plane has no share links, and stops asking", async () => {
		const list = vi.fn(async () => {
			throw new CloudCpError("not found", { status: 404 });
		});
		scope.client = { listSharedArtifacts: list };
		const { result } = renderHook(() => useSharedArtifacts(), { wrapper });
		await waitFor(() => expect(result.current.data).toEqual([]));
		expect(result.current.isError).toBe(false);
		vi.useFakeTimers();
		await vi.advanceTimersByTimeAsync(60_000);
		expect(list).toHaveBeenCalledOnce();
	});

	it("still reports any other failure", async () => {
		scope.client = { listSharedArtifacts: vi.fn(async () => { throw new CloudCpError("unavailable", { status: 503 }); }) };
		const { result } = renderHook(() => useSharedArtifacts(), { wrapper });
		// After its one retry.
		await waitFor(() => expect(result.current.isError).toBe(true), { timeout: 3_000 });
	});
});
