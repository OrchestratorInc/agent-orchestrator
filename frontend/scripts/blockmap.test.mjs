// @vitest-environment node
import { describe, it, expect } from "vitest";
import { writeBlockmap } from "./blockmap.mjs";
import { mkdtempSync, writeFileSync, existsSync, readFileSync, statSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { createHash } from "node:crypto";

describe("writeBlockmap", () => {
	it("writes a gzip sidecar and returns the file's base64 sha512 + size", async () => {
		const dir = mkdtempSync(join(tmpdir(), "bm-"));
		const file = join(dir, "artifact.bin");
		const buf = Buffer.alloc(200_000);
		for (let i = 0; i < buf.length; i++) buf[i] = (i * 37) % 256;
		writeFileSync(file, buf);

		const { sha512, size } = await writeBlockmap(file);

		expect(size).toBe(buf.length);
		expect(sha512).toBe(createHash("sha512").update(buf).digest("base64"));
		expect(existsSync(`${file}.blockmap`)).toBe(true);
		const sidecar = readFileSync(`${file}.blockmap`);
		expect(sidecar.length).toBeGreaterThan(0);
		expect(sidecar[0]).toBe(0x1f);
		expect(sidecar[1]).toBe(0x8b);
		expect(statSync(`${file}.blockmap`).size).toBe(sidecar.length);
	});
});
