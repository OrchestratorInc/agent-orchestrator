import assert from "node:assert/strict";
import { mkdtemp, readFile, rm, writeFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";
import test from "node:test";
import { runCommand } from "./audit_ao.mjs";

test("command timeout kills a same-group descendant after its leader exits on SIGTERM", { skip: process.platform !== "linux" }, async () => {
  const root = await mkdtemp(join(tmpdir(), "ao-timeout-descendant-"));
  const grandchildPath = join(root, "grandchild.mjs");
  const leaderPath = join(root, "leader.mjs");
  const childPidPath = join(root, "grandchild.pid");
  const leaderPidPath = join(root, "leader.pid");
  let descendantPid;
  try {
    await writeFile(grandchildPath, `import { writeFileSync } from "node:fs";
process.on("SIGTERM", () => {});
writeFileSync(${JSON.stringify(childPidPath)}, String(process.pid));
setInterval(() => {}, 1000);
`);
    await writeFile(leaderPath, `import { spawn } from "node:child_process";
import { existsSync, writeFileSync } from "node:fs";
writeFileSync(${JSON.stringify(leaderPidPath)}, String(process.pid));
spawn(process.execPath, [${JSON.stringify(grandchildPath)}], { stdio: "ignore" });
process.on("SIGTERM", () => process.exit(0));
const ready = setInterval(() => {
  if (existsSync(${JSON.stringify(childPidPath)})) { console.log("ready"); clearInterval(ready); }
}, 10);
setInterval(() => {}, 1000);
`);
    const result = await runCommand(process.execPath, [leaderPath], { timeoutMs: 1000, killGraceMs: 100 });
    assert.equal(result.timedOut, true);
    assert.match(result.stdout, /ready/, "fixture descendant initialized before timeout");
    descendantPid = Number(await readFile(childPidPath, "utf8"));
    await new Promise(resolve => setTimeout(resolve, 200));
    let state;
    try { state = (await readFile(`/proc/${descendantPid}/stat`, "utf8")).match(/^\d+ \(.*\) ([A-Z])/)[1]; }
    catch (error) { if (error.code !== "ENOENT") throw error; }
    assert.ok(state === undefined || state === "Z", `descendant remains alive in state ${state}`);
  } finally {
    // Keep even the red regression self-cleaning. Kill only recorded fixture PIDs.
    for (const [filename, processGroup] of [[childPidPath, false], [leaderPidPath, true]]) {
      try {
        const pid = Number(await readFile(filename, "utf8"));
        if (Number.isInteger(pid) && pid > 1) process.kill(processGroup ? -pid : pid, "SIGKILL");
      } catch (error) { if (!["ENOENT", "ESRCH"].includes(error.code)) throw error; }
    }
    await rm(root, { recursive: true, force: true });
  }
});
