import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { renderHook } from "@testing-library/react";
import type { ReactNode } from "react";
import { expect, it, vi } from "vitest";

vi.mock("./useCloudCp", () => ({ useCloudCp: () => ({ client: {}, ready: false, isLoading: true, baseUrl: "" }) }));

import { useCloudOrg } from "./useCloudOrg";

it("keeps org loading scoped to the org request while Cloud settings initialize", () => {
	const client = new QueryClient();
	const wrapper = ({ children }: { children: ReactNode }) => <QueryClientProvider client={client}>{children}</QueryClientProvider>;
	const { result } = renderHook(() => useCloudOrg(), { wrapper });
	expect(result.current.isLoading).toBe(false);
});
