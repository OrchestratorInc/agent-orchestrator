import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { render, screen, waitFor } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import type { ArtifactRow } from "../lib/artifacts";
import { ArtifactSnapshotThumbnail } from "./ArtifactSnapshotThumbnail";

const mocks = vi.hoisted(() => ({ get: vi.fn(), request: vi.fn() }));

vi.mock("../lib/bridge", () => ({ aoBridge: { artifactSnapshot: { get: mocks.get, request: mocks.request } } }));
vi.mock("./ArtifactThumbnail", () => ({ ArtifactThumbnail: () => <div data-testid="live-thumbnail" /> }));

class FakeObserver {
	constructor(private readonly callback: (entries: Array<{ isIntersecting: boolean }>) => void) {}
	observe() {
		this.callback([{ isIntersecting: true }]);
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

function renderCard(artifact: ArtifactRow) {
	return render(
		<QueryClientProvider client={new QueryClient()}>
			<ArtifactSnapshotThumbnail row={artifact} />
		</QueryClientProvider>,
	);
}

beforeEach(() => {
	vi.clearAllMocks();
	vi.stubGlobal("IntersectionObserver", FakeObserver);
	vi.stubGlobal("URL", Object.assign(URL, { createObjectURL: () => "blob:snapshot", revokeObjectURL: () => undefined }));
});

describe("ArtifactSnapshotThumbnail", () => {
	it("shows the cached image without asking main to render", async () => {
		mocks.get.mockResolvedValue(new Uint8Array([1, 2, 3]));
		const { container } = renderCard(row({ path: "cached.md" }));
		await waitFor(() => expect(container.querySelector("img")?.getAttribute("src")).toBe("blob:snapshot"));
		expect(mocks.request).not.toHaveBeenCalled();
		expect(screen.queryByTestId("live-thumbnail")).toBeNull();
	});

	it("asks main to render a preview that is not cached yet", async () => {
		mocks.get.mockResolvedValue(null);
		mocks.request.mockResolvedValue(new Uint8Array([4]));
		const { container } = renderCard(row({ path: "new.md" }));
		await waitFor(() => expect(container.querySelector("img")).not.toBeNull());
		expect(mocks.request).toHaveBeenCalledTimes(1);
	});

	it("falls back to the live preview when main cannot render it", async () => {
		mocks.get.mockResolvedValue(null);
		mocks.request.mockResolvedValue(null);
		renderCard(row({ path: "broken.md" }));
		expect(await screen.findByTestId("live-thumbnail")).toBeTruthy();
	});

	it("renders another host's artifact live, never in the hidden window", () => {
		renderCard(row({ path: "remote.md", rawUrl: "https://host.example/api/v1/sessions/s1/preview/files/remote.md" }));
		expect(screen.getByTestId("live-thumbnail")).toBeTruthy();
		expect(mocks.get).not.toHaveBeenCalled();
	});
});
