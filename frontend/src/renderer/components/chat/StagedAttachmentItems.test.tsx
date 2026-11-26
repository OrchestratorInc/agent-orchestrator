import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { rememberSentAttachments, resetSentAttachmentPreviewsForTest } from "../../lib/sent-attachment-previews";
import { AttachmentStrip, StagedAttachmentItems } from "./StagedAttachmentItems";
import { attachmentURL } from "./messageAttachments";

const API = "http://127.0.0.1:4000";
const IMAGE = ".ao/attachments/attachment-shot.png";
const NOTES = ".ao/attachments/attachment-notes.txt";

// A just-sent file's tile draws from the composer's object URL. The packaged app's
// CSP once blocked those, and the tile stopped on "Image 1" instead of reading the
// daemon's copy, which was there all along.
describe("StagedAttachmentItems with a just-sent local copy", () => {
	const originalCreate = URL.createObjectURL;
	const originalRevoke = URL.revokeObjectURL;
	let created = 0;

	beforeEach(() => {
		created = 0;
		URL.createObjectURL = vi.fn(() => `blob:app/${++created}`);
		URL.revokeObjectURL = vi.fn();
	});

	afterEach(() => {
		resetSentAttachmentPreviewsForTest();
		URL.createObjectURL = originalCreate;
		URL.revokeObjectURL = originalRevoke;
		vi.unstubAllGlobals();
		vi.restoreAllMocks();
	});

	it("draws an image tile from the local copy, then the daemon's when that fails", () => {
		rememberSentAttachments([{ path: IMAGE, mimeType: "image/png", bytes: 3, file: new Blob(["png"], { type: "image/png" }) }]);
		render(<StagedAttachmentItems paths={[IMAGE]} sessionId="s1" apiBaseUrl={API} ariaLabel="Attachments" />);
		expect(screen.getByRole("img")).toHaveAttribute("src", "blob:app/1");

		fireEvent.error(screen.getByRole("img"));
		expect(screen.getByRole("img")).toHaveAttribute("src", attachmentURL(API, "s1", IMAGE));
		expect(screen.queryByText("Image 1")).toBeNull();
	});

	it("reads a text preview from the daemon when the local copy cannot be read", async () => {
		vi.stubGlobal("IntersectionObserver", class {
			constructor(private readonly callback: IntersectionObserverCallback) {}
			observe() {
				this.callback([{ isIntersecting: true } as IntersectionObserverEntry], this as unknown as IntersectionObserver);
			}
			disconnect() {}
		});
		const fetch = vi.fn(async (input: RequestInfo | URL) => {
			if (String(input).startsWith("blob:")) throw new TypeError("Failed to fetch");
			return new Response("from the daemon", { status: 206 });
		});
		vi.stubGlobal("fetch", fetch);
		rememberSentAttachments([{ path: NOTES, mimeType: "text/plain", bytes: 5, file: new Blob(["local"], { type: "text/plain" }) }]);

		render(<StagedAttachmentItems paths={[NOTES]} sessionId="s1" apiBaseUrl={API} ariaLabel="Attachments" />);

		expect(await screen.findByText("from the daemon")).toBeInTheDocument();
		await waitFor(() => expect(fetch).toHaveBeenCalledTimes(2));
		expect(String(fetch.mock.calls[0]?.[0])).toBe("blob:app/1");
		expect(String(fetch.mock.calls[1]?.[0])).toBe(`${attachmentURL(API, "s1", NOTES)}?raw=true`);
	});

	it("reads only the start of a large local copy for its text preview", async () => {
		vi.stubGlobal("IntersectionObserver", class {
			constructor(private readonly callback: IntersectionObserverCallback) {}
			observe() {
				this.callback([{ isIntersecting: true } as IntersectionObserverEntry], this as unknown as IntersectionObserver);
			}
			disconnect() {}
		});
		// A file with no end: the tile must stop once it has enough to draw.
		let pulls = 0;
		const cancel = vi.fn();
		const chunk = new TextEncoder().encode("x".repeat(1024));
		const body = new ReadableStream<Uint8Array>({
			start(controller) {
				controller.enqueue(new TextEncoder().encode("first line\n"));
			},
			pull(controller) {
				pulls += 1;
				controller.enqueue(chunk);
			},
			cancel,
		});
		vi.stubGlobal("fetch", vi.fn(async () => new Response(body)));
		rememberSentAttachments([{ path: NOTES, mimeType: "text/plain", bytes: 5 * 1024 ** 3, file: new Blob(["local"], { type: "text/plain" }) }]);

		render(<StagedAttachmentItems paths={[NOTES]} sessionId="s1" apiBaseUrl={API} ariaLabel="Attachments" />);

		expect(await screen.findByText(/^first line/)).toBeInTheDocument();
		await waitFor(() => expect(cancel).toHaveBeenCalled());
		expect(pulls).toBeLessThan(8);
	});
});

describe("AttachmentStrip", () => {
	// The composer's row scrolls sideways; a file added past its end would land
	// out of sight behind the fade.
	it("glides to its end when a tile is added, and stays put otherwise", () => {
		const tiles = (count: number) => Array.from({ length: count }, (_, index) => <span key={index}>tile {index}</span>);
		const { rerender } = render(<AttachmentStrip itemCount={2} data-testid="strip">{tiles(2)}</AttachmentStrip>);
		const strip = screen.getByTestId("strip");
		Object.defineProperty(strip, "scrollWidth", { configurable: true, value: 900 });
		expect(strip).toHaveClass("edge-fade", "edge-fade-x", "overflow-x-auto");
		expect(strip.scrollLeft).toBe(0);

		rerender(<AttachmentStrip itemCount={3} data-testid="strip">{tiles(3)}</AttachmentStrip>);
		expect(strip.scrollLeft).toBe(900);

		strip.scrollLeft = 120;
		rerender(<AttachmentStrip itemCount={2} data-testid="strip">{tiles(2)}</AttachmentStrip>);
		expect(strip.scrollLeft).toBe(120);
	});
});

describe("StagedAttachmentItems opening a text file", () => {
	afterEach(() => {
		vi.unstubAllGlobals();
	});

	it("shows a sent text file's whole saved copy with Copy", async () => {
		vi.stubGlobal("IntersectionObserver", class {
			constructor(private readonly callback: IntersectionObserverCallback) {}
			observe() {
				this.callback([{ isIntersecting: true } as IntersectionObserverEntry], this as unknown as IntersectionObserver);
			}
			disconnect() {}
		});
		const whole = `first line\n${"middle\n".repeat(500)}last line`;
		const fetch = vi.fn(async (_input: RequestInfo | URL, init?: RequestInit) =>
			new Response((init?.headers as Record<string, string> | undefined)?.Range ? "first line" : whole, { status: 200 }));
		vi.stubGlobal("fetch", fetch);
		render(<StagedAttachmentItems paths={[NOTES]} sessionId="s1" apiBaseUrl={API} ariaLabel="Attachments" />);

		fireEvent.click(await screen.findByRole("button", { name: "attachment-notes.txt" }));

		const viewer = await screen.findByRole("dialog", { name: "attachment-notes.txt" });
		expect(await within(viewer).findByText(/last line$/)).toBeInTheDocument();
		expect(within(viewer).getByRole("button", { name: "Copy text" })).toBeInTheDocument();
		expect(fetch).toHaveBeenLastCalledWith(`${attachmentURL(API, "s1", NOTES)}?raw=true`, expect.not.objectContaining({ headers: expect.anything() }));
	});
});
