import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { renderHook } from "@testing-library/react";
import { createElement, type ReactNode } from "react";
import { beforeEach, expect, it, vi } from "vitest";
import { useAttachmentLeases, useStageAttachments } from "./useConversation";

const { post } = vi.hoisted(() => ({ post: vi.fn() }));
vi.mock("../lib/host-clients", async (importOriginal) => {
	const actual = await importOriginal<typeof import("../lib/host-clients")>();
	return { ...actual, clientForSessionHost: () => ({ POST: post }) };
});
vi.mock("../lib/attachment-uploads", async (importOriginal) => {
	const actual = await importOriginal<typeof import("../lib/attachment-uploads")>();
	return { ...actual, toWireAttachments: async (items: unknown[]) => items };
});
vi.mock("../lib/telemetry", () => ({ captureRendererEvent: vi.fn() }));

beforeEach(() => post.mockReset());

const file = { mimeType: "text/plain", data: "aGk=" };

function wrapper({ children }: { children: ReactNode }) {
	return createElement(QueryClientProvider, { client: new QueryClient() }, children);
}

it("leases files only when staged for a draft", async () => {
	post.mockResolvedValue({ data: { sessionId: "s-1", paths: [".ao/attachments/attachment-a.txt"] } });
	const plain = renderHook(() => useStageAttachments("s-1"), { wrapper }).result.current;
	const draft = renderHook(() => useStageAttachments("s-1", undefined, false, true), { wrapper }).result.current;
	await plain([file]);
	await draft([file]);
	expect(post.mock.calls[0]?.[1]).toMatchObject({ body: { attachments: [file] } });
	expect(post.mock.calls[0]?.[1].body).not.toHaveProperty("draft");
	expect(post.mock.calls[1]?.[1]).toMatchObject({ body: { attachments: [file], draft: true } });
});

it("releases and renews draft files and reports the gone ones", async () => {
	post.mockResolvedValue({ data: { missing: [".ao/attachments/attachment-b.txt"] } });
	const update = renderHook(() => useAttachmentLeases("s-1")).result.current;
	const missing = await update([".ao/attachments/attachment-a.txt"], [".ao/attachments/attachment-b.txt"]);
	expect(missing).toEqual([".ao/attachments/attachment-b.txt"]);
	expect(post).toHaveBeenCalledWith("/api/v1/sessions/{sessionId}/attachments/leases", {
		params: { path: { sessionId: "s-1" } },
		body: { release: [".ao/attachments/attachment-a.txt"], renew: [".ao/attachments/attachment-b.txt"] },
	});
	// Nothing to say means no request.
	expect(await update([], [])).toEqual([]);
	expect(post).toHaveBeenCalledOnce();
});
