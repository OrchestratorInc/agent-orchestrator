import { execFileSync } from "node:child_process";

const CONTENT_DIR = "content";

type Dates = { published: Date; modified: Date };

let fileDates: Map<string, Dates> | undefined;

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
        const known = dates.get(file);
        if (known) known.published = new Date(date);
        else dates.set(file, { published: new Date(date), modified: new Date(date) });
      }
    }
  } catch {
  }
  return dates;
}

export function lastModified(contentPath: string): Date {
  return commitDates(contentPath).modified;
}

export function commitDates(contentPath: string): Dates {
  fileDates ??= loadCommitDates();
  const now = new Date();
  return fileDates.get(`${CONTENT_DIR}/${contentPath}`) ?? { published: now, modified: now };
}
