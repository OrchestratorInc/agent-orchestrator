import { act, cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { useLayoutEffect, useRef } from "react";
import userEvent from "@testing-library/user-event";
import { ArtifactFindScope, useArtifactFindRegistration } from "./ArtifactFindScope";
import { Dialog, DialogContent, DialogTitle } from "./ui/dialog";
import { DropdownMenu, DropdownMenuContent, DropdownMenuItem, DropdownMenuTrigger } from "./ui/dropdown-menu";
import type { ArtifactFindAdapter, ArtifactFindResult } from "../lib/artifact-find";
import { artifactFindOwner } from "../lib/artifact-find-focus";
import { useKeybindingsStore } from "../stores/keybindings-store";

function Harness({ identity = "a", text = "hello hello", unavailable, dialog = false }: { identity?: string; text?: string; unavailable?: string; dialog?: boolean }) {
	return <div role={dialog ? "dialog" : undefined}>
		<ArtifactFindScope identity={identity}><p tabIndex={0} data-testid="content" data-artifact-find-content="" data-artifact-find-unavailable={unavailable}>{text}</p></ArtifactFindScope>
	</div>;
}
async function search(query = "hello") {
	fireEvent.pointerDown(screen.getByTestId("content"));
	fireEvent.keyDown(document.body, { key: "f", ctrlKey: true });
	const input = await screen.findByRole("searchbox", { name: "Find in artifact" });
	fireEvent.change(input, { target: { value: query } });
	return input;
}
beforeEach(() => {
	vi.spyOn(HTMLElement.prototype, "getClientRects").mockReturnValue([{}] as unknown as DOMRectList);
	Object.defineProperty(HTMLElement.prototype, "scrollIntoView", { configurable: true, value: vi.fn() });
	Object.defineProperty(Range.prototype, "getBoundingClientRect", { configurable: true, value: () => ({ top: 0, bottom: 10, left: 0, right: 10, width: 10, height: 10 }) });
	vi.stubGlobal("Highlight", class extends Set<Range> {});
	vi.stubGlobal("CSS", { ...CSS, escape: CSS.escape, highlights: new Map() });
	useKeybindingsStore.setState({ overrides: {} });
});
afterEach(() => { cleanup(); vi.restoreAllMocks(); vi.unstubAllGlobals(); });

describe("find within a local artifact", () => {
	it("preserves the iframe and its document when search is disabled and re-enabled", () => {
		const content = <iframe title="persistent document" />;
		const view = render(<ArtifactFindScope identity="html">{content}</ArtifactFindScope>);
		const frame = screen.getByTitle("persistent document") as HTMLIFrameElement;
		const doc = frame.contentDocument!;
		doc.body.innerHTML = '<input value="draft"><p>interactive state</p>';
		const post = vi.spyOn(frame.contentWindow!, "postMessage");
		view.rerender(<ArtifactFindScope identity="html" enabled={false}>{content}</ArtifactFindScope>);
		expect(screen.getByTitle("persistent document")).toBe(frame);
		expect(artifactFindOwner()).toBeUndefined();
		expect(post).toHaveBeenCalledWith(expect.objectContaining({ method: "ao/find-configure", params: expect.objectContaining({ bindings: [], open: false }) }), "*");
		expect(post).toHaveBeenCalledWith(expect.objectContaining({ method: "ao/find", params: expect.objectContaining({ query: "" }) }), "*");
		view.rerender(<ArtifactFindScope identity="html">{content}</ArtifactFindScope>);
		expect(frame.contentDocument).toBe(doc);
		expect(doc.querySelector("input")).toHaveValue("draft");
		expect(post.mock.calls.at(-1)?.[0].params.bindings.length).toBeGreaterThan(0);
	});
	it("aborts a pending adapter search when disabled and ignores its late result", async () => {
		let finish!: (result: ArtifactFindResult) => void;
		let signal!: AbortSignal;
		const paint = vi.fn();
		const adapter: ArtifactFindAdapter = { search: vi.fn((_query: string, stop: AbortSignal) => { signal = stop; return new Promise<ArtifactFindResult>((resolve) => { finish = resolve; }); }) };
		function Document() {
			const register = useArtifactFindRegistration(), element = useRef<HTMLParagraphElement>(null);
			useLayoutEffect(() => element.current ? register?.(element.current, adapter) : undefined, [register]);
			return <p ref={element} data-testid="content">hello</p>;
		}
		const view = render(<ArtifactFindScope identity="pdf"><Document /></ArtifactFindScope>);
		await search(); await waitFor(() => expect(adapter.search).toHaveBeenCalled());
		view.rerender(<ArtifactFindScope identity="pdf" enabled={false}><Document /></ArtifactFindScope>);
		expect(signal.aborted).toBe(true);
		await act(async () => finish({ total: 2, paint, clear: vi.fn() }));
		expect(paint).not.toHaveBeenCalled(); expect(screen.queryByRole("searchbox")).toBeNull();
	});
	it("dismisses a portalled menu, then Find, then its real dialog on separate Escapes", async () => {
		const user = userEvent.setup();
		render(<Dialog defaultOpen><DialogContent aria-describedby={undefined}><DialogTitle>Artifact</DialogTitle>
			<ArtifactFindScope identity="menu"><p data-testid="content" data-artifact-find-content="">hello hello</p>
				<DropdownMenu><DropdownMenuTrigger>Actions</DropdownMenuTrigger><DropdownMenuContent><DropdownMenuItem>Action</DropdownMenuItem></DropdownMenuContent></DropdownMenu>
			</ArtifactFindScope>
		</DialogContent></Dialog>);
		await search(); await screen.findByText("1/2");
		await user.click(screen.getByRole("button", { name: "Actions" }));
		await screen.findByRole("menuitem", { name: "Action" });
		await user.keyboard("{Escape}");
		expect(screen.queryByRole("menu")).toBeNull(); expect(screen.getByRole("searchbox")).toHaveValue("hello"); expect(screen.getByRole("dialog")).toBeInTheDocument();
		await user.keyboard("{Escape}");
		expect(screen.queryByRole("searchbox")).toBeNull(); expect(screen.getByRole("dialog")).toBeInTheDocument();
		await user.keyboard("{Escape}"); expect(screen.queryByRole("dialog")).toBeNull();
	});
	it("yields Escape to native prevention and IME composition", async () => {
		render(<Harness />); const input = await search(); await screen.findByText("1/2");
		const prevented = new KeyboardEvent("keydown", { key: "Escape", bubbles: true, cancelable: true }); prevented.preventDefault();
		fireEvent(input, prevented); expect(screen.getByRole("searchbox")).toBe(input);
		fireEvent.keyDown(input, { key: "Escape", isComposing: true }); expect(screen.getByRole("searchbox")).toBe(input);
		fireEvent.compositionStart(input); fireEvent.keyDown(input, { key: "Escape" }); expect(screen.getByRole("searchbox")).toBe(input);
		fireEvent.compositionEnd(input); fireEvent.keyDown(input, { key: "Escape" }); expect(screen.queryByRole("searchbox")).toBeNull();
	});
	it("opens only on the shortcut and navigates and wraps matches", async () => {
		render(<Harness />);
		expect(screen.queryByRole("searchbox")).toBeNull();
		const input = await search();
		await screen.findByText("1/2");
		fireEvent.keyDown(input, { key: "Enter" }); expect(screen.getByText("2/2")).toBeInTheDocument();
		fireEvent.click(screen.getByRole("button", { name: "Next match" })); expect(screen.getByText("1/2")).toBeInTheDocument();
		fireEvent.keyDown(input, { key: "Enter", shiftKey: true }); expect(screen.getByText("2/2")).toBeInTheDocument();
	});
	it("retains the query across files and resets the active match", async () => {
		const view = render(<Harness />);
		const input = await search(); await screen.findByText("1/2");
		fireEvent.keyDown(input, { key: "Enter" });
		view.rerender(<Harness identity="b" text="hello three hello hello" />);
		await screen.findByText("1/3"); expect(input).toHaveValue("hello");
	});
	it("closes Find before a dialog handles Escape, restoring focus", async () => {
		const escaped = vi.fn();
		render(<div onKeyDown={escaped}><Harness dialog /></div>);
		act(() => screen.getByTestId("content").focus());
		const input = await search(); await screen.findByText("1/2");
		escaped.mockClear(); fireEvent.keyDown(input, { key: "Escape" });
		expect(screen.queryByRole("searchbox")).toBeNull(); expect(escaped).not.toHaveBeenCalled();
		expect(screen.getByTestId("content")).toHaveFocus();
	});
	it("reports preview limits instead of a complete-file count", async () => {
		render(<Harness unavailable="partial" />); await search();
		await screen.findByText(/Search is incomplete/); expect(screen.queryByText("1/2")).toBeNull();
	});
	it("updates matches after document content changes", async () => {
		const view = render(<Harness />); await search(); await screen.findByText("1/2");
		view.rerender(<Harness text="hello" />);
		await screen.findByText("1/1");
	});
	it("keeps the active table occurrence when rows are sorted", async () => {
		render(<ArtifactFindScope identity="table"><table data-testid="content" data-artifact-find-content=""><tbody><tr><td>hello first</td></tr><tr><td>hello second</td></tr></tbody></table></ArtifactFindScope>);
		const input = await search(); await screen.findByText("1/2");
		fireEvent.keyDown(input, { key: "Enter" }); expect(screen.getByText("2/2")).toBeInTheDocument();
		const body = screen.getByTestId("content").querySelector("tbody")!;
		act(() => { body.prepend(body.lastElementChild!); });
		await screen.findByText("1/2");
		expect((CSS.highlights.get("artifact-find-active") as unknown as Set<Range>).values().next().value?.startContainer.parentElement?.textContent).toBe("hello second");
	});
	it("gives the last used pane Find and yields to editing", async () => {
		render(<><Harness /><div data-chat-find-scope=""><button>Chat</button></div></>);
		fireEvent.pointerDown(screen.getByTestId("content")); expect(artifactFindOwner()).toBeDefined();
		fireEvent.pointerDown(screen.getByRole("button", { name: "Chat" })); expect(artifactFindOwner()).toBeUndefined();
		const editor = document.createElement("div"); editor.className = "cm-editor"; editor.tabIndex = 0;
		screen.getByTestId("content").append(editor); act(() => editor.focus());
		expect(artifactFindOwner()).toBeUndefined();
	});
	it("claims Find when the pointer targets code inside a shadow root", async () => {
		render(<Harness />);
		const host = document.createElement("diffs-container");
		screen.getByTestId("content").append(host);
		const shadow = host.attachShadow({ mode: "open" });
		const code = document.createElement("span"); code.textContent = "shadow needle"; shadow.append(code);
		code.dispatchEvent(new Event("pointerdown", { bubbles: true, composed: true }));
		fireEvent.keyDown(document.body, { key: "f", ctrlKey: true });
		const input = await screen.findByRole("searchbox", { name: "Find in artifact" });
		fireEvent.change(input, { target: { value: "needle" } });
		await screen.findByText("1/1");
	});
	it("ignores another frame and stale request results", async () => {
		const view = render(<ArtifactFindScope identity="html"><iframe title="document" /></ArtifactFindScope>);
		const frame = screen.getByTitle("document") as HTMLIFrameElement;
		const post = vi.spyOn(frame.contentWindow!, "postMessage");
		fireEvent.pointerDown(frame); fireEvent.keyDown(document.body, { key: "f", ctrlKey: true });
		const input = await screen.findByRole("searchbox"); fireEvent.change(input, { target: { value: "hello" } });
		await waitFor(() => expect(post.mock.calls.some(([message]) => message.method === "ao/find" && message.params.query === "hello")).toBe(true));
		const request = post.mock.calls.findLast(([message]) => message.method === "ao/find" && message.params.query === "hello")![0];
		const data = { jsonrpc: "2.0", method: "ao/find-result", params: { generation: request.params.generation, total: 3 } };
		act(() => window.dispatchEvent(new MessageEvent("message", { source: window, data })));
		expect(screen.queryByText("1/3")).toBeNull();
		act(() => window.dispatchEvent(new MessageEvent("message", { source: frame.contentWindow!, data })));
		await screen.findByText("1/3"); view.unmount();
	});
});
