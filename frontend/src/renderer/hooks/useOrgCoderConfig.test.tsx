import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { renderHook, waitFor } from "@testing-library/react";
import type { ReactNode } from "react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { useOrgCoderConfig } from "./useOrgCoderConfig";

const mocks = vi.hoisted(() => ({ getOrgCoderConfig: vi.fn() }));

vi.mock("./useCloudCp", () => ({ useCloudCp: () => ({ client: { getOrgCoderConfig: mocks.getOrgCoderConfig }, ready: true }) }));
vi.mock("./useCloudOrg", () => ({ useCloudOrg: () => ({ org: { id: "signed-in-org" } }) }));
vi.mock("./useCloudOffering", () => ({ useCloudOffering: () => ({ customCoder: true }) }));

function wrapper({ children }: { children: ReactNode }) {
	const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
	return <QueryClientProvider client={client}>{children}</QueryClientProvider>;
}

describe("useOrgCoderConfig", () => {
	beforeEach(() => {
		mocks.getOrgCoderConfig.mockReset().mockResolvedValue({ coderConfig: { baseUrl: "https://coder.test", workerUser: "ao-worker" }, editable: true });
	});

	it("reads the given org's Coder connection, not the signed-in org's", async () => {
		const { result } = renderHook(() => useOrgCoderConfig(true, "project-org"), { wrapper });
		await waitFor(() => expect(result.current.data).toBeTruthy());
		expect(mocks.getOrgCoderConfig).toHaveBeenCalledWith("project-org");
		expect(mocks.getOrgCoderConfig).not.toHaveBeenCalledWith("signed-in-org");
	});

	it("reads the signed-in org's connection without an org", async () => {
		const { result } = renderHook(() => useOrgCoderConfig(), { wrapper });
		await waitFor(() => expect(result.current.data).toBeTruthy());
		expect(mocks.getOrgCoderConfig).toHaveBeenCalledWith("signed-in-org");
	});
});
