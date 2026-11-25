import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { render, screen } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { CloudCpError } from "../lib/cloud-cp";
import { SharedArtifactFrame } from "./SharedArtifactFrame";

const mocks = vi.hoisted(() => ({ ready: true, view: vi.fn() }));
vi.mock("../hooks/useCloudCp", () => ({ useCloudCp: () => ({ client: { viewArtifactShare: mocks.view }, ready: mocks.ready, baseUrl: "https://cp.test" }) }));

function renderFrame() {
	return render(<QueryClientProvider client={new QueryClient()}><SharedArtifactFrame linkId="q3-report" title="q3.html" /></QueryClientProvider>);
}

describe("SharedArtifactFrame", () => {
	beforeEach(() => {
		mocks.ready = true;
		mocks.view.mockReset();
	});

	it("frames the file from the URL its link opens with, sandboxed", async () => {
		mocks.view.mockResolvedValue({ linkId: "q3-report", name: "q3.html", contentType: "text/html", size: 1, updatedAt: "", contentUrl: "https://q3-report.share.test/?vt=token" });
		renderFrame();
		const frame = await screen.findByTitle("q3.html");
		expect(frame).toHaveAttribute("src", "https://q3-report.share.test/?vt=token");
		expect(frame).toHaveAttribute("sandbox", "allow-scripts allow-forms");
		expect(mocks.view).toHaveBeenCalledWith("q3-report", expect.anything());
	});

	it("plays a shared video in a video element, not a frame", async () => {
		mocks.view.mockResolvedValue({ linkId: "q3-report", name: "demo.mp4", contentType: "video/mp4", size: 1, updatedAt: "", contentUrl: "https://q3-report.share.test/?vt=token" });
		renderFrame();
		const video = await screen.findByLabelText("q3.html");
		expect(video.tagName).toBe("VIDEO");
		expect(video).toHaveAttribute("src", "https://q3-report.share.test/?vt=token");
		expect(video).toHaveAttribute("controls");
		expect(screen.queryByTitle("q3.html")).not.toBeInTheDocument();
	});

	it("plays a video stored without its type, by its name", async () => {
		mocks.view.mockResolvedValue({ linkId: "q3-report", name: "demo.MOV", contentType: "binary/octet-stream", size: 1, updatedAt: "", contentUrl: "https://q3-report.share.test/" });
		renderFrame();
		expect((await screen.findByLabelText("q3.html")).tagName).toBe("VIDEO");
	});

	it("frames anything else, even with a video's name but another type", async () => {
		mocks.view.mockResolvedValue({ linkId: "q3-report", name: "demo.mp4", contentType: "text/html", size: 1, updatedAt: "", contentUrl: "https://q3-report.share.test/" });
		renderFrame();
		expect((await screen.findByTitle("q3.html")).tagName).toBe("IFRAME");
	});

	it("says when the link no longer opens for this person", async () => {
		mocks.view.mockRejectedValue(new CloudCpError("not found", { status: 404, code: "not_found" }));
		renderFrame();
		expect(await screen.findByText("This artifact is no longer shared with you.")).toBeInTheDocument();
	});

	it("asks for Cloud sign-in when signed out", () => {
		mocks.ready = false;
		renderFrame();
		expect(screen.getByText("Sign in to Orchestrator Cloud to open artifacts shared with you.")).toBeInTheDocument();
		expect(mocks.view).not.toHaveBeenCalled();
	});
});
