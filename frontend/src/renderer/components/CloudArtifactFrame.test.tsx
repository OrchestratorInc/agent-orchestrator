import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { act, fireEvent, render, screen } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { aoBridge } from "../lib/bridge";
import { artifactPathUnder, CloudArtifactFrame } from "./CloudArtifactFrame";

const mocks = vi.hoisted(() => ({ status: 200 }));
vi.mock("../lib/cloud-cp/renderer-client", () => ({ cloudCpFetch: async () => new Response(null, { status: mocks.status }) }));

const ROOT = "https://cp.test/api/cloud/v1/artifact-files/token/";

describe("CloudArtifactFrame", () => {
	let navigate: (navigation: { url: string; status: number }) => void = () => {};

	beforeEach(() => {
		mocks.status = 200;
		vi.spyOn(aoBridge.cloudArtifact, "onFrameNavigated").mockImplementation((listener) => {
			navigate = listener;
			return () => undefined;
		});
	});

	function renderFrame(props: { onAccessRefused?: () => void; onNavigate?: (path: string) => void }) {
		return render(
			<QueryClientProvider client={new QueryClient()}>
				<CloudArtifactFrame title="index.html" url={`${ROOT}index.html?v=1`} {...props} />
			</QueryClientProvider>,
		);
	}

	it("reports another file of its session that the page opened", async () => {
		const onNavigate = vi.fn();
		renderFrame({ onNavigate });
		await screen.findByTitle("index.html");
		act(() => navigate({ url: `${ROOT}docs/page%20two.html`, status: 200 }));
		expect(onNavigate).toHaveBeenCalledWith("docs/page two.html");
	});

	it("takes a fresh link when the page opened a file with an expired one", async () => {
		const onAccessRefused = vi.fn();
		const onNavigate = vi.fn();
		renderFrame({ onAccessRefused, onNavigate });
		await screen.findByTitle("index.html");
		act(() => navigate({ url: `${ROOT}page.html`, status: 401 }));
		expect(onAccessRefused).toHaveBeenCalledTimes(1);
		expect(onNavigate).toHaveBeenCalledWith("page.html");
	});

	it("tells a thumbnail's card whether the page is loading, framed, or replaced by the fallback", async () => {
		const renderThumbnail = (onThumbnailState: (state: string) => void) => render(
			<QueryClientProvider client={new QueryClient()}>
				<CloudArtifactFrame title="index.html" url={`${ROOT}index.html?v=1`} onThumbnailState={onThumbnailState} thumbnail={{ width: 200, fallback: <span>fallback icon</span> }} />
			</QueryClientProvider>,
		);
		const framed: string[] = [];
		const first = renderThumbnail((state) => framed.push(state));
		const frame = await screen.findByTitle("index.html");
		// Framed but not loaded yet: the card keeps waiting.
		expect(framed.at(-1)).toBe("loading");
		fireEvent.load(frame);
		await vi.waitFor(() => expect(framed.at(-1)).toBe("ready"));
		expect(framed[0]).toBe("loading");
		first.unmount();
		mocks.status = 403;
		const refused: string[] = [];
		renderThumbnail((state) => refused.push(state));
		// The fallback shows while the status is pending too, so wait for the state itself.
		await vi.waitFor(() => expect(refused.at(-1)).toBe("failed"));
		expect(refused[0]).toBe("loading");
		expect(screen.getByText("fallback icon")).toBeTruthy();
	});

	it("ignores frames of other sessions", async () => {
		const onNavigate = vi.fn();
		renderFrame({ onNavigate });
		await screen.findByTitle("index.html");
		act(() => navigate({ url: "https://cp.test/api/cloud/v1/artifact-files/other/page.html", status: 200 }));
		expect(onNavigate).not.toHaveBeenCalled();
	});
});

describe("artifactPathUnder", () => {
	it("decodes the path under the access prefix", () => {
		expect(artifactPathUnder(ROOT, `${ROOT}a%23b/c.html?x=1#top`)).toBe("a#b/c.html");
		expect(artifactPathUnder(ROOT, "https://evil.test/api/cloud/v1/artifact-files/token/a.html")).toBeUndefined();
		expect(artifactPathUnder(ROOT, "not a url")).toBeUndefined();
	});
});
