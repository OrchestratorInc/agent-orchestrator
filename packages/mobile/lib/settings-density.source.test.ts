import { readFileSync } from "node:fs";
import { fileURLToPath } from "node:url";
import { describe, expect, it } from "vitest";

const read = (path: string) => readFileSync(fileURLToPath(new URL(path, import.meta.url)), "utf8");

const settings = read("../app/settings/index.tsx");

describe("settings screen", () => {
	it("renders from the already-resolved app config without a second storage loader", () => {
		expect(settings).toMatch(/const \{ config,[^}]*reloadConfig \} = useApp\(\);/);
		expect(settings).not.toMatch(/\bloadConfig\(\)/);
	});
});
