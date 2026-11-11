import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { rememberSentAttachments, resetSentAttachmentPreviewsForTest } from "../../lib/sent-attachment-previews";
import { StagedAttachmentItems } from "./StagedAttachmentItems";
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
});
