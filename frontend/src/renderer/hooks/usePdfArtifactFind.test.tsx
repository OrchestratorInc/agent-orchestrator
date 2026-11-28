import { useEffect, useRef, useState } from "react";
import { act, cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { PDFDocumentProxy } from "pdfjs-dist";
import { ArtifactFindScope } from "../components/ArtifactFindScope";
import { usePdfArtifactFind } from "./usePdfArtifactFind";

function Page({ page, text }: { page: number; text: string }) {
	const root = useRef<HTMLDivElement>(null);
	const [painted, setPainted] = useState(page === 1);
	useEffect(() => {
		const element = root.current!;
		const reveal = () => setPainted(true);
		element.addEventListener("artifact-find-reveal", reveal);
		return () => element.removeEventListener("artifact-find-reveal", reveal);
	}, []);
	return <div ref={root} data-page-number={page}><div className="pdf-text-layer">{painted ? <span>{text}</span> : null}</div></div>;
}
function Pdf({ pdf, texts }: { pdf: PDFDocumentProxy; texts: string[] }) {
	const root = useRef<HTMLDivElement>(null);
	usePdfArtifactFind(pdf, root, true);
	return <div ref={root} data-testid="pdf">{texts.map((text, index) => <Page key={index} page={index + 1} text={text} />)}</div>;
}
function fakePdf(texts: string[]) {
	return { numPages: texts.length, getPage: vi.fn(async (page: number) => ({ getTextContent: async () => ({ items: [{ str: texts[page - 1], hasEOL: false }] }) })) } as unknown as PDFDocumentProxy;
}
async function search(value: string) {
	fireEvent.pointerDown(screen.getByTestId("pdf")); fireEvent.keyDown(document.body, { key: "f", ctrlKey: true });
	const input = await screen.findByRole("searchbox"); fireEvent.change(input, { target: { value } }); return input;
}
beforeEach(() => {
	vi.spyOn(HTMLElement.prototype, "getClientRects").mockReturnValue([{}] as unknown as DOMRectList);
	Object.defineProperty(HTMLElement.prototype, "scrollIntoView", { configurable: true, value: vi.fn() });
	Object.defineProperty(Range.prototype, "getBoundingClientRect", { configurable: true, value: () => ({ top: 0, bottom: 10, left: 0, right: 10, width: 10, height: 10 }) });
	vi.stubGlobal("Highlight", class extends Set<Range> {}); vi.stubGlobal("CSS", { highlights: new Map() });
});
afterEach(() => { cleanup(); vi.restoreAllMocks(); vi.unstubAllGlobals(); });

describe("PDF artifact find", () => {
	it("searches unpainted pages, reveals their text and preserves highlights after redraw", async () => {
		const texts = ["first page", "second page", "needle and needle"];
		const pdf = fakePdf(texts);
		render(<ArtifactFindScope identity="pdf"><Pdf pdf={pdf} texts={texts} /></ArtifactFindScope>);
		const input = await search("needle"); await screen.findByText("1/2");
		await waitFor(() => expect((CSS.highlights.get("artifact-find-active") as unknown as Set<Range>)?.values().next().value?.toString()).toBe("needle"));
		expect(pdf.getPage).toHaveBeenCalledTimes(3);
		fireEvent.keyDown(input, { key: "Enter" }); expect(screen.getByText("2/2")).toBeInTheDocument();
		const layer = document.querySelector("[data-page-number='3'] .pdf-text-layer")!;
		act(() => { layer.innerHTML = "<span>needle and needle</span>"; });
		await waitFor(() => expect((CSS.highlights.get("artifact-find-active") as unknown as Set<Range>)?.values().next().value?.startOffset).toBe(11));
		expect(pdf.getPage).toHaveBeenCalledTimes(3);
	});
	it("distinguishes scanned documents from a text search with no matches", async () => {
		render(<ArtifactFindScope identity="scan"><Pdf pdf={fakePdf(["", ""])} texts={["", ""]} /></ArtifactFindScope>);
		await search("needle"); await screen.findByText(/no searchable text/);
	});
	it("marks the search incomplete when a page cannot be extracted", async () => {
		const pdf = fakePdf(["needle", "broken"]);
		vi.mocked(pdf.getPage).mockImplementation(async (page) => {
			if (page === 2) throw new Error("corrupt page");
			return { getTextContent: async () => ({ items: [{ str: "needle" }] }) } as never;
		});
		render(<ArtifactFindScope identity="broken"><Pdf pdf={pdf} texts={["needle", ""]} /></ArtifactFindScope>);
		await search("needle"); await screen.findByText(/Search is incomplete/); expect(screen.queryByText("1/1")).toBeNull();
	});
	it("discards an obsolete search after the query changes", async () => {
		let resolve!: (value: unknown) => void;
		const content = new Promise((done) => { resolve = done; });
		const pdf = { numPages: 1, getPage: async () => ({ getTextContent: () => content }) } as unknown as PDFDocumentProxy;
		render(<ArtifactFindScope identity="slow"><Pdf pdf={pdf} texts={["hello"]} /></ArtifactFindScope>);
		const input = await search("hello"); fireEvent.change(input, { target: { value: "absent" } });
		await act(async () => { resolve({ items: [{ str: "hello" }] }); await content; });
		await screen.findByText("No results"); expect(screen.queryByText("1/1")).toBeNull();
	});
});
