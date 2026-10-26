import { fireEvent, render, screen } from "@testing-library/react";
import { describe, expect, it } from "vitest";
import { normalizeToolContent, ToolImage } from "./tool-content-images";

// Public-domain synthetic 32x32 checkerboard PNG. No session content or private screenshots.
const data = "iVBORw0KGgoAAAANSUhEUgAAACAAAAAgCAYAAABzenr0AAAASUlEQVR4nO3OMQ0AMAwDwcApxGIKpnBJGcRDFXn5wePLF1nd086tcb99AAAAwA7YPlA9AAAA/IDtA9UDAADAD9g+UD0AAADsgAePf/6mCWRFdQAAAABJRU5ErkJggg==";
const block = { type: "image", data, mimeType: "image/png" };

function Content({ value }: { value: unknown }) {
	return <>{normalizeToolContent(value).map((part, index) => part.kind === "image"
		? <ToolImage key={index} image={part} /> : <pre key={index}>{part.text}</pre>)}</>;
}

describe("tool content images", () => {
	it.each([
		block,
		{ content: block },
		{ content: [{ content: block }] },
		{ source: { data, media_type: "image/png" } },
	])("renders supported image blocks without binary DOM text", (value) => {
		const { container } = render(<Content value={value} />);
		expect(screen.getByRole("img")).toHaveAttribute("src", `data:image/png;base64,${data}`);
		expect(container.querySelector("pre")).toBeNull();
		expect(container.textContent).not.toContain("iVBOR");
		fireEvent.click(screen.getByRole("button", { name: /Open image/ }));
		expect(screen.getByRole("dialog")).toBeInTheDocument();
	});

	it("preserves text alongside an image, including on the same block", () => {
		const { container } = render(<Content value={[{ ...block, text: "caption" }, { type: "text", text: "result" }]} />);
		expect(screen.getByText("caption")).toBeInTheDocument();
		expect(screen.getByText("result")).toBeInTheDocument();
		expect(screen.getByRole("img")).toBeInTheDocument();
		expect(container.textContent).not.toContain(data);
	});

	it.each([
		{ ...block, data: "iVBOR<invalid>" },
		{ ...block, mimeType: data },
		{ type: "image" },
		{ ...block, truncated: true },
		{ truncated: true, content: [block] },
		{ source: { data: "iVBOR-prefix", media_type: "image/png" } },
	])("uses a short safe placeholder for malformed or partial blocks", (value) => {
		const { container } = render(<Content value={value} />);
		expect(screen.queryByRole("img")).toBeNull();
		expect(container.textContent).toMatch(/Tool image \(image\/.+, (?:\d+ base64 characters|size unavailable)\)/);
		expect(container.textContent).not.toContain("iVBOR");
		expect(container.textContent!.length).toBeLessThan(150);
	});

	it("keeps the known size of image data stripped by the provider adapter", () => {
		render(<Content value={{ type: "image", mimeType: "image/png", bytes: 1_800_000 }} />);
		expect(screen.getByText("Tool image (image/png, 1800000 bytes)")).toBeInTheDocument();
		expect(screen.queryByRole("img")).toBeNull();
	});

	it("uses a safe type/size fallback when the browser cannot decode an image", () => {
		const { container } = render(<Content value={block} />);
		fireEvent.error(screen.getByRole("img"));
		expect(screen.queryByRole("img")).toBeNull();
		expect(container.textContent).toContain("image/png, 176 base64 characters");
		expect(container.textContent).not.toContain("iVBOR");
	});
});
