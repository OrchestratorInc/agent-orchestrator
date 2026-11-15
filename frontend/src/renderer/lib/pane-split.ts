export type SplitSide = "left" | "right" | "top" | "bottom";

export type SplitNode =
	| { type: "leaf"; key: string }
	| { type: "split"; dir: "row" | "column"; ratio: number; a: SplitNode; b: SplitNode };

export type TabGroup = { id: string; root: SplitNode; focus?: string };

export const SESSION_TAB_KEY = "session";
export const GROUP_KEY_PREFIX = "group:";
export const MIN_PANE_PX = 160;

export function groupTabKey(group: TabGroup): string {
	return `${GROUP_KEY_PREFIX}${group.id}`;
}

export function leafKeys(node: SplitNode): string[] {
	return node.type === "leaf" ? [node.key] : [...leafKeys(node.a), ...leafKeys(node.b)];
}

export function expandGroupTabOrder(order: readonly string[], groups: readonly TabGroup[]): string[] {
	const members = new Map(groups.map((group) => [groupTabKey(group), leafKeys(group.root)]));
	return order.flatMap((key) => members.get(key) ?? [key]).filter((key) => key !== SESSION_TAB_KEY);
}

export function groupContaining(groups: readonly TabGroup[], key: string): TabGroup | undefined {
	return groups.find((group) => leafKeys(group.root).includes(key));
}

export function removeLeaf(node: SplitNode, key: string): SplitNode | null {
	if (node.type === "leaf") return node.key === key ? null : node;
	const a = removeLeaf(node.a, key);
	const b = removeLeaf(node.b, key);
	if (!a) return b;
	if (!b) return a;
	return a === node.a && b === node.b ? node : { ...node, a, b };
}

export function insertBeside(node: SplitNode, target: string, key: string, side: SplitSide): SplitNode {
	if (node.type === "leaf") {
		if (node.key !== target) return node;
		const added: SplitNode = { type: "leaf", key };
		const first = side === "left" || side === "top";
		return {
			type: "split",
			dir: side === "left" || side === "right" ? "row" : "column",
			ratio: 0.5,
			a: first ? added : node,
			b: first ? node : added,
		};
	}
	const a = insertBeside(node.a, target, key, side);
	const b = insertBeside(node.b, target, key, side);
	return a === node.a && b === node.b ? node : { ...node, a, b };
}

export function setRatioAt(node: SplitNode, path: string, ratio: number, at = "r"): SplitNode {
	if (node.type === "leaf") return node;
	if (path === at) return node.ratio === ratio ? node : { ...node, ratio };
	if (!path.startsWith(at)) return node;
	const a = setRatioAt(node.a, path, ratio, `${at}a`);
	const b = setRatioAt(node.b, path, ratio, `${at}b`);
	return a === node.a && b === node.b ? node : { ...node, a, b };
}

function renameLeaf(node: SplitNode, rename: (key: string) => string | undefined): SplitNode {
	if (node.type === "leaf") {
		const next = rename(node.key);
		return next && next !== node.key ? { type: "leaf", key: next } : node;
	}
	const a = renameLeaf(node.a, rename);
	const b = renameLeaf(node.b, rename);
	return a === node.a && b === node.b ? node : { ...node, a, b };
}

export function reconcileGroups(
	groups: TabGroup[],
	available: ReadonlySet<string>,
	rename: (key: string) => string | undefined = () => undefined,
): TabGroup[] {
	let changed = false;
	const next: TabGroup[] = [];
	for (const original of groups) {
		const renamed = renameLeaf(original.root, rename);
		const focus = original.focus ? (rename(original.focus) ?? original.focus) : undefined;
		const group = renamed === original.root && focus === original.focus ? original : { ...original, root: renamed, focus };
		if (group !== original) changed = true;
		let root: SplitNode | null = group.root;
		for (const key of leafKeys(group.root)) {
			if (!available.has(key) && root) root = removeLeaf(root, key);
		}
		if (!root || root.type === "leaf") {
			changed = true;
			continue;
		}
		if (root !== group.root) changed = true;
		next.push(root === group.root ? group : { ...group, root });
	}
	return changed ? next : groups;
}

let groupSequence = 0;
export function newGroupId(): string {
	groupSequence += 1;
	return `${Date.now().toString(36)}-${groupSequence}`;
}

