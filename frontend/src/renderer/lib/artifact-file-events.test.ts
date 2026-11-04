import type { Query } from "@tanstack/react-query";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

const { eventStreamBaseUrlMock, getApiBaseUrlMock, hasTrustedApiBaseUrlMock, subscribeApiBaseUrlMock, unsubscribeBaseUrlMock } = vi.hoisted(() => ({
	eventStreamBaseUrlMock: vi.fn((baseUrl: string) => baseUrl.replace(/\/+$/, "")),
	getApiBaseUrlMock: vi.fn(() => "http://127.0.0.1:3001"),
	hasTrustedApiBaseUrlMock: vi.fn(() => true),
	subscribeApiBaseUrlMock: vi.fn(),
	unsubscribeBaseUrlMock: vi.fn(),
}));

vi.mock("./api-client", () => ({
	eventStreamBaseUrl: eventStreamBaseUrlMock,
	getApiBaseUrl: getApiBaseUrlMock,
	hasTrustedApiBaseUrl: hasTrustedApiBaseUrlMock,
	subscribeApiBaseUrl: subscribeApiBaseUrlMock,
}));
vi.mock("../hooks/useWorkspaceQuery", () => ({ workspaceQueryKey: ["workspaces"] }));
vi.mock("./sse-backoff", () => ({ computeSseRetryDelayMs: () => 5_000 }));

import { subscribeArtifactFileChanges } from "./artifact-file-events";

let baseUrlListener: (() => void) | undefined;
let subscriptions: Array<() => void> = [];
const subscribe = (...args: Parameters<typeof subscribeArtifactFileChanges>) => {
	const unsubscribe = subscribeArtifactFileChanges(...args);
	subscriptions.push(unsubscribe);
	return unsubscribe;
};

class EventSourceStub {
	static instances: EventSourceStub[] = [];
	url: string;
	closed = false;
	readyState = 0;
	onerror: (() => void) | null = null;
	listeners = new Map<string, Set<() => void>>();

	constructor(url: string) {
		this.url = url;
		EventSourceStub.instances.push(this);
	}

	addEventListener(type: string, listener: () => void) {
		const listeners = this.listeners.get(type) ?? new Set();
		listeners.add(listener);
		this.listeners.set(type, listeners);
	}

	dispatch(type: string) {
		for (const listener of this.listeners.get(type) ?? []) listener();
	}

	close() {
		this.closed = true;
		this.readyState = 2;
	}
}

type Filters = { queryKey?: readonly unknown[]; predicate?: (query: Query) => boolean };

function fakeQueryClient() {
	const invalidateQueries = vi.fn((_filters: Filters) => Promise.resolve());
	const client = { invalidateQueries } as unknown as Parameters<typeof subscribeArtifactFileChanges>[1];
	const fileRefreshes = () => invalidateQueries.mock.calls.filter(([filters]) => filters.predicate).map(([filters]) => filters.predicate!);
	const listRefreshes = () => invalidateQueries.mock.calls.filter(([filters]) => filters.queryKey?.[0] === "workspaces").length;
	return { client, fileRefreshes, listRefreshes };
}

const matches = (predicate: (query: Query) => boolean, queryKey: unknown[]) => predicate({ queryKey } as unknown as Query);

beforeEach(() => {
	vi.useFakeTimers();
	EventSourceStub.instances = [];
	baseUrlListener = undefined;
	eventStreamBaseUrlMock.mockReset().mockImplementation((baseUrl: string) => baseUrl.replace(/\/+$/, ""));
	getApiBaseUrlMock.mockReset().mockReturnValue("http://127.0.0.1:3001");
	hasTrustedApiBaseUrlMock.mockReset().mockReturnValue(true);
	subscribeApiBaseUrlMock.mockReset().mockImplementation((listener: () => void) => {
		baseUrlListener = listener;
		return unsubscribeBaseUrlMock;
	});
	unsubscribeBaseUrlMock.mockReset();
	(globalThis as unknown as { EventSource: unknown }).EventSource = EventSourceStub;
});

afterEach(() => {
	for (const unsubscribe of subscriptions) unsubscribe();
	subscriptions = [];
	vi.useRealTimers();
});

