import { act, fireEvent, render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";
import { insertLink, prefixLines, PRCommentComposer, PR_ATTACHMENT_MAX_BYTES, wrapSelection } from "./PRCommentComposer";
import { TooltipProvider } from "./ui/tooltip";

const renderComposer = (props: Partial<Parameters<typeof PRCommentComposer>[0]> = {}) => {
	const onSubmit = vi.fn(async () => {});
	render(
		<TooltipProvider>
			<PRCommentComposer busy={false} onSubmit={onSubmit} placeholder="Add a comment…" submitLabel="Comment" {...props} />
		</TooltipProvider>,
	);
	return { onSubmit, box: screen.getByRole("textbox", { name: "Add a comment…" }) as HTMLTextAreaElement };
};

describe("markdown edits", () => {
	it("wraps the selection, or a placeholder it then selects", () => {
		expect(wrapSelection("make it fast", 8, 12, "**", "bold")).toEqual({ text: "make it **fast**", selectionStart: 10, selectionEnd: 14 });
		expect(wrapSelection("x ", 2, 2, "_", "italic")).toEqual({ text: "x _italic_", selectionStart: 3, selectionEnd: 9 });
	});

	it("prefixes every selected line, counting a numbered list", () => {
		expect(prefixLines("one\ntwo\nthree", 0, 7, () => "> ").text).toBe("> one\n> two\nthree");
		expect(prefixLines("a\nb", 0, 3, (index) => `${index + 1}. `).text).toBe("1. a\n2. b");
	});

	it("links the selection and selects the URL to type over", () => {
		const edit = insertLink("see docs", 4, 8, "link text");
		expect(edit.text).toBe("see [docs](url)");
		expect(edit.text.slice(edit.selectionStart, edit.selectionEnd)).toBe("url");
	});
});

describe("PRCommentComposer", () => {
	it("formats from the toolbar and shortcuts, and previews the markdown before posting", async () => {
		const { box, onSubmit } = renderComposer();
		await userEvent.type(box, "ship it");
		box.setSelectionRange(5, 7);
		await userEvent.click(screen.getByRole("button", { name: "Bold (⌘B)" }));
		await waitFor(() => expect(box.value).toBe("ship **it**"));

		box.setSelectionRange(0, 4);
		fireEvent.keyDown(box, { key: "i", metaKey: true });
		await waitFor(() => expect(box.value).toBe("_ship_ **it**"));

		await userEvent.click(screen.getByRole("button", { name: "Preview" }));
		const preview = screen.getByTestId("pr-comment-preview");
		expect(preview.querySelector("em")).toHaveTextContent("ship");
		expect(preview.querySelector("strong")).toHaveTextContent("it");
		expect(screen.getByRole("button", { name: "Bold (⌘B)" })).toBeDisabled();

		await userEvent.click(screen.getByRole("button", { name: "Comment" }));
		expect(onSubmit).toHaveBeenCalledWith("_ship_ **it**");
	});

	it("uploads an attached image while a placeholder holds its place, then writes its markdown", async () => {
		let finish: (markdown: string) => void = () => {};
		const upload = vi.fn(() => new Promise<string>((resolve) => { finish = resolve; }));
		const { box } = renderComposer({ upload });
		await userEvent.type(box, "Before:");
		const file = new File([new Uint8Array([137, 80, 78, 71])], "shot.png", { type: "image/png" });
		await userEvent.upload(screen.getByTestId("pr-comment-file"), file);

		await waitFor(() => expect(box.value).toBe("Before:\n![Uploading shot.png…]()\n"));
		expect(upload).toHaveBeenCalledWith({ name: "shot.png", contentType: "image/png", data: "iVBORw==" });
		expect(screen.getByRole("button", { name: "Comment" })).toBeDisabled();

		await act(async () => finish("![shot.png](https://github.com/acme/repo/blob/ao-attachments/pr-7/shot.png?raw=true)"));
		await waitFor(() => expect(box.value).toBe("Before:\n![shot.png](https://github.com/acme/repo/blob/ao-attachments/pr-7/shot.png?raw=true)\n"));
		expect(screen.getByRole("button", { name: "Comment" })).toBeEnabled();
	});

	it("takes a pasted image, and drops the placeholder with a message when the upload fails", async () => {
		const upload = vi.fn(async () => { throw new Error("GitHub refused the upload"); });
		const { box } = renderComposer({ upload });
		const file = new File(["x"], "paste.png", { type: "image/png" });
		fireEvent.paste(box, { clipboardData: { files: [file] } });
		expect(await screen.findByText("GitHub refused the upload")).toBeInTheDocument();
		expect(box.value).toBe("");
	});

	it("refuses a file over the limit without uploading it", async () => {
		const upload = vi.fn(async () => "");
		renderComposer({ upload });
		const big = new File(["x"], "huge.mov", { type: "video/quicktime" });
		Object.defineProperty(big, "size", { value: PR_ATTACHMENT_MAX_BYTES + 1 });
		await userEvent.upload(screen.getByTestId("pr-comment-file"), big);
		expect(await screen.findByText("huge.mov is larger than 10 MB.")).toBeInTheDocument();
		expect(upload).not.toHaveBeenCalled();
	});

	it("has no Attach without an upload", async () => {
		const { box } = renderComposer();
		await userEvent.click(box);
		expect(screen.queryByRole("button", { name: "Attach files" })).not.toBeInTheDocument();
	});
});
