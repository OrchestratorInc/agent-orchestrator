import { fireEvent, render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";
import { captureFileReference } from "../../../shared/chat-file-reference";
import { CapturedFileAnnotation } from "./CapturedFileAnnotation";

function reference(texts: string[]) {
	return captureFileReference({ path: `src/${"long-name-".repeat(20)}.ts`, side: "file", line: 3, endLine: 2 + texts.length, lines: texts.map((text, i) => ({ kind: "context", oldNo: i + 3, newNo: i + 3, text })) }, "session");
}
describe("captured file annotation preview", () => {
	it("clips six lines only for display and expands without changing captured code", async () => {
		const user = userEvent.setup();
		const ref = reference(Array.from({ length: 9 }, (_, i) => `line ${i + 1}`));
		const original = JSON.stringify(ref);
		render(<CapturedFileAnnotation reference={ref} />);
		const preview = document.querySelector("pre")!;
		expect(preview.textContent).toBe("line 1\nline 2\nline 3\nline 4\nline 5\nline 6…");
		expect(preview).toHaveClass("line-clamp-6", "select-text");
		const toggle = screen.getByRole("button", { name: "Show more" });
		toggle.focus();
		await user.keyboard("{Enter}");
		expect(preview.textContent).toContain("line 9");
		expect(preview).toHaveClass("overflow-auto");
		expect(screen.getByRole("button", { name: "Show less" })).toHaveAttribute("aria-expanded", "true");
		await user.click(screen.getByRole("button", { name: "Show less" }));
		expect(preview.textContent).not.toContain("line 9");
		expect(JSON.stringify(ref)).toBe(original);
		expect(screen.queryByText("Captured selection truncated")).not.toBeInTheDocument();
	});
	it("clips by Unicode characters and keeps capture truncation a separate notice", () => {
		const ref = reference(["😀".repeat(650)]);
		ref.truncated = true;
		render(<CapturedFileAnnotation reference={ref} />);
		expect([...document.querySelector("pre")!.textContent!]).toHaveLength(601);
		expect(screen.getByText("Captured selection truncated")).toBeInTheDocument();
		fireEvent.click(screen.getByRole("button", { name: "Show more" }));
		expect([...document.querySelector("pre")!.textContent!]).toHaveLength(650);
	});
	it("keeps the range and side outside the shortened filename and has no navigation", () => {
		const ref = reference(["short();"]);
		render(<CapturedFileAnnotation reference={ref} />);
		expect(screen.getByText("L3 · file")).toHaveClass("shrink-0");
		expect(screen.getByTitle(ref.path)).toHaveClass("border-b");
		expect(screen.queryByRole("button")).not.toBeInTheDocument();
	});
});

it("keeps expansion, code selection, and drag gestures separate from navigation", async () => {
 const user = userEvent.setup();
 const onOpen = vi.fn();
 render(<CapturedFileAnnotation reference={reference(Array.from({ length: 9 }, (_, i) => `line ${i}`))} onOpen={onOpen} />);
 const card = screen.getByRole("button", { name: /^Open file:/ });
 const toggle = screen.getByRole("button", { name: "Show more" });
 toggle.focus();
 await user.keyboard("{Enter}");
 expect(onOpen).not.toHaveBeenCalled();
 const pre = document.querySelector("pre")!;
 const selection = vi.spyOn(window, "getSelection").mockReturnValue({
  isCollapsed: false, rangeCount: 1, getRangeAt: () => ({ intersectsNode: () => true }),
 } as unknown as Selection);
 fireEvent.click(pre);
 expect(onOpen).not.toHaveBeenCalled();
 selection.mockRestore();
 fireEvent.pointerDown(pre, { clientX: 1, clientY: 1 });
 fireEvent.pointerMove(pre, { clientX: 30, clientY: 1 });
 fireEvent.pointerUp(pre);
 fireEvent.click(pre);
 expect(onOpen).not.toHaveBeenCalled();
 fireEvent.pointerDown(pre, { clientX: 1, clientY: 1 });
 fireEvent.pointerUp(pre);
 fireEvent.click(pre);
 expect(onOpen).toHaveBeenCalledOnce();
 card.focus();
 await user.keyboard(" ");
 expect(onOpen).toHaveBeenCalledTimes(2);
});


it("exposes a standalone header button while keeping preview content readable", async () => {
 const user=userEvent.setup(),onOpen=vi.fn();
 const ref=reference(Array.from({length:9},(_,i)=>`readable line ${i}`));
 const view=render(<CapturedFileAnnotation reference={ref} onOpen={onOpen}><span>Source verified</span></CapturedFileAnnotation>);
 const header=screen.getByRole("button",{name:/^Open file:/});
 const pre=document.querySelector("pre")!;
 expect(header.tagName).toBe("BUTTON");
 expect(header.contains(pre)).toBe(false);
 expect(pre.closest('[role="button"],button')).toBeNull();
 expect(screen.getByText("Source verified").closest('button,[role="button"]')).toBeNull();
 expect(header.contains(screen.getByRole("button",{name:"Show more"}))).toBe(false);
 header.focus();await user.keyboard("{Enter}");expect(onOpen).toHaveBeenCalledOnce();
 await user.keyboard(" ");expect(onOpen).toHaveBeenCalledTimes(2);
 await user.click(header);expect(onOpen).toHaveBeenCalledTimes(3);
 await user.click(screen.getByRole("button",{name:"Show more"}));expect(onOpen).toHaveBeenCalledTimes(3);
 view.rerender(<CapturedFileAnnotation reference={ref} onOpen={onOpen} opening/>);
 expect(screen.getByRole("button",{name:/^Open file:/})).toBeDisabled();
 fireEvent.click(pre);expect(onOpen).toHaveBeenCalledTimes(3);
});
