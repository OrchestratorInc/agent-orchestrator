import { act, fireEvent, render, screen } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";
import { SessionFileWorkspace } from "./SessionFileWorkspace";
import type { FileAnnotationModel } from "./WorkspaceDiffView";

vi.mock("./FileContentPane", () => ({
	FileContentPane: ({ initialEditing, initialLine, initialMode, initialRequestKey, onContentReady, onDirtyChange, onInitialLineConsumed, rememberDisplayMode, split }: { initialEditing?: boolean; initialLine?: number; initialMode?: string; initialRequestKey?: number; onContentReady?: () => void; onDirtyChange?: (dirty: boolean) => void; onInitialLineConsumed?: (requestKey: number) => void; rememberDisplayMode?: boolean; split: boolean }) => <div data-editing={String(Boolean(initialEditing))} data-line={initialLine} data-mode={initialMode} data-request-key={initialRequestKey} data-split={String(split)} data-remember-mode={String(Boolean(rememberDisplayMode))} data-testid="file-content"><button onClick={() => onDirtyChange?.(true)} type="button">mark dirty</button><button onClick={onContentReady} type="button">content ready</button><button onClick={() => onInitialLineConsumed?.(initialRequestKey ?? 0)} type="button">consume line</button></div>,
}));

const annotation: FileAnnotationModel = {
	target: null,
	draft: "",
	status: "idle",
	error: "",
	begin: vi.fn(),
	setDraft: vi.fn(),
	cancel: vi.fn(),
	submit: vi.fn(),
};

