import assert from "node:assert/strict";
import { copyFileSync, mkdirSync, mkdtempSync, readFileSync, readdirSync, rmSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { spawnSync } from "node:child_process";
import { test } from "node:test";
import { fileURLToPath } from "node:url";

import { createMigration } from "./create-migration.mjs";

test("creates a UTC millisecond migration and preserves existing files", (t) => {
	const directory = mkdtempSync(join(tmpdir(), "ao-migrations-"));
	t.after(() => rmSync(directory, { recursive: true, force: true }));
	t.mock.timers.enable({ apis: ["Date"], now: Date.UTC(2026, 9, 13, 9, 30, 0, 123) });
	const oldMigration = join(directory, "0196_existing.sql");
	writeFileSync(oldMigration, "existing SQL");

	const filename = createMigration("add_reports", directory);
	assert.equal(filename, join(directory, "20261013093000123_add_reports.sql"));
	assert.equal(readFileSync(filename, "utf8"), "-- +goose Up\n\n-- +goose Down\n");
	assert.equal(readFileSync(oldMigration, "utf8"), "existing SQL");
	assert.equal(readdirSync(directory).length, 2);

	writeFileSync(filename, "SQL already written");
	assert.throws(() => createMigration("add_reports", directory), { code: "EEXIST" });
	assert.equal(readFileSync(filename, "utf8"), "SQL already written");
});

test("rejects invalid migration names before writing", () => {
	for (const name of [undefined, "", "../escape", "add-reports", "AddReports", "add reports"]) {
		assert.throws(() => createMigration(name), /Migration name must/);
	}
});

test("CLI creates a migration relative to its script, not the working directory", (t) => {
	const directory = mkdtempSync(join(tmpdir(), "ao-migration-cli-"));
	t.after(() => rmSync(directory, { recursive: true, force: true }));
	mkdirSync(join(directory, "scripts"));
	const migrations = join(directory, "backend/internal/storage/sqlite/migrations");
	mkdirSync(migrations, { recursive: true });
	copyFileSync(new URL("./create-migration.mjs", import.meta.url), join(directory, "scripts/create-migration.mjs"));

	const result = spawnSync(process.execPath, [join(directory, "scripts/create-migration.mjs"), "add_reports"], { cwd: tmpdir(), encoding: "utf8" });
	assert.equal(result.status, 0, result.stderr);
	assert.match(result.stdout, /Append its ID and filename to shippedMigrations/);
	const files = readdirSync(migrations);
	assert.equal(files.length, 1);
	assert.match(files[0], /^\d{17}_add_reports\.sql$/);
	assert.match(readFileSync(join(migrations, files[0]), "utf8"), /-- \+goose Up/);
});

test("CLI requires exactly one migration name", () => {
	const script = fileURLToPath(new URL("./create-migration.mjs", import.meta.url));
	for (const args of [[], ["add_reports", "extra"]]) {
		const result = spawnSync(process.execPath, [script, ...args], { encoding: "utf8" });
		assert.equal(result.status, 2);
		assert.match(result.stderr, /Usage: npm run migration:create/);
	}
});
