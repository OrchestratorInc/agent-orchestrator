import { act, render, screen } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
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
