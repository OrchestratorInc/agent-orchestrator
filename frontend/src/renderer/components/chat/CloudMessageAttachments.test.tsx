import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import type { ReactNode } from "react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import type { CloudAttachment } from "../../lib/cloud-attachments";
import { useUiStore } from "../../stores/ui-store";
import { CloudAttachmentSourceProvider, CloudMessageAttachments } from "./CloudMessageAttachments";

const mocks = vi.hoisted(() => ({
	fetchCloudAttachment: vi.fn(),
	save: vi.fn(),
	pdfPreview: vi.fn(),
}));
vi.mock("../../lib/cloud-attachments", async (importOriginal) => ({
	...await importOriginal<typeof import("../../lib/cloud-attachments")>(),
	fetchCloudAttachment: mocks.fetchCloudAttachment,
}));
vi.mock("../../lib/bridge", async (importOriginal) => {
	const actual = await importOriginal<typeof import("../../lib/bridge")>();
	return { ...actual, aoBridge: { ...actual.aoBridge, chatImage: { ...actual.aoBridge.chatImage, save: mocks.save } } };
});
vi.mock("../PdfPreview", () => ({
	PdfPreview: (props: { path: string; firstPageOnly?: boolean; load: () => Promise<Uint8Array> }) => {
		mocks.pdfPreview(props);
		return <div data-testid="pdf-preview" data-first-page={String(Boolean(props.firstPageOnly))}>{props.path}</div>;
	},
}));

const source = { baseUrl: "https://cp.example.com", orgId: "org-1", sessionId: "session-1" };
const attachment = (overrides: Partial<CloudAttachment>): CloudAttachment => ({
	id: "a1", fileName: "notes.txt", contentType: "text/plain; charset=utf-8", size: 2048, ...overrides,
});

function renderAttachments(attachments: CloudAttachment[] | undefined, withSource = true) {
	const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
	const wrap = (children: ReactNode) => withSource
		? <CloudAttachmentSourceProvider source={source}>{children}</CloudAttachmentSourceProvider>
		: children;
	return render(<QueryClientProvider client={client}>{wrap(<CloudMessageAttachments attachments={attachments} />)}</QueryClientProvider>);
}

beforeEach(() => {
	mocks.fetchCloudAttachment.mockReset();
	mocks.save.mockReset().mockResolvedValue(true);
	mocks.pdfPreview.mockReset();
});

describe("CloudMessageAttachments", () => {
	it("shows nothing for a message without files", () => {
		const { container } = renderAttachments([]);
		expect(container).toBeEmptyDOMElement();
		expect(renderAttachments(undefined).container).toBeEmptyDOMElement();
	});

	it("shows an image in place from its bytes", async () => {
		mocks.fetchCloudAttachment.mockResolvedValue(new Blob([new Uint8Array([137, 80, 78, 71])], { type: "image/png" }));
		renderAttachments([attachment({ id: "img", fileName: "shot.png", contentType: "image/png" })]);
		const image = await screen.findByRole("img", { name: "shot.png" });
		await waitFor(() => expect(image.getAttribute("src")).toBe("data:image/png;base64,iVBORw=="));
		expect(mocks.fetchCloudAttachment).toHaveBeenCalledWith(source.baseUrl, source.orgId, source.sessionId, expect.objectContaining({ id: "img" }));
	});

	it("shows a PDF as its first page, opening the whole document", async () => {
		mocks.fetchCloudAttachment.mockResolvedValue(new Blob([new Uint8Array([37, 80, 68, 70])], { type: "application/pdf" }));
		renderAttachments([attachment({ id: "pdf", fileName: "spec.pdf", contentType: "application/pdf" })]);
		const card = screen.getByRole("button", { name: /spec\.pdf/ });
		expect(within(card).getByTestId("pdf-preview")).toHaveAttribute("data-first-page", "true");
		fireEvent.click(card);
		expect(await screen.findByRole("dialog", { name: "spec.pdf" })).toBeInTheDocument();
		const whole = screen.getAllByTestId("pdf-preview").find((preview) => preview.dataset.firstPage === "false");
		expect(whole).toBeDefined();
		// Each viewer gets its own copy of the bytes.
		const load = mocks.pdfPreview.mock.calls.at(-1)?.[0].load as () => Promise<Uint8Array>;
		expect(Array.from(await load())).toEqual([37, 80, 68, 70]);
	});

	it("lists any other file with its size, and saves it when chosen", async () => {
		mocks.fetchCloudAttachment.mockResolvedValue(new Blob(["hello"], { type: "text/plain" }));
		renderAttachments([attachment({})]);
		const chip = screen.getByRole("button", { name: /notes\.txt/ });
		expect(chip).toHaveTextContent("2.0 KB");
		expect(chip).toHaveAttribute("title", "Save notes.txt");
		fireEvent.click(chip);
		await waitFor(() => expect(mocks.save).toHaveBeenCalledTimes(1));
		const saved = mocks.save.mock.calls[0]![0] as { name: string; bytes: Uint8Array };
		expect(saved.name).toBe("notes.txt");
		expect(new TextDecoder().decode(saved.bytes)).toBe("hello");
	});

	it("never renders an SVG as an image", () => {
		renderAttachments([attachment({ id: "svg", fileName: "logo.svg", contentType: "image/svg+xml", size: 300 })]);
		expect(screen.queryByRole("img")).not.toBeInTheDocument();
		expect(screen.getByRole("button", { name: /logo\.svg/ })).toHaveTextContent("300 B");
		expect(mocks.fetchCloudAttachment).not.toHaveBeenCalled();
	});

	it("says when a file could not be saved", async () => {
		const showGlobalToast = vi.fn();
		useUiStore.setState({ showGlobalToast });
		mocks.fetchCloudAttachment.mockRejectedValue(new Error("This session was deleted. Restore it to continue."));
		renderAttachments([attachment({ size: 3 * 1024 * 1024 })]);
		const chip = screen.getByRole("button", { name: /notes\.txt/ });
		expect(chip).toHaveTextContent("3.0 MB");
		fireEvent.click(chip);
		await waitFor(() => expect(showGlobalToast).toHaveBeenCalledWith("Couldn't save the file", "This session was deleted. Restore it to continue.", "error"));
		expect(mocks.save).not.toHaveBeenCalled();
	});

	it("cannot save a file without a session to read it from", () => {
		renderAttachments([attachment({})], false);
		expect(screen.getByRole("button", { name: /notes\.txt/ })).toBeDisabled();
	});
});
