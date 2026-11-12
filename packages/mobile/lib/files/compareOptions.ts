import type { PickerOption } from "../NativePickerList";
import { relativeTime } from "../notificationView";
import type { WorkspaceBranch, WorkspaceFileSummary, WorkspaceHistory, WorkspaceManifest } from "./api";
import { plural, sumFiles } from "./model";
import { branchId, branchOfRow, changesLabel, normalizeSelection, scopesAvailable, shortSha, type FilesChanges, type FilesSelection } from "./selection";

// The rows of the Files screen's two picker sheets: which branch to compare,
// and which of its changes.

function age(iso: string | null | undefined, now: number): string {
	return iso ? relativeTime(iso, now) : "";
}

function counts(files: readonly Pick<WorkspaceFileSummary, "additions" | "deletions">[]): string {
	return totals(sumFiles(files));
}

function totals({ files, additions, deletions }: { files: number; additions: number; deletions: number }): string {
	if (!files) return "No changes";
	// A side with nothing on it says nothing; binary-only changes count no lines at all.
	const lines = [additions ? `+${additions}` : "", deletions ? `−${deletions}` : ""].filter(Boolean).join(" ");
	return lines ? `${plural(files, "file")} · ${lines}` : plural(files, "file");
}

/** The branch sheet's rows, in the daemon's order (the checked-out branch first, then newest first), narrowed by `query`. */
export function branchOptions(branches: readonly WorkspaceBranch[], query: string, now: number = Date.now()): PickerOption[] {
	const needle = query.trim().toLowerCase();
	const rows = needle
		? branches.filter((row) => [row.name, row.prTitle ?? "", row.prNumber ? `#${row.prNumber}` : ""].some((text) => text.toLowerCase().includes(needle)))
		: branches;
	return rows.map((row) => {
		const title = row.kind === "pull_request"
			? `PR #${row.prNumber ?? 0}${row.prTitle ? ` · ${row.prTitle}` : ""}`
			: row.detached ? "Detached HEAD" : row.name;
		const kind = row.checkedOut ? "Checked out"
			: row.kind === "pull_request" ? row.name
			: row.kind === "remote" ? "Remote"
			: row.isDefault ? "Default branch" : "";
		return { id: branchId(branchOfRow(row)), title, detail: [kind, age(row.updatedAt, now)].filter(Boolean).join(" · ") || undefined };
	});
}

/** The changes sheet's row id for a pick. */
export function changesId(changes: FilesChanges): string {
	if (changes.kind !== "commits") return changes.kind;
	return changes.sha ? `commit:${changes.sha}` : "commits";
}

/** The pick a changes sheet row names. */
export function changesOfId(id: string): FilesChanges {
	if (id.startsWith("commit:")) return { kind: "commits", sha: id.slice("commit:".length) };
	if (id === "commits") return { kind: "commits" };
	if (id === "uncommitted" || id === "staged" || id === "unstaged") return { kind: id };
	return { kind: "all" };
}

/**
 * The changes sheet's rows: on the checked-out branch its worktree's scopes,
 * each with what it holds (an empty one is left out unless it is the pick),
 * then the branch's commits. Another branch or a pull request has only commits.
 * `history` is undefined while it loads.
 */
export function changesOptions({ selection, manifest, history, base, now = Date.now() }: {
	selection: FilesSelection;
	manifest: Pick<WorkspaceManifest, "summary" | "sections" | "degraded" | "files" | "compareMode"> | null | undefined;
	history: Pick<WorkspaceHistory, "commits" | "view"> | undefined;
	base?: string;
	now?: number;
}): PickerOption[] {
	const pick = normalizeSelection(selection);
	const options: PickerOption[] = [];
	if (pick.branch.kind === "checked_out" && manifest) {
		const section = "Working tree";
		// The summary, as the Files screen shows it: the file list can be capped.
		options.push({ id: "all", title: changesLabel({ kind: "all" }, base), detail: totals(manifest.summary), section });
		if (scopesAvailable(manifest)) {
			const { sections } = manifest;
			const scopes: [FilesChanges["kind"], string, readonly WorkspaceFileSummary[]][] = [
				["uncommitted", "Uncommitted", sections.uncommitted ?? []],
				["staged", "Staged", sections.staged],
				["unstaged", "Unstaged", [...sections.unstaged, ...sections.untracked]],
			];
			for (const [kind, title, files] of scopes) {
				if (files.length || pick.changes.kind === kind) options.push({ id: kind, title, detail: counts(files), section });
			}
		}
	}
	if (!history) return options;
	const section = "Commits";
	// A default branch has no base to count from, so the daemon lists its newest commits.
	const recent = history.view.rangeMode === "recent";
	// With no base the daemon compares with HEAD and lists no commits as committed,
	// so the checked-out branch's whole range has nothing to read; its single commits still do.
	const noRange = pick.branch.kind === "checked_out" && manifest?.compareMode === "head_fallback";
	if (history.commits.length && !noRange) {
		options.push({
			id: "commits",
			title: recent ? `Latest ${plural(history.commits.length, "commit")}` : "All commits",
			detail: pick.branch.kind === "checked_out" && manifest ? counts(manifest.sections.committed) : plural(history.commits.length, "commit"),
			section,
		});
	}
	for (const commit of history.commits) {
		options.push({
			id: `commit:${commit.sha}`,
			title: commit.subject || shortSha(commit.sha),
			detail: [shortSha(commit.sha), age(commit.timestamp, now), commit.files.length ? counts(commit.files) : ""].filter(Boolean).join(" · "),
			section,
		});
	}
	return options;
}
