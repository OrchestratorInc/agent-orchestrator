import { act, fireEvent, render, renderHook } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";
import {
	hasCodeSelection,
	useSelectionSnapshot,
} from "./SelectionStableContent";

function setup() {
	const container = document.createElement("div");
	const host = document.createElement("diffs-container");
	const root = host.attachShadow({ mode: "open" });
	root.innerHTML =
		'<div data-line="1" data-line-type="context">original text</div>';
	container.append(host);
	document.body.append(container);
	const text = root.firstChild!.firstChild!;
	let selected = false;
	Object.assign(root, {
		getSelection: () => ({
			isCollapsed: true,
			anchorNode: text,
			focusNode: text,
			anchorOffset: 0,
			focusOffset: selected ? 8 : 0,
		}),
	});
	return {
		ref: { current: container },
		host,
		text,
		select() {
			selected = true;
			fireEvent(document, new Event("selectionchange"));
		},
		clear() {
			selected = false;
			fireEvent(document, new Event("selectionchange"));
		},
	};
}

describe("selected content retention", () => {
	it("recognizes Electron shadow selections even when the outer selection is collapsed", () => {
		const h = setup();
		expect(hasCodeSelection(h.ref.current)).toBe(false);
		h.select();
		expect(window.getSelection()?.isCollapsed).toBe(true);
		expect(hasCodeSelection(h.ref.current)).toBe(true);
	});
	it("holds an in-flight refresh result until selection clears, then exposes the latest revision", () => {
		const h = setup();
		const first = { content: "original", revision: 1 };
		const hook = renderHook(
			({ value, identity }) => useSelectionSnapshot(h.ref, value, identity),
			{ initialProps: { value: first, identity: "file-a" } },
		);
		act(() => h.select());
		hook.rerender({
			value: { content: "new content", revision: 2 },
			identity: "file-a",
		});
		expect(hook.result.current).toBe(first);
		act(() => h.clear());
		expect(hook.result.current.content).toBe("new content");
	});
	it("does not freeze explicit navigation while an old range remains selected", () => {
		const h = setup();
		const hook = renderHook(
			({ identity }) => useSelectionSnapshot(h.ref, identity, identity),
			{ initialProps: { identity: "file-a" } },
		);
		act(() => h.select());
		hook.rerender({ identity: "file-b" });
		expect(hook.result.current).toBe("file-b");
	});
	it("retains only content while presentation and callbacks update", () => {
		const h = setup();
		const firstClick = vi.fn(),
			nextClick = vi.fn();
		function Surface({
			content,
			theme,
			onClick,
		}: {
			content: string;
			theme: string;
			onClick: () => void;
		}) {
			const retained = useSelectionSnapshot(h.ref, content, "file");
			return (
				<button data-theme={theme} onClick={onClick}>
					{retained}
				</button>
			);
		}
		const view = render(
			<Surface content="original" theme="light" onClick={firstClick} />,
		);
		act(() => h.select());
		view.rerender(<Surface content="new" theme="dark" onClick={nextClick} />);
		const button = view.getByRole("button");
		expect(button).toHaveTextContent("original");
		expect(button).toHaveAttribute("data-theme", "dark");
		fireEvent.click(button);
		expect(nextClick).toHaveBeenCalledOnce();
		expect(firstClick).not.toHaveBeenCalled();
		act(() => h.clear());
		expect(button).toHaveTextContent("new");
	});
});
