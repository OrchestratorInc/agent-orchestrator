import { afterEach, describe, expect, it, vi } from "vitest";
import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { aoBridge } from "../../lib/bridge";
import { useUiStore } from "../../stores/ui-store";
import { useState } from "react";
import { ChatImage } from "./ChatImage";
import { chatImageSaveName } from "./chat-image-actions";
import { MAX_CHAT_IMAGE_BYTES } from "../../../shared/chat-image";

const url = "http://127.0.0.1:4000/api/v1/sessions/s1/preview/files/.ao/attachments/attachment-1.png";
const png = new Uint8Array([137, 80, 78, 71]);
const pngResponse = () => new Response(png, { headers: { "content-type": "image/png" } });

afterEach(() => vi.restoreAllMocks());

describe("ChatImage copy and save", () => {
	it("uses CORS for protected thumbnails and full-size images", () => {
		render(<ChatImage src={url} alt="Image 1" />);
		expect(screen.getByRole("img")).toHaveAttribute("crossorigin", "anonymous");
		fireEvent.click(screen.getByRole("button", { name: "Open image: Image 1" }));
		for (const image of screen.getAllByRole("img")) expect(image).toHaveAttribute("crossorigin", "anonymous");
	});

	it("preserves external image loading without requiring third-party CORS", () => {
		render(<ChatImage src="https://example.com/image.png" alt="External" />);
		expect(screen.getByRole("img")).not.toHaveAttribute("crossorigin");
	});

	it("offers copy and save on right-click without opening the preview", () => {
		render(<ChatImage src={url} alt="Image 1" />);
		fireEvent.contextMenu(screen.getByRole("button", { name: "Open image: Image 1" }));
		expect(screen.queryByRole("dialog")).toBeNull();
		expect(screen.getAllByRole("menuitem").map((item) => item.textContent)).toEqual(["Copy image", "Save image"]);
	});

	it("copies a loopback image fetched by the renderer, not the main process", async () => {
		const user = userEvent.setup();
		const fetchImage = vi.spyOn(globalThis, "fetch").mockResolvedValue(pngResponse());
		const mainFetch = vi.spyOn(aoBridge.chatImage, "fetch");
		const writeImage = vi.spyOn(aoBridge.clipboard, "writeImage").mockResolvedValue(undefined);
		const toast = vi.spyOn(useUiStore.getState(), "showGlobalToast");
		useUiStore.setState({ showGlobalToast: toast });
		render(<ChatImage src={url} alt="Image 1" />);
		fireEvent.contextMenu(screen.getByRole("button", { name: "Open image: Image 1" }));
		await user.click(screen.getByRole("menuitem", { name: "Copy image" }));
		await waitFor(() => expect(writeImage).toHaveBeenCalledExactlyOnceWith(png));
		expect(String(fetchImage.mock.calls[0]?.[0])).toBe(url);
		expect(mainFetch).not.toHaveBeenCalled();
		expect(toast).not.toHaveBeenCalled();
	});

	it.each([
		"https://example.com/image.png",
		"https://localhost:4443/image.png",
	])("copies %s through the main process, outside the renderer's connect-src", async (external) => {
		const user = userEvent.setup();
		const mainFetch = vi.spyOn(aoBridge.chatImage, "fetch").mockResolvedValue({ bytes: png, mimeType: "image/png" });
		const writeImage = vi.spyOn(aoBridge.clipboard, "writeImage").mockResolvedValue(undefined);
		render(<ChatImage src={external} alt="External" />);
		fireEvent.contextMenu(screen.getByRole("button", { name: "Open image: External" }));
		await user.click(screen.getByRole("menuitem", { name: "Copy image" }));
		await waitFor(() => expect(writeImage).toHaveBeenCalledExactlyOnceWith(png));
		expect(mainFetch).toHaveBeenCalledExactlyOnceWith(external);
	});

	it("saves under the attachment's own file name", async () => {
		const user = userEvent.setup();
		vi.spyOn(globalThis, "fetch").mockResolvedValue(pngResponse());
		const save = vi.spyOn(aoBridge.chatImage, "save").mockResolvedValue(true);
		render(<ChatImage src={url} alt="Image 1" />);
		fireEvent.contextMenu(screen.getByRole("button", { name: "Open image: Image 1" }));
		await user.click(screen.getByRole("menuitem", { name: "Save image" }));
		await waitFor(() => expect(save).toHaveBeenCalledExactlyOnceWith({ name: "attachment-1.png", bytes: png }));
	});

	it("names a saved image from its content type without MIME parameters", async () => {
		const user = userEvent.setup();
		const render1 = "http://127.0.0.1:4000/api/v1/sessions/s1/renders/r1";
		vi.spyOn(globalThis, "fetch").mockResolvedValue(new Response(png, { headers: { "content-type": "image/jpeg; charset=binary" } }));
		const save = vi.spyOn(aoBridge.chatImage, "save").mockResolvedValue(true);
		render(<ChatImage src={render1} alt="Chart" />);
		fireEvent.contextMenu(screen.getByRole("button", { name: "Open image: Chart" }));
		await user.click(screen.getByRole("menuitem", { name: "Save image" }));
		await waitFor(() => expect(save).toHaveBeenCalledExactlyOnceWith({ name: "r1.jpg", bytes: png }));
	});

	it("refuses a loopback image over the chat image size limit before reading it", async () => {
		const user = userEvent.setup();
		const huge = new Response(png, { headers: { "content-type": "image/png", "content-length": String(MAX_CHAT_IMAGE_BYTES + 1) } });
		const read = vi.spyOn(huge, "blob");
		vi.spyOn(globalThis, "fetch").mockResolvedValue(huge);
		const toast = vi.spyOn(useUiStore.getState(), "showGlobalToast");
		useUiStore.setState({ showGlobalToast: toast });
		render(<ChatImage src={url} alt="Image 1" />);
		fireEvent.contextMenu(screen.getByRole("button", { name: "Open image: Image 1" }));
		await user.click(screen.getByRole("menuitem", { name: "Save image" }));
		await waitFor(() => expect(toast).toHaveBeenCalledWith("Couldn’t save image", "Image is too large", "error"));
		expect(read).not.toHaveBeenCalled();
	});

	it("refuses a loopback response that is not an image", async () => {
		const user = userEvent.setup();
		vi.spyOn(globalThis, "fetch").mockResolvedValue(new Response("<html></html>", { headers: { "content-type": "text/html" } }));
		const toast = vi.spyOn(useUiStore.getState(), "showGlobalToast");
		useUiStore.setState({ showGlobalToast: toast });
		render(<ChatImage src={url} alt="Image 1" />);
		fireEvent.contextMenu(screen.getByRole("button", { name: "Open image: Image 1" }));
		await user.click(screen.getByRole("menuitem", { name: "Copy image" }));
		await waitFor(() => expect(toast).toHaveBeenCalledWith("Couldn’t copy image", "Not an image", "error"));
	});
});