describe("SessionFileWorkspace", () => {
	it("renders file content without a duplicate path toolbar", () => {
		render(<SessionFileWorkspace annotation={annotation} path="src/App.tsx" sessionId="sess-1" split />);

		expect(screen.getByTestId("session-file-workspace").querySelector("header")).not.toBeInTheDocument();
		expect(screen.getByTestId("file-content")).toHaveAttribute("data-split", "true");
		expect(screen.getByTestId("file-content")).toHaveAttribute("data-mode", "file");
		// Centre tabs remember the display mode picked in the toolbar.
		expect(screen.getByTestId("file-content")).toHaveAttribute("data-remember-mode", "true");
	});

	it("leaves whole-file feedback rendering to the focused file pane", () => {
		const activeAnnotation: FileAnnotationModel = {
			...annotation,
			target: { path: "src/App.tsx", side: "file", surface: "focused" },
		};
		render(<SessionFileWorkspace annotation={activeAnnotation} path="src/App.tsx" sessionId="sess-1" split={false} />);

		expect(screen.queryByRole("textbox")).not.toBeInTheDocument();
	});

	it("reports center-editor dirty state with the opened path", () => {
		const onDirtyChange = vi.fn();
		render(<SessionFileWorkspace annotation={annotation} onDirtyChange={onDirtyChange} path="src/App.tsx" sessionId="sess-1" split={false} />);

		screen.getByRole("button", { name: "mark dirty" }).click();
		expect(onDirtyChange).toHaveBeenCalledWith("src/App.tsx", true);
	});

	it("forwards an explicit center mode and consumes one-shot edit requests on exit", () => {
		const onInitialEditingConsumed = vi.fn();
		const { unmount } = render(
			<SessionFileWorkspace
				annotation={annotation}
				initialEditing
				initialMode="diff"
				initialRequestKey={3}
				onInitialEditingConsumed={onInitialEditingConsumed}
				path="src/App.tsx"
				sessionId="sess-1"
				split={false}
			/>,
		);

		expect(screen.getByTestId("file-content")).toHaveAttribute("data-mode", "diff");
		expect(screen.getByTestId("file-content")).toHaveAttribute("data-editing", "true");
		unmount();
		expect(onInitialEditingConsumed).toHaveBeenCalledWith("src/App.tsx", 3);
	});

	it("forwards line consumption with the opened path", () => {
		const onInitialLineConsumed = vi.fn();
		render(
			<SessionFileWorkspace
				annotation={annotation}
				initialLine={120}
				initialRequestKey={7}
				onInitialLineConsumed={onInitialLineConsumed}
				path="src/App.tsx"
				sessionId="sess-1"
				split={false}
			/>,
		);

		expect(screen.getByTestId("file-content")).toHaveAttribute("data-line", "120");
		fireEvent.click(screen.getByRole("button", { name: "consume line" }));
		expect(onInitialLineConsumed).toHaveBeenCalledWith("src/App.tsx", 7);
	});

	it("restores a file's scroll position after leaving and returning", () => {
		const { rerender } = render(<SessionFileWorkspace annotation={annotation} path="src/App.tsx" sessionId="scroll-session" split={false} />);
		const appScroll = screen.getByTestId("session-file-scroll");
		appScroll.scrollTop = 320;
		fireEvent.scroll(appScroll);

		rerender(<SessionFileWorkspace annotation={annotation} path="src/Other.tsx" sessionId="scroll-session" split={false} />);
		rerender(<SessionFileWorkspace annotation={annotation} path="src/App.tsx" sessionId="scroll-session" split={false} />);

		expect(screen.getByTestId("session-file-scroll").scrollTop).toBe(320);
	});

	it("retries scroll restoration after delayed file content becomes ready", () => {
		const { rerender } = render(<SessionFileWorkspace annotation={annotation} path="src/Delayed.tsx" sessionId="delayed-scroll-session" split={false} />);
		const scroll = screen.getByTestId("session-file-scroll");
		let contentReady = true;
		let scrollTop = 0;
		Object.defineProperty(scroll, "scrollTop", {
			configurable: true,
			get: () => scrollTop,
			set: (value: number) => { scrollTop = contentReady ? value : 0; },
		});
		scroll.scrollTop = 320;
		fireEvent.scroll(scroll);

		rerender(<SessionFileWorkspace annotation={annotation} path="src/Other.tsx" sessionId="delayed-scroll-session" split={false} />);
		contentReady = false;
		rerender(<SessionFileWorkspace annotation={annotation} path="src/Delayed.tsx" sessionId="delayed-scroll-session" split={false} />);
		expect(scroll.scrollTop).toBe(0);

		contentReady = true;
		fireEvent.click(screen.getByRole("button", { name: "content ready" }));
		expect(scroll.scrollTop).toBe(320);
	});

	it("finishes restoration at the reachable offset when a file becomes shorter", async () => {
		const { rerender } = render(<SessionFileWorkspace annotation={annotation} path="src/Shorter.tsx" sessionId="shorter-scroll-session" split={false} />);
		const scroll = screen.getByTestId("session-file-scroll");
		let maxScrollTop = 500;
		let scrollTop = 0;
		Object.defineProperties(scroll, {
			clientHeight: { configurable: true, get: () => 100 },
			scrollHeight: { configurable: true, get: () => maxScrollTop + 100 },
			scrollTop: {
				configurable: true,
				get: () => scrollTop,
				set: (value: number) => { scrollTop = Math.min(value, maxScrollTop); },
			},
		});
		scroll.scrollTop = 500;
		fireEvent.scroll(scroll);

		rerender(<SessionFileWorkspace annotation={annotation} path="src/Other.tsx" sessionId="shorter-scroll-session" split={false} />);
		maxScrollTop = 200;
		rerender(<SessionFileWorkspace annotation={annotation} path="src/Shorter.tsx" sessionId="shorter-scroll-session" split={false} />);
		expect(scroll.scrollTop).toBe(200);

		fireEvent.click(screen.getByRole("button", { name: "content ready" }));
		await act(() => new Promise<void>((resolve) => requestAnimationFrame(() => resolve())));
		scroll.scrollTop = 100;
		fireEvent.scroll(scroll);

		rerender(<SessionFileWorkspace annotation={annotation} path="src/Other.tsx" sessionId="shorter-scroll-session" split={false} />);
		rerender(<SessionFileWorkspace annotation={annotation} path="src/Shorter.tsx" sessionId="shorter-scroll-session" split={false} />);
		expect(scroll.scrollTop).toBe(100);
	});
});