export function withoutTab(groups: TabGroup[], key: string): TabGroup[] {
	let changed = false;
	const next = groups.flatMap((group) => {
		if (!leafKeys(group.root).includes(key)) return [group];
		changed = true;
		const root = removeLeaf(group.root, key);
		return root?.type === "split" ? [{ ...group, root }] : [];
	});
	return changed ? next : groups;
}

export function placeInGroup(
	groups: TabGroup[],
	group: TabGroup,
	key: string,
	build: (root: SplitNode) => SplitNode,
): TabGroup[] {
	const latest = groups.find((candidate) => candidate.id === group.id) ?? group;
	const others = withoutTab(groups.filter((candidate) => candidate.id !== latest.id), key);
	const base = removeLeaf(latest.root, key) ?? latest.root;
	return [...others, { ...latest, root: build(base) }];
}

export function splitBeside(groups: TabGroup[], anchor: string, key: string, side: SplitSide): TabGroup[] {
	if (anchor === key) return groups;
	const group = groupContaining(groups, anchor);
	if (group) {
		return placeInGroup(groups, group, key, (root) => insertBeside(root, anchor, key, side)).map((candidate) =>
			candidate.id === group.id ? { ...candidate, focus: key } : candidate,
		);
	}
	return [...withoutTab(groups, key), { id: newGroupId(), root: insertBeside({ type: "leaf", key: anchor }, anchor, key, side), focus: key }];
}

export function ungroupById(groups: TabGroup[], groupId: string): TabGroup[] {
	const next = groups.filter((group) => group.id !== groupId);
	return next.length === groups.length ? groups : next;
}

export type Box = { x: number; y: number; w: number; h: number };
export type ClientRect = { left: number; top: number; width: number; height: number };

export function layoutBoxes(root: SplitNode): { leaves: Map<string, Box>; splits: Map<string, { node: Extract<SplitNode, { type: "split" }>; box: Box }> } {
	const leaves = new Map<string, Box>();
	const splits = new Map<string, { node: Extract<SplitNode, { type: "split" }>; box: Box }>();
	const walk = (node: SplitNode, box: Box, path: string) => {
		if (node.type === "leaf") {
			leaves.set(node.key, box);
			return;
		}
		splits.set(path, { node, box });
		if (node.dir === "row") {
			const w = box.w * node.ratio;
			walk(node.a, { ...box, w }, `${path}a`);
			walk(node.b, { ...box, x: box.x + w, w: box.w - w }, `${path}b`);
		} else {
			const h = box.h * node.ratio;
			walk(node.a, { ...box, h }, `${path}a`);
			walk(node.b, { ...box, y: box.y + h, h: box.h - h }, `${path}b`);
		}
	};
	walk(root, { x: 0, y: 0, w: 1, h: 1 }, "r");
	return { leaves, splits };
}

export function toClientRect(screen: ClientRect, box: Box): ClientRect {
	return {
		left: screen.left + box.x * screen.width,
		top: screen.top + box.y * screen.height,
		width: box.w * screen.width,
		height: box.h * screen.height,
	};
}

export function ratioVar(path: string): string {
	return `--split-${path}`;
}

export type CssBox = { left: string; top: string; width: string; height: string };
export type CssDivider = { path: string; dir: "row" | "column"; ratio: number; style: CssBox; box: Box };

export function layoutCss(root: SplitNode): { leaves: Map<string, CssBox>; dividers: CssDivider[]; vars: Record<string, number> } {
	const leaves = new Map<string, CssBox>();
	const dividers: CssDivider[] = [];
	const vars: Record<string, number> = {};
	const pct = (expr: string) => (expr === "0" ? "0%" : expr === "1" ? "100%" : `calc((${expr}) * 100%)`);
	const { splits } = layoutBoxes(root);
	const walk = (node: SplitNode, box: { x: string; y: string; w: string; h: string }, path: string) => {
		if (node.type === "leaf") {
			leaves.set(node.key, { left: pct(box.x), top: pct(box.y), width: pct(box.w), height: pct(box.h) });
			return;
		}
		const ratio = `var(${ratioVar(path)})`;
		vars[ratioVar(path)] = node.ratio;
		const numeric = splits.get(path)!.box;
		if (node.dir === "row") {
			const w = `(${box.w}) * ${ratio}`;
			const edge = `(${box.x}) + ${w}`;
			dividers.push({ path, dir: "row", ratio: node.ratio, box: numeric, style: { left: pct(edge), top: pct(box.y), width: "", height: pct(box.h) } });
			walk(node.a, { ...box, w }, `${path}a`);
			walk(node.b, { ...box, x: edge, w: `(${box.w}) * (1 - ${ratio})` }, `${path}b`);
		} else {
			const h = `(${box.h}) * ${ratio}`;
			const edge = `(${box.y}) + ${h}`;
			dividers.push({ path, dir: "column", ratio: node.ratio, box: numeric, style: { left: pct(box.x), top: pct(edge), width: pct(box.w), height: "" } });
			walk(node.a, { ...box, h }, `${path}a`);
			walk(node.b, { ...box, y: edge, h: `(${box.h}) * (1 - ${ratio})` }, `${path}b`);
		}
	};
	walk(root, { x: "0", y: "0", w: "1", h: "1" }, "r");
	return { leaves, dividers, vars };
}

