import { execFileSync } from "node:child_process";

const CONTENT_DIR = "content";

type Dates = { published: Date; modified: Date };

let fileDates: Map<string, Dates> | undefined;

/** The oldest and newest commit dates of every file under `content/`, from a single `git log`. */
function loadCommitDates(): Map<string, Dates> {
  const dates = new Map<string, Dates>();
  try {
    const log = execFileSync("git", ["log", "--relative", "--name-only", "--format=%x00%cI", "--", CONTENT_DIR], {
      encoding: "utf8",
      maxBuffer: 64 * 1024 * 1024,
      stdio: ["ignore", "pipe", "ignore"],
    });
    for (const entry of log.split("\0").slice(1)) {
      const [date = "", ...files] = entry.split("\n");
      for (const file of files) {
        if (!file) continue;
        // Commits are listed newest first, so the first date seen for a file
        // is its last change and the last date seen is when it was added.
        const known = dates.get(file);
        if (known) known.published = new Date(date);
        else dates.set(file, { published: new Date(date), modified: new Date(date) });
      }
    }
  } catch {
    // No git, or no history: every page falls back to the build time.
  }
  return dates;
}

/**
 * When an article's source file last changed, given its path inside `content/`
 * (e.g. `quickstart.ja.mdx`). Falls back to the build time when git has no
 * history for the file, so local builds and shallow clones still work.
 */
export function lastModified(contentPath: string): Date {
  return commitDates(contentPath).modified;
}

/** When an article's source file was added and last changed, with the same fallback. */
export function commitDates(contentPath: string): Dates {
  fileDates ??= loadCommitDates();
  const now = new Date();
  return fileDates.get(`${CONTENT_DIR}/${contentPath}`) ?? { published: now, modified: now };
}
