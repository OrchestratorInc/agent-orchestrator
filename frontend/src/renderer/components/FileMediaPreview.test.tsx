import { fireEvent, render, screen } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";
import { FileMediaPreview } from "./FileMediaPreview";

vi.mock("../lib/api-client", () => ({ getApiBaseUrl: () => "http://127.0.0.1:3001" }));
vi.mock("../hooks/useHostConnection", () => ({
	useHostConnection: (hostId?: string) => ({ baseUrl: hostId === "remote" ? "http://127.0.0.1:4000/token" : undefined }),
}));

describe("FileMediaPreview", () => {
	it.each(["image", "video", "audio"] as const)("uses renderer-origin CORS for protected %s", (kind) => {
		render(<FileMediaPreview kind={kind} path="media" sessionId="s1" source={{kind: "workspace"}} version={1} />);
		expect(screen.getByLabelText("media")).toHaveAttribute("crossorigin", "anonymous");
	});

	it("encodes paths and streams from the selected remote host", () => {
		render(<FileMediaPreview hostId="remote" kind="video" path="clips/a #1.mp4" sessionId="session 1" source={{ kind: "workspace" }} version={7} />);
		expect(screen.getByLabelText("clips/a #1.mp4")).toHaveAttribute("src", "http://127.0.0.1:4000/token/api/v1/sessions/session%201/preview/files/clips/a%20%231.mp4?raw=true&v=7");
	});

	it("does not fall back to local media when the selected host is offline", () => {
		render(<FileMediaPreview hostId="offline" kind="audio" path="voice.mp3" sessionId="session-1" source={{ kind: "workspace" }} version={1} />);
		expect(screen.getByText("Unable to load this file.")).toBeInTheDocument();
		expect(screen.queryByLabelText("voice.mp3")).not.toBeInTheDocument();
	});

	it("retries a changed artifact URL after a failed load", () => {
		const props = { kind: "image" as const, path: "logo.png", sessionId: "session-1", source: { kind: "artifact" as const, rawUrl: "https://artifacts.example.test/logo.png?raw=true" } };
		const view = render(<FileMediaPreview {...props} version={1} />);
		fireEvent.error(screen.getByLabelText("logo.png"));
		expect(screen.getByRole("button", { name: "Retry" })).toBeInTheDocument();
		view.rerender(<FileMediaPreview {...props} version={2} />);
		expect(screen.getByLabelText("logo.png")).toHaveAttribute("src", `${props.source.rawUrl}&v=2`);
	});

	it("plays a cloud video from its control-plane link and asks for a fresh link when it fails", () => {
		const onAccessRefused = vi.fn();
		const source = { kind: "artifact" as const, cloud: true, rawUrl: "https://cp.test/api/cloud/v1/artifact-files/t/demo.mp4?v=1", onAccessRefused };
		render(<FileMediaPreview kind="video" path="demo.mp4" sessionId="session-1" source={source} version={1} />);
		const video = screen.getByLabelText("demo.mp4");
		expect(video.getAttribute("src")).toMatch(/^https:\/\/cp\.test\/api\/cloud\/v1\/artifact-files\/t\/demo\.mp4\?v=1/);
		fireEvent.error(video);
		expect(onAccessRefused).toHaveBeenCalledTimes(1);
	});

	it("takes one fresh link per file version, then shows a cloud file that still fails", () => {
		const onAccessRefused = vi.fn();
		const props = { kind: "video" as const, path: "broken.mp4", sessionId: "session-1", version: 1 };
		const source = (token: string) => ({ kind: "artifact" as const, cloud: true, rawUrl: `https://cp.test/api/cloud/v1/artifact-files/${token}/broken.mp4?v=1`, onAccessRefused });
		const view = render(<FileMediaPreview {...props} source={source("t1")} />);
		fireEvent.error(screen.getByLabelText("broken.mp4"));
		view.rerender(<FileMediaPreview {...props} source={source("t2")} />);
		fireEvent.error(screen.getByLabelText("broken.mp4"));
		view.rerender(<FileMediaPreview {...props} source={source("t3")} />);
		expect(onAccessRefused).toHaveBeenCalledTimes(1);

		// The fresh link failed too: the error shows, and Retry may take one more link.
		view.rerender(<FileMediaPreview {...props} source={source("t2")} />);
		fireEvent.click(screen.getByRole("button", { name: "Retry" }));
		fireEvent.error(screen.getByLabelText("broken.mp4"));
		expect(onAccessRefused).toHaveBeenCalledTimes(2);
	});

	it("does not add a second version to a cloud file's URL", () => {
		const source = { kind: "artifact" as const, cloud: true, rawUrl: "https://cp.test/api/cloud/v1/artifact-files/t/chart.png?v=1" };
		render(<FileMediaPreview kind="image" path="chart.png" sessionId="session-1" source={source} version="2026-10-22T00:00:00Z" />);
		expect(screen.getByLabelText("chart.png")).toHaveAttribute("src", source.rawUrl);
	});

	it("shows a cloud image from its control-plane link", () => {
		const source = { kind: "artifact" as const, cloud: true, rawUrl: "https://cp.test/api/cloud/v1/artifact-files/t/chart.png?v=1" };
		render(<FileMediaPreview kind="image" path="chart.png" sessionId="session-1" source={source} version={1} />);
		expect(screen.getByLabelText("chart.png").getAttribute("src")).toContain("https://cp.test/api/cloud/v1/artifact-files/t/chart.png");
	});

	it("streams local artifacts through the explicit loopback artifact route", () => {
		render(<FileMediaPreview kind="video" path="clips/a #1.mp4" sessionId="session 1" source={{ kind: "artifact", rawUrl: "http://ao-preview-artifact.abc.localhost/clip.mp4?raw=true" }} version={7} />);
		expect(screen.getByLabelText("clips/a #1.mp4")).toHaveAttribute("src", "http://127.0.0.1:3001/api/v1/sessions/session%201/preview/files/clips/a%20%231.mp4?source=artifact&raw=true&v=7");
	});
});
