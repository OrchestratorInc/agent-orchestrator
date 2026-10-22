import { execFileSync } from "node:child_process";

const CONTENT_DIR = "content";

let commitDates: Map<string, Date> | undefined;

/** The newest commit date of every file under `content/`, from a single `git log`. */
function loadCommitDates(): Map<string, Date> {
  const dates = new Map<string, Date>();
  try {
    const log = execFileSync("git", ["log", "--relative", "--name-only", "--format=%x00%cI", "--", CONTENT_DIR], {
      encoding: "utf8",
      maxBuffer: 64 * 1024 * 1024,
      stdio: ["ignore", "pipe", "ignore"],
    });
    for (const entry of log.split("\0").slice(1)) {
      const [date = "", ...files] = entry.split("\n");
      for (const file of files) {
        // Commits are listed newest first, so the first date seen for a file is its last change.
        if (file && !dates.has(file)) dates.set(file, new Date(date));
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
  commitDates ??= loadCommitDates();
  return commitDates.get(`${CONTENT_DIR}/${contentPath}`) ?? new Date();
}
