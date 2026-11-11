import assert from "node:assert/strict";
import { createHash } from "node:crypto";
import { readFileSync, readdirSync } from "node:fs";
import { fileURLToPath } from "node:url";
import test from "node:test";

// Installed bots run ao-update, which downloads this manifest and each skill
// from the same commit and installs only when every sha256 matches. A skill
// edited without regenerating the manifest therefore ships an update every bot
// rejects, silently: the 2026-11-07 manifest kept ao-setup's pre-#8038 hash.
const directory = fileURLToPath(new URL(".", import.meta.url));
const manifest = JSON.parse(readFileSync(new URL("./manifest.json", import.meta.url), "utf8"));

test("the manifest lists exactly the shipped skills", () => {
	const shipped = readdirSync(new URL("./skills/", import.meta.url), { withFileTypes: true })
		.filter(entry => entry.isDirectory())
		.map(entry => entry.name)
		.sort();
	assert.deepEqual(manifest.skills.map(skill => skill.id), shipped);
});

test("every manifest hash and size matches the skill file", () => {
	for (const skill of manifest.skills) {
		const data = readFileSync(new URL(`./${skill.path}`, import.meta.url));
		assert.equal(data.length, skill.bytes, `${skill.id}: byte count is stale, run python3 make-manifest.py <version>`);
		assert.equal(createHash("sha256").update(data).digest("hex"), skill.sha256,
			`${skill.id}: sha256 is stale, run python3 make-manifest.py <version>`);
	}
});

test("the manifest lives where the skills document says", () => {
	assert.equal(manifest.name, "ao-grok-bot");
	assert.match(manifest.version, /^\d{4}\.\d{2}\.\d{2}\.\d+$/);
	for (const skill of manifest.skills) {
		assert.equal(skill.path, `skills/${skill.id}/SKILL.md`);
	}
});
