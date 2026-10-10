import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import type { ReactNode } from "react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { HoverCard } from "../ui/hover-card";
import { ChatLinkProvider, ChatMarkdown } from "./ChatMarkdown";
import { MarkdownFrontmatterPreview } from "./MarkdownFrontmatterPreview";
import type { MarkdownFilePreviewSource } from "../../lib/markdown-file-preview";

const { getMock, hostGet, cloudGet, cloudState } = vi.hoisted(() => ({
	getMock: vi.fn(),
	hostGet: vi.fn(),
	cloudGet: vi.fn(),
	cloudState: { ready: true, baseUrl: "https://cloud.example.test" },
}));

vi.mock("../../lib/api-client", () => ({
	apiClient: { GET: getMock },
	apiErrorMessage: (_error: unknown, fallback = "Request failed") => fallback,
}));

vi.mock("../../lib/host-clients", () => ({
	clientForSessionHost: (hostId?: string) => ({ GET: hostId ? hostGet : getMock }),
}));

vi.mock("../../hooks/useCloudCp", () => ({
	useCloudCp: () => ({
		client: { getWorkspaceReviewFile: cloudGet },
		ready: cloudState.ready,
		baseUrl: cloudState.baseUrl,
		userId: "user-1",
	}),
}));

function renderWithQuery(children: ReactNode) {
	const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
	return render(<QueryClientProvider client={client}>{children}</QueryClientProvider>);
}

function renderCard(path: string, source: MarkdownFilePreviewSource) {
	return renderWithQuery(
		<HoverCard open>
			<MarkdownFrontmatterPreview path={path} source={source} />
		</HoverCard>,
	);
}

function fileResponse(content: string, extra: Record<string, unknown> = {}) {
	return { data: { content, binary: false, deleted: false, ...extra }, error: undefined };
}

const doc = `---\nname: Deployment checklist\ndescription: Steps to verify a build before deployment.\n---\n# Body\n`;

describe("MarkdownFrontmatterPreview", () => {
	beforeEach(() => {
		getMock.mockReset();
		hostGet.mockReset();
		cloudGet.mockReset();
		cloudState.ready = true;
		cloudState.baseUrl = "https://cloud.example.test";
	});

	it("shows name and description as text and does not render metadata HTML", async () => {
		getMock.mockResolvedValue(fileResponse("---\nname: \"<img alt=injected>\"\ndescription: <b>Steps</b>\n---\n"));
		const { container } = renderCard("docs/check.md", { kind: "workspace", sessionId: "sess-1" });
		expect(await screen.findByText("<img alt=injected>")).toBeInTheDocument();
		expect(screen.getByText("<b>Steps</b>")).toBeInTheDocument();
		expect(container.querySelector("img, b, script")).toBeNull();
		expect(getMock).toHaveBeenCalledWith("/api/v1/sessions/{sessionId}/workspace/file", expect.objectContaining({
			params: { path: { sessionId: "sess-1" }, query: { path: "docs/check.md" } },
		}));
	});

	it("routes a remote host read through that host", async () => {
		hostGet.mockResolvedValue(fileResponse(doc));
		renderCard("docs/check.md", { kind: "workspace", sessionId: "sess-1", hostId: "host-a" });
		expect(await screen.findByText("Deployment checklist")).toBeInTheDocument();
		expect(hostGet).toHaveBeenCalled();
		expect(getMock).not.toHaveBeenCalled();
		expect(cloudGet).not.toHaveBeenCalled();
	});

	it("routes a cloud read through the cloud workspace file", async () => {
		cloudGet.mockResolvedValue({ content: doc, binary: false, deleted: false });
		renderCard("docs/check.md", { kind: "cloud", orgId: "org-1", sessionId: "sess-1" });
		expect(await screen.findByText("Steps to verify a build before deployment.")).toBeInTheDocument();
		expect(cloudGet).toHaveBeenCalledWith("org-1", "sess-1", { path: "docs/check.md", scope: "combined" });
		expect(getMock).not.toHaveBeenCalled();
	});

	it("does not fetch when cloud is not ready", () => {
		cloudState.ready = false;
		renderCard("docs/check.md", { kind: "cloud", orgId: "org-1", sessionId: "sess-1" });
		expect(screen.getByText("Unable to load this file.")).toBeInTheDocument();
		expect(cloudGet).not.toHaveBeenCalled();
	});

	it("shows a file failure without another file's metadata", async () => {
		getMock.mockResolvedValue({ data: undefined, error: { message: "nope" } });
		renderCard("docs/missing.md", { kind: "workspace", sessionId: "sess-1" });
		expect(await screen.findByText("Unable to load this file.")).toBeInTheDocument();
		expect(screen.queryByText("Deployment checklist")).not.toBeInTheDocument();
	});

	it("handles missing frontmatter", async () => {
		getMock.mockResolvedValue(fileResponse("# plain\n"));
		renderCard("docs/plain.md", { kind: "workspace", sessionId: "sess-1" });
		expect(await screen.findByText("No name or description.")).toBeInTheDocument();
	});

	it("handles malformed frontmatter", async () => {
		getMock.mockResolvedValue(fileResponse("---\nname: [\n---\n"));
		renderCard("docs/bad.md", { kind: "workspace", sessionId: "sess-1" });
		expect(await screen.findByText("This file's frontmatter could not be read.")).toBeInTheDocument();
	});

	it("clamps a long description", async () => {
		const tail = "TAIL_MARKER";
		getMock.mockResolvedValue(fileResponse(`---\nname: Long\ndescription: ${"word ".repeat(200)}${tail}\n---\n`));
		renderCard("docs/long.md", { kind: "workspace", sessionId: "sess-1" });
		const description = await screen.findByText(/word/);
		expect(description.textContent?.endsWith("...")).toBe(true);
		expect(description.textContent).not.toContain(tail);
	});

	it("ignores a stale response from a previous path", async () => {
		let releaseFirst: () => void = () => undefined;
		getMock.mockImplementation((_url: string, init: { params: { query: { path: string } } }) => {
			if (init.params.query.path === "docs/first.md") {
				return new Promise((resolve) => {
					releaseFirst = () => resolve(fileResponse("---\nname: First file\ndescription: Old\n---\n"));
				});
			}
			return Promise.resolve(fileResponse("---\nname: Second file\ndescription: Current\n---\n"));
		});
		const source: MarkdownFilePreviewSource = { kind: "workspace", sessionId: "sess-1" };
		const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
		const { rerender } = render(
			<QueryClientProvider client={client}>
				<HoverCard open><MarkdownFrontmatterPreview path="docs/first.md" source={source} /></HoverCard>
			</QueryClientProvider>,
		);
		rerender(
			<QueryClientProvider client={client}>
				<HoverCard open><MarkdownFrontmatterPreview path="docs/second.md" source={source} /></HoverCard>
			</QueryClientProvider>,
		);
		expect(await screen.findByText("Second file")).toBeInTheDocument();
		releaseFirst();
		await waitFor(() => expect(screen.queryByText("First file")).not.toBeInTheDocument());
		expect(screen.getByText("Second file")).toBeInTheDocument();
	});
});

