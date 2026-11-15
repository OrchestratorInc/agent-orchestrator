import { describe, it } from "node:test";
import assert from "node:assert/strict";
import { wakeHelperBuildOptions, buildWakeHelper } from "./build-wake-helper.mjs";
describe("native wake helper build", () => {
  for (const [arch, cpu] of [["arm64", "arm64"], ["x64", "x86_64"]]) {
    it(`cross-compiles ${arch} without raising the desktop minimum OS`, () => {
      const { args, output } = wakeHelperBuildOptions(arch, "/project/frontend");
      assert.ok(args.includes(`${cpu}-apple-macosx11.0`));
      for (const file of ["WakePolicy.swift", "WakeLowPower.swift", "WakeEffects.swift", "WakeLights.swift", "WakeBrowserActions.swift", "WakeNetworkActions.swift", "main.swift"]) {
        assert.ok(args.includes(`/project/frontend/native/wake-helper/${file}`));
      }
      assert.equal(output, "/project/frontend/wake-helper/ao-wake");
    });
  }
  it("rejects non-macOS and invalid architectures", () => {
    assert.throws(() => buildWakeHelper({ platform: "linux" }), /must be built on macOS/);
    assert.throws(() => wakeHelperBuildOptions("universal"), /Unsupported/);
  });
  it("does not mask a failed native compiler", () => {
    assert.throws(() => buildWakeHelper({ platform: "darwin", arch: "arm64", ensureDirectory: () => {}, run: () => ({ status: 1 }) }), /compilation failed/);
  });
});
