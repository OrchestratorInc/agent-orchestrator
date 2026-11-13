import { act, render } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { StreamingCode } from "../../lib/code-highlight-protocol";
import { createEngine } from "../../lib/code-highlight-engine";
import { createCodeHighlightStream, type GrammarName } from "../../lib/code-highlight";
import { HighlightedCode } from "./HighlightedCode";

vi.mock("../../lib/code-highlight", async (original) => ({
	...await original<typeof import("../../lib/code-highlight")>(),
	createCodeHighlightStream: vi.fn(),
}));
const engine = await createEngine();
const update = vi.fn<(source: string, language: GrammarName) => Promise<StreamingCode | undefined>>();
const dispose = vi.fn();
beforeEach(() => {
	vi.useFakeTimers(); update.mockReset(); dispose.mockReset();
	vi.mocked(createCodeHighlightStream).mockReturnValue({ update, dispose });
});
async function snapshot(source: string, language: GrammarName): Promise<StreamingCode> {
	return { source, language, lines: await engine.createStream(language)(source) };
}
afterEach(() => { vi.useRealTimers(); });
const tick = () => act(async () => { await vi.advanceTimersByTimeAsync(50); });

describe("streaming code", () => {
	it("keeps resolved string/literal colors separate across template-string lines", async () => {
		update.mockImplementation(snapshot);
		const code = 'function greet(name = "world") {\n  return `hello ${true}\nnext ${false}`;\n}\n';
		const { container } = render(<HighlightedCode code={code + "tail"} language="javascript" streaming />);
		await tick();
		expect([...container.querySelectorAll(".hljs-string")].map((node) => node.textContent).join("")).toContain('"world"');
		const literals = [...container.querySelectorAll(".hljs-keyword")].map((node) => node.textContent);
		expect(literals).toContain("true");
		expect(literals).toContain("false");
		expect(container.querySelector(".hljs-string.hljs-keyword")).toBeNull();
		expect(container.textContent).toBe(code + "tail");
	});

	it("preserves multiline syntax, completed line DOM and exact code while the tail grows", async () => {
		update.mockImplementation(async (code, language) => snapshot(code, language));
		const code = '/* comment\ncontinued\n*/\nconst value = `one\ntwo\n`;\n';
		const { container, rerender } = render(<code><HighlightedCode code={code + "c"} language="javascript" streaming /></code>);
		await tick();
		expect(container.querySelector(".hljs-comment")?.textContent).toContain("comment");
		expect([...container.querySelectorAll(".hljs-comment")].some((node) => node.textContent?.includes("continued"))).toBe(true);
		expect([...container.querySelectorAll(".hljs-string")].some((node) => node.textContent?.includes("two"))).toBe(true);
		const firstLine = container.querySelector("code > span");
		rerender(<code><HighlightedCode code={code + "const next = 2;"} language="javascript" streaming /></code>);
		await tick();
		expect(update).toHaveBeenCalledTimes(1);
		expect(container.querySelector("code > span")).toBe(firstLine);
		expect(container.textContent).toBe(code + "const next = 2;");
		rerender(<code><HighlightedCode code={code + "const next = 2;\nmore"} language="javascript" streaming /></code>);
		await tick();
		expect(container.querySelector("code > span")).toBe(firstLine);
		expect(container.textContent).toBe(code + "const next = 2;\nmore");
	});

	it("coalesces continuous input and keeps only one request in flight", async () => {
		let finish: (result: StreamingCode | undefined) => void = () => {};
		update.mockImplementationOnce(() => new Promise((resolve) => { finish = resolve; }));
		update.mockImplementation(async (code, language) => snapshot(code, language));
		const view = (code: string) => <HighlightedCode code={code} language="javascript" streaming />;
		const { rerender } = render(view("const a = 1;\na"));
		await act(async () => { await vi.advanceTimersByTimeAsync(30); });
		rerender(view("const a = 1;\nconst b = 2;\nb"));
		await act(async () => { await vi.advanceTimersByTimeAsync(20); });
		expect(update).toHaveBeenCalledTimes(1);
		for (let i = 0; i < 5; i++) { rerender(view(`const a = 1;\nconst b = ${i};\nnext`)); await tick(); }
		expect(update).toHaveBeenCalledTimes(1);
		await act(async () => { finish(await snapshot("const a = 1;\nconst b = 2;\n", "javascript")); });
		await tick();
		expect(update).toHaveBeenCalledTimes(2);
		expect(update).toHaveBeenLastCalledWith("const a = 1;\nconst b = 4;\n", "javascript");
	});

	it("rejects stale code/language results, keeps failed highlighting readable and does not retry-loop", async () => {
		let finish: (result: StreamingCode | undefined) => void = () => {};
		update.mockImplementationOnce(() => new Promise((resolve) => { finish = resolve; }));
		update.mockResolvedValue(undefined);
		const { container, rerender } = render(<HighlightedCode code={"const old = 1;\nx"} language="javascript" streaming />);
		await tick();
		rerender(<HighlightedCode code={"different\ntext"} language="python" streaming />);
		await act(async () => { finish(await snapshot("const old = 1;\n", "javascript")); });
		expect(container.textContent).toBe("different\ntext");
		await tick();
		await tick();
		expect(update).toHaveBeenCalledTimes(2);
		expect(container.textContent).toBe("different\ntext");
	});
	it("releases worker state when the code block unmounts", () => {
		const { unmount } = render(<HighlightedCode code="plain" streaming />);
		unmount();
		expect(dispose).toHaveBeenCalledOnce();
	});

});
