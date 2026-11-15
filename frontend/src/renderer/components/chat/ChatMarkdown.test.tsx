import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { act, fireEvent, render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import type { ReactElement, ReactNode } from "react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { aoBridge } from "../../lib/bridge";
import { findBlocks, findRange, findTextOccurrences, renderedFindBlocks } from "../../lib/chat-find";
import { setApiBaseUrl } from "../../lib/api-client";
import { renderMermaidDiagram } from "../../lib/mermaid-diagram";
import { useUiStore } from "../../stores/ui-store";
import { ActivityTitle, ArtifactPathCode, ChatLinkProvider, ChatMarkdown, ChatOpeningRef } from "./ChatMarkdown";
import { WorkerUpdateRows } from "./WorkerUpdates";
import { ChatImageSourceProvider } from "./chat-image-source";

const sessionPreview = vi.hoisted(() => vi.fn());
vi.mock("../SessionLinkPreviewCard", () => ({
	ArtifactLinkPreviewCard: () => <div data-testid="artifact-link-preview" />,
	SessionLinkPreviewCard: (props: unknown) => {
		sessionPreview(props);
		return <div data-testid="session-link-preview" />;
	},
}));

const linkSource = vi.hoisted(() => ({ reads: 0, workspaces: [] as Array<{ id: string; sessions: Array<{ id: string; artifactFiles?: Array<{ path: string }> }> }> }));
// Like the real source, this reads through the query client, so a render
// without one fails here as it would in the app.
vi.mock("../../lib/use-session-link-navigation", async () => {
	const { useQueryClient } = await import("@tanstack/react-query");
	return {
		useSessionLinkSource: () => {
			useQueryClient();
			linkSource.reads += 1;
			// A real source with no data answers a fresh [] on every render.
			return { ready: true, isLoading: false, isError: false, workspaces: linkSource.workspaces.length > 0 ? linkSource.workspaces : [] };
		},
	};
});

// Mermaid needs real SVG layout APIs jsdom lacks; pin the routing boundary and
// let MermaidBlock.test.tsx own the block's states.
vi.mock("../../lib/mermaid-diagram", () => ({
	isRenderableDiagram: (code: string) => code.trim().length > 0 && code.length <= 20_000,
	renderMermaidDiagram: vi.fn(async () => '<svg xmlns="http://www.w3.org/2000/svg"><g>diagram</g></svg>'),
}));

beforeEach(() => {
	vi.mocked(renderMermaidDiagram).mockClear();
	linkSource.workspaces = [];
	linkSource.reads = 0;
});

/** Render inside a query client, which a chat's link workspaces are read through. */
function renderWithQueries(ui: ReactElement) {
	const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
	return render(ui, { wrapper: ({ children }: { children: ReactNode }) => <QueryClientProvider client={client}>{children}</QueryClientProvider> });
}

// The point of these is that the SYNTAX stops being visible. Every case here is a
// shape agents actually emit, and the assertion is that structure replaced markup.

function renderWithLinkHandler(
	text: string,
	onLinkOpen: (url: string) => void,
	workspacePaths: string[] = [],
	onFileOpen?: (path: string, line?: number) => void,
) {
	return render(
		<ChatLinkProvider onLinkOpen={onLinkOpen} onFileOpen={onFileOpen} workspacePaths={workspacePaths}>
			<ChatMarkdown text={text} />
		</ChatLinkProvider>,
	);
}

function renderWithSessionLinkHandler(text: string, onSessionLinkOpen: (url: string) => void) {
	return renderWithQueries(<ChatLinkProvider onSessionLinkOpen={onSessionLinkOpen}><ChatMarkdown text={text} /></ChatLinkProvider>);
}

describe("ChatMarkdown", () => {
	for (const streaming of [false, true]) {
		it.each(["hello  \nneedle", "hello\\\nneedle", "hello  \n**needle**", "hello  \n\\\nneedle", "hello\nneedle", "😀  \nneedle", "```text\n  hello\n\n  needle\n```"])(`keeps find offsets across hard breaks (streaming=${streaming}): %s`, (source) => {
			const { container } = render(<ChatMarkdown text={source} streaming={streaming} />);
			const blocks = renderedFindBlocks(container);
			const expected = findTextOccurrences(findBlocks(source, true)[0], "needle");
			const actual = findTextOccurrences(blocks[0].text, "needle");
			expect(actual).toEqual(expected);
			expect(findRange(blocks[0].nodes, expected[0].start, expected[0].end)?.toString()).toBe("needle");
		});
	}
	it("uses current file and browser handlers after a parent refresh", async () => {
		const before = vi.fn();
		const after = vi.fn();
		const paths = ["src/app.ts"];
		const content = <ChatMarkdown text={"Open `src/app.ts:12` or [website](https://example.com)."} />;
		const view = render(<ChatLinkProvider workspacePaths={paths} onFileOpen={before} onLinkOpen={before}>{content}</ChatLinkProvider>);
		view.rerender(<ChatLinkProvider workspacePaths={paths} onFileOpen={after} onLinkOpen={after}>{content}</ChatLinkProvider>);
		await userEvent.click(screen.getByRole("button", { name: "Open src/app.ts in Files" }));
		await userEvent.click(screen.getByRole("link", { name: "website" }));
		expect(before).not.toHaveBeenCalled();
		expect(after).toHaveBeenCalledWith("src/app.ts", 12);
		expect(after).toHaveBeenCalledWith("https://example.com");
	});

	it("updates file links when their catalog or opening capability changes", () => {
		const open = vi.fn();
		const content = <ChatMarkdown text={"`app.ts` `new.ts`"} />;
		const view = render(<ChatLinkProvider workspacePaths={["src/app.ts"]} onFileOpen={open}>{content}</ChatLinkProvider>);
		expect(screen.getByRole("button", { name: "Open src/app.ts in Files" })).toBeInTheDocument();
		expect(screen.queryByRole("button", { name: "Open src/new.ts in Files" })).not.toBeInTheDocument();
		view.rerender(<ChatLinkProvider workspacePaths={["src/new.ts", "src/app.ts", "other/app.ts"]} onFileOpen={open}>{content}</ChatLinkProvider>);
		expect(screen.queryByRole("button", { name: "Open src/app.ts in Files" })).not.toBeInTheDocument();
		expect(screen.getByRole("button", { name: "Open src/new.ts in Files" })).toBeInTheDocument();
		view.rerender(<ChatLinkProvider workspacePaths={["src/new.ts"]}>{content}</ChatLinkProvider>);
		expect(screen.queryByRole("button", { name: "Open src/new.ts in Files" })).not.toBeInTheDocument();
	});

	it("uses the latest session-link handler", async () => {
		const before = vi.fn();
		const after = vi.fn();
		const content = <ChatMarkdown text="[Worker](ao://sessions/project/session)" />;
		const view = render(<ChatLinkProvider onSessionLinkOpen={before}>{content}</ChatLinkProvider>);
		view.rerender(<ChatLinkProvider onSessionLinkOpen={after}>{content}</ChatLinkProvider>);
		await userEvent.click(screen.getByRole("link", { name: "Worker" }));
		expect(before).not.toHaveBeenCalled();
		expect(after).toHaveBeenCalledExactlyOnceWith("ao://sessions/project/session");
	});

	it("renders headings as headings rather than literal hashes", () => {
		render(<ChatMarkdown text={"## Findings\n\nTwo files changed."} />);
		expect(screen.getByRole("heading", { name: "Findings" })).toBeInTheDocument();
		expect(screen.queryByText(/## Findings/)).not.toBeInTheDocument();
	});

	it("renders bullet and numbered lists as lists", () => {
		render(<ChatMarkdown text={"- first\n- second\n\n1. one\n2. two"} />);
		const lists = screen.getAllByRole("list");
		expect(lists).toHaveLength(2);
		expect(screen.getAllByRole("listitem")).toHaveLength(4);
	});

	it("keeps the first number of an ordered list that starts after 1", () => {
		render(<ChatMarkdown text={"6."} />);
		expect(screen.getByRole("list")).toHaveAttribute("start", "6");
	});

	it("renders a task list with read-only checkboxes reflecting the agent's state", () => {
		render(<ChatMarkdown text={"- [x] done thing\n- [ ] pending thing"} />);
		const boxes = screen.getAllByRole("checkbox") as HTMLInputElement[];
		expect(boxes).toHaveLength(2);
		expect(boxes[0]!.checked).toBe(true);
		expect(boxes[1]!.checked).toBe(false);
		// Clicking must not imply AO can rewrite the agent's plan.
		expect(boxes[0]!.readOnly).toBe(true);
	});

	it("renders a GFM table, in a container that scrolls instead of widening the column", () => {
		render(<ChatMarkdown text={"| file | lines |\n| --- | --- |\n| a.ts | 12 |"} />);
		const table = screen.getByRole("table");
		expect(table).toBeInTheDocument();
		expect(screen.getByRole("columnheader", { name: "file" })).toBeInTheDocument();
		expect(screen.getByRole("cell", { name: "a.ts" })).toBeInTheDocument();
		const scroller = table.closest("div");
		expect(scroller?.className).toContain("overflow-x-auto");
	});

	it("compacts emoji status markers inside markdown tables", () => {
		render(<ChatMarkdown text={"| status |\n| --- |\n| 🟡 idle |\n| ✅ merged |"} />);
		const idle = screen.getByRole("cell", { name: "🟡 idle" });
		const merged = screen.getByRole("cell", { name: "✅ merged" });

		expect(idle.querySelector(".chat-md-emoji")).toHaveTextContent("🟡");
		expect(merged.querySelector(".chat-md-emoji")).toHaveTextContent("✅");
	});

	it("keeps skin-tone and ZWJ emoji together as grapheme clusters", () => {
		render(<ChatMarkdown text={"Ready 👍🏽 with family 👨‍👩‍👧‍👦 and flag 🏳️‍🌈."} />);
		const emoji = [...document.querySelectorAll(".chat-md-emoji")];
		expect(emoji.map((node) => node.textContent)).toEqual(["👍🏽", "👨‍👩‍👧‍👦", "🏳️‍🌈"]);
	});

	it("renders a fenced code block with its language and a copy control, and no stray backticks", () => {
		render(<ChatMarkdown text={"```go\nfunc main() {}\n```"} />);
		expect(screen.getByText("go")).toBeInTheDocument();
		expect(screen.getByText("func main() {}")).toBeInTheDocument();
		expect(screen.getByRole("button", { name: /copy code/i })).toBeInTheDocument();
		expect(document.body.textContent).not.toContain("```");
	});

	it("keeps inline code inline rather than promoting it to a block", () => {
		render(<ChatMarkdown text={"run `go test ./...` first"} />);
		const code = screen.getByText("go test ./...");
		expect(code.tagName).toBe("CODE");
		expect(code.closest("pre")).toBeNull();
		expect(code).toHaveClass("text-markdown-code");
	});

	it("uses the AO logo colour for file paths and inline notation", () => {
		render(
			<ChatMarkdown
				text={"See `backend/internal/adapters/agent/` and then pass `--resume` to `ao`."}
			/>,
		);

		expect(screen.getByText("backend/internal/adapters/agent/")).toHaveClass("text-markdown-code");
		expect(screen.getByText("--resume")).toHaveClass("text-markdown-code");
		expect(screen.getByText("ao")).toHaveClass("text-markdown-code");
	});

	it("recognizes standalone filenames and paths with line locations", () => {
		render(<ChatMarkdown text={"Compare `README.md` with `backend/service.go:42`."} />);
		expect(screen.getByText("README.md")).toHaveClass("text-markdown-code");
		expect(screen.getByText("backend/service.go:42")).toHaveClass("text-markdown-code");
	});

	it("opens a known inline-code file path in Files", async () => {
		const onFileOpen = vi.fn();
		renderWithLinkHandler(
			"Open `backend/service.go:42` but keep `--resume` as code.",
			vi.fn(),
			["backend/service.go"],
			onFileOpen,
		);

		await userEvent.click(screen.getByRole("button", { name: "Open backend/service.go in Files" }));

		expect(onFileOpen).toHaveBeenCalledWith("backend/service.go", 42);
		expect(screen.getByText("--resume").closest("button")).toBeNull();
	});

	it("reveals filename matches in clickable inline code using the same offsets as search", () => {
		const text = "Open `backend/service.go:42` but keep `--resume` as code.";
		const { container } = renderWithLinkHandler(text, vi.fn(), ["backend/service.go"], vi.fn());
		const rendered = renderedFindBlocks(container);
		expect(rendered.map((block) => block.text)).toEqual(findBlocks(text, true));
		const [hit] = findTextOccurrences(findBlocks(text, true)[0], "service.go");
		expect(findRange(rendered[0].nodes, hit.start, hit.end)?.toString()).toBe("service.go");
	});

	it("opens an absolute markdown file link in Files instead of externally", async () => {
		const onFileOpen = vi.fn();
		const openExternal = vi.spyOn(aoBridge.app, "openExternal").mockResolvedValue(undefined);
		renderWithLinkHandler(
			"See [the component](/Users/me/project/frontend/src/App.tsx:42).",
			vi.fn(),
			["frontend/src/App.tsx"],
			onFileOpen,
		);

		await userEvent.click(screen.getByRole("link", { name: "the component" }));

		// The absolute reference is passed through as written: its tail matching
		// a workspace file does not make it that file, so the opener asks the daemon.
		expect(onFileOpen).toHaveBeenCalledWith("/Users/me/project/frontend/src/App.tsx", 42);
		expect(openExternal).not.toHaveBeenCalled();
		openExternal.mockRestore();
	});

	it("does not nest a file-path button inside a markdown file link", () => {
		renderWithLinkHandler(
			"See [`frontend/src/App.tsx`](frontend/src/App.tsx).",
			vi.fn(),
			["frontend/src/App.tsx"],
			vi.fn(),
		);

		const link = screen.getByRole("link", { name: "frontend/src/App.tsx" });
		expect(link.querySelector("button")).toBeNull();
	});

	it("does not swap an absolute link or inline path for a same-named workspace file", async () => {
		const onFileOpen = vi.fn();
		renderWithLinkHandler(
			"See [the readme](/other-repo/README.md) and `/other-repo/README.md:3`.",
			vi.fn(),
			["README.md"],
			onFileOpen,
		);

		await userEvent.click(screen.getByRole("link", { name: "the readme" }));
		await userEvent.click(screen.getByRole("button", { name: "Open README.md in Files" }));

		expect(onFileOpen).toHaveBeenNthCalledWith(1, "/other-repo/README.md");
		expect(onFileOpen).toHaveBeenNthCalledWith(2, "/other-repo/README.md", 3);
	});

	it("opens an explicit relative file link before the workspace catalog catches up", async () => {
		const onFileOpen = vi.fn();
		renderWithLinkHandler(
			"See [the new file](src/generated/new-file.ts#L8).",
			vi.fn(),
			[],
			onFileOpen,
		);

		await userEvent.click(screen.getByRole("link", { name: "the new file" }));

		expect(onFileOpen).toHaveBeenCalledWith("src/generated/new-file.ts", 8);
	});

	it("escapes raw HTML instead of rendering it", () => {
		// Agent output is only as trustworthy as the files it just read, so an
		// <img onerror> in a README must never become a live element.
		render(<ChatMarkdown text={'before <img src=x onerror="alert(1)"> after'} />);
		expect(document.querySelector("img")).toBeNull();
		expect(document.body.textContent).toContain("onerror");
	});

	it("marks external links to open outside the app", () => {
		render(<ChatMarkdown text={"see [the issue](https://example.com/i/1)"} />);
		const link = screen.getByRole("link", { name: "the issue" });
		expect(link).toHaveAttribute("href", "https://example.com/i/1");
		expect(link).toHaveAttribute("target", "_blank");
		expect(link).toHaveClass("text-markdown-link", "hover:text-markdown-link-hover");
		// Without noreferrer the opened page gets a handle on the renderer.
		expect(link.getAttribute("rel")).toContain("noreferrer");
	});

	it("routes a plain web-link click to the AO Browser handler", async () => {
		const user = userEvent.setup();
		const onLinkOpen = vi.fn();
		const openExternal = vi.spyOn(aoBridge.app, "openExternal").mockResolvedValue(undefined);
		renderWithLinkHandler("see [the issue](https://example.com/i/1)", onLinkOpen);

		await user.click(screen.getByRole("link", { name: "the issue" }));

		expect(onLinkOpen).toHaveBeenCalledWith("https://example.com/i/1");
		expect(openExternal).not.toHaveBeenCalled();
		openExternal.mockRestore();
	});

	it.each([
		"http://localhost:5173",
		"http://127.0.0.1:5173",
		"http://[::1]:5173",
		"http://0.0.0.0:5173",
		"http://127.0.0.2:5173",
	])("does not open a remote host-local preview on the client: %s", (href) => {
		const onLinkOpen = vi.fn();
		const openExternal = vi.spyOn(aoBridge.app, "openExternal").mockResolvedValue(undefined);
		render(<ChatLinkProvider onLinkOpen={onLinkOpen} remoteHost><ChatMarkdown text={`[preview](${href})`} /></ChatLinkProvider>);

		const preview = screen.getByText("preview");
		expect(preview.closest("a")).toBeNull();
		expect(preview.closest("[title]")).toHaveAttribute("title", expect.stringContaining("remote host"));
		fireEvent.click(preview);
		fireEvent.click(preview, { altKey: true });
		fireEvent.contextMenu(preview);
		expect(onLinkOpen).not.toHaveBeenCalled();
		expect(openExternal).not.toHaveBeenCalled();
		openExternal.mockRestore();
	});

	it("scopes a session preview to the chat source", async () => {
		sessionPreview.mockClear();
		const onSessionLinkOpen = vi.fn();
		render(
			<ChatLinkProvider
				onSessionLinkOpen={onSessionLinkOpen}
				sessionLinkHostId="box-a"
			>
				<ChatMarkdown text="[Worker](ao://sessions/project/session)" />
			</ChatLinkProvider>,
		);

		fireEvent.pointerOver(screen.getByRole("link"), { pointerType: "mouse" });
		await waitFor(() => expect(screen.getByTestId("session-link-preview")).toBeInTheDocument());
		expect(sessionPreview).toHaveBeenCalledWith({
			href: "ao://sessions/project/session",
			sourceHostId: "box-a",
			sourceKind: undefined,
			onOpen: expect.any(Function),
		});

		// The card opens the session through the same handler as the link click.
		(sessionPreview.mock.calls.at(-1)![0] as { onOpen: () => void }).onOpen();
		expect(onSessionLinkOpen).toHaveBeenCalledExactlyOnceWith("ao://sessions/project/session");
	});

	it("renders remote workspace paths as text while keeping external links working", async () => {
		const onLinkOpen = vi.fn();
		const openExternal = vi.spyOn(aoBridge.app, "openExternal").mockResolvedValue(undefined);
		render(<ChatLinkProvider onLinkOpen={onLinkOpen} remoteHost>
			<ChatMarkdown text="[report](reports/new-report.html) and [issue](https://example.com/i/1)" />
		</ChatLinkProvider>);

		const report = screen.getByText("report");
		expect(report.closest("a")).toBeNull();
		expect(report.closest("[title]")).toHaveAttribute("title", expect.stringContaining("remote host"));
		fireEvent.click(report);
		fireEvent.click(report, { metaKey: true });
		fireEvent.contextMenu(report);
		expect(screen.getAllByRole("menuitem").map((item) => item.textContent)).toEqual(["Copy path"]);
		await userEvent.click(screen.getByRole("menuitem", { name: "Copy path" }));
		await userEvent.click(screen.getByRole("link", { name: "issue" }));
		expect(onLinkOpen).toHaveBeenCalledExactlyOnceWith("https://example.com/i/1");
		expect(openExternal).not.toHaveBeenCalled();
		openExternal.mockRestore();
	});

	it("opens remote source links in Files without opening the client browser", async () => {
		const onFileOpen = vi.fn();
		const onLinkOpen = vi.fn();
		render(<ChatLinkProvider onFileOpen={onFileOpen} onLinkOpen={onLinkOpen} remoteHost workspacePaths={["src/App.tsx", "reports/index.html"]}>
			<ChatMarkdown text="[source](src/App.tsx) and [new](src/new.ts#L8) and [preview](reports/index.html)" />
		</ChatLinkProvider>);

		await userEvent.click(screen.getByRole("link", { name: "source" }));
		await userEvent.click(screen.getByRole("link", { name: "new" }));
		expect(onFileOpen).toHaveBeenNthCalledWith(1, "src/App.tsx");
		expect(onFileOpen).toHaveBeenNthCalledWith(2, "src/new.ts", 8);
		expect(screen.getByText("preview").closest("a")).toBeNull();
		expect(onLinkOpen).not.toHaveBeenCalled();
	});

	it("blocks remote host-local links inside Mermaid diagrams", async () => {
		vi.mocked(renderMermaidDiagram).mockResolvedValueOnce('<svg xmlns="http://www.w3.org/2000/svg"><a href="http://localhost:5173"><text>preview</text></a></svg>');
		const onLinkOpen = vi.fn();
		const openExternal = vi.spyOn(aoBridge.app, "openExternal").mockResolvedValue(undefined);
		render(<ChatLinkProvider onLinkOpen={onLinkOpen} remoteHost>
			<ChatMarkdown text={'```mermaid\ngraph LR\n  A-->B\n```'} />
		</ChatLinkProvider>);

		const preview = await screen.findByText("preview");
		fireEvent.click(preview);
		fireEvent.click(preview, { altKey: true });
		fireEvent(preview, new MouseEvent("auxclick", { button: 1, bubbles: true, cancelable: true }));
		fireEvent.contextMenu(preview);
		expect(onLinkOpen).not.toHaveBeenCalled();
		expect(openExternal).not.toHaveBeenCalled();
		openExternal.mockRestore();
	});

	it("does not load a remote host-local markdown image from the client", () => {
		render(<ChatLinkProvider remoteHost><ChatMarkdown text="![remote screenshot](http://localhost:5173/screenshot.png)" /></ChatLinkProvider>);
		expect(screen.queryByRole("img", { name: "remote screenshot" })).not.toBeInTheDocument();
		expect(screen.getByText("remote screenshot")).toHaveAttribute("title", expect.stringContaining("remote host"));
	});

	it("routes workspace file clicks to the AO Browser handler", async () => {
		const user = userEvent.setup();
		const onLinkOpen = vi.fn();
		const openExternal = vi.spyOn(aoBridge.app, "openExternal").mockResolvedValue(undefined);
		renderWithLinkHandler("see [test-ui-2.html](test-ui-2.html)", onLinkOpen, ["test-ui-2.html"]);

		await user.click(screen.getByRole("link", { name: "test-ui-2.html" }));

		expect(onLinkOpen).toHaveBeenCalledWith("test-ui-2.html");
		expect(openExternal).not.toHaveBeenCalled();
		openExternal.mockRestore();
	});

	it("routes a newly reported local file through AO even before Files has indexed it", async () => {
		const user = userEvent.setup();
		const onLinkOpen = vi.fn();
		const openExternal = vi.spyOn(aoBridge.app, "openExternal").mockResolvedValue(undefined);
		renderWithLinkHandler("see [new report](reports/new-report.html)", onLinkOpen);

		await user.click(screen.getByRole("link", { name: "new report" }));

		expect(onLinkOpen).toHaveBeenCalledWith("reports/new-report.html");
		expect(openExternal).not.toHaveBeenCalled();
	});

	it("preserves Windows workspace paths and offers the verified file in Files", async () => {
		const user = userEvent.setup();
		const onLinkOpen = vi.fn();
		const onFileOpen = vi.fn();
		renderWithLinkHandler(
			"see [final report](C:\\worktree\\reports\\final.html)",
			onLinkOpen,
			["reports/final.html"],
			onFileOpen,
		);
		const link = screen.getByRole("link", { name: "final report" });

		fireEvent.contextMenu(link);
		await user.click(await screen.findByRole("menuitem", { name: "Open in Orchestrator" }));

		expect(onFileOpen).toHaveBeenCalledWith("reports/final.html");
		expect(onLinkOpen).not.toHaveBeenCalled();
	});

	it.each([
		"ao://sessions/project/session",
		"[Open session](ao://sessions/project/session)",
	])("renders and activates a canonical session link: %s", async (text) => {
		const onSessionLinkOpen = vi.fn();
		renderWithSessionLinkHandler(text, onSessionLinkOpen);
		const link = screen.getByRole("link");
		expect(link).toHaveAttribute("href", "ao://sessions/project/session");
		await userEvent.setup().click(link);
		expect(onSessionLinkOpen).toHaveBeenCalledWith("ao://sessions/project/session");
	});

	it("does not auto-activate a session link while streaming", () => {
		const onSessionLinkOpen = vi.fn();
		render(<ChatLinkProvider onSessionLinkOpen={onSessionLinkOpen}><ChatMarkdown streaming text="ao://sessions/project/session" /></ChatLinkProvider>);
		expect(screen.getByRole("link")).toBeInTheDocument();
		expect(onSessionLinkOpen).not.toHaveBeenCalled();
	});

	it.each([
		["`worker-1/plan.md`", "ao://sessions/project/worker-1/artifacts/plan.md"],
		["`/tmp/ao/artifacts/worker-1/plans/q3.md`", "ao://sessions/project/worker-1/artifacts/plans/q3.md"],
		["`worker-1/plans/my plan.md`", "ao://sessions/project/worker-1/artifacts/plans/my%20plan.md"],
		["`/tmp/ao data/artifacts/worker-1/plans/my plan.md`", "ao://sessions/project/worker-1/artifacts/plans/my%20plan.md"],
	])("links inline code naming a listed session artifact: %s", async (text, href) => {
		linkSource.workspaces = [{ id: "project", sessions: [{ id: "worker-1", artifactFiles: [{ path: "plan.md" }, { path: "plans/q3.md" }, { path: "plans/my plan.md" }] }] }];
		const onSessionLinkOpen = vi.fn();
		renderWithSessionLinkHandler(text, onSessionLinkOpen);
		const link = screen.getByRole("link");
		expect(link).toHaveAttribute("href", href);
		await userEvent.setup().click(link);
		expect(onSessionLinkOpen).toHaveBeenCalledWith(href);
	});

	it("offers path actions on an absolute artifact reference while preserving its artifact link", async () => {
		linkSource.workspaces = [{ id: "project", sessions: [{ id: "worker-1", artifactFiles: [{ path: "plan.md" }] }] }];
		const copy = vi.spyOn(aoBridge.clipboard, "writeText").mockResolvedValue(undefined);
		const open = vi.fn();
		renderWithSessionLinkHandler("`/tmp/ao/artifacts/worker-1/plan.md`", open);
		const link = screen.getByRole("link");
		await userEvent.click(link);
		expect(open).toHaveBeenCalledExactlyOnceWith("ao://sessions/project/worker-1/artifacts/plan.md");
		fireEvent.contextMenu(link);
		await userEvent.click(screen.getByRole("menuitem", { name: "Copy path" }));
		expect(copy).toHaveBeenCalledExactlyOnceWith("/tmp/ao/artifacts/worker-1/plan.md");
		copy.mockRestore();
	});

	it("opens a Markdown link to a listed artifact's file as that artifact, not as a workspace file", async () => {
		// A Cloud agent links the file it wrote under the worker's artifact
		// directory; the file view cannot read outside the checkout (422).
		linkSource.workspaces = [{ id: "project", sessions: [{ id: "worker-1", artifactFiles: [{ path: "conductor-migration-guide.md" }] }] }];
		const onSessionLinkOpen = vi.fn();
		const onFileOpen = vi.fn();
		renderWithQueries(<ChatLinkProvider onSessionLinkOpen={onSessionLinkOpen} onFileOpen={onFileOpen}>
			<ChatMarkdown text="See the [migration guide](/workspace/.ao/worker/artifacts/worker-1/conductor-migration-guide.md)." />
		</ChatLinkProvider>);
		const link = screen.getByRole("link", { name: "migration guide" });
		expect(link).toHaveAttribute("href", "ao://sessions/project/worker-1/artifacts/conductor-migration-guide.md");
		await userEvent.click(link);
		expect(onSessionLinkOpen).toHaveBeenCalledExactlyOnceWith("ao://sessions/project/worker-1/artifacts/conductor-migration-guide.md");
		expect(onFileOpen).not.toHaveBeenCalled();
	});

	it("opens a Markdown link to a listed artifact whose name Markdown percent-encodes", async () => {
		// Markdown percent-encodes a link's target, so a space or a non-ASCII
		// letter in the file name arrives as %20 or %C3%A9; the listed path is
		// the name as written.
		linkSource.workspaces = [{ id: "project", sessions: [{ id: "worker-1", artifactFiles: [{ path: "Migration Guide.md" }, { path: "notes/résumé.md" }] }] }];
		const onSessionLinkOpen = vi.fn();
		const onFileOpen = vi.fn();
		renderWithQueries(<ChatLinkProvider onSessionLinkOpen={onSessionLinkOpen} onFileOpen={onFileOpen}>
			<ChatMarkdown text="[guide](</workspace/.ao/worker/artifacts/worker-1/Migration Guide.md>) and [cv](/workspace/.ao/worker/artifacts/worker-1/notes/résumé.md)" />
		</ChatLinkProvider>);
		expect(screen.getByRole("link", { name: "guide" })).toHaveAttribute("href", "ao://sessions/project/worker-1/artifacts/Migration%20Guide.md");
		expect(screen.getByRole("link", { name: "cv" })).toHaveAttribute("href", "ao://sessions/project/worker-1/artifacts/notes/r%C3%A9sum%C3%A9.md");
		await userEvent.click(screen.getByRole("link", { name: "guide" }));
		expect(onSessionLinkOpen).toHaveBeenCalledExactlyOnceWith("ao://sessions/project/worker-1/artifacts/Migration%20Guide.md");
		expect(onFileOpen).not.toHaveBeenCalled();
	});

	it("matches a listed artifact whose name itself contains a percent escape", () => {
		// A file literally named "50%25-done.md": Markdown keeps a valid escape as
		// written, so the target matches as it is, before any decoding.
		linkSource.workspaces = [{ id: "project", sessions: [{ id: "worker-1", artifactFiles: [{ path: "50%25-done.md" }] }] }];
		renderWithQueries(<ChatLinkProvider onSessionLinkOpen={vi.fn()} onFileOpen={vi.fn()}>
			<ChatMarkdown text="[half](/workspace/.ao/worker/artifacts/worker-1/50%25-done.md)" />
		</ChatLinkProvider>);
		expect(screen.getByRole("link", { name: "half" })).toHaveAttribute("href", "ao://sessions/project/worker-1/artifacts/50%2525-done.md");
	});

	it("leaves a Markdown link to an unlisted artifact path as a file link", async () => {
		linkSource.workspaces = [{ id: "project", sessions: [{ id: "worker-1", artifactFiles: [{ path: "plan.md" }] }] }];
		const onSessionLinkOpen = vi.fn();
		renderWithQueries(<ChatLinkProvider onSessionLinkOpen={onSessionLinkOpen} onFileOpen={vi.fn()}>
			<ChatMarkdown text="[other](/workspace/.ao/worker/artifacts/worker-1/other.md)" />
		</ChatLinkProvider>);
		expect(screen.getByRole("link", { name: "other" })).toHaveAttribute("href", "/workspace/.ao/worker/artifacts/worker-1/other.md");
		expect(onSessionLinkOpen).not.toHaveBeenCalled();
	});

	it("keeps a Markdown link to a listed artifact's file inert in safe-origin text", () => {
		// Automation reports, review comments and other agents' messages link only
		// what they wrote as an ao:// link; their file links stay text.
		linkSource.workspaces = [{ id: "project", sessions: [{ id: "worker-1", artifactFiles: [{ path: "guide.md" }] }] }];
		const { container } = renderWithQueries(<ChatLinkProvider onSessionLinkOpen={vi.fn()} onFileOpen={vi.fn()}>
			<ChatMarkdown safeOrigin text="[Approve deployment](/workspace/.ao/worker/artifacts/worker-1/guide.md) or [the guide](ao://sessions/project/worker-1/artifacts/guide.md)" />
		</ChatLinkProvider>);
		expect(container).toHaveTextContent("Approve deployment or the guide");
		expect(screen.queryByRole("link", { name: "Approve deployment" })).not.toBeInTheDocument();
		expect(screen.getByRole("link", { name: "the guide" })).toHaveAttribute("href", "ao://sessions/project/worker-1/artifacts/guide.md");
	});

	it("opens a listed artifact's file linked with a line, a query or a Windows path as that artifact", () => {
		linkSource.workspaces = [{ id: "project", sessions: [{ id: "worker-1", artifactFiles: [{ path: "plan.md" }] }] }];
		renderWithQueries(<ChatLinkProvider onSessionLinkOpen={vi.fn()} onFileOpen={vi.fn()}>
			<ChatMarkdown text={"[line](/workspace/.ao/worker/artifacts/worker-1/plan.md#L10), [raw](/workspace/.ao/worker/artifacts/worker-1/plan.md?raw) and [windows](C:\\ao\\artifacts\\worker-1\\plan.md)"} />
		</ChatLinkProvider>);
		for (const name of ["line", "raw", "windows"]) {
			expect(screen.getByRole("link", { name })).toHaveAttribute("href", "ao://sessions/project/worker-1/artifacts/plan.md");
		}
	});

	it("keeps the linked file's path actions on a link opened as an artifact", async () => {
		linkSource.workspaces = [{ id: "project", sessions: [{ id: "worker-1", artifactFiles: [{ path: "guide.md" }] }] }];
		const copy = vi.spyOn(aoBridge.clipboard, "writeText").mockResolvedValue(undefined);
		try {
			renderWithQueries(<ChatLinkProvider onSessionLinkOpen={vi.fn()} onFileOpen={vi.fn()}>
				<ChatMarkdown text="[the guide](/workspace/.ao/worker/artifacts/worker-1/guide.md)" />
			</ChatLinkProvider>);
			const link = screen.getByRole("link", { name: "the guide" });
			expect(link).toHaveAttribute("href", "ao://sessions/project/worker-1/artifacts/guide.md");
			fireEvent.contextMenu(link);
			await userEvent.click(screen.getByRole("menuitem", { name: "Copy path" }));
			expect(copy).toHaveBeenCalledExactlyOnceWith("/workspace/.ao/worker/artifacts/worker-1/guide.md");
		} finally {
			copy.mockRestore();
		}
	});

	it("keeps a listed artifact's file link a file link in a chat that cannot open artifact links", async () => {
		// A new chat or a starting orchestrator has no session link handler: an
		// artifact link there would do nothing when clicked.
		linkSource.workspaces = [{ id: "project", sessions: [{ id: "worker-1", artifactFiles: [{ path: "guide.md" }] }] }];
		const onFileOpen = vi.fn();
		renderWithQueries(<ChatLinkProvider onFileOpen={onFileOpen}>
			<ChatMarkdown text="[the guide](/workspace/.ao/worker/artifacts/worker-1/guide.md)" />
		</ChatLinkProvider>);
		const link = screen.getByRole("link", { name: "the guide" });
		expect(link).toHaveAttribute("href", "/workspace/.ao/worker/artifacts/worker-1/guide.md");
		await userEvent.click(link);
		expect(onFileOpen).toHaveBeenCalledExactlyOnceWith("/workspace/.ao/worker/artifacts/worker-1/guide.md");
	});

	it("renders a link to an artifact's file without a query client, as a file link", () => {
		// An isolated tree (a test, another root) has no query client to read
		// the session list with; the link stays what it was written as.
		linkSource.workspaces = [{ id: "project", sessions: [{ id: "worker-1", artifactFiles: [{ path: "guide.md" }] }] }];
		render(<>
			<ChatLinkProvider onSessionLinkOpen={vi.fn()} onFileOpen={vi.fn()}>
				<ChatMarkdown text="[in a chat](/workspace/.ao/worker/artifacts/worker-1/guide.md) and `worker-1/guide.md`" />
			</ChatLinkProvider>
			<ChatMarkdown text="[on its own](/workspace/.ao/worker/artifacts/worker-1/guide.md)" />
		</>);
		expect(screen.getByRole("link", { name: "in a chat" })).toHaveAttribute("href", "/workspace/.ao/worker/artifacts/worker-1/guide.md");
		expect(screen.getByRole("link", { name: "on its own" })).toHaveAttribute("href", "/workspace/.ao/worker/artifacts/worker-1/guide.md");
	});

	it("reads the chat's link workspaces once for all its links, and not at all without one", () => {
		linkSource.workspaces = [{ id: "project", sessions: [{ id: "worker-1", artifactFiles: [{ path: "a.md" }, { path: "b.md" }, { path: "c.md" }] }] }];
		const link = (name: string) => `[${name}](/workspace/.ao/worker/artifacts/worker-1/${name}.md)`;
		const chat = (text: string) => <ChatLinkProvider onSessionLinkOpen={vi.fn()}><ChatMarkdown text={text} /></ChatLinkProvider>;
		const plain = renderWithQueries(chat("No links here."));
		expect(linkSource.reads).toBe(0);
		plain.unmount();

		const one = renderWithQueries(chat(link("a")));
		expect(screen.getByRole("link", { name: "a" })).toHaveAttribute("href", "ao://sessions/project/worker-1/artifacts/a.md");
		const readsForOne = linkSource.reads;
		one.unmount();
		linkSource.reads = 0;
		renderWithQueries(chat(`${link("a")}, ${link("b")} and ${link("c")}`));
		expect(screen.getByRole("link", { name: "c" })).toHaveAttribute("href", "ao://sessions/project/worker-1/artifacts/c.md");
		expect(linkSource.reads).toBe(readsForOne);
	});

	it("opens a file in this session's artifact directory with the file opener, an HTML page too", async () => {
		// The file opener shows it in Artifacts, listed or not; the Browser
		// cannot show a Cloud sandbox file, so the click would do nothing.
		const onLinkOpen = vi.fn();
		const onFileOpen = vi.fn();
		renderWithQueries(<ChatLinkProvider sessionId="s1" onLinkOpen={onLinkOpen} onFileOpen={onFileOpen} onSessionLinkOpen={vi.fn()}>
			<ChatMarkdown text="[report](/workspace/.ao/worker/artifacts/s1/report.html), [plan](/workspace/.ao/worker/artifacts/s1/plan.md?raw) and [site](/workspace/repository/site/index.html)" />
		</ChatLinkProvider>);
		await userEvent.click(screen.getByRole("link", { name: "report" }));
		expect(onFileOpen).toHaveBeenCalledExactlyOnceWith("/workspace/.ao/worker/artifacts/s1/report.html");
		expect(onLinkOpen).not.toHaveBeenCalled();
		await userEvent.click(screen.getByRole("link", { name: "plan" }));
		expect(onFileOpen).toHaveBeenLastCalledWith("/workspace/.ao/worker/artifacts/s1/plan.md");
		// Any other HTML file still opens as a page.
		await userEvent.click(screen.getByRole("link", { name: "site" }));
		expect(onLinkOpen).toHaveBeenCalledExactlyOnceWith("/workspace/repository/site/index.html");
		expect(onFileOpen).toHaveBeenCalledTimes(2);
	});

	it("opens an HTML page in this session's artifact directory with the file opener when a workspace file shares its name", async () => {
		// A Cloud chat's file list holds the checkout's top-level files once "@"
		// is used; an artifact named like one of them is still the artifact.
		const onLinkOpen = vi.fn();
		const onFileOpen = vi.fn();
		renderWithQueries(<ChatLinkProvider sessionId="s1" workspacePaths={["index.html"]} onLinkOpen={onLinkOpen} onFileOpen={onFileOpen} onSessionLinkOpen={vi.fn()}>
			<ChatMarkdown text="[page](/workspace/.ao/worker/artifacts/s1/index.html)" />
		</ChatLinkProvider>);
		await userEvent.click(screen.getByRole("link", { name: "page" }));
		expect(onFileOpen).toHaveBeenCalledExactlyOnceWith("/workspace/.ao/worker/artifacts/s1/index.html");
		expect(onLinkOpen).not.toHaveBeenCalled();
	});

	it("opens this session's artifact by the link's file path: no query or fragment, an encoded # or ? kept", async () => {
		// A link's query and fragment are cut while its target is still encoded,
		// so a name's own "#" or "?" (written %23, %3F) is not taken for one.
		const onFileOpen = vi.fn();
		renderWithQueries(<ChatLinkProvider sessionId="s1" onFileOpen={onFileOpen} onSessionLinkOpen={vi.fn()}>
			<ChatMarkdown text="[raw](/workspace/.ao/worker/artifacts/s1/plan.md?raw), [setup](/workspace/.ao/worker/artifacts/s1/guide.md#setup) and [issue](/workspace/.ao/worker/artifacts/s1/issue%20%2342%3F.md)" />
		</ChatLinkProvider>);
		for (const name of ["raw", "setup", "issue"]) await userEvent.click(screen.getByRole("link", { name }));
		expect(onFileOpen.mock.calls.map(([path]) => path)).toEqual([
			"/workspace/.ao/worker/artifacts/s1/plan.md",
			"/workspace/.ao/worker/artifacts/s1/guide.md",
			"/workspace/.ao/worker/artifacts/s1/issue #42?.md",
		]);
	});

	it("leaves inline code naming an unlisted artifact as plain code", () => {
		linkSource.workspaces = [{ id: "project", sessions: [{ id: "worker-1", artifactFiles: [{ path: "plan.md" }] }] }];
		renderWithSessionLinkHandler("`worker-1/other.md` and `/tmp/ao/artifacts/worker-2/plan.md`", vi.fn());
		expect(screen.queryByRole("link")).not.toBeInTheDocument();
		expect(document.querySelectorAll("code")).toHaveLength(2);
	});

	it.each([
		["`worker-1/plan.md`"],
		["[`plan.md`](ao://sessions/project/worker-1/artifacts/plan.md)"],
	])("names a linked artifact in the UI font, not as code: %s", (text) => {
		linkSource.workspaces = [{ id: "project", sessions: [{ id: "worker-1", artifactFiles: [{ path: "plan.md" }] }] }];
		renderWithSessionLinkHandler(text, vi.fn());
		expect(screen.getByRole("link").querySelector("code")).toBeNull();
	});

	it.each(["text", "element"])("keeps an unlisted artifact's %s children renderable and copyable", async (kind) => {
		linkSource.workspaces = [];
		const copy = vi.spyOn(aoBridge.clipboard, "writeText").mockResolvedValue(undefined);
		try {
			render(<ArtifactPathCode text="/tmp/ao/artifacts/worker-1/plan.md">
				{kind === "text" ? "plan.md" : <code>plan.md</code>}
			</ArtifactPathCode>);
			fireEvent.contextMenu(screen.getByText("plan.md"));
			await userEvent.click(screen.getByRole("menuitem", { name: "Copy path" }));
			expect(copy).toHaveBeenCalledExactlyOnceWith("/tmp/ao/artifacts/worker-1/plan.md");
		} finally {
			copy.mockRestore();
		}
	});

	it("links an artifact named with a # or ? in inline code and in a worker report, by its whole name", () => {
		// Inline code and a report's artifact line are file paths, not link
		// targets: "#" and "?" there are part of the name.
		linkSource.workspaces = [{ id: "project", sessions: [{ id: "worker-1", artifactFiles: [{ path: "issue#42.md" }, { path: "why?.md" }] }] }];
		renderWithQueries(<ChatLinkProvider onSessionLinkOpen={vi.fn()}>
			<ChatMarkdown text="See `worker-1/why?.md`." />
			<WorkerUpdateRows updates={[{
				key: "report-1", state: "done", name: "Worker", sessionId: "worker-1", projectId: "project",
				body: "Filed it\nartifact: issue#42.md", createdAt: "2026-10-28T12:00:00Z",
			}]} />
		</ChatLinkProvider>);
		expect(screen.getByRole("link", { name: "worker-1/why?.md" })).toHaveAttribute("href", "ao://sessions/project/worker-1/artifacts/why%3F.md");
		expect(screen.getByRole("link", { name: "issue#42.md" })).toHaveAttribute("href", "ao://sessions/project/worker-1/artifacts/issue%2342.md");
	});

	it("links an inline artifact path once a workspace refresh lists it, and unlinks it when it goes", () => {
		linkSource.workspaces = [];
		// The same children each time, as a re-render of unchanged markdown passes.
		const children = <code>plan.md</code>;
		const path = () => <ChatLinkProvider onSessionLinkOpen={vi.fn()}>
			<ArtifactPathCode text="/tmp/ao/artifacts/worker-1/plan.md">{children}</ArtifactPathCode>
		</ChatLinkProvider>;
		const view = renderWithQueries(path());
		expect(screen.queryByRole("link")).not.toBeInTheDocument();
		linkSource.workspaces = [{ id: "project", sessions: [{ id: "worker-1", artifactFiles: [{ path: "plan.md" }] }] }];
		view.rerender(path());
		expect(screen.getByRole("link", { name: "plan.md" })).toHaveAttribute("href", "ao://sessions/project/worker-1/artifacts/plan.md");
		linkSource.workspaces = [];
		view.rerender(path());
		expect(screen.queryByRole("link")).not.toBeInTheDocument();
		expect(screen.getByText("plan.md")).toBeInTheDocument();
	});

	it("keeps a worker report renderable when its artifact is removed from the catalog", async () => {
		linkSource.workspaces = [{ id: "project", sessions: [{ id: "worker-1", artifactFiles: [{ path: "plan.md" }] }] }];
		const copy = vi.spyOn(aoBridge.clipboard, "writeText").mockResolvedValue(undefined);
		const report = () => <ChatLinkProvider onSessionLinkOpen={vi.fn()}><WorkerUpdateRows updates={[{
			key: "report-1", state: "done", name: "Worker", sessionId: "worker-1", projectId: "project",
			body: "Finished planning\nartifact: plan.md", createdAt: "2026-10-28T12:00:00Z",
		}]} /></ChatLinkProvider>;
		try {
			const view = renderWithQueries(report());
			expect(screen.getByRole("link", { name: "plan.md" })).toHaveAttribute("href", "ao://sessions/project/worker-1/artifacts/plan.md");
			linkSource.workspaces = [];
			view.rerender(report());
			expect(screen.getByTestId("worker-update")).toHaveTextContent("Finished planning");
			expect(screen.queryByRole("link", { name: "plan.md" })).not.toBeInTheDocument();
			fireEvent.contextMenu(screen.getByText("plan.md"));
			await userEvent.click(screen.getByRole("menuitem", { name: "Copy path" }));
			expect(copy).toHaveBeenCalledExactlyOnceWith("worker-1/plan.md");
		} finally {
			copy.mockRestore();
		}
	});

	it("leaves malformed session-like text inert", () => {
		renderWithSessionLinkHandler("ao://sessions/project/session/kill", vi.fn());
		expect(screen.queryByRole("link")).not.toBeInTheDocument();
	});

	it("opens a web link in the system browser on Option/Alt-click", () => {
		const onLinkOpen = vi.fn();
		const openExternal = vi.spyOn(aoBridge.app, "openExternal").mockResolvedValue(undefined);
		renderWithLinkHandler("see [the issue](https://example.com/i/1)", onLinkOpen);

		fireEvent.click(screen.getByRole("link", { name: "the issue" }), { altKey: true });

		expect(openExternal).toHaveBeenCalledWith("https://example.com/i/1");
		expect(onLinkOpen).not.toHaveBeenCalled();
		openExternal.mockRestore();
	});

	it("opens a web link in the system browser on Cmd-click", () => {
		const onLinkOpen = vi.fn();
		const openExternal = vi.spyOn(aoBridge.app, "openExternal").mockResolvedValue(undefined);
		renderWithLinkHandler("see [the issue](https://example.com/i/1)", onLinkOpen);

		fireEvent.click(screen.getByRole("link", { name: "the issue" }), { metaKey: true });

		expect(openExternal).toHaveBeenCalledWith("https://example.com/i/1");
		expect(onLinkOpen).not.toHaveBeenCalled();
		openExternal.mockRestore();
	});

	it("opens a web link in the system browser on Ctrl-click", () => {
		const onLinkOpen = vi.fn();
		const openExternal = vi.spyOn(aoBridge.app, "openExternal").mockResolvedValue(undefined);
		renderWithLinkHandler("see [the issue](https://example.com/i/1)", onLinkOpen);

		fireEvent.click(screen.getByRole("link", { name: "the issue" }), { ctrlKey: true });

		expect(openExternal).toHaveBeenCalledWith("https://example.com/i/1");
		expect(onLinkOpen).not.toHaveBeenCalled();
		openExternal.mockRestore();
	});

	it("offers 'Open in external browser' on right-click, without opening in the panel", async () => {
		const user = userEvent.setup();
		const onLinkOpen = vi.fn();
		const openExternal = vi.spyOn(aoBridge.app, "openExternal").mockResolvedValue(undefined);
		renderWithLinkHandler("see [the issue](https://example.com/i/1)", onLinkOpen);

		fireEvent.contextMenu(screen.getByRole("link", { name: "the issue" }));
		await user.click(await screen.findByRole("menuitem", { name: "Open in external browser" }));

		expect(openExternal).toHaveBeenCalledWith("https://example.com/i/1");
		expect(onLinkOpen).not.toHaveBeenCalled();
		openExternal.mockRestore();
	});

	it("offers 'Copy link' on right-click", async () => {
		const user = userEvent.setup();
		const writeText = vi.spyOn(aoBridge.clipboard, "writeText").mockResolvedValue(undefined);
		renderWithLinkHandler("see [the issue](https://example.com/i/1)", vi.fn());

		fireEvent.contextMenu(screen.getByRole("link", { name: "the issue" }));
		await user.click(await screen.findByRole("menuitem", { name: "Copy link" }));

		expect(writeText).toHaveBeenCalledWith("https://example.com/i/1");
		writeText.mockRestore();
	});

	it("omits the system-browser item for non-web links but still offers copying", async () => {
		// mailto must never reach shell.openExternal from the menu.
		renderWithLinkHandler("[Email support](mailto:support@example.com)", vi.fn());

		fireEvent.contextMenu(screen.getByRole("link", { name: "Email support" }));

		expect(await screen.findByRole("menuitem", { name: "Copy link" })).toBeInTheDocument();
		expect(screen.queryByRole("menuitem", { name: "Open in external browser" })).not.toBeInTheDocument();
	});

	it("opens non-web links in the system browser", async () => {
		const user = userEvent.setup();
		const onLinkOpen = vi.fn();
		const openExternal = vi.spyOn(aoBridge.app, "openExternal").mockResolvedValue(undefined);
		renderWithLinkHandler("[Email support](mailto:support@example.com)", onLinkOpen);

		await user.click(screen.getByRole("link", { name: "Email support" }));

		expect(openExternal).toHaveBeenCalledWith("mailto:support@example.com");
		expect(onLinkOpen).not.toHaveBeenCalled();
		openExternal.mockRestore();
	});

	it("renders bold, strikethrough and blockquotes", () => {
		render(<ChatMarkdown text={"**bold** and ~~gone~~\n\n> quoted"} />);
		expect(screen.getByText("bold").tagName).toBe("STRONG");
		expect(screen.getByText("gone").tagName).toBe("DEL");
		expect(screen.getByText("quoted").closest("blockquote")).not.toBeNull();
	});

	it("renders an unterminated fence as a code block, because streaming text arrives mid-fence", () => {
		render(<ChatMarkdown text={"```ts\nconst x = 1;"} />);
		expect(document.querySelector("pre code")).toHaveTextContent("const x = 1;");
		expect(document.body.textContent).not.toContain("```");
	});

	it("renders a mermaid fence as a diagram rather than source text", async () => {
		render(<ChatMarkdown text={"```mermaid\nflowchart TD\n    A --> B\n```"} />);

		expect(await screen.findByTestId("mermaid-diagram")).toBeInTheDocument();
		expect(vi.mocked(renderMermaidDiagram)).toHaveBeenCalledWith(
			"flowchart TD\n    A --> B",
			expect.stringMatching(/light|dark/),
		);
	});

	it("treats the mermaid label case-insensitively", async () => {
		render(<ChatMarkdown text={"```Mermaid\nflowchart TD\n    A --> B\n```"} />);

		expect(await screen.findByTestId("mermaid-diagram")).toBeInTheDocument();
	});

	it("keeps a mermaid fence as source text while streaming", () => {
		render(<ChatMarkdown text={"```mermaid\nflowchart TD\n    A --> B\n```"} streaming />);

		expect(screen.queryByTestId("mermaid-diagram")).not.toBeInTheDocument();
		expect(screen.getByText(/A --> B/)).toBeInTheDocument();
		expect(vi.mocked(renderMermaidDiagram)).not.toHaveBeenCalled();
	});

	it("renders a fence with no language as a block, not as inline code", () => {
		// Matching on the `language-*` class alone used to send these down the inline
		// path, where a whole `go test` transcript rendered as one accent-coloured run.
		render(<ChatMarkdown text={"```\nok\tgithub.com/aoagents/ao\t0.4s\n```"} />);
		const code = screen.getByText(/aoagents/);
		expect(code.closest("pre")).not.toBeNull();
		expect(screen.getByRole("button", { name: /copy code/i })).toBeInTheDocument();
	});
});

/* -------------------------------------------------------------------------- */

describe("ChatMarkdown images", () => {
	it("caps a single image's height instead of letting it fill the column", () => {
		render(<ChatMarkdown text={"![diagram](https://example.com/a.png)"} />);
		const image = screen.getByRole("img", { name: "diagram" });
		expect(image).toHaveClass("max-h-80", "max-w-full", "object-contain");
		expect(screen.queryByRole("group", { name: "Images" })).not.toBeInTheDocument();
	});

	it("lays out a paragraph of only images as a gallery of equal-height thumbnails", () => {
		render(
			<ChatMarkdown
				text={"![before](https://example.com/a.png) ![after](https://example.com/b.png)\n![diff](https://example.com/c.png)"}
			/>,
		);
		const gallery = screen.getByRole("group", { name: "Images" });
		expect(gallery).toHaveClass("flex-wrap");
		// A gallery is block content; nesting it in a <p> is invalid markup.
		expect(gallery.closest("p")).toBeNull();
		const thumbnails = screen.getAllByRole("img");
		expect(thumbnails).toHaveLength(3);
		for (const thumbnail of thumbnails) {
			expect(gallery).toContainElement(thumbnail);
			// A wide screenshot in a narrow column is clamped by max-w-full; cropping
			// it would cut the edges off the before/after pair this is for.
			expect(thumbnail).toHaveClass("h-40", "object-contain");
			expect(thumbnail).not.toHaveClass("object-cover");
			// A lazy image has no width until it decodes, so without a floor the row
			// wraps against zero-width boxes and reflows as each one lands.
			expect(thumbnail).toHaveClass("min-w-24");
		}
	});

	it("keeps images inline with prose when the paragraph also has text", () => {
		render(
			<ChatMarkdown text={"Before ![a](https://example.com/a.png) and after ![b](https://example.com/b.png)"} />,
		);
		expect(screen.queryByRole("group", { name: "Images" })).not.toBeInTheDocument();
		expect(screen.getByRole("img", { name: "a" }).closest("p")).not.toBeNull();
	});

	it("opens a clicked image at full size in a dialog that Escape dismisses", async () => {
		const user = userEvent.setup();
		render(<ChatMarkdown text={"![diagram](https://example.com/a.png)"} />);

		await user.click(screen.getByRole("button", { name: "Open image: diagram" }));

		const dialog = await screen.findByRole("dialog", { name: "diagram" });
		expect(dialog.querySelector("img")).toHaveAttribute("src", "https://example.com/a.png");
		expect(screen.getByRole("button", { name: "Close" })).toBeInTheDocument();

		await user.keyboard("{Escape}");
		await waitFor(() => expect(screen.queryByRole("dialog")).not.toBeInTheDocument());
	});

	it("falls back to the alt text when an image fails to load", () => {
		render(<ChatMarkdown text={"![build graph](https://example.com/missing.png)"} />);

		fireEvent.error(screen.getByRole("img", { name: "build graph" }));

		expect(screen.queryByRole("img")).not.toBeInTheDocument();
		expect(screen.queryByRole("button", { name: /open image/i })).not.toBeInTheDocument();
		expect(screen.getByText("build graph")).toBeInTheDocument();
	});

	it("keeps a failed thumbnail in the gallery and names an image with no alt text", () => {
		render(
			<ChatMarkdown
				text={"![first](https://example.com/missing.png) ![](https://example.com/other.png)"}
			/>,
		);

		fireEvent.error(screen.getByRole("img", { name: "first" }));

		const gallery = screen.getByRole("group", { name: "Images" });
		expect(gallery.querySelector("span")).toHaveClass("h-40");
		expect(screen.getByText("first")).toBeInTheDocument();

		fireEvent.error(document.querySelector('img[alt=""]')!);
		expect(screen.getByText("https://example.com/other.png")).toBeInTheDocument();
	});

	it("leaves a linked image to its link rather than nesting a button inside it", () => {
		render(<ChatMarkdown text={"[![badge](https://example.com/badge.svg)](https://example.com/ci)"} />);
		const link = screen.getByRole("link", { name: "badge" });
		expect(link.querySelector("button")).toBeNull();
		expect(link).toContainElement(screen.getByRole("img", { name: "badge" }));
	});
});

/* -------------------------------------------------------------------------- */

describe("ChatMarkdown image sources", () => {
	function renderInSession(text: string) {
		return render(
			<ChatImageSourceProvider sessionId="session-1">
				<ChatMarkdown text={text} />
			</ChatImageSourceProvider>,
		);
	}

	it("resolves relative image paths against the session workspace", () => {
		renderInSession("![screenshot](docs/screen%20shot.png)");
		const src = screen.getByRole("img", { name: "screenshot" }).getAttribute("src") ?? "";
		const url = new URL(src, "http://127.0.0.1");
		expect(url.pathname).toBe("/api/v1/sessions/session-1/workspace/file/blob");
		expect(url.searchParams.get("path")).toBe("docs/screen shot.png");
		expect(url.searchParams.get("side")).toBe("after");
	});

	it("loads a remote conversation image through that host's proxy", () => {
		render(
			<ChatImageSourceProvider sessionId="session-1" assetBaseUrl="http://127.0.0.1:4000/token-a">
				<ChatMarkdown text="![remote](docs/shot.png)" />
			</ChatImageSourceProvider>,
		);
		expect(screen.getByRole("img", { name: "remote" }).getAttribute("src")).toMatch(
			/^http:\/\/127\.0\.0\.1:4000\/token-a\/api\/v1\/sessions\/session-1\/workspace\/file\/blob\?/,
		);
	});

	it("does not resolve an offline remote image against the laptop daemon", () => {
		render(
			<ChatImageSourceProvider sessionId="session-1" remoteHost>
				<ChatMarkdown text="![remote](docs/shot.png)" />
			</ChatImageSourceProvider>,
		);
		expect(screen.queryByRole("img", { name: "remote" })).not.toBeInTheDocument();
	});

	it("keeps absolute image sources unchanged", () => {
		renderInSession("![remote](https://example.com/a.png)");
		expect(screen.getByRole("img", { name: "remote" })).toHaveAttribute("src", "https://example.com/a.png");
	});

	it("rebuilds relative image URLs when the daemon base URL changes", () => {
		renderInSession("![screenshot](docs/shot.png)");
		expect(screen.getByRole("img", { name: "screenshot" }).getAttribute("src")).toMatch(
			/^\/api\/v1\/sessions\/session-1\/workspace\/file\/blob/,
		);

		try {
			act(() => setApiBaseUrl("http://127.0.0.1:3111"));
			expect(screen.getByRole("img", { name: "screenshot" }).getAttribute("src")).toMatch(
				/^http:\/\/127\.0\.0\.1:3111\/api\/v1\/sessions\/session-1\/workspace\/file\/blob/,
			);
		} finally {
			act(() => setApiBaseUrl(null));
		}
	});

	it("leaves relative image sources unchanged without a session provider", () => {
		render(<ChatMarkdown text={"![screenshot](docs/shot.png)"} />);
		expect(screen.getByRole("img", { name: "screenshot" })).toHaveAttribute("src", "docs/shot.png");
	});
});

/* -------------------------------------------------------------------------- */

describe("ChatMarkdown code highlighting", () => {
	const block = (language: string, code: string) => `\`\`\`${language}\n${code}\n\`\`\``;

	/** The tokens land as `hljs-*` classes; `code-theme.css` colours them. */
	function tokens(): Element[] {
		return [...document.querySelectorAll("pre [class*='hljs-']")];
	}

	it("highlights a known language, leaving the code itself untouched", async () => {
		render(<ChatMarkdown text={block("go", "func main() {}")} />);
		await waitFor(() => expect(tokens().length).toBeGreaterThan(0), { timeout: 5_000 });
		expect(document.querySelector("pre")?.textContent).toBe("func main() {}");
	});

	it("leaves an unknown language as plain monospace rather than guessing", async () => {
		render(<ChatMarkdown text={block("brainfuck", "++++[>++++<-]")} />);
		// Nothing to wait for — there is no grammar to load — so a flush is enough to
		// prove no upgrade is coming.
		await waitFor(() => expect(screen.getByText("++++[>++++<-]")).toBeInTheDocument());
		expect(tokens()).toHaveLength(0);
		expect(screen.getByText("brainfuck")).toBeInTheDocument();
	});

	it("does not tokenize a fence that is still streaming, and does once it settles", async () => {
		const code = "type Session struct {\n\tID string\n}";
		const { rerender } = render(<ChatMarkdown text={block("go", code)} streaming />);
		expect(tokens()).toHaveLength(0);
		expect(document.querySelector("pre")?.textContent).toBe(code);

		rerender(<ChatMarkdown text={block("go", code)} />);
		await waitFor(() => expect(tokens().length).toBeGreaterThan(0));
		expect(document.querySelector("pre")?.textContent).toBe(code);
	});

	it("shows a settled block highlighted on its first render, from cache", async () => {
		const code = "SELECT 1 FROM turns;";
		const first = render(<ChatMarkdown text={block("sql", code)} />);
		await waitFor(() => expect(tokens().length).toBeGreaterThan(0));
		first.unmount();

		// A remount is what scrolling does. Without the cache this would flash plain
		// again; with it the very first commit is already highlighted.
		render(<ChatMarkdown text={block("sql", code)} />);
		expect(tokens().length).toBeGreaterThan(0);
	});

	it("toggles wrapping for long lines without re-tokenizing", async () => {
		const user = userEvent.setup();
		render(<ChatMarkdown text={block("sh", "echo one && echo two && echo three")} />);
		const wrap = screen.getByRole("button", { name: /wrap long lines/i });
		const wrapper = document.querySelector(".chat-code");

		expect(wrapper).toHaveAttribute("data-wrap", "false");
		await user.click(wrap);
		expect(wrapper).toHaveAttribute("data-wrap", "true");
		expect(wrap).toHaveAttribute("aria-pressed", "true");

		await user.click(wrap);
		expect(wrapper).toHaveAttribute("data-wrap", "false");
	});
});


describe("ChatMarkdown block streaming", () => {
	it.each([
		["```text\nprint( ", "print( "],
		["~~~text\n**literal ", "**literal "],
		["    **literal", "**literal"],
		["- item\n\n      **literal", "**literal"],
		[">     **literal", "**literal"],
		["[ref]: https://example.com\n\n    **literal", "**literal"],
	])("preserves streamed code source: %s", (text, code) => {
		const { container } = render(<ChatMarkdown text={text} streaming />);
		expect(container.querySelector("pre")?.textContent).toBe(code);
	});

	it("repairs incomplete inline formatting for display only and keeps partial links inert", () => {
		const { container, rerender } = render(<ChatMarkdown text="Some **unfinished" streaming />);
		expect(container.querySelector("strong")).toHaveTextContent("unfinished");
		rerender(<ChatMarkdown text="Some **unfinished" />);
		expect(container.querySelector("strong")).toBeNull();
		expect(container.textContent).toBe("Some **unfinished");
		rerender(<ChatMarkdown text="See [docs](https://exam" streaming />);
		expect(container.querySelector("a")).toBeNull();
		expect(container.textContent).toBe("See docs");
	});

	it("leaves open fences literal and highlights a completed fence before the reply ends", async () => {
		const code = 'const literal = "**unfinished";';
		const fence = "```js\n" + code;
		const { container, rerender } = render(<ChatMarkdown text={fence} streaming />);
		expect(container.querySelector("pre")?.textContent).toBe(code);
		rerender(<ChatMarkdown text={fence + "\n```\n\nMore prose"} streaming />);
		await waitFor(() => expect(container.querySelector("pre .hljs-keyword")).toBeInTheDocument());
		expect(container.querySelector("pre")?.textContent).toBe(code);
	});

	it("resolves late reference definitions and keeps footnote IDs document-wide", () => {
		const text = "First [docs][ref]\n\nSecond\n\nThird";
		const { container, rerender } = render(<ChatMarkdown text={text} streaming />);
		rerender(<ChatMarkdown text={text + "\n\n[ref]: https://example.com/docs"} streaming />);
		expect(screen.getByRole("link", { name: "docs" })).toHaveAttribute("href", "https://example.com/docs");
		rerender(<ChatMarkdown text={"First[^a]\n\nSecond[^b]\n\n[^a]: Alpha\n\n[^b]: Beta"} />);
		const ids = [...container.querySelectorAll("[id]")].map((node) => node.id);
		expect(new Set(ids).size).toBe(ids.length);
		expect(container.textContent).toContain("Alpha");
		expect(container.textContent).toContain("Beta");
	});

	it("keeps source-relative fade offsets when a chat opens across several blocks", () => {
		const text = "First\n\nSecond\n\nAlready visible";
		const view = (source: string) => <ChatOpeningRef.Provider value={{ current: true }}><ChatMarkdown text={source} streaming /></ChatOpeningRef.Provider>;
		const { container, rerender } = render(view(text));
		expect(container.querySelectorAll(".stream-char")).toHaveLength(0);
		rerender(view(text + " new"));
		expect(container.querySelectorAll(".stream-char")).toHaveLength(4);
		const first = container.querySelector("p");
		rerender(view(text + " new\n\nTail"));
		expect(container.querySelector("p")).toBe(first);
	});
});


describe("ActivityTitle", () => {
	it("keeps code delimiters inside multi-backtick code spans", () => {
		const { container } = render(<ActivityTitle text={"Edit ``file`name.ts``"} />);
		expect(container.querySelector("code")).toHaveTextContent("file`name.ts");
	});

	it("keeps disclosure titles inline and non-interactive", () => {
		const { container } = render(
			<button><ActivityTitle text={'# **Edit** [file](https://example.com) `path.ts` ![image](https://example.com/image.png) <input autofocus />'} /></button>,
		);
		expect(screen.getByRole("button")).toHaveTextContent("Edit file path.ts");
		expect(container.querySelector("strong")).toHaveTextContent("Edit");
		expect(container.querySelector("a, img, input, p, h1, pre")).toBeNull();
	});
});

describe("ChatMarkdown streaming fade", () => {
	afterEach(() => {
		vi.useRealTimers();
		vi.restoreAllMocks();
	});
	const fading = (container: HTMLElement) => container.querySelectorAll(".stream-char").length;

	it("never wraps a reply that mounts settled", () => {
		const { container } = render(<ChatMarkdown text="from history" />);
		expect(fading(container)).toBe(0);
	});

	it("does not fade text already on screen when a chat opens mid-stream", () => {
		const opening = { current: true };
		const reply = (text: string) => (
			<ChatOpeningRef.Provider value={opening}>
				<ChatMarkdown text={text} streaming />
			</ChatOpeningRef.Provider>
		);
		const { container, rerender } = render(reply("already shown"));
		opening.current = false;
		expect(fading(container)).toBe(0);
		expect(container.textContent).toBe("already shown");

		rerender(reply("already shown and more"));
		expect(fading(container)).toBe(" and more".length);
		expect(container.textContent).toBe("already shown and more");
	});

	it("does not fade text that streamed while the window was hidden", () => {
		const { container, rerender } = render(<ChatMarkdown text="seen" streaming />);
		Object.defineProperty(document, "visibilityState", { configurable: true, get: () => "hidden" });
		try {
			// Its fade could only play once the reader is back, replaying the whole stretch.
			rerender(<ChatMarkdown text="seen while away" streaming />);
		} finally {
			Reflect.deleteProperty(document, "visibilityState");
		}
		const afterReturn = fading(container);
		expect(container.textContent).toBe("seen while away");
		// Only what arrives once the reader is back fades in.
		rerender(<ChatMarkdown text="seen while away, back" streaming />);
		expect(fading(container)).toBe(afterReturn + ", back".length);
		expect(afterReturn).toBeLessThanOrEqual("seen".length);
	});

	it("fades while streaming and sheds the spans shortly after it settles", () => {
		vi.useFakeTimers();
		const { container, rerender } = render(<ChatMarkdown text="live text" streaming />);
		expect(fading(container)).toBeGreaterThan(0);
		expect(container.textContent).toBe("live text");

		rerender(<ChatMarkdown text="live text" />);
		expect(fading(container)).toBeGreaterThan(0);
		act(() => vi.advanceTimersByTime(500));
		expect(fading(container)).toBe(0);
		expect(container.textContent).toBe("live text");
	});

	it("keeps fading across a retry that restarts the stream inside the grace period", () => {
		vi.useFakeTimers();
		const { container, rerender } = render(<ChatMarkdown text="one" streaming />);
		rerender(<ChatMarkdown text="one" />);
		act(() => vi.advanceTimersByTime(200));
		rerender(<ChatMarkdown text="one two" streaming />);
		act(() => vi.advanceTimersByTime(500));
		expect(fading(container)).toBeGreaterThan(0);
	});

	it("renders plain text under reduced motion", () => {
		vi.spyOn(window, "matchMedia").mockImplementation((query) => ({
			matches: query === "(prefers-reduced-motion: reduce)",
			media: query,
			onchange: null,
			addEventListener: vi.fn(),
			removeEventListener: vi.fn(),
			addListener: vi.fn(),
			removeListener: vi.fn(),
			dispatchEvent: vi.fn(() => false),
		}));
		const { container } = render(<ChatMarkdown text="no motion" streaming />);
		expect(fading(container)).toBe(0);
		expect(screen.getByText("no motion")).toBeInTheDocument();
	});

	it("still compacts emoji while the fade is on", () => {
		const { container } = render(<ChatMarkdown text="done ✅ now" streaming />);
		expect(container.querySelector(".chat-md-emoji")).toHaveTextContent("✅");
		expect(container.textContent).toBe("done ✅ now");
	});

	it("keeps links working while their text fades", () => {
		const onLinkOpen = vi.fn();
		render(
			<ChatLinkProvider onLinkOpen={onLinkOpen}>
				<ChatMarkdown text="see [docs](https://example.com/docs)" streaming />
			</ChatLinkProvider>,
		);
		expect(screen.getByRole("link")).toHaveTextContent("docs");
	});
});

describe("ChatMarkdown math", () => {
	afterEach(() => useUiStore.setState({ renderMath: false }));

	it("stops repairing a paused stream's prose once math turns it into a formula", async () => {
		// With math off, `$a~b$` is prose, and streaming repair escapes its `~`.
		const { container } = render(<ChatMarkdown text="Paused at $a~b$" streaming />);
		act(() => useUiStore.setState({ renderMath: true }));
		await waitFor(() => expect(container.querySelector('[data-math="inline"] .katex')).not.toBeNull());
		expect(container.querySelector("[data-tex]")).toHaveAttribute("data-tex", "a~b");
	});

	it("leaves dollar signs as text while the setting is off", () => {
		const { container } = render(<ChatMarkdown text="Costs $5 and $10, or $x^2$." />);
		expect(container).toHaveTextContent("Costs $5 and $10, or $x^2$.");
		expect(container.querySelector("[data-math]")).toBeNull();
	});

	it("typesets inline and display formulas in every delimiter when on", async () => {
		useUiStore.setState({ renderMath: true });
		const { container } = render(
			<ChatMarkdown text={"Euler $e^{i\\pi}+1=0$ and \\(x^2\\).\n\n\\[\n\\int_0^1 x\\,dx\n\\]\n\n$$\nE=mc^2\n$$"} />,
		);
		await waitFor(() => expect(container.querySelectorAll('[data-math="inline"] .katex')).toHaveLength(2));
		expect(container.querySelectorAll('[data-math="display"] .katex-display')).toHaveLength(2);
		expect(container).not.toHaveTextContent("\\(");
	});

	it("typesets a ```math fence without the setting", async () => {
		const { container } = render(<ChatMarkdown text={"```math\na^2+b^2=c^2\n```"} />);
		await waitFor(() => expect(container.querySelector('[data-math="display"] .katex-display')).not.toBeNull());
		expect(container.querySelector("pre")).toBeNull();
	});

	it("shows the TeX source when KaTeX cannot parse it", async () => {
		useUiStore.setState({ renderMath: true });
		const { container } = render(<ChatMarkdown text={"Try $\\notacommand{x}$ and $y$."} />);
		await waitFor(() => expect(container.querySelector('[data-math="inline"] .katex')).not.toBeNull());
		expect(container.querySelectorAll('[data-math="inline"] .katex')).toHaveLength(1);
		expect(container).toHaveTextContent("$\\notacommand{x}$");
	});
});
