import { fireEvent, render, screen } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";
import { FileMediaPreview } from "./FileMediaPreview";

vi.mock("../lib/api-client", () => ({ getApiBaseUrl: () => "http://127.0.0.1:3001" }));
vi.mock("../hooks/useHostConnection", () => ({
	useHostConnection: (hostId?: string) => ({ baseUrl: hostId === "remote" ? "http://127.0.0.1:4000/token" : undefined }),
}));

describe("FileMediaPreview", () => {
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

	it("streams local artifacts through the explicit loopback artifact route", () => {
		render(<FileMediaPreview kind="video" path="clips/a #1.mp4" sessionId="session 1" source={{ kind: "artifact", rawUrl: "http://ao-preview-artifact.abc.localhost/clip.mp4?raw=true" }} version={7} />);
		expect(screen.getByLabelText("clips/a #1.mp4")).toHaveAttribute("src", "http://127.0.0.1:3001/api/v1/sessions/session%201/preview/files/clips/a%20%231.mp4?source=artifact&raw=true&v=7");
	});
});
