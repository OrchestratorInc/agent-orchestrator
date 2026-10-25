import { act, render } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { Root } from "hast";
import { createEngine } from "../../lib/code-highlight-engine";
import { highlightStreaming } from "../../lib/code-highlight";
import { HighlightedCode } from "./HighlightedCode";

vi.mock("../../lib/code-highlight", async (original) => ({
	...await original<typeof import("../../lib/code-highlight")>(),
	highlightStreaming: vi.fn(),
}));
const engine = createEngine();
beforeEach(() => { vi.useFakeTimers(); vi.mocked(highlightStreaming).mockReset(); });
afterEach(() => { vi.useRealTimers(); });
const tick = () => act(async () => { await vi.advanceTimersByTimeAsync(50); });

describe("streaming code", () => {
	it("keeps nested token scopes separate on every highlighted line", async () => {
		vi.mocked(highlightStreaming).mockImplementation(async (code, language) => engine.highlight(language, code));
		const code = 'function greet(name = "world") {\n  return `hello ${true}\nnext ${false}`;\n}\n';
		const { container } = render(<HighlightedCode code={code + "tail"} language="javascript" streaming />);
		await tick();
		expect(container.querySelector(".hljs-params .hljs-string")).toHaveTextContent('"world"');
		expect(container.querySelector(".hljs-params.hljs-string")).toBeNull();
		const literals = [...container.querySelectorAll(".hljs-string .hljs-subst .hljs-literal")];
		expect(literals.map((node) => node.textContent)).toEqual(["true", "false"]);
		expect(container.querySelector(".hljs-string.hljs-literal")).toBeNull();
		expect(container.textContent).toBe(code + "tail");
	});

	it("preserves multiline syntax, completed line DOM and exact code while the tail grows", async () => {
		vi.mocked(highlightStreaming).mockImplementation(async (code, language) => engine.highlight(language, code));
		const code = '/* comment\ncontinued\n*/\nconst value = `one\ntwo\n`;\n';
		const { container, rerender } = render(<code><HighlightedCode code={code + "c"} language="javascript" streaming /></code>);
		await tick();
		expect(container.querySelector(".hljs-comment")?.textContent).toContain("comment");
		expect([...container.querySelectorAll(".hljs-comment")].some((node) => node.textContent?.includes("continued"))).toBe(true);
		expect([...container.querySelectorAll(".hljs-string")].some((node) => node.textContent?.includes("two"))).toBe(true);
		const firstLine = container.querySelector("code > span");
		rerender(<code><HighlightedCode code={code + "const next = 2;"} language="javascript" streaming /></code>);
		await tick();
		expect(highlightStreaming).toHaveBeenCalledTimes(1);
		expect(container.querySelector("code > span")).toBe(firstLine);
		expect(container.textContent).toBe(code + "const next = 2;");
		rerender(<code><HighlightedCode code={code + "const next = 2;\nmore"} language="javascript" streaming /></code>);
		await tick();
		expect(container.querySelector("code > span")).toBe(firstLine);
		expect(container.textContent).toBe(code + "const next = 2;\nmore");
	});

	it("coalesces continuous input and keeps only one request in flight", async () => {
		let finish: (tree: Root | undefined) => void = () => {};
		vi.mocked(highlightStreaming).mockImplementationOnce(() => new Promise((resolve) => { finish = resolve; }));
		vi.mocked(highlightStreaming).mockImplementation(async (code, language) => engine.highlight(language, code));
		const view = (code: string) => <HighlightedCode code={code} language="javascript" streaming />;
		const { rerender } = render(view("const a = 1;\na"));
		await act(async () => { await vi.advanceTimersByTimeAsync(30); });
		rerender(view("const a = 1;\nconst b = 2;\nb"));
		await act(async () => { await vi.advanceTimersByTimeAsync(20); });
		expect(highlightStreaming).toHaveBeenCalledTimes(1);
		for (let i = 0; i < 5; i++) { rerender(view(`const a = 1;\nconst b = ${i};\nnext`)); await tick(); }
		expect(highlightStreaming).toHaveBeenCalledTimes(1);
		await act(async () => { finish(engine.highlight("javascript", "const a = 1;\nconst b = 2;\n")); });
		await tick();
		expect(highlightStreaming).toHaveBeenCalledTimes(2);
		expect(highlightStreaming).toHaveBeenLastCalledWith("const a = 1;\nconst b = 4;\n", "javascript");
	});

	it("rejects stale code/language results, keeps failed highlighting readable and does not retry-loop", async () => {
		let finish: (tree: Root | undefined) => void = () => {};
		vi.mocked(highlightStreaming).mockImplementationOnce(() => new Promise((resolve) => { finish = resolve; }));
		vi.mocked(highlightStreaming).mockResolvedValue(undefined);
		const { container, rerender } = render(<HighlightedCode code={"const old = 1;\nx"} language="javascript" streaming />);
		await tick();
		rerender(<HighlightedCode code={"different\ntext"} language="python" streaming />);
		await act(async () => { finish(engine.highlight("javascript", "const old = 1;\n")); });
		expect(container.textContent).toBe("different\ntext");
		await tick();
		await tick();
		expect(highlightStreaming).toHaveBeenCalledTimes(2);
		expect(container.textContent).toBe("different\ntext");
	});
});
