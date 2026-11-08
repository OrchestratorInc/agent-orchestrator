import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { act, renderHook } from "@testing-library/react";
import type { ReactNode } from "react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { CloudCpClientEvent } from "../lib/cloud-cp";
import type { CloudSessionEventsOptions } from "./useCloudSessionEvents";

const mocks = vi.hoisted(() => ({
	subscribeLocal: vi.fn(),
	unsubscribeLocal: vi.fn(),
	cloudListener: vi.fn(),
}));

vi.mock("../lib/artifact-file-events", () => ({ subscribeArtifactFileChanges: mocks.subscribeLocal }));
vi.mock("./useCloudCp", () => ({ useCloudCp: () => ({ baseUrl: "https://cp.test" }) }));
vi.mock("./useCloudSessionEvents", () => ({ useCloudSessionEventListener: mocks.cloudListener }));

import { useArtifactFileChanges } from "./useArtifactFileChanges";

const cloudSession = { id: "cloud-1", cloud: { orgId: "org-1" } } as Parameters<typeof useArtifactFileChanges>[0];

function event(type: string, sequence: number): CloudCpClientEvent {
	return { sessionId: "cloud-1", sequence, type, payload: {}, createdAt: "2026-11-03T00:00:00Z" };
}

/** The options of the newest render's cloud listener. */
function cloudListener(): CloudSessionEventsOptions {
	return mocks.cloudListener.mock.lastCall![0] as CloudSessionEventsOptions;
}

function render(session: Parameters<typeof useArtifactFileChanges>[0], enabled: boolean) {
	const client = new QueryClient();
	const invalidate = vi.spyOn(client, "invalidateQueries").mockResolvedValue();
	const wrapper = ({ children }: { children: ReactNode }) => <QueryClientProvider client={client}>{children}</QueryClientProvider>;
	const view = renderHook(({ on }) => useArtifactFileChanges(session, on), { initialProps: { on: enabled }, wrapper });
	return { ...view, client, invalidate };
}

beforeEach(() => {
	vi.useFakeTimers();
	mocks.subscribeLocal.mockReset().mockReturnValue(mocks.unsubscribeLocal);
	mocks.unsubscribeLocal.mockReset();
	mocks.cloudListener.mockReset();
});

afterEach(() => {
	vi.useRealTimers();
});

describe("useArtifactFileChanges", () => {
	it("follows a local session's files through the daemon only while enabled", () => {
		const { client, rerender, unmount } = render({ id: "sess-1" }, false);
		expect(mocks.subscribeLocal).not.toHaveBeenCalled();

		rerender({ on: true });
		expect(mocks.subscribeLocal).toHaveBeenCalledWith("sess-1", client);
		expect(cloudListener().enabled).toBe(false);

		unmount();
		expect(mocks.unsubscribeLocal).toHaveBeenCalledTimes(1);
	});

	it("follows a cloud session's files through its event stream, not the daemon", () => {
		render(cloudSession, true);

		expect(mocks.subscribeLocal).not.toHaveBeenCalled();
		expect(cloudListener()).toMatchObject({ baseUrl: "https://cp.test", orgId: "org-1", sessionId: "cloud-1", enabled: true });
	});

	it("folds a burst of artifact changes into one reread of the session's file list", () => {
		const { invalidate } = render(cloudSession, true);

		act(() => {
			for (let sequence = 1; sequence <= 5; sequence += 1) cloudListener().onEvent?.(event("artifact.changed", sequence));
		});
		expect(invalidate).not.toHaveBeenCalled();
		act(() => {
			vi.advanceTimersByTime(250);
		});

		expect(invalidate).toHaveBeenCalledTimes(1);
		expect(invalidate).toHaveBeenCalledWith({ queryKey: ["cloud-artifact-files", "https://cp.test", "org-1", "cloud-1"] });

		act(() => {
			cloudListener().onEvent?.(event("artifact.changed", 6));
			vi.advanceTimersByTime(250);
		});
		expect(invalidate).toHaveBeenCalledTimes(2);
	});

	it("ignores the session's other events", () => {
		const { invalidate } = render(cloudSession, true);

		act(() => {
			cloudListener().onEvent?.(event("chat.assistant_delta", 1));
			cloudListener().onEvent?.(event("workspace.changed", 2));
			vi.advanceTimersByTime(1_000);
		});
		expect(invalidate).not.toHaveBeenCalled();
	});
});
