import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { act, renderHook, waitFor } from "@testing-library/react";
import type { ReactNode } from "react";
import { expect, it, vi } from "vitest";
import { reviewerConversationQueryKey, useReviewerConversationCommands } from "./useReviewerConversation";

const mocks = vi.hoisted(() => ({ patch: vi.fn(), client: vi.fn() }));
vi.mock("../lib/host-clients", () => ({ clientForSessionHost: mocks.client }));

it("keeps a settings response scoped to the review host that submitted it", async () => {
	let finish!: (value: { data: object }) => void;
	mocks.patch.mockReturnValue(new Promise((resolve) => { finish = resolve; }));
	mocks.client.mockReturnValue({ PATCH: mocks.patch });
	const client = new QueryClient({ defaultOptions: { mutations: { retry: false } } });
	const invalidate = vi.spyOn(client, "invalidateQueries");
	const wrapper = ({ children }: { children: ReactNode }) => <QueryClientProvider client={client}>{children}</QueryClientProvider>;
	const { result, rerender } = renderHook(({ hostId }) => useReviewerConversationCommands("same-review", hostId), {
		initialProps: { hostId: "host-a" }, wrapper,
	});
	act(() => result.current.chooseSettings({ model: "review-model", reasoningEffort: "high", approvalMode: "auto" }));
	await waitFor(() => expect(mocks.patch).toHaveBeenCalled());
	expect(mocks.client).toHaveBeenCalledWith("host-a");
	expect(mocks.patch).toHaveBeenCalledWith("/api/v1/reviews/{reviewId}/conversation/settings", {
		params: { path: { reviewId: "same-review" } },
		body: { model: "review-model", reasoningEffort: "high", approvalMode: "auto" },
	});
	rerender({ hostId: "host-b" });
	await act(async () => finish({ data: {} }));
	await waitFor(() => expect(invalidate).toHaveBeenCalledWith({ queryKey: reviewerConversationQueryKey("same-review", "host-a") }));
	expect(invalidate).not.toHaveBeenCalledWith({ queryKey: reviewerConversationQueryKey("same-review", "host-b") });
	client.clear();
});
