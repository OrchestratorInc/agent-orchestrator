import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { act, render, screen } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { aoBridge } from "../lib/bridge";
import { artifactPathUnder, CloudArtifactFrame } from "./CloudArtifactFrame";

vi.mock("../lib/cloud-cp/renderer-client", () => ({ cloudCpFetch: async () => new Response(null, { status: 200 }) }));

const ROOT = "https://cp.test/api/cloud/v1/artifact-files/token/";

describe("CloudArtifactFrame", () => {
	let navigate: (navigation: { url: string; status: number }) => void = () => {};

	beforeEach(() => {
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
