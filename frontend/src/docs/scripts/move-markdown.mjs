import { cpSync, existsSync, rmSync } from "node:fs";
import { join } from "node:path";
import { fileURLToPath } from "node:url";

const out = fileURLToPath(new URL("../out", import.meta.url));
const markdown = join(out, "markdown");

if (!existsSync(markdown)) throw new Error("No out/markdown/ to move. Run next build first.");
cpSync(markdown, out, { recursive: true });
rmSync(markdown, { recursive: true });
