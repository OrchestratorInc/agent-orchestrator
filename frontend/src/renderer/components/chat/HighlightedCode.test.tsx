import { render, waitFor } from "@testing-library/react";
import { describe, expect, it } from "vitest";
import { highlight } from "../../lib/code-highlight";
import { HighlightedCode } from "./HighlightedCode";

describe("highlighted code", () => {
	it("keeps resolved string/literal colors separate across template-string lines", async () => {
		await highlight("const warm = 1;", "javascript");
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
});
