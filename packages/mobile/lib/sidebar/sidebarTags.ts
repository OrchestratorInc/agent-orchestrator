import type { SidebarTree } from "./sidebarModel";

/**
 * A tag the user makes on home's devices bar: a name and the projects and
 * sessions it gathers, so choosing it shows only those. Projects and sessions
 * are held by their sidebar keys (host and id), so a tag spans machines.
 */
export type SidebarTag = { id: string; name: string; projects: string[]; sessions: string[] };

/** The devices bar's choice for a tag: tag choices share the slot a device id takes. */
export const TAG_CHOICE_PREFIX = "tag:";

export function tagChoice(id: string): string {
	return `${TAG_CHOICE_PREFIX}${id}`;
}

/** The tag a devices bar choice names, if it names one that still exists. */
export function chosenTag(choice: string, tags: readonly SidebarTag[]): SidebarTag | undefined {
	if (!choice.startsWith(TAG_CHOICE_PREFIX)) return undefined;
	const id = choice.slice(TAG_CHOICE_PREFIX.length);
	return tags.find((tag) => tag.id === id);
}

/**
 * Home narrowed to a tag. A tagged project keeps its orchestrator and every
 * session; a project that is not tagged but holds a tagged session keeps
 * just those sessions; untagged standalone sessions leave. Pinned (the
 * drawer's) is left alone.
 */
export function applyTag(tree: SidebarTree, tag: SidebarTag): SidebarTree {
	const projects = new Set(tag.projects);
	const sessions = new Set(tag.sessions);
	return {
		pinned: tree.pinned,
		standalone: tree.standalone.filter((row) => sessions.has(row.key)),
		projects: tree.projects.flatMap((node) => {
			if (projects.has(node.key)) return [node];
			const own = node.sessions.filter((row) => sessions.has(row.key));
			return own.length ? [{ ...node, sessions: own }] : [];
		}),
	};
}

/** A tag as saved: a trimmed name, and no duplicates in what it gathers. */
export function normalizedTag(tag: SidebarTag): SidebarTag {
	return { id: tag.id, name: tag.name.trim(), projects: [...new Set(tag.projects)], sessions: [...new Set(tag.sessions)] };
}

/** A tag can be saved once it has a name and gathers something. */
export function canSaveTag(tag: Pick<SidebarTag, "name" | "projects" | "sessions">): boolean {
	return tag.name.trim().length > 0 && (tag.projects.length > 0 || tag.sessions.length > 0);
}

export const TAG_NAME_MAX = 24;
