import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";
import { insertLink, linkedAttachments, PRCommentComposer, PR_ATTACHMENT_MAX_BYTES, wrapSelection } from "./PRCommentComposer";

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

describe("linkedAttachments", () => {
	it("leaves a link inside code alone, as the daemon does", () => {
		const a = new File(["a"], "a.png");
		expect(linkedAttachments("![a](attachment:4)\n```\n![x](attachment:4)\n```\n`[y](attachment:4)`", new Map([[4, a]]))).toEqual({
			body: "![a](attachment:0)\n```\n![x](attachment:4)\n```\n`[y](attachment:4)`",
			files: [a],
		});
	});

	it("leaves a link inside double-backtick code alone", () => {
		const a = new File(["a"], "a.png");
		expect(linkedAttachments("``![a](attachment:0)``", new Map([[0, a]]))).toEqual({ body: "``![a](attachment:0)``", files: [] });
	});

	it("keeps the linked files in link order, renumbered, and leaves unknown links alone", () => {
		const a = new File(["a"], "a.png"), b = new File(["b"], "b.png");
		const files = new Map([[3, a], [7, b]]);
		expect(linkedAttachments("![b](attachment:7) ![a](attachment:3) ![b](attachment:7) [x](attachment:9)", files)).toEqual({
			body: "![b](attachment:0) ![a](attachment:1) ![b](attachment:0) [x](attachment:9)",
			files: [b, a],
		});
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
		await waitFor(() => expect(onSubmit).toHaveBeenCalledWith("_ship_ **it**", []));
		fireEvent.keyDown(box, { key: "Escape" });
		expect(onCancel).toHaveBeenCalled();
	});

	const png = (name: string) => new File([new Uint8Array([137, 80, 78, 71])], name, { type: "image/png" });

	it("keeps a picked file on this machine, linked from the text, until the comment posts", async () => {
		const { box, onSubmit } = renderComposer({ attachments: {} });
		await userEvent.type(box, "Before:");
		const file = png("shot.png");
		await userEvent.upload(screen.getByTestId("pr-comment-file"), file);

		await waitFor(() => expect(box.value).toBe("Before:\n![shot.png](attachment:0)\n"));
		expect(onSubmit).not.toHaveBeenCalled();
		expect(screen.getByRole("button", { name: "Comment" })).toBeEnabled();

		fireEvent.click(screen.getByRole("button", { name: "Comment" }));
		await waitFor(() => expect(onSubmit).toHaveBeenCalledWith("Before:\n![shot.png](attachment:0)", [file]));
		await waitFor(() => expect(box.value).toBe(""));
	});

	it("sends only the files the text still links, numbered in the order it links them", async () => {
		const { box, onSubmit } = renderComposer({ attachments: {} });
		const [one, two, log] = [png("one.png"), png("two.png"), new File(["ok"], "run.log", { type: "text/plain" })];
		await userEvent.upload(screen.getByTestId("pr-comment-file"), [one, two, log]);
		await waitFor(() => expect(box.value).toBe("![one.png](attachment:0)\n![two.png](attachment:1)\n[run.log](attachment:2)\n"));

		// The first link is deleted and the log is linked before the second image.
		fireEvent.change(box, { target: { value: "Log: [run.log](attachment:2), then ![two.png](attachment:1)" } });
		fireEvent.click(screen.getByRole("button", { name: "Comment" }));
		await waitFor(() => expect(onSubmit).toHaveBeenCalledWith("Log: [run.log](attachment:0), then ![two.png](attachment:1)", [log, two]));
	});

	it("keeps the draft and its files when posting fails, or the person backs out", async () => {
		const onSubmit = vi.fn(async () => { throw new Error("declined"); });
		const { box } = renderComposer({ attachments: {}, onSubmit });
		const file = png("shot.png");
		await userEvent.upload(screen.getByTestId("pr-comment-file"), file);
		await waitFor(() => expect(box.value).toContain("](attachment:0)"));

		fireEvent.click(screen.getByRole("button", { name: "Comment" }));
		await waitFor(() => expect(onSubmit).toHaveBeenCalledTimes(1));
		expect(box.value).toBe("![shot.png](attachment:0)\n");
		fireEvent.click(screen.getByRole("button", { name: "Comment" }));
		await waitFor(() => expect(onSubmit).toHaveBeenLastCalledWith("![shot.png](attachment:0)", [file]));
	});

	it("takes a pasted image as a link, with a label that can't break the markdown", async () => {
		const { box } = renderComposer({ attachments: {} });
		fireEvent.paste(box, { clipboardData: { files: [png("a](b).png")] } });
		await waitFor(() => expect(box.value).toBe("![a__b_.png](attachment:0)\n"));
	});

	it("refuses a file over the limit, and files over the comment's total", async () => {
		const { box } = renderComposer({ attachments: {} });
		const big = new File(["x"], "huge.mov", { type: "video/quicktime" });
		Object.defineProperty(big, "size", { value: PR_ATTACHMENT_MAX_BYTES + 1 });
		await userEvent.upload(screen.getByTestId("pr-comment-file"), big);
		expect(await screen.findByText("huge.mov is larger than 10 MB.")).toBeInTheDocument();
		expect(box.value).toBe("");

		const nine = ["a.zip", "b.zip", "c.zip"].map((name) => {
			const file = new File(["x"], name, { type: "application/zip" });
			Object.defineProperty(file, "size", { value: 9 * 1024 * 1024 });
			return file;
		});
		await userEvent.upload(screen.getByTestId("pr-comment-file"), nine);
		expect(await screen.findByText("A comment's files can be 25 MB together.")).toBeInTheDocument();
		expect(box.value).toBe("[a.zip](attachment:0)\n[b.zip](attachment:1)\n");
	});

	it("says why files can't be attached, and takes none", async () => {
		const { box } = renderComposer({ attachments: { blockedReason: "You can't attach files here: you don't have write access to acme/repo." } });
		const attach = screen.getByRole("button", { name: "Attach" });
		expect(attach).toHaveAttribute("aria-disabled", "true");
		fireEvent.click(attach);
		expect(await screen.findByText("You can't attach files here: you don't have write access to acme/repo.")).toBeInTheDocument();
		fireEvent.paste(box, { clipboardData: { files: [png("x.png")] } });
		expect(box.value).toBe("");
	});

	it("takes no file while the comment posts", () => {
		const { box } = renderComposer({ busy: true, attachments: {} });
		expect(screen.getByRole("button", { name: "Attach" })).toBeDisabled();
		fireEvent.drop(screen.getByTestId("pr-comment-composer"), { dataTransfer: { files: [png("late.png")], types: ["Files"] } });
		expect(box.value).toBe("");
	});

	it("takes up to ten files, and a deleted link frees its file's room", async () => {
		const { box } = renderComposer({ attachments: {} });
		const many = Array.from({ length: 11 }, (_, i) => png(`f${i}.png`));
		await userEvent.upload(screen.getByTestId("pr-comment-file"), many);
		expect(await screen.findByText("A comment can have up to 10 files.")).toBeInTheDocument();
		expect(box.value.match(/attachment:/g)).toHaveLength(10);

		// Two deleted links make room for two more.
		fireEvent.change(box, { target: { value: box.value.split("\n").slice(2).join("\n") } });
		await userEvent.upload(screen.getByTestId("pr-comment-file"), [png("g0.png"), png("g1.png")]);
		await waitFor(() => expect(box.value).toContain("![g1.png](attachment:11)"));
		expect(box.value.match(/attachment:/g)).toHaveLength(10);
	});

	it("posts once, however often Comment is pressed, and keeps Cancel off while it posts", async () => {
		let finish: () => void = () => {};
		const onSubmit = vi.fn(() => new Promise<void>((resolve) => { finish = resolve; }));
		const { box } = renderComposer({ onSubmit, onCancel: vi.fn() });
		await userEvent.type(box, "ship it");
		const comment = screen.getByRole("button", { name: "Comment" });
		fireEvent.click(comment);
		fireEvent.click(comment);
		fireEvent.keyDown(box, { key: "Enter", metaKey: true });
		expect(onSubmit).toHaveBeenCalledTimes(1);
		await waitFor(() => expect(screen.getByRole("button", { name: "Cancel" })).toBeDisabled());
		finish();
		await waitFor(() => expect(box.value).toBe(""));
	});

	it("has no Attach without attachments", () => {
		renderComposer();
		expect(screen.queryByRole("button", { name: /Attach/ })).not.toBeInTheDocument();
	});
});
