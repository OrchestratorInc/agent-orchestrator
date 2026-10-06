import { act, render, renderHook, screen } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import {
	CollapsibleBody,
	createSidebarDisclosureStore,
	EXPANDED_PROJECTS_STORAGE_KEY,
	useProjectExpanded,
	useSectionOpen,
} from "./sidebarDisclosure";

beforeEach(() => window.localStorage.clear());
afterEach(() => vi.restoreAllMocks());

describe("createSidebarDisclosureStore", () => {
	it("starts every project collapsed unless persisted, and persists each toggle", () => {
		window.localStorage.setItem(EXPANDED_PROJECTS_STORAGE_KEY, JSON.stringify(["a"]));
		const store = createSidebarDisclosureStore();
		expect(store.isProjectExpanded("a")).toBe(true);
		expect(store.isProjectExpanded("b")).toBe(false);

		store.toggleProject("b");
		store.toggleProject("a");
		expect(store.isProjectExpanded("b")).toBe(true);
		expect(store.isProjectExpanded("a")).toBe(false);
		expect(JSON.parse(window.localStorage.getItem(EXPANDED_PROJECTS_STORAGE_KEY) ?? "null")).toEqual(["b"]);
	});

	it("opens the project that owned the route's active session until the user collapses it, without persisting that default", () => {
		const store = createSidebarDisclosureStore("active");
		expect(store.isProjectExpanded("active")).toBe(true);
		expect(window.localStorage.getItem(EXPANDED_PROJECTS_STORAGE_KEY)).toBeNull();

		store.toggleProject("active");
		expect(store.isProjectExpanded("active")).toBe(false);
		store.toggleProject("active");
		expect(store.isProjectExpanded("active")).toBe(true);
	});

	it("keeps remote projects expanded by default and toggles them apart from local ones", () => {
		const store = createSidebarDisclosureStore();
		expect(store.isRemoteExpanded("host:p")).toBe(true);
		store.toggleRemote("host:p");
		expect(store.isRemoteExpanded("host:p")).toBe(false);
		expect(store.isProjectExpanded("p")).toBe(false);
	});

	it("re-renders only the subscribers whose own flag changed", () => {
		const store = createSidebarDisclosureStore();
		const renders = { a: 0, b: 0, pinned: 0 };
		const projectA = renderHook(() => (renders.a++, useProjectExpanded(store, "a", "a", false)));
		const projectB = renderHook(() => (renders.b++, useProjectExpanded(store, "b", "b", false)));
		const pinned = renderHook(() => (renders.pinned++, useSectionOpen(store, "pinned")));
		const initial = { ...renders };

		act(() => store.toggleProject("a"));
		expect(projectA.result.current).toBe(true);
		expect(renders).toEqual({ a: initial.a + 1, b: initial.b, pinned: initial.pinned });

		act(() => store.sectionToggles.pinned());
		expect(pinned.result.current).toBe(false);
		expect(projectB.result.current).toBe(false);
		expect(renders).toEqual({ a: initial.a + 1, b: initial.b, pinned: initial.pinned + 1 });
	});

	it("hands out one toggle identity per section for the store's lifetime", () => {
		const store = createSidebarDisclosureStore();
		const toggle = store.sectionToggles.projects;
		act(() => toggle());
		expect(store.sectionToggles.projects).toBe(toggle);
		expect(store.isSectionOpen("projects")).toBe(false);
	});
});

describe("CollapsibleBody", () => {
	it("paints open with no animation step when it is open on first render", () => {
		render(
			<CollapsibleBody open>
				<p>content</p>
			</CollapsibleBody>,
		);
		const body = screen.getByText("content").closest("[data-sidebar-collapse]");
		expect(body).toHaveAttribute("data-open", "true");
		expect(body).not.toHaveAttribute("inert");
		expect(body).not.toHaveAttribute("aria-hidden");
	});

	it("mounts nothing until it first opens", () => {
		const { rerender } = render(
			<CollapsibleBody open={false}>
				<p>content</p>
			</CollapsibleBody>,
		);
		expect(screen.queryByText("content")).not.toBeInTheDocument();
		rerender(
			<CollapsibleBody open>
				<p>content</p>
			</CollapsibleBody>,
		);
		expect(screen.getByText("content").closest("[data-sidebar-collapse]")).toHaveAttribute("data-open", "true");
	});

	it("stays mounted but inert and aria-hidden once closed, and reopens as the same node", () => {
		const { rerender } = render(
			<CollapsibleBody open>
				<p>content</p>
			</CollapsibleBody>,
		);
		const node = screen.getByText("content");
		rerender(
			<CollapsibleBody open={false}>
				<p>content</p>
			</CollapsibleBody>,
		);
		const body = node.closest("[data-sidebar-collapse]");
		expect(node).toBeInTheDocument();
		expect(body).toHaveAttribute("data-open", "false");
		expect(body).toHaveAttribute("inert");
		expect(body).toHaveAttribute("aria-hidden", "true");

		rerender(
			<CollapsibleBody open>
				<p>content</p>
			</CollapsibleBody>,
		);
		expect(screen.getByText("content")).toBe(node);
		expect(body).toHaveAttribute("data-open", "true");
	});
});
