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
});
