import { useLayoutEffect, useRef, useState, useSyncExternalStore, type ReactNode } from "react";
import { cn } from "../lib/utils";

/** Disclosure state for the sidebar: which projects are expanded and which
 *  sections are open. It lives outside React state on purpose. Each consumer
 *  subscribes to the one boolean it renders, so toggling a project re-renders
 *  that project (and its session list), and toggling a section re-renders that
 *  section's header and body, never the Sidebar or the sibling rows. */
export const EXPANDED_PROJECTS_STORAGE_KEY = "ao.sidebar.expanded-projects";

export type SidebarSection = "pinned" | "projects" | "scratchpad";

function readExpandedProjectIds(): ReadonlySet<string> {
	if (typeof window === "undefined" || !window.localStorage) return new Set();
	try {
		const value: unknown = JSON.parse(window.localStorage.getItem(EXPANDED_PROJECTS_STORAGE_KEY) ?? "null");
		return new Set(Array.isArray(value) ? value.filter((id): id is string => typeof id === "string") : []);
	} catch {
		return new Set();
	}
}

export type SidebarDisclosureStore = ReturnType<typeof createSidebarDisclosureStore>;

/** `initialActiveProjectId`: the project that owned the active session when the
 *  sidebar first mounted. It starts expanded without being persisted, until the
 *  user collapses it. An empty/missing persisted set means every other project
 *  starts collapsed. */
export function createSidebarDisclosureStore(initialActiveProjectId?: string) {
	let expanded = readExpandedProjectIds();
	let dismissedInitialActive: ReadonlySet<string> = new Set();
	let collapsedRemote: ReadonlySet<string> = new Set();
	let sections: Record<SidebarSection, boolean> = { pinned: true, projects: true, scratchpad: true };
	const listeners = new Set<() => void>();
	const emit = () => listeners.forEach((listener) => listener());

	const isProjectExpanded = (id: string) =>
		expanded.has(id) || (initialActiveProjectId === id && !dismissedInitialActive.has(id));
	const isRemoteExpanded = (projectKey: string) => !collapsedRemote.has(projectKey);

	const toggleProject = (id: string) => {
		const currentlyExpanded = isProjectExpanded(id);
		const next = new Set(expanded);
		if (currentlyExpanded) next.delete(id);
		else next.add(id);
		expanded = next;
		if (initialActiveProjectId === id) {
			const dismissed = new Set(dismissedInitialActive);
			if (currentlyExpanded) dismissed.add(id);
			else dismissed.delete(id);
			dismissedInitialActive = dismissed;
		}
		try {
			if (typeof window !== "undefined") window.localStorage?.setItem(EXPANDED_PROJECTS_STORAGE_KEY, JSON.stringify([...next]));
		} catch {
			/* storage is a convenience; the in-memory state still applies */
		}
		emit();
	};
	const toggleRemote = (projectKey: string) => {
		const next = new Set(collapsedRemote);
		if (next.has(projectKey)) next.delete(projectKey);
		else next.add(projectKey);
		collapsedRemote = next;
		emit();
	};
	// One identity per section for the life of the store, so handing a toggle to a
	// header never defeats memoization.
	const sectionToggles: Record<SidebarSection, () => void> = {
		pinned: () => toggleSection("pinned"),
		projects: () => toggleSection("projects"),
		scratchpad: () => toggleSection("scratchpad"),
	};
	function toggleSection(section: SidebarSection) {
		sections = { ...sections, [section]: !sections[section] };
		emit();
	}
	const subscribe = (listener: () => void) => {
		listeners.add(listener);
		return () => {
			listeners.delete(listener);
		};
	};
	return { isProjectExpanded, isRemoteExpanded, isSectionOpen: (section: SidebarSection) => sections[section], toggleProject, toggleRemote, sectionToggles, subscribe };
}

export function useProjectExpanded(store: SidebarDisclosureStore, id: string, projectKey: string, remote: boolean): boolean {
	const read = () => (remote ? store.isRemoteExpanded(projectKey) : store.isProjectExpanded(id));
	return useSyncExternalStore(store.subscribe, read, read);
}

export function useSectionOpen(store: SidebarDisclosureStore, section: SidebarSection): boolean {
	const read = () => store.isSectionOpen(section);
	return useSyncExternalStore(store.subscribe, read, read);
}

/** Open/close body for sections and project session lists, driven by a CSS
 *  grid-rows + opacity transition (see `.sidebar-collapse` in styles.css): no JS
 *  measurement, and no Motion layout. The content mounts the first time the body
 *  opens and then stays mounted, so re-opening never rebuilds every row. While
 *  closed it is `inert`, `aria-hidden`, `visibility: hidden` and
 *  `content-visibility: hidden`, so it cannot take focus and costs no layout or
 *  paint. A body that is open on first render paints open with no animation. */
export function CollapsibleBody({
	open,
	children,
	className,
	innerClassName,
	variant = "section",
}: {
	open: boolean;
	children: ReactNode;
	className?: string;
	innerClassName?: string;
	variant?: "section" | "project";
}) {
	const ref = useRef<HTMLDivElement>(null);
	const everOpenRef = useRef(open);
	if (open) everOpenRef.current = true;
	// False only when the first open happens after mount: the body paints closed
	// once so the transition has a starting point, then flips in a layout effect
	// (before paint).
	const [entered, setEntered] = useState(open);
	useLayoutEffect(() => {
		if (!open || entered) return;
		void ref.current?.offsetHeight;
		setEntered(true);
	}, [entered, open]);
	if (!everOpenRef.current) return null;
	const shown = open && entered;
	return (
		<div
			ref={ref}
			aria-hidden={shown ? undefined : true}
			className={cn("sidebar-collapse", variant === "project" && "sidebar-collapse--project", className)}
			data-open={shown ? "true" : "false"}
			data-sidebar-collapse=""
			inert={!shown}
		>
			<div className={cn("sidebar-collapse__inner", innerClassName)}>{children}</div>
		</div>
	);
}
