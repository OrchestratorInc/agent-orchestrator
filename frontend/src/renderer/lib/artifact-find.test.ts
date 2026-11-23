import { afterEach, describe, expect, it, vi } from "vitest";
import { createArtifactFindEngine, readArtifactFindResult } from "./artifact-find";
import { findTextOccurrences } from "./chat-find";

afterEach(() => { document.body.replaceChildren(); vi.unstubAllGlobals(); });

describe("artifact text search", () => {
	it.each([
		["Hello HELLO hello", "hello"], ["Straße STRASSE ẞ", "strasse"], ["café cafe\u0301", "café"],
		["first\nsecond\tthird", "first second"], ["a  b\u00a0c", "a b c"], ["ﬃ file", "ffi"],
		["Σ σ ς", "σ"], ["İstanbul", "İSTANBUL"], ["a😀b😀", "😀"], ["nothing", "  "],
	])("matches session find rules for %s", (text, query) => {
		expect(createArtifactFindEngine().occurrences(text, query)).toEqual(findTextOccurrences(text, query));
	});
	it("searches formatted text, table headers and shadow code, excluding controls and hidden content", () => {
		document.body.innerHTML = `<article><p>hello <strong>world</strong></p><table><thead><tr><th><button>Header</button></th></tr></thead><tbody><tr><td>value</td></tr></tbody></table><span hidden>secret</span><span style="display:none">secret</span><script>secret</script><button>secret</button><div data-selection-toolbar-header>secret</div><div data-line-annotation>secret</div><details><summary>Summary</summary>secret</details><diffs-container></diffs-container></article>`;
		const shadow = document.querySelector("diffs-container")!.attachShadow({ mode: "open" });
		shadow.innerHTML = `<span data-column-number="1">secret</span><span data-line="1">const <b>needle</b> = 1;</span>`;
		const engine = createArtifactFindEngine();
		expect(engine.ranges(document.body, "hello world")[0].toString()).toBe("hello world");
		expect(engine.ranges(document.body, "Header")).toHaveLength(1);
		expect(engine.ranges(document.body, "secret")).toHaveLength(0);
		expect(engine.ranges(document.body, "const needle")[0].toString()).toBe("const needle");
		expect(engine.ranges(document.body, "Summary")).toHaveLength(1);
	});
	it("finds across PDF text items and line breaks", () => {
		document.body.innerHTML = `<div class="pdf-text-layer"><span>hello </span><span>world</span><br><span>next</span></div>`;
		const engine = createArtifactFindEngine();
		expect(engine.ranges(document.body, "hello world next")).toHaveLength(1);
	});
	it("finds whitespace across Pierre code lines without including line numbers or feedback", () => {
		document.body.innerHTML = "<diffs-container></diffs-container>";
		const shadow = document.querySelector("diffs-container")!.attachShadow({ mode: "open" });
		shadow.innerHTML = `<pre><code><div data-column-number>1234</div><div data-content><div data-line="1">hello\n</div><div data-line="2"><b>world</b></div><div data-annotation-slot>feedback</div></div></code></pre>`;
		const engine = createArtifactFindEngine();
		expect(engine.ranges(document.body, "hello world")[0].toString()).toBe("hello\nworld");
		expect(engine.ranges(document.body, "1234")).toHaveLength(0);
		expect(engine.ranges(document.body, "feedback")).toHaveLength(0);
	});
	it("retains a sorted table match even when React reuses row and text nodes", () => {
		document.body.innerHTML = "<table><tbody><tr><td>hello first</td></tr><tr><td>hello second</td></tr></tbody></table>";
		const engine = createArtifactFindEngine();
		const previous = engine.anchor(engine.ranges(document.body, "hello")[1]);
		const cells = document.querySelectorAll("td");
		cells[0].firstChild!.textContent = "hello second";
		cells[1].firstChild!.textContent = "hello first";
		expect(engine.retained(engine.ranges(document.body, "hello"), previous)).toBe(0);
	});
	it("clears only its own highlights and preserves document markup", () => {
		const registry = new Map();
		vi.stubGlobal("CSS", { highlights: registry });
		vi.stubGlobal("Highlight", class extends Set<Range> {});
		document.body.innerHTML = "<p>one two one</p>";
		const first = createArtifactFindEngine(), second = createArtifactFindEngine();
		first.paint(first.ranges(document.body, "one"), 0, document);
		expect(registry.get("artifact-find-matches").size).toBe(2);
		second.paint(second.ranges(document.body, "two"), 0, document);
		first.clear();
		expect(registry.get("artifact-find-matches").size).toBe(1);
		expect(document.body.innerHTML).toBe("<p>one two one</p>");
		second.clear(); expect(registry.size).toBe(0);
	});
	it("accepts only valid responses for the current frame request", () => {
		const message = { jsonrpc: "2.0", method: "ao/find-result", params: { generation: 4, total: 2 } };
		expect(readArtifactFindResult(message, 4)).toEqual({ total: 2 });
		expect(readArtifactFindResult(message, 3)).toBeUndefined();
		for (const total of [-1, 0.5, Infinity, "2", null]) expect(readArtifactFindResult({ ...message, params: { generation: 4, total } }, 4)).toBeUndefined();
	});
});
