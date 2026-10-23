import { describe, it } from "node:test";
import assert from "node:assert/strict";
import { mkdtempSync, rmSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import path from "node:path";
import { scrollPhaseBuildOptions, buildScrollPhase } from "./build-scroll-phase.mjs";
describe("native scroll phase addon build", () => {
  for (const [arch, cpu] of [["arm64", "arm64"], ["x64", "x86_64"]]) {
    it(`cross-compiles ${arch} without raising the desktop minimum OS`, () => {
      const { args, output } = scrollPhaseBuildOptions(arch, "/project/frontend", "/node/include");
      assert.ok(args.includes(`${cpu}-apple-macosx11.0`));
      assert.ok(args.includes("/node/include"));
      assert.equal(output, "/project/frontend/scroll-phase/ao-scroll-phase.node");
    });
  }
  it("rejects non-macOS and invalid architectures", () => {
    assert.throws(() => buildScrollPhase({ platform: "linux" }), /must be built on macOS/);
    assert.throws(() => scrollPhaseBuildOptions("universal"), /Unsupported/);
  });
  it("does not mask missing headers or a failed native compiler", () => {
    assert.throws(() => buildScrollPhase({ platform: "darwin", include: "/missing" }), /headers not found/);
    const include = mkdtempSync(path.join(tmpdir(), "ao-scroll-phase-headers-"));
    try {
      writeFileSync(path.join(include, "node_api.h"), "");
      assert.throws(() => buildScrollPhase({ platform: "darwin", arch: "arm64", include, ensureDirectory: () => {}, run: () => ({ status: 1 }) }), /compilation failed/);
    } finally {
      rmSync(include, { recursive: true, force: true });
    }
  });
});
