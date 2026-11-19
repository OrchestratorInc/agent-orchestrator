import { render, waitFor } from "@testing-library/react";
import { describe, expect, it } from "vitest";
import { HighlightedCode } from "./HighlightedCode";

describe("highlighted code", () => {
	it("keeps resolved string/literal colors separate across template-string lines", async () => {
		const code = 'function greet(name = "world") {\n  return `hello ${true}\nnext ${false}`;\n}\n';
		const { container } = render(<HighlightedCode code={code} language="javascript" />);
		await waitFor(() => expect(container.querySelector(".hljs-string")).not.toBeNull());
		expect([...container.querySelectorAll(".hljs-string")].map((node) => node.textContent).join("")).toContain('"world"');
		const literals = [...container.querySelectorAll(".hljs-keyword")].map((node) => node.textContent);
		expect(literals).toContain("true");
		expect(literals).toContain("false");
		expect(container.querySelector(".hljs-string.hljs-keyword")).toBeNull();
		expect(container.textContent).toBe(code);
	});

	it("streams a fence line by line into the same markup the settled block gets", () => {
		const code = "/* a\n   b */\nconst s = `x ${1}\ny`;\nfunction f(a: number) {\n\treturn a;\n}\n";
		const { container, rerender } = render(<HighlightedCode code={code.slice(0, 3)} language="typescript" streaming />);
		for (let end = 7; end < code.length; end += 4) {
			rerender(<HighlightedCode code={code.slice(0, end)} language="typescript" streaming />);
			expect(container.textContent).toBe(code.slice(0, end));
		}
		rerender(<HighlightedCode code={code} language="typescript" streaming />);
		const streamed = container.innerHTML;
		expect([...container.querySelectorAll(".hljs-comment")].map((node) => node.textContent).join("")).toBe("/* a   b */");
		rerender(<HighlightedCode code={code} language="typescript" />);
		expect(streamed.startsWith(container.innerHTML)).toBe(true);
		const { container: fresh } = render(<HighlightedCode code={code} language="typescript" />);
		expect(fresh.innerHTML).toBe(container.innerHTML);
	});
});
