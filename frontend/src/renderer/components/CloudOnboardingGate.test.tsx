import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { render } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { cloudChatEventsKey, cloudChatHistoryKey } from "../lib/cloud-chat-history";
import { CloudOnboardingGate } from "./CloudOnboardingGate";

const state = vi.hoisted(() => ({ status: "authenticated" as "authenticated" | "unauthenticated" | "loading" }));
vi.mock("../lib/cloud-session", () => ({ useCloudSession: () => ({ status: state.status }) }));
vi.mock("../hooks/useCloudGate", () => ({ useCloudGate: () => ({ cloudEnabled: false }) }));

const stepsKey = ["cloud-script-steps", "org-1", "session-1"] as const;

function seeded() {
	const queryClient = new QueryClient();
	queryClient.setQueryData(cloudChatEventsKey("org-1", "session-1"), [{ sessionId: "session-1", sequence: 1, type: "workspace.script.step", payload: { output: "member only" }, createdAt: "2026-11-08T10:00:00Z" }]);
	queryClient.setQueryData(cloudChatHistoryKey("org-1", "session-1"), { hasOlder: true, nextBefore: 1, recentPending: false, loadingOlder: false, generation: 1 });
	queryClient.setQueryData(stepsKey, [{ sessionId: "session-1", sequence: 1, type: "workspace.script.step", payload: {}, createdAt: "2026-11-08T10:00:00Z" }]);
	return queryClient;
}

describe("CloudOnboardingGate", () => {
	beforeEach(() => {
		state.status = "authenticated";
	});

	it("drops every Cloud chat's events, history window and step log on sign-out", () => {
		const queryClient = seeded();
		const view = render(<QueryClientProvider client={queryClient}><CloudOnboardingGate /></QueryClientProvider>);
		// Signed in, the chats stay cached.
		expect(queryClient.getQueryData(cloudChatEventsKey("org-1", "session-1"))).toBeDefined();
		state.status = "unauthenticated";
		view.rerender(<QueryClientProvider client={queryClient}><CloudOnboardingGate /></QueryClientProvider>);
		expect(queryClient.getQueryData(cloudChatEventsKey("org-1", "session-1"))).toBeUndefined();
		expect(queryClient.getQueryData(cloudChatHistoryKey("org-1", "session-1"))).toBeUndefined();
		expect(queryClient.getQueryData(stepsKey)).toBeUndefined();
	});
});
