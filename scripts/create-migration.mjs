import { readdirSync, realpathSync, writeFileSync } from "node:fs";
import { join } from "node:path";
import { fileURLToPath } from "node:url";

export function createMigration(name, directory = fileURLToPath(new URL("../backend/internal/storage/sqlite/migrations/", import.meta.url))) {
	if (typeof name !== "string" || !/^[a-z][a-z0-9_]*$/.test(name)) {
		throw new Error("Migration name must use lowercase letters, numbers and underscores, starting with a letter.");
	}

	const version = new Date().toISOString().replace(/\D/g, "");
	// A second migration created in the same millisecond gets the same version.
	// goose refuses to start on a duplicate version, so report it here instead of
	// letting the failure surface as a daemon that cannot boot.
	const clash = readdirSync(directory).find((file) => file.startsWith(`${version}_`) && file !== `${version}_${name}.sql`);
	if (clash) {
		throw new Error(`Migration version ${version} is already claimed by ${clash}; rerun in a later millisecond so the version differs.`);
	}

	const filename = join(directory, `${version}_${name}.sql`);
	writeFileSync(filename, "-- +goose Up\n\n-- +goose Down\n", { flag: "wx" });
	return filename;
}

if (process.argv[1] && realpathSync(process.argv[1]) === fileURLToPath(import.meta.url)) {
	if (process.argv.length !== 3) {
		console.error("Usage: npm run migration:create -- <snake_case_name>");
		process.exitCode = 2;
	} else {
		try {
			console.log(`Created ${createMigration(process.argv[2])}`);
			console.log("Append its ID and filename to shippedMigrations in migrate_burned_versions_test.go.");
		} catch (error) {
			console.error(error.message);
			process.exitCode = 1;
		}
	}
}