export function edgeAt(rect: ClientRect, x: number, y: number): SplitSide | null {
	if (rect.width <= 0 || rect.height <= 0) return null;
	const nx = (x - rect.left) / rect.width;
	const ny = (y - rect.top) / rect.height;
	if (nx < 0 || nx > 1 || ny < 0 || ny > 1) return null;
	const dy = Math.min(ny, 1 - ny);
	const dx = Math.min(nx, 1 - nx);
	if (dy < 0.3 && dy < dx) return ny < 0.5 ? "top" : "bottom";
	return nx < 0.5 ? "left" : "right";
}

export function halfOf(rect: ClientRect, side: SplitSide): ClientRect {
	if (side === "left") return { ...rect, width: rect.width / 2 };
	if (side === "right") return { ...rect, left: rect.left + rect.width / 2, width: rect.width / 2 };
	if (side === "top") return { ...rect, height: rect.height / 2 };
	return { ...rect, top: rect.top + rect.height / 2, height: rect.height / 2 };
}

export function containsPoint(rect: ClientRect, x: number, y: number): boolean {
	return x >= rect.left && x <= rect.left + rect.width && y >= rect.top && y <= rect.top + rect.height;
}

export const MAX_STORED_SPLIT_SESSIONS = 50;

function parseNode(value: unknown, dropKey: (key: string) => boolean): SplitNode | null {
	if (!value || typeof value !== "object") return null;
	const node = value as Record<string, unknown>;
	if (node.type === "leaf") {
		return typeof node.key === "string" && node.key && !dropKey(node.key) ? { type: "leaf", key: node.key } : null;
	}
	if (node.type !== "split" || (node.dir !== "row" && node.dir !== "column")) return null;
	const a = parseNode(node.a, dropKey);
	const b = parseNode(node.b, dropKey);
	if (!a) return b;
	if (!b) return a;
	const ratio = typeof node.ratio === "number" && Number.isFinite(node.ratio) ? Math.min(0.95, Math.max(0.05, node.ratio)) : 0.5;
	return { type: "split", dir: node.dir, ratio, a, b };
}

export function parseStoredGroups(raw: string | null | undefined, dropKey: (key: string) => boolean = () => false): Record<string, TabGroup[]> {
	if (!raw) return {};
	let parsed: unknown;
	try {
		parsed = JSON.parse(raw);
	} catch {
		return {};
	}
	if (!parsed || typeof parsed !== "object" || Array.isArray(parsed)) return {};
	const result: Record<string, TabGroup[]> = {};
	for (const [sessionKey, value] of Object.entries(parsed as Record<string, unknown>)) {
		if (!Array.isArray(value)) continue;
		const seen = new Set<string>();
		const groups: TabGroup[] = [];
		for (const candidate of value) {
			if (!candidate || typeof candidate !== "object") continue;
			const group = candidate as Record<string, unknown>;
			if (typeof group.id !== "string" || !group.id) continue;
			let root = parseNode(group.root, dropKey);
			for (const key of root ? leafKeys(root) : []) {
				if (seen.has(key) && root) root = removeLeaf(root, key);
			}
			if (!root || root.type !== "split") continue;
			const keys = leafKeys(root);
			if (new Set(keys).size !== keys.length) continue;
			for (const key of keys) seen.add(key);
			const focus = typeof group.focus === "string" && keys.includes(group.focus) ? group.focus : undefined;
			groups.push(focus ? { id: group.id, root, focus } : { id: group.id, root });
		}
		if (groups.length) result[sessionKey] = groups;
	}
	return result;
}

export function trimStoredGroups(groupsBySession: Record<string, TabGroup[]>): Record<string, TabGroup[]> {
	const entries = Object.entries(groupsBySession).filter(([, groups]) => groups.length > 0);
	return Object.fromEntries(entries.slice(-MAX_STORED_SPLIT_SESSIONS));
}
