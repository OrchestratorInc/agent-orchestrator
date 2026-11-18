import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { act, render, waitFor } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { useArtifactsViewStore } from "../stores/artifacts-view-store";
import { useUiStore } from "../stores/ui-store";
import { SharedArtifactLinkHost } from "./SharedArtifactLinkHost";

const mocks = vi.hoisted(() => ({
	ready: true,
	userId: "user-a",
	status: "authenticated" as "loading" | "authenticated" | "unauthenticated",
	pending: [] as string[],
	listener: undefined as ((viewerUrl: string) => void) | undefined,
	save: vi.fn(),
	navigate: vi.fn(),
	openWindow: vi.fn(),
}));
vi.mock("../hooks/useCloudCp", () => ({ useCloudCp: () => ({ client: { saveSharedArtifact: mocks.save }, ready: mocks.ready, userId: mocks.userId }) }));
vi.mock("../hooks/useCloudGate", () => ({ useCloudGate: () => ({ loading: false }) }));
vi.mock("../lib/cloud-session", () => ({ useCloudSession: () => ({ status: mocks.status }) }));
vi.mock("@tanstack/react-router", () => ({ useNavigate: () => mocks.navigate }));
vi.mock("../lib/bridge", () => ({
	aoBridge: {
		cloud: {
			onOpenSharedArtifact: (listener: (viewerUrl: string) => void) => {
				mocks.listener = listener;
				return () => { mocks.listener = undefined; };
			},
			// Like the main process: links are held in order until taken, once each.
			takePendingSharedArtifact: async () => mocks.pending.shift() ?? null,
			openSharedArtifactWindow: mocks.openWindow,
		},
	},
}));

const viewer = "https://orchestrator.inc/artifact/q3-report";
const saved = { linkId: "q3-report", name: "q3.html", contentType: "text/html", size: 1, updatedAt: "2030-01-01T00:00:00Z", access: "anyone", via: "saved", sharedAt: "2030-01-01T00:00:00Z", viewerUrl: viewer };

/** The main process holding a link, then telling the listening host to take it. */
async function send(viewerUrl: string) {
	mocks.pending.push(viewerUrl);
	await act(async () => mocks.listener?.(viewerUrl));
}

function renderHost() {
	return render(<QueryClientProvider client={new QueryClient()}><SharedArtifactLinkHost /></QueryClientProvider>);
}