describe("subscribeArtifactFileChanges", () => {
	it("shares one stream per session and closes it with the last view", () => {
		const { client } = fakeQueryClient();
		const first = subscribe("ao-1", client);
		const second = subscribe("ao-1", client);

		expect(EventSourceStub.instances).toHaveLength(1);
		expect(EventSourceStub.instances[0].url).toBe("http://127.0.0.1:3001/api/v1/sessions/ao-1/artifact-events");

		first();
		first();
		expect(EventSourceStub.instances[0].closed).toBe(false);
		second();
		expect(EventSourceStub.instances[0].closed).toBe(true);
		expect(unsubscribeBaseUrlMock).toHaveBeenCalledTimes(1);
	});

	it("opens on the event stream host, so it does not hold one of the API host's connections", () => {
		eventStreamBaseUrlMock.mockReturnValue("http://ao-events.localhost:3001");
		const { client } = fakeQueryClient();
		subscribe("ao-1", client);

		expect(eventStreamBaseUrlMock).toHaveBeenCalledWith("http://127.0.0.1:3001");
		expect(EventSourceStub.instances[0].url).toBe("http://ao-events.localhost:3001/api/v1/sessions/ao-1/artifact-events");

		// The same daemon address does not rebuild the stream.
		baseUrlListener?.();
		expect(EventSourceStub.instances).toHaveLength(1);
	});

	it("rereads only that session's artifact files when one changes", () => {
		const { client, fileRefreshes, listRefreshes } = fakeQueryClient();
		const unsubscribe = subscribe("ao-1", client);
		const stream = EventSourceStub.instances[0];

		stream.dispatch("artifacts_changed");
		stream.dispatch("artifacts_changed");
		expect(fileRefreshes()).toHaveLength(0);
		vi.advanceTimersByTime(150);

		const [predicate] = fileRefreshes();
		expect(fileRefreshes()).toHaveLength(1);
		expect(matches(predicate, ["session-artifact-file", "", "ao-1", "plan.md"])).toBe(true);
		expect(matches(predicate, ["session-artifact-file", "", "ao-2", "plan.md"])).toBe(false);
		expect(matches(predicate, ["session-workspace-file", "ao-1", "plan.md"])).toBe(false);
		expect(listRefreshes()).toBe(1);
		unsubscribe();
	});

	it("keeps an agent writing continuously visible while throttling the list", () => {
		const { client, fileRefreshes, listRefreshes } = fakeQueryClient();
		const unsubscribe = subscribe("ao-1", client);
		const stream = EventSourceStub.instances[0];

		stream.dispatch("ready");
		for (let elapsed = 0; elapsed < 900; elapsed += 100) {
			vi.advanceTimersByTime(100);
			stream.dispatch("artifacts_changed");
		}
		// The file is reread 150ms after the first event of each window: at
		// 150, 350, 550 and 750ms by 900ms, and the last write at 950ms.
		expect(fileRefreshes()).toHaveLength(4);
		// The list reads once on ready, then once more when its second ends.
		expect(listRefreshes()).toBe(1);
		vi.advanceTimersByTime(100);
		expect(listRefreshes()).toBe(2);
		vi.advanceTimersByTime(2_000);
		expect(fileRefreshes()).toHaveLength(5);
		expect(listRefreshes()).toBe(2);
		unsubscribe();
	});

	it("rebuilds a stream the daemon refused, after a backoff", () => {
		const { client } = fakeQueryClient();
		const unsubscribe = subscribe("ao-1", client);
		const refused = EventSourceStub.instances[0];

		refused.readyState = 2;
		refused.onerror?.();
		expect(EventSourceStub.instances).toHaveLength(1);
		vi.advanceTimersByTime(5_000);
		expect(EventSourceStub.instances).toHaveLength(2);
		expect(EventSourceStub.instances[1].url).toBe(refused.url);
		unsubscribe();
		expect(EventSourceStub.instances[1].closed).toBe(true);
	});

	it("waits for the daemon's address and follows it when it moves", () => {
		hasTrustedApiBaseUrlMock.mockReturnValue(false);
		const { client } = fakeQueryClient();
		const unsubscribe = subscribe("ao-1", client);
		expect(EventSourceStub.instances).toHaveLength(0);

		hasTrustedApiBaseUrlMock.mockReturnValue(true);
		baseUrlListener?.();
		expect(EventSourceStub.instances).toHaveLength(1);

		getApiBaseUrlMock.mockReturnValue("http://127.0.0.1:4002");
		baseUrlListener?.();
		expect(EventSourceStub.instances[0].closed).toBe(true);
		expect(EventSourceStub.instances[1].url).toBe("http://127.0.0.1:4002/api/v1/sessions/ao-1/artifact-events");
		unsubscribe();
	});
});