describe("markdown link hover", () => {
	beforeEach(() => {
		getMock.mockReset();
		hostGet.mockReset();
		cloudGet.mockReset();
		cloudState.ready = true;
	});

	function renderLinks(text: string, source?: MarkdownFilePreviewSource) {
		const onFileOpen = vi.fn();
		renderWithQuery(
			<ChatLinkProvider markdownFileSource={source} onFileOpen={onFileOpen} workspacePaths={["docs/check.md", "docs/other.md"]}>
				<ChatMarkdown text={text} />
			</ChatLinkProvider>,
		);
		return onFileOpen;
	}

	it("keeps the label and opens the same file", () => {
		getMock.mockResolvedValue(fileResponse(doc));
		const onFileOpen = renderLinks("[Deployment checklist](docs/check.md)", { kind: "workspace", sessionId: "sess-1" });
		const link = screen.getByRole("link", { name: "Deployment checklist" });
		expect(link).toHaveAttribute("href", "docs/check.md");
		fireEvent.click(link);
		expect(onFileOpen).toHaveBeenCalledExactlyOnceWith("docs/check.md");
		expect(getMock).not.toHaveBeenCalled();
	});

	it("fetches only after hover and not for an external markdown URL", async () => {
		getMock.mockResolvedValue(fileResponse(doc));
		renderLinks("[Local](docs/check.md) [Remote](https://example.com/README.md)", { kind: "workspace", sessionId: "sess-1" });
		expect(getMock).not.toHaveBeenCalled();
		fireEvent.pointerOver(screen.getByRole("link", { name: "Remote" }), { pointerType: "mouse" });
		await waitFor(() => expect(getMock).toHaveBeenCalled());
		expect(getMock).not.toHaveBeenCalledWith("/api/v1/sessions/{sessionId}/workspace/file", expect.anything());
		fireEvent.pointerOver(screen.getByRole("link", { name: "Local" }), { pointerType: "mouse" });
		expect(await screen.findByText("Steps to verify a build before deployment.")).toBeInTheDocument();
		expect(getMock).toHaveBeenCalledWith("/api/v1/sessions/{sessionId}/workspace/file", expect.objectContaining({
			params: { path: { sessionId: "sess-1" }, query: { path: "docs/check.md" } },
		}));
	});

	it("opens from keyboard focus and dismisses when focus leaves", async () => {
		getMock.mockResolvedValue(fileResponse(doc));
		renderLinks("[Local](docs/check.md)", { kind: "workspace", sessionId: "sess-1" });
		const link = screen.getByRole("link", { name: "Local" });
		fireEvent.focus(link);
		expect(await screen.findByText("Deployment checklist")).toBeInTheDocument();
		fireEvent.blur(link);
		await waitFor(() => expect(screen.queryByText("Steps to verify a build before deployment.")).not.toBeInTheDocument());
	});

	it("does not read a parent path", () => {
		renderLinks("[Outside](../secret.md)", { kind: "workspace", sessionId: "sess-1" });
		fireEvent.pointerOver(screen.getByRole("link", { name: "Outside" }), { pointerType: "mouse" });
		expect(getMock).not.toHaveBeenCalledWith("/api/v1/sessions/{sessionId}/workspace/file", expect.anything());
	});
});