describe("SharedArtifactLinkHost", () => {
	beforeEach(() => {
		mocks.ready = true;
		mocks.userId = "user-a";
		mocks.status = "authenticated";
		mocks.pending = [];
		mocks.save.mockReset();
		mocks.navigate.mockReset();
		mocks.openWindow.mockReset();
		useArtifactsViewStore.setState({ openSharedArtifact: null });
		useUiStore.getState().closeSettings();
	});

	it("saves a link sent from its web page and opens it on the Artifacts page", async () => {
		mocks.save.mockResolvedValue({ artifact: saved });
		renderHost();
		await send(viewer);
		await waitFor(() => expect(mocks.navigate).toHaveBeenCalledWith({ to: "/artifacts" }));
		// With a deadline, so a save that never answers cannot hold later links.
		expect(mocks.save).toHaveBeenCalledWith("q3-report", { signal: expect.any(AbortSignal) });
		expect(useArtifactsViewStore.getState().openSharedArtifact).toEqual(saved);
		expect(mocks.openWindow).not.toHaveBeenCalled();
	});

	it("saves a link that arrives while Settings is open, and opens it once Settings closes", async () => {
		mocks.save.mockResolvedValue({ artifact: saved });
		act(() => useUiStore.getState().openGlobalSettings());
		renderHost();
		await send(viewer);
		await waitFor(() => expect(mocks.save).toHaveBeenCalledTimes(1));
		await act(async () => {});
		// Settings stays as the person left it; nothing opens behind it.
		expect(useUiStore.getState().settingsModal).not.toBeNull();
		expect(mocks.navigate).not.toHaveBeenCalled();
		act(() => useUiStore.getState().closeSettings());
		await waitFor(() => expect(mocks.navigate).toHaveBeenCalledWith({ to: "/artifacts" }));
		expect(useArtifactsViewStore.getState().openSharedArtifact).toEqual(saved);
	});

	it("opens a link that arrived before the window, once the sign-in is read", async () => {
		mocks.save.mockResolvedValue({ artifact: saved });
		mocks.pending = [viewer];
		mocks.status = "loading";
		const { rerender } = renderHost();
		await act(async () => {});
		expect(mocks.save).not.toHaveBeenCalled();
		expect(mocks.openWindow).not.toHaveBeenCalled();
		mocks.status = "authenticated";
		rerender(<QueryClientProvider client={new QueryClient()}><SharedArtifactLinkHost /></QueryClientProvider>);
		await waitFor(() => expect(mocks.save).toHaveBeenCalledWith("q3-report", expect.anything()));
	});

	it("keeps every link that arrived before the window, and opens them in order", async () => {
		// Each save waits until the test finishes it, so they can finish out of order.
		const finish = new Map<string, () => void>();
		mocks.save.mockImplementation((linkId: string) => new Promise((resolve) => {
			finish.set(linkId, () => resolve({ artifact: { ...saved, linkId, viewerUrl: `https://orchestrator.inc/artifact/${linkId}` } }));
		}));
		const opened: string[] = [];
		const stop = useArtifactsViewStore.subscribe((state) => {
			if (state.openSharedArtifact) opened.push(state.openSharedArtifact.linkId);
		});
		mocks.pending = [viewer, "https://orchestrator.inc/artifact/roadmap"];
		renderHost();
		await waitFor(() => expect(finish.has("q3-report")).toBe(true));
		// The second is not saved until the first has finished.
		expect(mocks.save).toHaveBeenCalledTimes(1);
		await act(async () => finish.get("q3-report")?.());
		await waitFor(() => expect(finish.has("roadmap")).toBe(true));
		await act(async () => finish.get("roadmap")?.());
		await waitFor(() => expect(opened).toEqual(["q3-report", "roadmap"]));
		stop();

		expect(mocks.save.mock.calls.map(([linkId]) => linkId)).toEqual(["q3-report", "roadmap"]);
		expect(mocks.pending).toEqual([]);
	});

	it("drops a saved link when another account signed in while it was saving", async () => {
		let finish: (() => void) | undefined;
		mocks.save.mockImplementation(() => new Promise((resolve) => {
			finish = () => resolve({ artifact: saved });
		}));
		const { rerender } = renderHost();
		await send(viewer);
		await waitFor(() => expect(finish).toBeDefined());
		mocks.userId = "user-b";
		rerender(<QueryClientProvider client={new QueryClient()}><SharedArtifactLinkHost /></QueryClientProvider>);
		await act(async () => finish?.());
		expect(useArtifactsViewStore.getState().openSharedArtifact).toBeNull();
		expect(mocks.navigate).not.toHaveBeenCalled();
	});

	it("opens it in its own window when signed out of Cloud or when it does not open for this account", async () => {
		mocks.ready = false;
		mocks.status = "unauthenticated";
		const { unmount } = renderHost();
		await send(viewer);
		await waitFor(() => expect(mocks.openWindow).toHaveBeenCalledWith(viewer));
		expect(mocks.save).not.toHaveBeenCalled();
		unmount();

		mocks.ready = true;
		mocks.status = "authenticated";
		mocks.openWindow.mockReset();
		mocks.save.mockRejectedValue(new Error("not found"));
		renderHost();
		await send(viewer);
		await waitFor(() => expect(mocks.openWindow).toHaveBeenCalledWith(viewer));
		expect(mocks.navigate).not.toHaveBeenCalled();
	});

	it("opens a link from another origin in its own window, not as the control plane's link of that ID", async () => {
		mocks.save.mockResolvedValue({ artifact: saved });
		renderHost();
		const elsewhere = "https://elsewhere.example/artifact/q3-report";
		await send(elsewhere);
		await waitFor(() => expect(mocks.openWindow).toHaveBeenCalledWith(elsewhere));
		expect(mocks.navigate).not.toHaveBeenCalled();
		expect(useArtifactsViewStore.getState().openSharedArtifact).toBeNull();
	});

	it("ignores anything that is not a published artifact's viewer", async () => {
		renderHost();
		await send("https://evil.example/artifact/x?vt=1");
		await act(async () => {});
		expect(mocks.save).not.toHaveBeenCalled();
		expect(mocks.openWindow).not.toHaveBeenCalled();
	});
});
