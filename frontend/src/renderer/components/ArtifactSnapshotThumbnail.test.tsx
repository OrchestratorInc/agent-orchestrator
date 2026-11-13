import { act, fireEvent, render, screen } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { ArtifactRow } from "../lib/artifacts";
import { ArtifactSnapshotThumbnail } from "./ArtifactSnapshotThumbnail";

const mocks = vi.hoisted(() => ({ request: vi.fn(), state: "content" }));

vi.mock("../lib/bridge", () => ({ aoBridge: { artifactSnapshot: { request: mocks.request } } }));
vi.mock("./ArtifactThumbnail", () => ({ ArtifactThumbnail: () => <div data-testid="live-thumbnail" data-preview-state={mocks.state} /> }));

const observers: Array<(entries: Array<{ isIntersecting: boolean }>) => void> = [];
class FakeObserver {
	constructor(private readonly callback: (entries: Array<{ isIntersecting: boolean }>) => void) {}
	observe() {
		observers.push(this.callback);
	}
	disconnect() {}
}

function row(overrides: Partial<ArtifactRow> = {}): ArtifactRow {
	return {
		path: "report.md",
		name: "report.md",
		kind: "markdown",
		updatedAt: "2026-11-10T10:00:00Z",
		sessionId: "s1",
		sessionTitle: "Session",
		rawUrl: "http://127.0.0.1:3002/api/v1/sessions/s1/preview/files/report.md?source=artifact&raw=true",
		...overrides,
	};
}

/** The card's <img> reports the snapshot as not cached, as main's 404 does. */
function miss(container: HTMLElement) {
	fireEvent.error(container.querySelector("img")!);
}

const scrollIntoView = (visible = true) => act(() => observers.forEach((callback) => callback([{ isIntersecting: visible }])));

beforeEach(() => {
	vi.useFakeTimers();
	vi.clearAllMocks();
	observers.length = 0;
	mocks.state = "content";
	mocks.request.mockResolvedValue(true);
	vi.stubGlobal("IntersectionObserver", FakeObserver);
});

afterEach(() => {
	vi.useRealTimers();
	vi.unstubAllGlobals();
});

describe("ArtifactSnapshotThumbnail", () => {
	it("loads the cached image from the snapshot scheme, with no live preview and no request", () => {
		const { container } = render(<ArtifactSnapshotThumbnail row={row({ path: "cached.md" })} />);
		const src = container.querySelector("img")?.getAttribute("src") ?? "";
		expect(src.startsWith("ao-artifact-preview://cache/?")).toBe(true);
		expect(new URL(src).searchParams.get("p")).toBe("cached.md");
		expect(screen.queryByTestId("live-thumbnail")).toBeNull();
		expect(mocks.request).not.toHaveBeenCalled();
	});

	it("draws live at once when nothing is cached, and asks for the snapshot only after the card has stayed in view", async () => {
		const { container, unmount } = render(<ArtifactSnapshotThumbnail row={row({ path: "new.md" })} />);
		miss(container);
		expect(screen.getByTestId("live-thumbnail")).toBeTruthy();
		scrollIntoView();
		await act(() => vi.advanceTimersByTimeAsync(1_000));
		expect(mocks.request).not.toHaveBeenCalled();
		await act(() => vi.advanceTimersByTimeAsync(1_000));
		expect(mocks.request).toHaveBeenCalledTimes(1);
		expect(mocks.request.mock.calls[0]?.[0]).toMatchObject({ path: "new.md", theme: "dark" });
		unmount();
		// The next card for the file loads the image just rendered, with a fresh URL.
		const next = render(<ArtifactSnapshotThumbnail row={row({ path: "new.md" })} />);
		expect(next.container.querySelector("img")?.getAttribute("src")).toMatch(/&r=1$/);
	});

	it("does not render a card that only scrolled past", async () => {
		const { container } = render(<ArtifactSnapshotThumbnail row={row({ path: "passed.md" })} />);
		miss(container);
		scrollIntoView();
		await act(() => vi.advanceTimersByTimeAsync(500));
		scrollIntoView(false);
		await act(() => vi.advanceTimersByTimeAsync(5_000));
		expect(mocks.request).not.toHaveBeenCalled();
	});

	it("waits for the live preview to finish before asking, and skips a file with nothing to show", async () => {
		mocks.state = "loading";
		const loading = render(<ArtifactSnapshotThumbnail row={row({ path: "slow.md" })} />);
		miss(loading.container);
		scrollIntoView();
		await act(() => vi.advanceTimersByTimeAsync(4_000));
		expect(mocks.request).not.toHaveBeenCalled();
		loading.unmount();
		observers.length = 0;
		mocks.state = "unavailable";
		const empty = render(<ArtifactSnapshotThumbnail row={row({ path: "empty.md" })} />);
		miss(empty.container);
		scrollIntoView();
		await act(() => vi.advanceTimersByTimeAsync(4_000));
		expect(mocks.request).not.toHaveBeenCalled();
	});

	it("snapshots a card past the page's live-preview limit, whose only preview is the snapshot", async () => {
		mocks.state = "unavailable";
		const { container } = render(<ArtifactSnapshotThumbnail row={row({ path: "page.html", kind: "html" })} previewLive={false} />);
		miss(container);
		scrollIntoView();
		await act(() => vi.advanceTimersByTimeAsync(2_000));
		expect(mocks.request).toHaveBeenCalledTimes(1);
	});

	it("renders another host's artifact live, even with the *.localhost URL the remote proxy gives it", async () => {
		const { container } = render(<ArtifactSnapshotThumbnail row={row({ path: "remote.md", hostId: "host-2", rawUrl: "http://ao-preview-abc123.localhost:4100/api/v1/sessions/s1/preview/files/remote.md" })} />);
		expect(screen.getByTestId("live-thumbnail")).toBeTruthy();
		expect(container.querySelector("img")).toBeNull();
		scrollIntoView();
		await act(() => vi.advanceTimersByTimeAsync(4_000));
		expect(mocks.request).not.toHaveBeenCalled();
	});
});
