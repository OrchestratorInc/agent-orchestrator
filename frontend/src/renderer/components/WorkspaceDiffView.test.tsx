import { act, cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { FileAnnotationComposer, FileAnnotationSendBar, ReviewDiffBody, type FileAnnotationModel } from "./WorkspaceDiffView";
import type { WorkspaceFileDetail } from "../hooks/useSessionWorkspaceFiles";

const { postMock } = vi.hoisted(() => ({ postMock: vi.fn() }));

vi.mock("../lib/api-client", () => ({
	apiClient: { POST: postMock },
	getApiBaseUrl: () => "",
	apiErrorMessage: (error: unknown, fallback = "Request failed") => {
		if (error instanceof Error) return error.message;
		return fallback;
	},
}));

/**
 * @tanstack/react-virtual falls back to a 150ms scroll-end debounce when the
 * environment has no `scrollend` event (jsdom). That timer is not cancelled on
 * unmount, so it can fire after vitest tears down jsdom — drain past it while
 * `window` still exists.
 */
async function drainVirtualizerScrollDebounce() {
	cleanup();
	await act(async () => {
		await new Promise((resolve) => setTimeout(resolve, 200));
	});
}

// A diff line's content lives in a span with a `whitespace-pre*` class. Intra-line
// word highlighting splits that span into child spans, so match on the wrapper's
// full text content rather than a single text node.
function diffLine(text: string) {
	return (_content: string, element: Element | null): boolean =>
		element != null && /whitespace-pre/.test(element.className) && element.textContent === text;
}

// Intra-line word highlights nest the row's code text a level or two deeper
// than a plain row, so a selection Range needs to walk down to a real text
// node rather than assuming firstChild is one.
function findTextNode(node: Node): Text | null {
	if (node.nodeType === Node.TEXT_NODE) return node as Text;
	for (const child of Array.from(node.childNodes)) {
		const found = findTextNode(child);
		if (found) return found;
	}
	return null;
}

function noopAnnotation(): FileAnnotationModel {
	return {
		targets: [],
		status: "idle",
		error: "",
		begin: vi.fn(),
		draftFor: () => "", statusFor: () => "idle",
		setDraft: vi.fn(),
		cancel: vi.fn(),
		submit: vi.fn(),
	};
}

function baseDetail(overrides: Partial<WorkspaceFileDetail> = {}): WorkspaceFileDetail {
	return {
		sessionId: "sess-1",
		path: "src/App.tsx",
		status: "modified",
		additions: 1,
		deletions: 1,
		size: 120,
		binary: false,
		deleted: false,
		content: "",
		contentTruncated: false,
		diff: "diff --git a/src/App.tsx b/src/App.tsx\nindex 111..222 100644\n--- a/src/App.tsx\n+++ b/src/App.tsx\n@@ -1,1 +1,1 @@\n-const value = 0;\n+const value = 1;\n",
		diffTruncated: false,
		...overrides,
	};
}

describe("ReviewDiffBody", () => {
	beforeEach(() => {
		postMock.mockReset().mockResolvedValue({ data: {} });
		window.getSelection()?.removeAllRanges();
		Object.defineProperty(navigator, "clipboard", { configurable: true, value: { writeText: vi.fn() } });
	});

	afterEach(async () => {
		await drainVirtualizerScrollDebounce();
		vi.useRealTimers();
		window.getSelection()?.removeAllRanges();
	});

	it("renders a real diff without git-header noise and with markers in the gutter", async () => {
		const { container } = render(
			<ReviewDiffBody
				annotation={noopAnnotation()}
				detail={baseDetail()}
				detailLoadedAt={1}
				filePath="src/App.tsx"
				onActiveSelectionChange={vi.fn()}
				sessionId="sess-1"
				split={false}
				wrap={true}
			/>,
		);

		expect(await screen.findByText(diffLine("const value = 1;"))).toBeInTheDocument();
		expect(screen.getByText(diffLine("const value = 0;"))).toBeInTheDocument();
		expect(screen.getByText("@@ -1,1 +1,1 @@")).toBeInTheDocument();
		expect(screen.queryByText("diff --git a/src/App.tsx b/src/App.tsx")).not.toBeInTheDocument();
		expect(container.querySelector(".diff-code")).toHaveClass("select-text");
	});

	it("highlights only the changed tokens within a replaced line", async () => {
		const { container } = render(
			<ReviewDiffBody
				annotation={noopAnnotation()}
				detail={baseDetail()}
				detailLoadedAt={1}
				filePath="src/App.tsx"
				onActiveSelectionChange={vi.fn()}
				sessionId="sess-1"
				split={false}
				wrap={true}
			/>,
		);

		await screen.findByText(diffLine("const value = 1;"));
		expect(container.querySelector('[class*="bg-success/35"]')?.textContent).toBe("1");
		expect(container.querySelector('[class*="bg-error/35"]')?.textContent).toBe("0");
	});

	it("renders both sides side by side when split is requested", async () => {
		const { container } = render(
			<ReviewDiffBody
				annotation={noopAnnotation()}
				detail={baseDetail()}
				detailLoadedAt={1}
				filePath="src/App.tsx"
				onActiveSelectionChange={vi.fn()}
				sessionId="sess-1"
				split={true}
				wrap={true}
			/>,
		);

		await screen.findByText(diffLine("const value = 1;"));
		expect(container.querySelector(".grid-cols-2")).not.toBeNull();
		expect(screen.getByText(diffLine("const value = 0;"))).toBeInTheDocument();
	});

	it("shows a binary placeholder instead of a diff", () => {
		render(
			<ReviewDiffBody
				annotation={noopAnnotation()}
				detail={baseDetail({ binary: true, diff: "" })}
				detailLoadedAt={1}
				filePath="screenshot.png"
				onActiveSelectionChange={vi.fn()}
				sessionId="sess-1"
				split={false}
				wrap={true}
			/>,
		);

		expect(screen.getByText("Binary file preview is not available.")).toBeInTheDocument();
	});

	it("renders both sides of a changed image instead of the binary placeholder", async () => {
		render(
			<ReviewDiffBody
				annotation={noopAnnotation()}
				detail={baseDetail({ binary: true, diff: "", imageMediaType: "image/png", path: "docs/logo.png" })}
				detailLoadedAt={1}
				filePath="docs/logo.png"
				onActiveSelectionChange={vi.fn()}
				sessionId="sess-1"
				split={false}
				wrap={true}
			/>,
		);

		const before = await screen.findByAltText("Before version of docs/logo.png");
		const after = await screen.findByAltText("After version of docs/logo.png");
		expect(before).toHaveAttribute(
			"src",
			expect.stringContaining("/api/v1/sessions/sess-1/workspace/file/blob?path=docs%2Flogo.png&side=before"),
		);
		expect(after).toHaveAttribute(
			"src",
			expect.stringContaining("/api/v1/sessions/sess-1/workspace/file/blob?path=docs%2Flogo.png&side=after"),
		);
		expect(screen.queryByText("Binary file preview is not available.")).not.toBeInTheDocument();
	});

	it("keeps native text selection and does not replace its context menu", async () => {
		const onActiveSelectionChange = vi.fn();
		render(
			<ReviewDiffBody
				annotation={noopAnnotation()}
				detail={baseDetail()}
				detailLoadedAt={1}
				filePath="src/App.tsx"
				onActiveSelectionChange={onActiveSelectionChange}
				sessionId="sess-1"
				split={false}
				wrap={true}
			/>,
		);
		const addedRow = (await screen.findByText(diffLine("const value = 1;"))).closest("[data-row-index]") as HTMLElement;
		const range = document.createRange();
		const textNode = findTextNode(addedRow.querySelector("span:last-child")!)!;
		range.setStart(textNode, 0);
		range.setEnd(textNode, textNode.textContent?.length ?? 0);
		const selection = window.getSelection();
		selection?.removeAllRanges();
		selection?.addRange(range);
		await act(async () => {
			await new Promise((resolve) => setTimeout(resolve, 0));
		});

		expect(onActiveSelectionChange).toHaveBeenLastCalledWith(true);
		expect(fireEvent.contextMenu(addedRow, { clientX: 5, clientY: 5 })).toBe(true);
		expect(screen.queryByRole("menuitem", { name: "Explain" })).not.toBeInTheDocument();
	});

	it("focuses the feedback textarea when an inline composer opens", async () => {
		const model = noopAnnotation();
		model.targets = [{ path: "src/App.tsx", side: "new", line: 12, surface: "focused" }];
		render(<FileAnnotationComposer annotation={model} target={model.targets[0]} />);

		const textarea = screen.getByRole("textbox", { name: /Feedback for src\/App\.tsx/ });
		await waitFor(() => expect(textarea).toHaveFocus());
	});

	it("keeps typing local to the box; ⌘/Ctrl+Enter sends, as its hint says, and plain Enter doesn't", () => {
		const model = noopAnnotation();
		model.targets = [{ path: "src/App.tsx", side: "file", surface: "review" }];
		render(<FileAnnotationComposer annotation={model} target={model.targets[0]} />);

		expect(screen.getByText("⌘/Ctrl + Enter to send")).toBeInTheDocument();
		const textarea = screen.getByRole("textbox", { name: /Feedback for src\/App\.tsx/ });
		fireEvent.change(textarea, { target: { value: "Rename this" } });
		// The model hears that the box now has text, then nothing per keystroke.
		expect(model.setDraft).toHaveBeenCalledTimes(1);
		fireEvent.change(textarea, { target: { value: "Rename this prop" } });
		expect(model.setDraft).toHaveBeenCalledTimes(1);
		fireEvent.change(textarea, { target: { value: "Rename this" } });
		const draftCalls = vi.mocked(model.setDraft).mock.calls.length;
		fireEvent.keyDown(textarea, { key: "Enter" });
		expect(model.setDraft).toHaveBeenCalledTimes(draftCalls);
		expect(model.submit).not.toHaveBeenCalled();
		// The shortcut hands over this box's text, then sends every written comment.
		fireEvent.keyDown(textarea, { key: "Enter", metaKey: true });
		expect(model.setDraft).toHaveBeenLastCalledWith(model.targets[0], "Rename this");
		expect(model.submit).toHaveBeenCalledWith();
		fireEvent.keyDown(textarea, { key: "Enter", ctrlKey: true });
		expect(model.submit).toHaveBeenCalledTimes(2);
	});

	it("puts Send beside Cancel below the field and sends the typed text on click", () => {
		const model = noopAnnotation();
		model.targets = [{ path: "src/App.tsx", side: "file", surface: "review" }];
		render(<FileAnnotationComposer annotation={model} target={model.targets[0]} />);

		const textarea = screen.getByRole("textbox", { name: /Feedback for src\/App\.tsx/ });
		const send = screen.getByRole("button", { name: "Send feedback" });
		expect(send).toBeDisabled();
		expect(send.parentElement).toContainElement(screen.getByRole("button", { name: "Cancel" }));
		expect(textarea.compareDocumentPosition(send) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy();

		fireEvent.change(textarea, { target: { value: "Add a test" } });
		fireEvent.click(send);
		expect(model.submit).toHaveBeenCalledWith(model.targets[0], "Add a test");
	});

	it("cancels on Escape or the Cancel button below the field and hands an unsent draft back to the model", () => {
		const model = noopAnnotation();
		model.targets = [{ path: "src/App.tsx", side: "new", line: 3, surface: "review" }];
		const { unmount } = render(<FileAnnotationComposer annotation={model} target={model.targets[0]} />);

		const textarea = screen.getByRole("textbox", { name: /Feedback for src\/App\.tsx/ });
		fireEvent.change(textarea, { target: { value: "draft" } });
		fireEvent.keyDown(textarea, { key: "Escape" });
		expect(model.cancel).toHaveBeenCalledTimes(1);
		expect(model.cancel).toHaveBeenCalledWith(model.targets[0]);
		const cancel = screen.getByRole("button", { name: "Cancel" });
		// A labelled text button under the field, not an icon beside it.
		expect(cancel).toHaveTextContent("Cancel");
		expect(textarea.compareDocumentPosition(cancel) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy();
		fireEvent.click(cancel);
		expect(model.cancel).toHaveBeenCalledTimes(2);
		unmount();
		expect(model.setDraft).toHaveBeenCalledWith(model.targets[0], "draft");
	});

	it("keeps a box per open comment, each sending only its own text", () => {
		const model = noopAnnotation();
		const first = { path: "src/App.tsx", side: "new" as const, line: 3, surface: "review" as const };
		const second = { path: "src/lib/api.ts", side: "old" as const, line: 9, surface: "review" as const };
		model.targets = [first, second];
		model.draftFor = (target) => (target === first ? "Rename this" : "");
		render(<><FileAnnotationComposer annotation={model} target={first} /><FileAnnotationComposer annotation={model} target={second} /></>);

		const [firstBox, secondBox] = screen.getAllByRole("textbox");
		expect(firstBox).toHaveValue("Rename this");
		expect(secondBox).toHaveValue("");

		fireEvent.change(secondBox, { target: { value: "Keep this guard" } });
		// Leaving a box hands its text to the model, so the bar's send includes it.
		fireEvent.blur(secondBox);
		expect(model.setDraft).toHaveBeenCalledWith(second, "Keep this guard");
		// With several comments written, the hint says the shortcut sends them all.
		expect(screen.getByText("⌘/Ctrl + Enter to send all")).toBeInTheDocument();
		const [, secondSend] = screen.getAllByRole("button", { name: "Send feedback" });
		fireEvent.click(secondSend);
		expect(model.submit).toHaveBeenCalledWith(second, "Keep this guard");
		fireEvent.keyDown(secondBox, { key: "Enter", metaKey: true });
		expect(model.submit).toHaveBeenLastCalledWith();
	});

	it("shows a send's progress only on the box it covers", () => {
		const model = noopAnnotation();
		const first = { path: "src/App.tsx", side: "new" as const, line: 3, surface: "review" as const };
		const second = { path: "src/lib/api.ts", side: "old" as const, line: 9, surface: "review" as const };
		model.targets = [first, second];
		model.draftFor = () => "Some feedback";
		model.status = "sending";
		model.statusFor = (target) => (target === first ? "sending" : "idle");
		render(<><FileAnnotationComposer annotation={model} target={first} /><FileAnnotationComposer annotation={model} target={second} /></>);

		const [firstBox, secondBox] = screen.getAllByRole("textbox");
		expect(firstBox).toBeDisabled();
		expect(secondBox).toBeEnabled();
	});

	it("offers one bar to send or discard every written comment once there are several", () => {
		const model = noopAnnotation();
		const first = { path: "src/App.tsx", side: "new" as const, line: 3, surface: "review" as const };
		const second = { path: "src/lib/api.ts", side: "file" as const, surface: "review" as const };
		const empty = { path: "README.md", side: "new" as const, line: 1, surface: "review" as const };
		model.targets = [first, second, empty];
		model.draftFor = (target) => (target === empty ? "" : "Some feedback");
		const { rerender } = render(<FileAnnotationSendBar annotation={model} surface="review" />);

		expect(screen.getByRole("status")).toBeEmptyDOMElement();
		fireEvent.click(screen.getByRole("button", { name: "Send all 2" }));
		expect(model.submit).toHaveBeenCalledWith();
		fireEvent.click(screen.getByRole("button", { name: "Discard all" }));
		expect(model.cancel).toHaveBeenCalledWith();

		// A pane with no written comment of its own stays clear of the bar.
		rerender(<FileAnnotationSendBar annotation={model} surface="focused" />);
		expect(screen.queryByTestId("file-feedback-bar")).not.toBeInTheDocument();

		// One comment needs no bar: its own box sends it.
		model.targets = [first];
		rerender(<FileAnnotationSendBar annotation={{ ...model }} surface="review" />);
		expect(screen.queryByTestId("file-feedback-bar")).not.toBeInTheDocument();
	});

	describe("large diff virtualization", () => {
		const VIEWPORT_HEIGHT = 600;

		beforeEach(() => {
			vi.spyOn(HTMLElement.prototype, "getBoundingClientRect").mockReturnValue({
				top: 0,
				left: 0,
				right: 800,
				bottom: VIEWPORT_HEIGHT,
				width: 800,
				height: VIEWPORT_HEIGHT,
				x: 0,
				y: 0,
				toJSON() {
					return this;
				},
			});
			vi.spyOn(HTMLElement.prototype, "clientHeight", "get").mockReturnValue(VIEWPORT_HEIGHT);
			vi.spyOn(HTMLElement.prototype, "offsetHeight", "get").mockReturnValue(VIEWPORT_HEIGHT);
			vi.spyOn(HTMLElement.prototype, "offsetWidth", "get").mockReturnValue(800);
		});

		afterEach(async () => {
			await drainVirtualizerScrollDebounce();
			vi.restoreAllMocks();
		});

		function bigModifiedDiff(lineCount: number) {
			const hunkLines: string[] = [`@@ -1,${lineCount} +1,${lineCount} @@`];
			for (let i = 0; i < lineCount; i += 1) {
				hunkLines.push(`-old line ${i} with some content to diff against`);
				hunkLines.push(`+new line ${i} with some different content entirely`);
			}
			return `diff --git a/big.txt b/big.txt\nindex 111..222 100644\n--- a/big.txt\n+++ b/big.txt\n${hunkLines.join("\n")}\n`;
		}

		it("opens a large diff without hanging, mounting far fewer DOM rows than the diff has", async () => {
			const lineCount = 400;
			render(
				<div data-files-scroll-root="" style={{ overflow: "auto" }}>
					<ReviewDiffBody
						annotation={noopAnnotation()}
						detail={baseDetail({ path: "big.txt", diff: bigModifiedDiff(lineCount) })}
						detailLoadedAt={1}
						filePath="big.txt"
						onActiveSelectionChange={vi.fn()}
						sessionId="sess-1"
						split={false}
						wrap={true}
					/>
				</div>,
			);

			await waitFor(() =>
				expect(screen.getByText(diffLine("new line 0 with some different content entirely"))).toBeInTheDocument(),
			);

			const mountedRows = document.querySelectorAll("[data-diff-row]").length;
			expect(mountedRows).toBeGreaterThan(0);
			expect(mountedRows).toBeLessThan(lineCount);
			expect(
				screen.queryByText(diffLine(`new line ${lineCount - 1} with some different content entirely`)),
			).not.toBeInTheDocument();
		});
	});
});
