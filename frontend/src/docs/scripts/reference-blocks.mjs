import { execFileSync } from "node:child_process";
import { mkdirSync, rmSync, writeFileSync } from "node:fs";
import { dirname } from "node:path";
import { fileURLToPath, pathToFileURL } from "node:url";

export const referencesPath = fileURLToPath(new URL("../.generated/agent-harnesses.json", import.meta.url));
const backend = fileURLToPath(new URL("../../../../backend/", import.meta.url));
const marker = "/* docsref:shipped-harnesses */";

/** @typedef {{ id: string, terminal: boolean, chat: boolean, reviewer: boolean }} HarnessRow */

/** @param {HarnessRow[]} rows */
function validateRows(rows) {
  if (!Array.isArray(rows) || rows.length === 0) throw new Error("Docs references: no harness rows");
  for (const [i, row] of rows.entries()) {
    if (!row || Object.keys(row).sort().join(",") !== "chat,id,reviewer,terminal" ||
        typeof row.id !== "string" || !/^[a-z0-9]+(?:-[a-z0-9]+)*$/.test(row.id) ||
        [row.terminal, row.chat, row.reviewer].some((value) => typeof value !== "boolean") ||
        (i > 0 && rows[i - 1].id >= row.id)) {
      throw new Error(`Docs references: invalid, duplicate or unsorted harness row ${i + 1}`);
    }
  }
}

export function generateReferences(outputPath = referencesPath) {
  // A failed run must leave nothing a later build could mistake for fresh data.
  rmSync(outputPath, { force: true });
  try {
    const json = execFileSync("go", ["run", "-p", "1", "./cmd/docsref"], {
      cwd: backend, encoding: "utf8", env: { ...process.env, GOTOOLCHAIN: "auto" },
      maxBuffer: 1024 * 1024,
    });
    validateRows(JSON.parse(json));
    mkdirSync(dirname(outputPath), { recursive: true });
    writeFileSync(outputPath, json);
  } catch (cause) {
    throw new Error("Docs references could not be generated. Install Go (see backend/go.mod) and rerun npm run build. No cached table will be used.", { cause });
  }
}

/** @param {{ rows: HarnessRow[] }} options */
export function remarkReferenceBlocks({ rows }) {
  validateRows(rows);
  /** @type {import("unified").Transformer<import("mdast").Root>} */
  return (tree, file) => {
    for (let i = 0; i < tree.children.length; i++) {
      const node = tree.children[i];
      if (node.type !== "mdxFlowExpression" || !node.value.includes("docsref:")) continue;
      if (node.value.trim() !== marker) file.fail("Unknown docs reference marker", node);
      const table = tree.children[i + 1];
      if (table?.type !== "table" || table.children.length !== 1 || table.children[0].children.length !== 4) {
        file.fail("Shipped-harness marker needs a four-column table header with no authored rows", node);
      }
      table.children.push(...rows.map((row) => ({
        type: /** @type {const} */ ("tableRow"),
        children: [
          { type: /** @type {const} */ ("tableCell"), children: [{ type: /** @type {const} */ ("inlineCode"), value: row.id }] },
          ...[row.terminal, row.chat, row.reviewer].map((value) => ({
            type: /** @type {const} */ ("tableCell"),
            children: [{ type: /** @type {const} */ ("text"), value: value ? "Yes" : "No" }],
          })),
        ],
      })));
      tree.children.splice(i, 1);
    }
  };
}

if (process.argv[1] && import.meta.url === pathToFileURL(process.argv[1]).href) generateReferences();
