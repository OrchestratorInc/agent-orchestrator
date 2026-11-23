import { act, cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { ArtifactFindScope } from "./ArtifactFindScope";
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
	vi.stubGlobal("CSS", { highlights: new Map() });
	useKeybindingsStore.setState({ overrides: {} });
});
afterEach(() => { cleanup(); vi.restoreAllMocks(); vi.unstubAllGlobals(); });

describe("find within a local artifact", () => {
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
