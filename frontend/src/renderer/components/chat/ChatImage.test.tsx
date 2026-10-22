import { afterEach, describe, expect, it, vi } from "vitest";
import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { aoBridge } from "../../lib/bridge";
import { useUiStore } from "../../stores/ui-store";
import { ChatImage } from "./ChatImage";
import { chatImageSaveName } from "./chat-image-actions";

const url = "http://127.0.0.1:4000/api/v1/sessions/s1/preview/files/.ao/attachments/attachment-1.png";
const png = new Uint8Array([137, 80, 78, 71]);

afterEach(() => vi.restoreAllMocks());

describe("ChatImage copy and save", () => {
	it("offers copy and save on right-click without opening the preview", () => {
		render(<ChatImage src={url} alt="Image 1" />);
		fireEvent.contextMenu(screen.getByRole("button", { name: "Open image: Image 1" }));
		expect(screen.queryByRole("dialog")).toBeNull();
		expect(screen.getAllByRole("menuitem").map((item) => item.textContent)).toEqual(["Copy image", "Save image"]);
	});

	it("copies the image bytes fetched by the main process", async () => {
		const user = userEvent.setup();
		const fetchImage = vi.spyOn(aoBridge.chatImage, "fetch").mockResolvedValue({ bytes: png, mimeType: "image/png" });
		const writeImage = vi.spyOn(aoBridge.clipboard, "writeImage").mockResolvedValue(undefined);
		render(<ChatImage src={url} alt="Image 1" />);
		fireEvent.contextMenu(screen.getByRole("button", { name: "Open image: Image 1" }));
		await user.click(screen.getByRole("menuitem", { name: "Copy image" }));
		await waitFor(() => expect(writeImage).toHaveBeenCalledExactlyOnceWith(png));
		expect(fetchImage).toHaveBeenCalledExactlyOnceWith(url);
	});

	it("saves under the attachment's own file name", async () => {
		const user = userEvent.setup();
		vi.spyOn(aoBridge.chatImage, "fetch").mockResolvedValue({ bytes: png, mimeType: "image/png" });
		const save = vi.spyOn(aoBridge.chatImage, "save").mockResolvedValue(true);
		render(<ChatImage src={url} alt="Image 1" />);
		fireEvent.contextMenu(screen.getByRole("button", { name: "Open image: Image 1" }));
		await user.click(screen.getByRole("menuitem", { name: "Save image" }));
		await waitFor(() => expect(save).toHaveBeenCalledExactlyOnceWith({ name: "attachment-1.png", bytes: png }));
	});

	it("reports a failed copy", async () => {
		const user = userEvent.setup();
		vi.spyOn(aoBridge.chatImage, "fetch").mockRejectedValue(new Error("Not an image"));
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