describe("chatImageSaveName", () => {
	it("keeps a URL file name and otherwise names the image from its label and type", () => {
		expect(chatImageSaveName(url, "Image 1", "image/png")).toBe("attachment-1.png");
		expect(chatImageSaveName("https://example.com/render?id=3", "Chart: v2", "image/jpeg")).toBe("render.jpg");
		expect(chatImageSaveName("data:image/webp;base64,AAAA", "Chart: v2", "image/webp")).toBe("Chart v2.webp");
		expect(chatImageSaveName("https://example.com/", "", "image/unknown")).toBe("image.png");
	});
});

describe("ChatImage source fallback", () => {
	// A just-sent tile draws from an object URL first; when that fails it must move
	// to the daemon's copy, not stop on its alt text.
	function Tile({ onLocalError }: { onLocalError: () => void }) {
		const [local, setLocal] = useState<string | undefined>("blob:app/sent");
		return (
			<ChatImage
				src={local ?? url}
				alt="Image 1"
				onError={local ? () => {
					onLocalError();
					setLocal(undefined);
				} : undefined}
			/>
		);
	}

	it("draws the replacement source without showing the alt text in between", () => {
		const onLocalError = vi.fn();
		render(<Tile onLocalError={onLocalError} />);
		expect(screen.getByRole("img")).toHaveAttribute("src", "blob:app/sent");

		fireEvent.error(screen.getByRole("img"));
		expect(onLocalError).toHaveBeenCalledOnce();
		expect(screen.getByRole("img")).toHaveAttribute("src", url);
		expect(screen.queryByText("Image 1")).toBeNull();
	});

	it("settles on the alt text if the replacement fails too", () => {
		const onLocalError = vi.fn();
		render(<Tile onLocalError={onLocalError} />);
		fireEvent.error(screen.getByRole("img"));
		fireEvent.error(screen.getByRole("img"));
		expect(onLocalError).toHaveBeenCalledOnce();
		expect(screen.queryByRole("img")).toBeNull();
		expect(screen.getByText("Image 1")).toBeInTheDocument();
	});
});
