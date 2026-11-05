import { createHash, generateKeyPairSync } from "node:crypto";
import { createRequire } from "node:module";
import { mkdtempSync, readFileSync, writeFileSync, utimesSync, mkdirSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { promisify } from "node:util";
import { generateMacV2Assets } from "../mac-differential-v2.mjs";
const require = createRequire(import.meta.url);
const archiveZip = promisify(require("cross-zip").zip);

/** 生成真实的新旧 ZIP，供发布校验和更新重建测试共用。 */
export async function macV2Fixture(arches = ["arm64", "x64"]) {
  const dir = mkdtempSync(join(tmpdir(), "ao-v2-fixture-"));
  const source = join(dir, "source");
  const old = join(dir, "old");
  mkdirSync(source); mkdirSync(old);
  const candidate = { version: "2.0.0", tag: "v2.0.0", commit: "a".repeat(40) };
  const baseline = { version: "1.0.0", tag: "v1.0.0", commit: "b".repeat(40) };
  const inputs = [];
  /** 异步 ZIP 避免删除新归档；Windows 使用独占源目录，防止内部时间戳目录撞名。 */
  const zip = async (bytes, destination) => {
    const payload = join(source, "fixture.bin");
    writeFileSync(payload, bytes);
    utimesSync(payload, new Date("2026-01-01T00:00:00Z"), new Date("2026-01-01T00:00:00Z"));
    await archiveZip(process.platform === "win32" ? source : payload, destination);
  };
  for (const arch of arches) {
    const previous = Buffer.alloc(512_000);
    for (let offset = 0; offset < previous.length; offset += 64) createHash("sha512").update(`${arch}:${offset}`).digest().copy(previous, offset);
    const target = Buffer.from(previous);
    for (let offset = 180_000; offset < 228_000; offset += 64) createHash("sha512").update(`patch:${offset}`).digest().copy(target, offset);
    const oldPath = join(old, `Agent.Orchestrator-darwin-${arch}-1.0.0.zip`);
    const zipPath = join(dir, `Agent.Orchestrator-darwin-${arch}-2.0.0.zip`);
    await zip(previous, oldPath); await zip(target, zipPath);
    inputs.push({ arch, zipPath, baseline: { identity: baseline, zipPath: oldPath } });
  }
  const { privateKey, publicKey } = generateKeyPairSync("ed25519");
  const issuedAt = new Date(Date.now() - 60_000).toISOString();
  const expiresAt = new Date(Date.now() + 48 * 60 * 60 * 1000).toISOString();
  const trustedKeys = { test: { publicKey: publicKey.export({ type: "spki", format: "pem" }).toString(),
    validFrom: new Date(Date.now() - 24 * 60 * 60 * 1000).toISOString(), validUntil: new Date(Date.now() + 7 * 24 * 60 * 60 * 1000).toISOString() } };
  const envelope = await generateMacV2Assets({ minimumClientVersion: "1.0.0", allow: true, dir, candidate, channel: "nightly", inputs, keyId: "test", privateKey, issuedAt, expiresAt });
  return { dir, inputs, candidate, baseline, envelope, trustedKeys, privateKey,
    target: arch => readFileSync(inputs.find(input => input.arch === arch).zipPath) };
}
