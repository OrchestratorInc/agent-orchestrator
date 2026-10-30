import userEvent from "@testing-library/user-event";
import { render, screen } from "@testing-library/react";
import { MarkdownEditor } from "./MarkdownEditor";

describe("MarkdownEditor", () => {
	it("renders the source and reports edits without recreating the editor", () => {
		const onChange = vi.fn();
		const { rerender } = render(<MarkdownEditor filePath="README.md" onChange={onChange} value="# Hello" />);
		const editor = screen.getByRole("textbox", { name: "Edit README.md" });
		expect(editor.querySelector(".cm-content")).toHaveTextContent("# Hello");
		expect(screen.getByRole("toolbar", { name: "Markdown formatting" })).toBeInTheDocument();
		expect(screen.getByRole("button", { name: "Bold" })).toBeInTheDocument();
		expect(screen.getByRole("button", { name: "Bulleted list" })).toBeInTheDocument();
		expect(screen.getByRole("button", { name: "Insert link" })).toBeInTheDocument();

		rerender(<MarkdownEditor filePath="README.md" onChange={onChange} value="# Hello" />);
		expect(editor.querySelector(".cm-content")).toHaveTextContent("# Hello");
		expect(onChange).not.toHaveBeenCalled();
	});

	it("keeps local edits when the source value refreshes", async () => {
		const user = userEvent.setup();
		const onChange = vi.fn();
		const { rerender } = render(<MarkdownEditor filePath="README.md" onChange={onChange} value="# Hello" />);
		const content = screen.getByRole("textbox", { name: "Edit README.md" }).querySelector(".cm-content");
		expect(content).not.toBeNull();

		await user.click(content!);
		await user.type(content!, "!");
		expect(content).toHaveTextContent("!# Hello");

		rerender(<MarkdownEditor filePath="README.md" onChange={onChange} value="# Refreshed from disk" />);
		expect(content).toHaveTextContent("!# Hello");
		expect(content).not.toHaveTextContent("Refreshed from disk");
	});

	it("preserves CRLF line endings when emitting edits", async () => {
		const user = userEvent.setup();
		const onChange = vi.fn();
		const { container } = render(<MarkdownEditor filePath="README.md" onChange={onChange} value={"# Hello\r\n\r\nWorld"} />);
		const content = container.querySelector(".cm-content");
		expect(content).not.toBeNull();

		await user.click(content!);
		await user.type(content!, "!");

		expect(onChange).toHaveBeenLastCalledWith("!# Hello\r\n\r\nWorld");
	});
});
