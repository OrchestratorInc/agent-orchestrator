import { act, fireEvent, render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";
import { insertLink, PRCommentComposer, PR_ATTACHMENT_MAX_BYTES, wrapSelection } from "./PRCommentComposer";

const renderComposer = (props: Partial<Parameters<typeof PRCommentComposer>[0]> = {}) => {
	const onSubmit = vi.fn(async () => {});
	render(<PRCommentComposer busy={false} onSubmit={onSubmit} placeholder="Add a comment…" submitLabel="Comment" {...props} />);
	return { onSubmit, box: screen.getByRole("textbox", { name: "Add a comment…" }) as HTMLTextAreaElement };
};

describe("markdown edits", () => {
	it("wraps the selection, or a placeholder it then selects", () => {
		expect(wrapSelection("make it fast", 8, 12, "**", "bold")).toEqual({ text: "make it **fast**", selectionStart: 10, selectionEnd: 14 });
		expect(wrapSelection("x ", 2, 2, "_", "italic")).toEqual({ text: "x _italic_", selectionStart: 3, selectionEnd: 9 });
	});

	it("links the selection and selects the URL to type over", () => {
		const edit = insertLink("see docs", 4, 8, "link text");
		expect(edit.text).toBe("see [docs](url)");
		expect(edit.text.slice(edit.selectionStart, edit.selectionEnd)).toBe("url");
	});
});

describe("PRCommentComposer", () => {
	it("formats the selection from the keyboard, posts on ⌘↵, and cancels on Escape", async () => {
		const onCancel = vi.fn();
		const { box, onSubmit } = renderComposer({ onCancel });
		await userEvent.type(box, "ship it");
		box.setSelectionRange(5, 7);
		fireEvent.keyDown(box, { key: "b", metaKey: true });
		await waitFor(() => expect(box.value).toBe("ship **it**"));
		box.setSelectionRange(0, 4);
		fireEvent.keyDown(box, { key: "i", metaKey: true });
		await waitFor(() => expect(box.value).toBe("_ship_ **it**"));

		fireEvent.keyDown(box, { key: "Enter", metaKey: true });
		await waitFor(() => expect(onSubmit).toHaveBeenCalledWith("_ship_ **it**"));
		fireEvent.keyDown(box, { key: "Escape" });
		expect(onCancel).toHaveBeenCalled();
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

	it("keeps each finished link when several files are attached at once", async () => {
		const upload = vi.fn(async ({ name }: { name: string }) => `![${name}](https://example.com/${name})`);
		const { box } = renderComposer({ upload });
		const files = ["one.png", "two.png"].map((name) => new File([new Uint8Array([137, 80, 78, 71])], name, { type: "image/png" }));
		await userEvent.upload(screen.getByTestId("pr-comment-file"), files);

		await waitFor(() => expect(upload).toHaveBeenCalledTimes(2));
		await waitFor(() => expect(box.value).toContain("![two.png](https://example.com/two.png)"));
		expect(box.value).toContain("![one.png](https://example.com/one.png)");
		expect(box.value).not.toContain("Uploading");
	});

	it("takes no file while the comment posts", () => {
		const upload = vi.fn(async () => "");
		renderComposer({ busy: true, upload });
		expect(screen.getByRole("button", { name: "Attach" })).toBeDisabled();

		const file = new File([new Uint8Array([1])], "late.png", { type: "image/png" });
		fireEvent.drop(screen.getByTestId("pr-comment-composer"), { dataTransfer: { files: [file], types: ["Files"] } });
		expect(upload).not.toHaveBeenCalled();
	});

	it("has no Attach without an upload", () => {
		renderComposer();
		expect(screen.queryByRole("button", { name: /Attach/ })).not.toBeInTheDocument();
	});
});
