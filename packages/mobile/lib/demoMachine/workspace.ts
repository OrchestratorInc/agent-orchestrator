// Development builds only: a made-up worktree for each demo session, so the
// Files screens, the file viewer and the artifact viewer have something real
// to show (long files to scroll, diffs to expand, lines to select). Generated
// from the session id, so a session shows the same files every time.

type Status = "unmodified" | "modified" | "added";
type DemoFile = { path: string; content: string; status: Status; diff: string; additions: number; deletions: number };

const MODULES = ["requests", "queue", "store", "session", "cache", "router", "auth", "billing", "upload", "search"];
const FOLDERS = ["src/lib", "src/api", "src/ui", "src/store", "docs", "scripts"];

function hash(text: string): number {
	let h = 2166136261;
	for (let i = 0; i < text.length; i++) h = Math.imul(h ^ text.charCodeAt(i), 16777619);
	return h >>> 0;
}

function source(name: string, lines: number): string {
	const out = [`// ${name}: generated for the demo machine.`, `import { describe } from "./describe";`, ""];
	for (let fn = 0; out.length < lines; fn++) {
		out.push(`/** Step ${fn + 1} of the ${name} pipeline. */`);
		out.push(`export function ${name}Step${fn + 1}(input: string[], limit = ${10 + fn}): string[] {`);
		out.push(`\tconst seen = new Set<string>();`);
		out.push(`\tconst result: string[] = [];`);
		out.push(`\tfor (const item of input) {`);
		out.push(`\t\tif (seen.has(item) || result.length >= limit) continue;`);
		out.push(`\t\tseen.add(item);`);
		out.push(`\t\tresult.push(describe(item, ${fn}));`);
		out.push(`\t}`);
		out.push(`\treturn result;`);
		out.push(`}`);
		out.push("");
	}
	return out.slice(0, lines).join("\n");
}

/** A unified diff that changes every `every`th line of `after`'s functions. */
function diffFor(path: string, before: string, after: string): { diff: string; additions: number; deletions: number } {
	const a = before.split("\n");
	const b = after.split("\n");
	const out = [`diff --git a/${path} b/${path}`, `--- a/${path}`, `+++ b/${path}`];
	let additions = 0;
	let deletions = 0;
	let shown = 0;
	for (let i = 0; i < b.length; i++) {
		if (a[i] === b[i]) continue;
		// Hunks never overlap: each starts after the context the last one showed.
		const start = Math.max(shown, i - 3);
		const end = Math.min(b.length, i + 4);
		out.push(`@@ -${start + 1},${end - start} +${start + 1},${end - start} @@`);
		for (let j = start; j < end; j++) {
			if (a[j] === b[j]) out.push(` ${b[j]}`);
			else {
				if (a[j] !== undefined) { out.push(`-${a[j]}`); deletions++; }
				out.push(`+${b[j]}`);
				additions++;
			}
		}
		shown = end;
		i = end - 1;
	}
	return { diff: out.join("\n"), additions, deletions };
}

const workspaces = new Map<string, DemoFile[]>();

/** Every file in the session's worktree, changed ones first. */
export function demoWorkspace(sessionId: string): DemoFile[] {
	const cached = workspaces.get(sessionId);
	if (cached) return cached;
	const seed = hash(sessionId);
	const files: DemoFile[] = [];
	MODULES.forEach((name, index) => {
		const folder = FOLDERS[(seed + index) % FOLDERS.length];
		const path = folder === "docs" ? `docs/${name}.md` : `${folder}/${name}.ts`;
		const lines = 60 + ((seed >>> index) % 140);
		const content = folder === "docs"
			? Array.from({ length: Math.ceil(lines / 3) }, (_, n) => `## ${name} note ${n + 1}\n\nHow the ${name} module behaves on a slow link, part ${n + 1}.\n`).join("\n")
			: source(name, lines);
		const changeKind = (seed + index) % 4;
		if (changeKind === 0 && folder !== "docs") {
			const before = content.replace(/limit = (\d+)/g, (_m, n) => `limit = ${Number(n) + 5}`).replace(/seen\.add\(item\);/g, "seen.add(item.trim());");
			files.push({ path, content, status: "modified", ...diffFor(path, before, content) });
		} else if (changeKind === 1 && index % 3 === 0) {
			const diff = [`diff --git a/${path} b/${path}`, "new file mode 100644", "--- /dev/null", `+++ b/${path}`, `@@ -0,0 +1,${content.split("\n").length} @@`, ...content.split("\n").map((line) => `+${line}`)].join("\n");
			files.push({ path, content, status: "added", diff, additions: content.split("\n").length, deletions: 0 });
		} else {
			files.push({ path, content, status: "unmodified", diff: "", additions: 0, deletions: 0 });
		}
	});
	files.push({ path: "README.md", content: `# ${sessionId}\n\nA demo worktree. Nothing here is real.\n`, status: "unmodified", diff: "", additions: 0, deletions: 0 });
	files.push({ path: "package.json", content: JSON.stringify({ name: sessionId, version: "1.0.0", scripts: { test: "vitest run" } }, null, 2), status: "unmodified", diff: "", additions: 0, deletions: 0 });
	files.sort((x, y) => Number(x.status === "unmodified") - Number(y.status === "unmodified") || x.path.localeCompare(y.path));
	workspaces.set(sessionId, files);
	return files;
}

