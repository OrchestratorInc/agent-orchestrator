import { act, cleanup, fireEvent, render, screen } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { Timeline } from "./ChatWorkspace";
import { TooltipProvider } from "../ui/tooltip";
import { chatFixtureLongHistory } from "../../lib/chat-fixture";
import type { ConversationSnapshot } from "../../types/conversation";

const anchors = vi.hoisted(() => [] as Array<{ before: number; after: number; offset: number }>);
vi.mock("@tanstack/react-virtual", async (importOriginal) => {
	const actual = await importOriginal<typeof import("@tanstack/react-virtual")>();
	return {
		...actual,
		useVirtualizer: (options: Parameters<typeof actual.useVirtualizer<HTMLDivElement, HTMLDivElement>>[0]) =>
			actual.useVirtualizer({
				...options,
				scrollToFn: (offset, config, instance) => {
					const before = instance.scrollElement?.scrollTop ?? 0;
					if (!options.scrollToFn) throw new Error("Timeline must supply its scrollToFn");
					options.scrollToFn(offset, config, instance);
					anchors.push({ before, after: instance.scrollElement?.scrollTop ?? 0, offset });
				},
			}),
	};
});

let now: number;
let height: number;
let top: number;
let observers: Set<LabResizeObserver>;
let assignments: Array<{ before: number; after: number; target: number }>;

class LabResizeObserver {
	targets = new Set<Element>();
	constructor(public callback: ResizeObserverCallback) { observers.add(this); }
	observe(target: Element) { this.targets.add(target); }
	unobserve(target: Element) { this.targets.delete(target); }
	disconnect() { this.targets.clear(); }
}

beforeEach(() => {
	now = 100_000;
	height = 40_000;
	top = 0;
	anchors.length = 0;
	observers = new Set();
	assignments = [];
	vi.spyOn(performance, "now").mockImplementation(() => now);
	vi.stubGlobal("ResizeObserver", LabResizeObserver);
	const bounds = HTMLElement.prototype.getBoundingClientRect;
	vi.spyOn(HTMLElement.prototype, "getBoundingClientRect").mockImplementation(function (this: HTMLElement) {
		const index = this.dataset.index;
		if (index !== undefined) {
			const offset = Number(this.style.transform.match(/translateY\(([-\d.]+)px\)/)?.[1] ?? Number(index) * 618);
			return { ...bounds.call(this), top: 20 + offset - top, height: 600, width: 768 } as DOMRect;
		}
		return bounds.call(this);
	});
	const clientHeight = Object.getOwnPropertyDescriptor(Element.prototype, "clientHeight")!.get!;
	vi.spyOn(Element.prototype, "clientHeight", "get").mockImplementation(function (this: Element) {
		return this.getAttribute("role") === "log" ? 800 : clientHeight.call(this);
	});
	const scrollHeight = Object.getOwnPropertyDescriptor(Element.prototype, "scrollHeight")!.get!;
	vi.spyOn(Element.prototype, "scrollHeight", "get").mockImplementation(function (this: Element) {
		return this.getAttribute("role") === "log" ? height : scrollHeight.call(this);
	});
	const offsetHeight = Object.getOwnPropertyDescriptor(HTMLElement.prototype, "offsetHeight")!.get!;
	vi.spyOn(HTMLElement.prototype, "offsetHeight", "get").mockImplementation(function (this: HTMLElement) {
		return this.hasAttribute("data-index") ? 600 : Number.parseFloat(this.style.height) || offsetHeight.call(this);
	});
	const scrollTop = Object.getOwnPropertyDescriptor(Element.prototype, "scrollTop")!;
	vi.spyOn(Element.prototype, "scrollTop", "get").mockImplementation(function (this: Element) {
		return this.getAttribute("role") === "log" ? top : scrollTop.get!.call(this);
	});
	vi.spyOn(Element.prototype, "scrollTop", "set").mockImplementation(function (this: Element, value: number) {
		if (this.getAttribute("role") !== "log") {
			scrollTop.set!.call(this, value);
			return;
		}
		const before = top;
		top = Math.max(0, Math.min(height - 800, value));
		assignments.push({ before, after: top, target: value });
	});
});

afterEach(() => {
	cleanup();
	vi.restoreAllMocks();
	vi.unstubAllGlobals();
});

function timeline(snapshot: ConversationSnapshot, remoteHost: boolean) {
	return <TooltipProvider><Timeline
		snapshot={snapshot}
		remoteHost={remoteHost}
		draftScope={{ sessionId: snapshot.sessionId, incarnation: "scroll-lab" }}
		annotationNavigationRef={{ current: null }}
	/></TooltipProvider>;
}

