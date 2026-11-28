import { act, render, screen } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { ArtifactScrollMemory, ArtifactScrollMemoryContext } from "../lib/artifact-scroll-memory";
import { ArtifactZoomContext, ArtifactZoomController } from "../lib/artifact-zoom";
import { AppBrowserLinkContext } from "./AppLink";
import { ArtifactPageFrame } from "./ArtifactPageFrame";

const URL_TO_OPEN = "https://example.com/docs";

function frame() {
	return screen.getByTitle("report.html") as HTMLIFrameElement;
}

function post(data: unknown, source: MessageEventSource | null) {
	act(() => {
		window.dispatchEvent(new MessageEvent("message", { data, source }));
	});
}

const openLink = (url = URL_TO_OPEN) => ({ jsonrpc: "2.0", id: "ao-link-1", method: "ui/open-link", params: { url } });

describe("ArtifactPageFrame links", () => {
	let open: ReturnType<typeof vi.spyOn>;
	beforeEach(() => {
		open = vi.spyOn(window, "open").mockReturnValue(null);
	});
	afterEach(() => {
		open.mockRestore();
		vi.unstubAllGlobals();
	});

	it("opens a link the reader clicks in the page in the session's Browser tab", () => {
		const openInAppBrowser = vi.fn();
		render(
			<AppBrowserLinkContext.Provider value={openInAppBrowser}>
				<ArtifactPageFrame src="http://ao-inline-artifact.sess-1.localhost/report.html" title="report.html" />
			</AppBrowserLinkContext.Provider>,
		);
		act(() => frame().focus());
		post(openLink(), frame().contentWindow);
		expect(openInAppBrowser).toHaveBeenCalledWith(URL_TO_OPEN);
		expect(open).not.toHaveBeenCalled();
	});

	it("opens a link in the OS browser where no session is in view", () => {
		render(<ArtifactPageFrame src="http://ao-inline-artifact.sess-1.localhost/report.html" title="report.html" />);
		act(() => frame().focus());
		post(openLink(), frame().contentWindow);
		expect(open).toHaveBeenCalledWith(URL_TO_OPEN, "_blank", "noopener,noreferrer");
	});

	it("ignores a link request the page posts while the reader is not using it", () => {
		render(<ArtifactPageFrame src="http://ao-inline-artifact.sess-1.localhost/report.html" title="report.html" />);
		post(openLink(), frame().contentWindow);
		expect(open).not.toHaveBeenCalled();
	});

	it("ignores a link request without a user gesture", () => {
		vi.stubGlobal("navigator", { ...navigator, userActivation: { isActive: false, hasBeenActive: false } });
		render(<ArtifactPageFrame src="http://ao-inline-artifact.sess-1.localhost/report.html" title="report.html" />);
		act(() => frame().focus());
		post(openLink(), frame().contentWindow);
		expect(open).not.toHaveBeenCalled();
	});

	it("ignores link requests from other windows and non-web URLs", () => {
		render(<ArtifactPageFrame src="http://ao-inline-artifact.sess-1.localhost/report.html" title="report.html" />);
		act(() => frame().focus());
		post(openLink(), window);
		post(openLink("javascript:alert(1)"), frame().contentWindow);
		post(openLink("file:///etc/passwd"), frame().contentWindow);
		expect(open).not.toHaveBeenCalled();
	});
});

describe("ArtifactPageFrame scroll", () => {
	it("scrolls a page shown again back to where it was left, and keeps up with where it is scrolled", () => {
		const memory = new ArtifactScrollMemory({ x: 0, y: 640 });
		render(
			<ArtifactScrollMemoryContext.Provider value={memory}>
				<ArtifactPageFrame src="http://ao-inline-artifact.sess-1.localhost/report.html" title="report.html" />
			</ArtifactScrollMemoryContext.Provider>,
		);
		const page = frame().contentWindow!;
		const postMessage = vi.spyOn(page, "postMessage");
		act(() => { frame().dispatchEvent(new Event("load")); });
		expect(postMessage).toHaveBeenLastCalledWith({
			jsonrpc: "2.0",
			method: "ui/notifications/host-context-changed",
			params: { scrollReport: true, scrollTo: { x: 0, y: 640 } },
		}, "*");
		expect(memory.pendingPosition()).toBeUndefined();

		post({ jsonrpc: "2.0", method: "ao/scroll", params: { x: 0, y: 900 } }, page);
		expect(memory.current()).toEqual({ x: 0, y: 900 });
		// Another window's report, or a malformed one, is not the page's.
		post({ jsonrpc: "2.0", method: "ao/scroll", params: { x: 0, y: 5 } }, window);
		post({ jsonrpc: "2.0", method: "ao/scroll", params: { x: 0, y: "12" } }, page);
		expect(memory.current()).toEqual({ x: 0, y: 900 });

		// A reload of the page (the file rewritten) does not jump back to the old spot.
		act(() => { frame().dispatchEvent(new Event("load")); });
		expect(postMessage).toHaveBeenLastCalledWith({ jsonrpc: "2.0", method: "ui/notifications/host-context-changed", params: { scrollReport: true } }, "*");
	});

	it("asks for no scroll reports where no scroll is kept", () => {
		render(
			<ArtifactZoomContext.Provider value={new ArtifactZoomController()}>
				<ArtifactPageFrame src="http://ao-inline-artifact.sess-1.localhost/report.html" title="report.html" />
			</ArtifactZoomContext.Provider>,
		);
		const postMessage = vi.spyOn(frame().contentWindow!, "postMessage");
		act(() => { frame().dispatchEvent(new Event("load")); });
		expect(postMessage).toHaveBeenCalled();
		for (const [message] of postMessage.mock.calls) {
			expect((message as { params?: object }).params).not.toHaveProperty("scrollReport");
		}
	});
});
