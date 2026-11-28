import { afterEach, describe, expect, it, vi } from "vitest";
import { createArtifactFindEngine, readArtifactFindResult } from "./artifact-find";
import { findTextOccurrences } from "./chat-find";

afterEach(() => { document.body.replaceChildren(); vi.restoreAllMocks(); vi.unstubAllGlobals(); vi.useRealTimers(); });

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
	it("maps dense fragmented matches with linear node access and yields for cancellation", async () => {
		const root = document.createElement("pre"); document.body.append(root);
		let reads = 0;
		for (let i = 0; i < 2000; i++) {
			const text = document.createTextNode("needle ");
			Object.defineProperty(text, "length", { get: () => { reads++; return 7; } });
			root.append(text);
		}
		const engine = createArtifactFindEngine();
		expect(await engine.search(root, "needle")).toHaveLength(2000);
		expect(reads).toBeLessThan(16000);
		const stop = new AbortController();
		// Force a task boundary independently of the machine's speed.
		let clock = 0; vi.spyOn(performance, "now").mockImplementation(() => clock += 2);
		setTimeout(() => stop.abort(), 0);
		await expect(engine.search(root, "needle", stop.signal)).rejects.toMatchObject({ name: "AbortError" });
		vi.restoreAllMocks();
	});
	it("counts and highlights composed phrases once across assigned and fallback slots", async () => {
		document.body.innerHTML = '<x-card>needle</x-card>';
		const host = document.querySelector("x-card")!;
		const shadow = host.attachShadow({ mode: "open" });
		shadow.innerHTML = '<p>prefix <slot>fallback</slot> suffix</p>';
		const engine = createArtifactFindEngine();
		const matches = await engine.search(document.body, "prefix needle suffix");
		expect(matches).toHaveLength(1);
		expect(matches[0].map((part) => part.toString()).join("")).toBe("prefix needle suffix");
		const registry = new Map(); vi.stubGlobal("CSS", { highlights: registry }); vi.stubGlobal("Highlight", class extends Set<Range> {});
		engine.paint(matches, 0, document);
		expect(registry.get("artifact-find-active").size).toBe(3);
		expect(await engine.search(document.body, "fallback")).toHaveLength(0);
		host.replaceChildren();
		expect(await engine.search(document.body, "prefix fallback suffix")).toHaveLength(1);
	});
	it("follows slot order, nested slots, reassignment, and hidden exclusions", async () => {
		document.body.innerHTML = '<x-card><span slot="a">first</span><span slot="b">second</span></x-card>';
		const host = document.querySelector("x-card")!, shadow = host.attachShadow({ mode: "open" });
		shadow.innerHTML = '<p><slot name="b"></slot> <slot name="a"></slot><span hidden>secret</span></p>';
		const engine = createArtifactFindEngine();
		const matches = await engine.search(document.body, "second first");
		expect(matches).toHaveLength(1);
		expect(matches[0].map((part) => part.toString()).join("")).toBe("second first");
		expect(await engine.search(document.body, "secret")).toHaveLength(0);
		host.querySelector("span")!.slot = "b";
		expect(await engine.search(document.body, "second first")).toHaveLength(0);
		const nested = document.createElement("x-nested"); nested.slot = "a"; nested.textContent = "nested";
		nested.attachShadow({ mode: "open" }).innerHTML = "<slot></slot>"; host.append(nested);
		expect(await engine.search(document.body, "nested")).toHaveLength(1);
	});
	it("keeps text the walk skipped outside a match's highlight", async () => {
		document.body.innerHTML = '<x-card><span slot="a">first </span><span>unassigned</span><span slot="a">second</span></x-card>';
		document.querySelector("x-card")!.attachShadow({ mode: "open" }).innerHTML = '<p><slot name="a"></slot></p>';
		const matches = await createArtifactFindEngine().search(document.body, "first second");
		expect(matches).toHaveLength(1);
		expect(matches[0].map((part) => part.toString())).toEqual(["first ", "second"]);
		document.body.innerHTML = '<p><b>adjacent </b><i>words</i></p>';
		expect((await createArtifactFindEngine().search(document.body, "adjacent words"))[0].map((part) => part.toString())).toEqual(["adjacent words"]);
	});
	it("starts again when text changes during a yield instead of shifting offsets", async () => {
		document.body.innerHTML = '<p>needle</p><p><span>a</span>needle</p>';
		const span = document.querySelector("span")!.firstChild as Text;
		// Yield after every step, so the edit lands between the two paragraphs' mapping.
		let clock = 0; vi.spyOn(performance, "now").mockImplementation(() => clock += 10);
		const pending = createArtifactFindEngine().search(document.body, "needle");
		span.data = "abcdef";
		const matches = await pending;
		expect(matches.map((match) => match.map((part) => part.toString()).join(""))).toEqual(["needle", "needle"]);
	});
	it("refreshes once per interval while content keeps changing", async () => {
		vi.useFakeTimers();
		document.body.innerHTML = '<p id="clock">0</p>';
		const changed = vi.fn(), dispose = createArtifactFindEngine().observe(document.body, changed);
		let tick = 0; const timer = setInterval(() => { document.getElementById("clock")!.textContent = String(++tick); }, 50);
		try {
			await vi.advanceTimersByTimeAsync(500);
			expect(changed.mock.calls.length).toBeGreaterThanOrEqual(3);
		} finally { clearInterval(timer); dispose(); }
	});
	it("backs off shadow-root discovery while idle and returns to the short interval after a mutation", async () => {
		vi.useFakeTimers();
		document.body.innerHTML = "<x-late></x-late><p>idle</p>";
		const walk = vi.spyOn(Element.prototype, "shadowRoot", "get");
		const changed = vi.fn(), dispose = createArtifactFindEngine().observe(document.body, changed);
		try {
			await vi.advanceTimersByTimeAsync(500); const early = walk.mock.calls.length;
			await vi.advanceTimersByTimeAsync(20_000); const idle = walk.mock.calls.length - early;
			// At a fixed 500 ms the 20 s window would hold 40 passes; backed off it holds at most a handful.
			expect(idle / Math.max(1, early)).toBeLessThan(8);
			// A root attached without a mutation is still found within the longest interval.
			const shadow = document.querySelector("x-late")!.attachShadow({ mode: "open" }); shadow.innerHTML = "<p>late</p>";
			await vi.advanceTimersByTimeAsync(8_200); changed.mockClear();
			shadow.querySelector("p")!.textContent = "edited";
			await vi.advanceTimersByTimeAsync(150); expect(changed).toHaveBeenCalled();
		} finally { dispose(); }
	});
	it("refreshes when a reader excludes content from Find", async () => {
		vi.useFakeTimers();
		document.body.innerHTML = "<p>needle</p>";
		const changed = vi.fn(), dispose = createArtifactFindEngine().observe(document.body, changed);
		try {
			await vi.advanceTimersByTimeAsync(50); changed.mockClear();
			document.querySelector("p")!.setAttribute("data-artifact-find-exclude", "");
			await vi.advanceTimersByTimeAsync(150);
			expect(changed).toHaveBeenCalled();
		} finally { dispose(); }
	});
	it("finds text a descendant makes visible again under visibility:hidden", async () => {
		document.body.innerHTML = '<div style="visibility:hidden">secret <span style="visibility:visible">shown</span></div>';
		const engine = createArtifactFindEngine();
		expect(await engine.search(document.body, "shown")).toHaveLength(1);
		expect(await engine.search(document.body, "secret")).toHaveLength(0);
	});
	it("starts again when a text node is replaced by one with the same text", async () => {
		document.body.innerHTML = "<p>needle</p><p>a needle</p>";
		const second = document.querySelectorAll("p")[1];
		let clock = 0; vi.spyOn(performance, "now").mockImplementation(() => clock += 10);
		const pending = createArtifactFindEngine().search(document.body, "needle");
		second.replaceChildren(document.createTextNode("a needle"));
		expect(await pending).toHaveLength(2);
	});
	it("observes nested and late shadow roots and stops all discovery on teardown", async () => {
		vi.useFakeTimers();
		document.body.innerHTML = '<x-card></x-card>';
		const host = document.querySelector("x-card")!, changed = vi.fn(), engine = createArtifactFindEngine();
		const dispose = engine.observe(document.body, changed);
		try {
			const shadow = host.attachShadow({ mode: "open" }); shadow.innerHTML = '<p>needle</p><x-nested></x-nested>';
			const nested = shadow.querySelector("x-nested")!.attachShadow({ mode: "open" }); nested.innerHTML = "<slot>fallback</slot>";
			await vi.advanceTimersByTimeAsync(650); expect(changed).toHaveBeenCalled(); changed.mockClear();
			nested.querySelector("slot")!.textContent = "updated";
			await vi.advanceTimersByTimeAsync(150); expect(changed).toHaveBeenCalled();
			dispose(); changed.mockClear(); shadow.querySelector("p")!.textContent = "later";
			await vi.advanceTimersByTimeAsync(1000); expect(changed).not.toHaveBeenCalled();
			expect(vi.getTimerCount()).toBe(0);
		} finally { dispose(); }
	});
});