function resize(log: HTMLElement, growth = 200) {
	height += growth;
	const content = log.firstElementChild!;
	let delivered = false;
	act(() => {
		for (const observer of observers) {
			if (!observer.targets.has(content)) continue;
			delivered = true;
			observer.callback([{ target: content, borderBoxSize: [{ blockSize: height, inlineSize: 768 }] } as unknown as ResizeObserverEntry], observer as unknown as ResizeObserver);
		}
	});
	expect(delivered).toBe(true);
}

describe.each([false, true])("shared Timeline scroll ownership (remoteHost=%s)", (remoteHost) => {
	it.each([false, true])("keeps repeated upward input authoritative when the compositor moves before wheel delivery=%s", (compositorFirst) => {
		render(timeline(chatFixtureLongHistory(60), remoteHost));
		const log = screen.getByRole("log");
		fireEvent.scroll(log);
		const previousAppTop = top;
		height += 800;
		top = height - 800;
		fireEvent.scroll(log);
		now += 2000;
		if (compositorFirst) top = previousAppTop;
		fireEvent.wheel(log, { deltaY: -800 });
		top = previousAppTop;
		fireEvent.wheel(log, { deltaY: -120 });
		fireEvent.scroll(log);
		now += 300;
		resize(log);
		expect(top).toBe(previousAppTop);
		expect(screen.getByRole("button", { name: "Jump to latest" })).toBeInTheDocument();
	});

	it("keeps reader scrollback through a real count-change no-op anchor outside the initial snap window", () => {
		const snapshot = chatFixtureLongHistory(60);
		const view = render(timeline(snapshot, remoteHost));
		const log = screen.getByRole("log");
		fireEvent.scroll(log);
		now += 2000;
		height += 800;
		fireEvent.wheel(log, { deltaY: -618 });
		top -= 618;
		const readerTop = top;
		anchors.length = 0;
		const removedTurn = snapshot.turns[0]!.id;
		height -= 618;
		view.rerender(timeline({
			...snapshot,
			turns: snapshot.turns.slice(1),
			items: snapshot.items.filter((item) => item.turnId !== removedTurn),
			latestSequence: snapshot.latestSequence + 1,
		}, remoteHost));
		expect(top).toBe(readerTop);
		expect(anchors.some((write) => write.offset === readerTop && write.before === readerTop && write.after === readerTop)).toBe(true);
		fireEvent.scroll(log);
		now += 300;
		resize(log);
		expect(top).toBe(readerTop);
		expect(screen.getByRole("button", { name: "Jump to latest" })).toBeInTheDocument();
	});

	it("does not let an initial no-op snap claim a later reader scroll", () => {
		const view = render(timeline(chatFixtureLongHistory(60), remoteHost));
		const log = screen.getByRole("log");
		fireEvent.scroll(log);
		height += 200;
		top = height - 800;
		assignments.length = 0;
		resize(log, 0);
		const readerTop = top;
		expect(assignments).toContainEqual({ before: readerTop, after: readerTop, target: height });
		height += 200;
		top = height - 800;
		fireEvent.scroll(log);
		fireEvent.wheel(log, { deltaY: -200 });
		top = readerTop;
		now += 300;
		fireEvent.scroll(log);
		now += 1000;
		resize(log);
		expect(top).toBe(readerTop);
		expect(screen.getByRole("button", { name: "Jump to latest" })).toBeInTheDocument();
		view.unmount();
	});

	it.each([
		{ key: "ArrowUp" }, { key: "PageUp" }, { key: "Home" }, { key: " ", shiftKey: true },
	])("yields to the $key key before a streaming resize", (key) => {
		render(timeline(chatFixtureLongHistory(60), remoteHost));
		const log = screen.getByRole("log");
		fireEvent.scroll(log);
		fireEvent.keyDown(log, key);
		top -= 120;
		const readerTop = top;
		resize(log);
		expect(top).toBe(readerTop);
		fireEvent.scroll(log);
		now += 2000;
		resize(log);
		expect(top).toBe(readerTop);
		expect(screen.getByRole("button", { name: "Jump to latest" })).toBeInTheDocument();
	});

	it("resumes bottom following after a nested gesture that did not move the viewport", () => {
		render(timeline(chatFixtureLongHistory(60), remoteHost));
		const log = screen.getByRole("log");
		fireEvent.scroll(log);
		now += 2000;
		const nested = log.querySelector("[data-index]")!;
		fireEvent.wheel(nested, { deltaY: -120 });
		const heldTop = top;
		resize(log);
		expect(top).toBe(heldTop);
		now += 300;
		resize(log);
		expect(top).toBe(height - 800);
		expect(screen.queryByRole("button", { name: "Jump to latest" })).not.toBeInTheDocument();
	});
});
