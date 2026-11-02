import { useTranslation } from "react-i18next";
import type { WorkspaceFileSummary } from "../hooks/useSessionWorkspaceFiles";
import { cn } from "../lib/utils";
import { WorkspaceEntryIcon } from "./WorkspaceEntryIcon";

/** Files listed in place; the rest are left to the full changes view. */
export const CHANGED_FILES_SHOWN = 8;

const statusMark: Record<WorkspaceFileSummary["status"], { letter: string; className: string; key: "added" | "modified" | "deleted" | "renamed" }> = {
	added: { letter: "A", className: "text-success", key: "added" },
	modified: { letter: "M", className: "text-status-in-review", key: "modified" },
	deleted: { letter: "D", className: "text-error", key: "deleted" },
	renamed: { letter: "R", className: "text-status-merged", key: "renamed" },
	unmodified: { letter: "M", className: "text-passive", key: "modified" },
};

/** +/− for a file or row, leaving out a side with nothing on it. */
export function ChangeCounts({ additions, deletions, className }: { additions: number; deletions: number; className?: string }) {
	if (additions === 0 && deletions === 0) return null;
	return (
		<span className={cn("inline-flex shrink-0 gap-1 font-mono tabular-nums", className)}>
			{additions > 0 ? <span className="text-success">+{additions}</span> : null}
			{deletions > 0 ? <span className="text-error">−{deletions}</span> : null}
		</span>
	);
}

/**
 * A branch row's files, opened in place under it: one flat line per file
 * (name, its folder dimmed, +/−, change letter), each opening its own diff.
 * "View all changes" opens the full changes view.
 */
export function ChangedFilesList({ files, id, onOpenAll, onOpenFile }: {
	files: WorkspaceFileSummary[];
	id: string;
	onOpenAll: () => void;
	/** Opens one file's diff; without it, a file opens the full view. */
	onOpenFile?: (path: string) => void;
}) {
	const { t } = useTranslation();
	const sorted = [...files].sort((a, b) => a.path.localeCompare(b.path));
	const shown = sorted.slice(0, CHANGED_FILES_SHOWN);
	const hidden = files.length - shown.length;
	const rowClass = "-mx-1.5 flex min-h-7 w-[calc(100%+0.75rem)] min-w-0 items-center gap-2 rounded-md pr-1.5 pl-[27px] text-left text-xs transition-colors hover:bg-interactive-hover focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring/60";
	return (
		<ul aria-label={t("inspector.git.changedFiles")} className="m-0 flex min-w-0 list-none flex-col p-0 pb-1" data-testid="changed-files-list" id={id}>
			{shown.map((file) => {
				const mark = statusMark[file.status] ?? statusMark.modified;
				const slash = file.path.lastIndexOf("/");
				const name = file.path.slice(slash + 1);
				const dir = slash < 0 ? "" : file.path.slice(0, slash);
				return (
					<li className="min-w-0" key={file.path}>
						<button
							className={rowClass}
							onClick={() => (onOpenFile ? onOpenFile(file.path) : onOpenAll())}
							title={file.previousPath ? `${file.previousPath} → ${file.path}` : file.path}
							type="button"
						>
							<WorkspaceEntryIcon className="size-icon-2xs" kind="file" name={name} />
							<span className="flex min-w-0 flex-1 items-baseline gap-1.5">
								<span className="min-w-0 truncate text-foreground">{name}</span>
								{/* The folder gives way first when the line is tight. */}
								{dir ? <span className="min-w-0 shrink-[100] truncate text-passive">{dir}</span> : null}
							</span>
							<ChangeCounts additions={file.additions ?? 0} deletions={file.deletions ?? 0} className="text-micro" />
							<span aria-hidden="true" className={cn("w-3 shrink-0 text-center font-mono text-micro font-semibold", mark.className)}>{mark.letter}</span>
							<span className="sr-only">, {t(`inspector.git.fileStatus.${mark.key}`)}</span>
						</button>
					</li>
				);
			})}
			<li className="min-w-0">
				<button className={cn(rowClass, "text-muted-foreground hover:text-foreground")} onClick={onOpenAll} type="button">
					{hidden > 0 ? t("inspector.git.viewAllChangesMore", { count: hidden }) : t("inspector.git.viewAllChanges")}
				</button>
			</li>
		</ul>
	);
}
