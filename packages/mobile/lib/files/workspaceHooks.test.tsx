import { useEffect, useLayoutEffect } from "react";
import { act, create, type ReactTestRenderer } from "react-test-renderer";
import { afterEach, beforeEach, expect, it, vi } from "vitest";
import type { WorkspaceManifest } from "./api";
import type { WorkspaceSource } from "./workspaceSource";

const lifecycle = vi.hoisted(() => ({ focused: true, state: "active", listeners: new Set<(state: string) => void>() }));
vi.mock("expo-router", () => ({ useFocusEffect: (effect: () => void | (() => void)) => useEffect(() => lifecycle.focused ? effect() : undefined, [effect, lifecycle.focused]) }));
vi.mock("react-native", () => ({ AppState: { get currentState() { return lifecycle.state; }, addEventListener: (_: string, listener: (state: string) => void) => { lifecycle.listeners.add(listener); return { remove: () => lifecycle.listeners.delete(listener) }; } } }));
// Network access lives behind the source. A regression to the daemon API cannot supply Cloud data.
vi.mock("./api", () => ({ getWorkspaceManifest: async () => { throw new Error("Unexpected daemon request"); } }));
import { useWorkspaceManifest } from "./useWorkspace";
import { useChangeSummary } from "./useChangeSummary";

function deferred<T>() { let resolve!: (value: T) => void; const promise = new Promise<T>((done) => { resolve = done; }); return { promise, resolve }; }
const manifest = (version: string, files = 1): WorkspaceManifest => ({ sessionId: "same", files: [], sections: { committed: [], staged: [], unstaged: [], untracked: [] }, summary: { files, additions: 3, deletions: 1 }, truncated: false, workspaceVersion: version, degraded: false });
function fixture(scopeKey: string) {
	let current = true;
	const requests: Array<{ signal?: AbortSignal; resolve(value: WorkspaceManifest): void }> = [];
	const source = { scopeKey, isCurrent: () => current, getManifest: (_id: string, options?: { signal?: AbortSignal }) => { const request = deferred<WorkspaceManifest>(); requests.push({ signal: options?.signal, resolve: request.resolve }); return request.promise; } } as WorkspaceSource;
	return { source, requests, retire: () => { current = false; } };
}
let renderer: ReactTestRenderer | undefined;
let workspace: ReturnType<typeof useWorkspaceManifest>;
let summary: ReturnType<typeof useChangeSummary>;
let renders = 0;
function Harness({ source, sessionId = "same", signal = "turn-1" }: { source: WorkspaceSource | null; sessionId?: string; signal?: string }) {
	useLayoutEffect(() => { renders++; });
	workspace = useWorkspaceManifest(source, sessionId);
	summary = useChangeSummary(source, sessionId, signal);
	return null;
}
async function render(source: WorkspaceSource | null, signal = "turn-1") { await act(async () => { const element = <Harness source={source} signal={signal} />; if (renderer) renderer.update(element); else renderer = create(element); }); }
async function resolveAll(requests: ReturnType<typeof fixture>["requests"], value: WorkspaceManifest) { await act(async () => { for (const request of requests) request.resolve(value); }); }
async function appState(state: string) { await act(async () => { lifecycle.state = state; for (const listener of lifecycle.listeners) listener(state); }); }
beforeEach(() => { vi.stubGlobal("IS_REACT_ACT_ENVIRONMENT", true); vi.useFakeTimers(); lifecycle.focused = true; lifecycle.state = "active"; renders = 0; });
afterEach(async () => { await act(async () => renderer?.unmount()); renderer = undefined; lifecycle.listeners.clear(); vi.useRealTimers(); vi.unstubAllGlobals(); });

it("reads a workspace through its source and hides another account's same-ID results immediately", async () => {
	const first = fixture("account-one"), second = fixture("account-two");
	await render(first.source); await resolveAll(first.requests, manifest("one"));
	expect(workspace.manifest?.workspaceVersion).toBe("one"); expect(summary?.files).toBe(1);
	await render(second.source);
	expect(workspace.manifest).toBeNull(); expect(summary).toBeNull(); expect(workspace.loading).toBe(true);
	await resolveAll(second.requests, manifest("two", 2));
	expect(workspace.manifest?.workspaceVersion).toBe("two"); expect(summary?.files).toBe(2);
});

