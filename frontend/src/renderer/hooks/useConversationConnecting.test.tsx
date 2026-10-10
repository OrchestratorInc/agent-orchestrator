import { act, cleanup, render, screen } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { ChatWorkspace } from "../components/chat/ChatWorkspace";
import { TooltipProvider } from "../components/ui/tooltip";
import { createEventTransport } from "../lib/event-transport";
import { useConversation, useConversationCommands } from "./useConversation";

const { getMock, postMock } = vi.hoisted(() => ({ getMock: vi.fn(), postMock: vi.fn() }));

vi.mock("../lib/api-client", async (importOriginal) => ({
	...await importOriginal<typeof import("../lib/api-client")>(),
	apiClient: { GET: getMock, POST: postMock },
	getApiBaseUrl: () => "http://127.0.0.1:3001",
	hasTrustedApiBaseUrl: () => true,
}));

class EventSourceStub extends EventTarget {
	static instances: EventSourceStub[] = [];
	readyState = 1;
	onopen: (() => void) | null = null;
	onerror: (() => void) | null = null;
	onmessage: ((event: Event) => void) | null = null;
	constructor(readonly url: string) {
		super();
		EventSourceStub.instances.push(this);
	}
	close() { this.readyState = 2; }
}

const wire = {
	conversationId: "connecting-repro-conversation",
	sessionId: "connecting-repro-session",
	harness: "codex",
	mode: "chat",
	controller: "connecting",
	latestSequence: 0,
	settings: {},
	turns: [],
	messages: [],
	activities: [],
};

function LiveConversation() {
	const { snapshot } = useConversation(wire.sessionId);
	const commands = useConversationCommands(wire.sessionId);
	return snapshot ? (
		<TooltipProvider>
			<ChatWorkspace
				snapshot={snapshot}
				onSend={(text, attachments, clientMessageId) => commands.send({ text, attachments, clientMessageId })}
			/>
		</TooltipProvider>
	) : null;
}

let queryClient: QueryClient;
let disconnect: (() => void) | undefined;
let serverController: string;

beforeEach(() => {
	vi.useFakeTimers();
	vi.stubGlobal("EventSource", EventSourceStub);
	EventSourceStub.instances = [];
	serverController = "connecting";
	getMock.mockReset().mockImplementation(async () => ({ data: { ...wire, controller: serverController } }));
	postMock.mockReset().mockResolvedValue({ data: { duplicate: false, turnId: "repro-sent-turn" } });
	queryClient = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } });
});

afterEach(() => {
	disconnect?.();
	disconnect = undefined;
	cleanup();
	queryClient.clear();
	vi.unstubAllGlobals();
	vi.useRealTimers();
});

async function openWhileConnecting() {
	disconnect = createEventTransport(queryClient).connect();
	render(<QueryClientProvider client={queryClient}><LiveConversation /></QueryClientProvider>);
	await act(async () => { await vi.advanceTimersByTimeAsync(10); });
	expect(screen.getByText("Connecting to the agent…")).toBeInTheDocument();
}

type SessionEventKind = "ancillary" | "provisioned";

async function publishReadySession(kind: SessionEventKind = "ancillary", conversationChanged = false) {
	serverController = "ready";
	const source = EventSourceStub.instances.find((entry) => entry.url.endsWith("/api/v1/events"));
	if (!source) throw new Error("Missing CDC event source");
	await act(async () => {
		source.dispatchEvent(new MessageEvent("session_updated", { data: JSON.stringify({
			seq: 1,
			projectId: "connecting-repro-project",
			type: "session_updated",
			sessionId: wire.sessionId,
			// Neither this ancillary update nor the async spawn readiness
			// update carries conversationId; both must refresh the open chat.
			payload: {
				id: wire.sessionId,
				...(kind === "provisioned" ? {
					activity: "idle",
					isTerminated: false,
					terminateOnPrMerge: false,
					previewUrl: "",
					previewRevision: 0,
					isPinned: false,
					mode: "chat",
					autoInjectReview: false,
					autoInjectCI: false,
					autoReviewEnabled: false,
					provisionState: "ready",
				} : {}),
				...(conversationChanged ? { conversationId: wire.conversationId } : {}),
			},
			createdAt: "2026-10-04T00:00:00Z",
		}) }));
		await vi.advanceTimersByTimeAsync(60_000);
	});
}

describe("conversation readiness updates", () => {
	it.each(["ancillary", "provisioned"] as const)("clears connecting after ready is followed by the %s session update", async (kind) => {
		await openWhileConnecting();
		await publishReadySession(kind);
		expect(getMock).toHaveBeenCalledTimes(2);
		expect(postMock).not.toHaveBeenCalled();
		expect(screen.queryByText("Connecting to the agent…")).not.toBeInTheDocument();
	});

	it("clears connecting if the readiness event also identifies its conversation", async () => {
		await openWhileConnecting();
		await publishReadySession("ancillary", true);
		expect(getMock).toHaveBeenCalledTimes(2);
		expect(screen.queryByText("Connecting to the agent…")).not.toBeInTheDocument();
	});

	it("does not show connecting if the first fetch observes ready", async () => {
		serverController = "ready";
		disconnect = createEventTransport(queryClient).connect();
		render(<QueryClientProvider client={queryClient}><LiveConversation /></QueryClientProvider>);
		await act(async () => { await vi.advanceTimersByTimeAsync(60_000); });
		expect(screen.getByRole("combobox", { name: "Message the agent" })).toBeInTheDocument();
		expect(getMock).toHaveBeenCalledTimes(1);
		expect(screen.queryByText("Connecting to the agent…")).not.toBeInTheDocument();
	});
});