function summary(file: DemoFile) {
	return { path: file.path, status: file.status, additions: file.additions, deletions: file.deletions, binary: false, size: file.content.length, fileFingerprint: `fp-${hash(file.content)}` };
}

export function demoManifest(sessionId: string) {
	const changed = demoWorkspace(sessionId).filter((file) => file.status !== "unmodified").map(summary);
	return {
		sessionId,
		files: changed,
		sections: { committed: [], staged: [], unstaged: changed.filter((file) => file.status === "modified"), untracked: changed.filter((file) => file.status === "added"), uncommitted: changed },
		summary: { files: changed.length, additions: changed.reduce((n, f) => n + f.additions, 0), deletions: changed.reduce((n, f) => n + f.deletions, 0) },
		truncated: false,
		workspaceVersion: "demo-1",
		compareBaseRef: "main",
		compareMode: "base",
		ahead: 2,
		behind: 0,
		degraded: false,
	};
}

export function demoFile(sessionId: string, path: string) {
	const file = demoWorkspace(sessionId).find((entry) => entry.path === path);
	if (!file) return undefined;
	return { ...summary(file), deleted: false, content: file.content, contentTruncated: false, diff: file.diff, diffTruncated: false, workspaceVersion: "demo-1" };
}

export function demoTree(sessionId: string, dir: string) {
	const prefix = dir ? `${dir.replace(/\/$/, "")}/` : "";
	const entries = new Map<string, { name: string; path: string; type: "file" | "dir"; size?: number; binary?: boolean; hasChanges?: boolean; status?: Status }>();
	for (const file of demoWorkspace(sessionId)) {
		if (!file.path.startsWith(prefix)) continue;
		const rest = file.path.slice(prefix.length);
		const [head, ...tail] = rest.split("/");
		const path = `${prefix}${head}`;
		const changed = file.status !== "unmodified";
		if (tail.length) {
			const existing = entries.get(path);
			entries.set(path, { name: head, path, type: "dir", hasChanges: Boolean(existing?.hasChanges) || changed });
		} else {
			entries.set(path, { name: head, path, type: "file", size: file.content.length, binary: false, hasChanges: changed, status: file.status });
		}
	}
	const sorted = [...entries.values()].sort((a, b) => (a.type === b.type ? a.name.localeCompare(b.name) : a.type === "dir" ? -1 : 1));
	return { path: dir, entries: sorted, truncated: false };
}

export function demoSearch(sessionId: string, query: string) {
	const needle = query.toLowerCase();
	const results = demoWorkspace(sessionId)
		.filter((file) => file.path.toLowerCase().includes(needle))
		.map((file) => ({ path: file.path, status: file.status, binary: false, size: file.content.length }));
	return { results, truncated: false };
}

/** The session's artifacts: a report, a long note and a page. */
export function demoArtifacts(sessionId: string, updatedAt: string) {
	return [
		{ path: "report.md", name: "report.md", kind: "markdown", size: demoArtifactText(sessionId, "report.md")?.length ?? 0, updatedAt, registered: true },
		{ path: "notes/slow-links.md", name: "slow-links.md", kind: "markdown", size: demoArtifactText(sessionId, "notes/slow-links.md")?.length ?? 0, updatedAt },
		{ path: "summary.txt", name: "summary.txt", kind: "file", size: demoArtifactText(sessionId, "summary.txt")?.length ?? 0, updatedAt },
	];
}

export function demoArtifactText(sessionId: string, path: string): string | undefined {
	if (path === "report.md") {
		const sections = Array.from({ length: 12 }, (_, n) => `## Finding ${n + 1}\n\nOn a slow link the ${MODULES[n % MODULES.length]} module waited for a pause that never came. The fix reads the first change at once and later ones at a steady pace.\n\n- Before: frozen, then a block of text\n- After: a few updates a second\n`);
		return `# ${sessionId} report\n\n${sections.join("\n")}`;
	}
	if (path === "notes/slow-links.md") return Array.from({ length: 40 }, (_, n) => `${n + 1}. A slow link note, long enough to scroll.`).join("\n");
	if (path === "summary.txt") return `Session ${sessionId}\nAll checks passed.\n`;
	return undefined;
}
