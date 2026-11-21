import { act, renderHook, waitFor } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import type { ReactNode } from "react";
import { expect, it, vi } from "vitest";
import { useChatPermissionModes } from "./useChatPermissionModes";

const h = vi.hoisted(() => ({ get: vi.fn() }));
vi.mock("../lib/host-clients", () => ({
	clientForSessionHost: () => ({ GET: h.get }),
}));

it("does not reuse another agent's modes while Kimi's capability request is pending", async () => {
	let answerKimi!: (value: unknown) => void;
	h.get.mockImplementation((_path: string, options: { params: { query: { agents: string } } }) => {
		if (options.params.query.agents === "kimi") return new Promise((resolve) => { answerKimi = resolve; });
		return Promise.resolve({ data: { agents: [{ agent: "claude-code", modes: ["default", "accept-edits", "auto", "bypass-permissions"] }] } });
	});
	const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
	const wrapper = ({ children }: { children: ReactNode }) => <QueryClientProvider client={client}>{children}</QueryClientProvider>;
	const { result, rerender, unmount } = renderHook(({ harness }) => useChatPermissionModes(undefined, harness), {
		wrapper, initialProps: { harness: "claude-code" },
	});
	await waitFor(() => expect(result.current?.get("claude-code")).toEqual(["default", "accept-edits", "auto", "bypass-permissions"]));
	rerender({ harness: "kimi" });
	expect(result.current).toBeUndefined();
	await waitFor(() => expect(h.get).toHaveBeenCalledWith("/api/v1/agents/chat/permission-modes", {
		params: { query: { agents: "kimi" } },
	}));
	await act(async () => { answerKimi({ data: { agents: [{ agent: "kimi", modes: ["default"] }] } }); });
	await waitFor(() => expect(result.current?.get("kimi")).toEqual(["default"]));
	expect(result.current?.has("claude-code")).toBe(false);
	unmount();
	client.clear();
});