it("aborts reads and rejects late data after logout", async () => {
	const first = fixture("account-one"); await render(first.source);
	await render(null);
	expect(first.requests.length).toBeGreaterThan(0);
	expect(first.requests.every((request) => request.signal?.aborted)).toBe(true);
	await resolveAll(first.requests, manifest("retired"));
	expect(workspace.manifest).toBeNull(); expect(summary).toBeNull();
});

it("does not publish a response from a retired login before the source rerenders", async () => {
	const first = fixture("account-one"); await render(first.source); first.retire();
	expect(first.requests.length).toBeGreaterThan(0);
	await resolveAll(first.requests, manifest("retired"));
	expect(workspace.manifest).toBeNull(); expect(summary).toBeNull();
});

it("does not overlap manifest polls and resumes reads after backgrounding", async () => {
	const first = fixture("account-one"); await render(first.source);
	const initialCount = first.requests.length;
	await act(async () => { await vi.advanceTimersByTimeAsync(15_000); });
	expect(first.requests).toHaveLength(initialCount);
	await appState("background");
	expect(first.requests.every((request) => request.signal?.aborted)).toBe(true);
	await resolveAll(first.requests, manifest("background"));
	expect(workspace.manifest).toBeNull(); expect(summary).toBeNull();
	await appState("active");
	expect(first.requests.length).toBeGreaterThan(initialCount);
	await resolveAll(first.requests.slice(initialCount), manifest("foreground"));
	expect(workspace.manifest?.workspaceVersion).toBe("foreground");
});

it("drops late reads when the Files screen loses focus", async () => {
	const first = fixture("account-one"); await render(first.source);
	expect(first.requests.length).toBeGreaterThan(0);
	lifecycle.focused = false; await render(first.source);
	expect(first.requests.every((request) => request.signal?.aborted)).toBe(true);
	await resolveAll(first.requests, manifest("blurred"));
	expect(workspace.manifest).toBeNull(); expect(summary).toBeNull();
});

it("keeps an idle Files screen still when polling returns the same workspace version", async () => {
	const stable = (): WorkspaceManifest => ({ ...manifest("stable"), repositories: [{ name: "main", main: true, present: true }] });
	const first = fixture("idle-account"); await render(first.source); await resolveAll(first.requests, stable());
	const before = renders, requestsBefore = first.requests.length;
	await act(async () => { await vi.advanceTimersByTimeAsync(5_000); });
	expect(first.requests.length).toBeGreaterThan(requestsBefore);
	await resolveAll(first.requests.slice(requestsBefore), stable());
	expect(renders).toBe(before);
});

it.each([
	{ change: "availability", repository: { name: "shared-api", label: "Shared API", repository: "acme/shared", branch: "main", present: true } },
	{ change: "display name", repository: { name: "shared-api", label: "Renamed API", repository: "acme/shared", branch: "main", present: false } },
	{ change: "branch", repository: { name: "shared-api", label: "Shared API", repository: "acme/shared", branch: "develop", present: false } },
])("updates repository $change even when the workspace's files keep the same version", async ({ repository }) => {
	const first = fixture("catalog-account"); await render(first.source);
	await resolveAll(first.requests, { ...manifest("stable"), repositories: [{ name: "shared-api", label: "Shared API", repository: "acme/shared", branch: "main", present: false }] });
	const requestsBefore = first.requests.length;
	await act(async () => { await vi.advanceTimersByTimeAsync(5_000); });
	await resolveAll(first.requests.slice(requestsBefore), { ...manifest("stable"), repositories: [repository] });
	expect(workspace.manifest?.repositories).toEqual([repository]);
});

it("keeps an unchanged changes pill still after a new turn signal", async () => {
	const first = fixture("summary-account"); await render(first.source); await resolveAll(first.requests, manifest("stable"));
	const requestsBefore = first.requests.length;
	await render(first.source, "turn-2"); const before = renders;
	await resolveAll(first.requests.slice(requestsBefore), manifest("next-version"));
	expect(renders).toBe(before);
});
