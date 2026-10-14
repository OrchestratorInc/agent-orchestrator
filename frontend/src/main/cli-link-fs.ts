import { execFile } from "node:child_process";
import { access, lstat, mkdir, readFile, readlink, realpath, rename, rm, symlink, writeFile } from "node:fs/promises";
import path from "node:path";
import { constants } from "node:fs";
import { randomUUID } from "node:crypto";
import { promisify } from "node:util";
import type { CliLinkIo } from "../shared/cli-link";

function missing(error: unknown): boolean {
	return error instanceof Error && "code" in error && (error as NodeJS.ErrnoException).code === "ENOENT";
}

export function nodeCliLinkIo(): CliLinkIo {
	return {
		resolve: (value) => path.resolve(value),
		parent: (value) => path.dirname(value),
		join: (directory, name) => path.join(directory, name),
		async lstat(value) {
			try {
				const stat = await lstat(value);
				return { symbolicLink: stat.isSymbolicLink() };
			} catch (error) {
				if (missing(error)) return null;
				throw error;
			}
		},
		readlink,
		async readText(value) {
			try {
				return await readFile(value, "utf8");
			} catch (error) {
				if (missing(error)) return null;
				throw error;
			}
		},
		async writeText(value, text) {
			await mkdir(path.dirname(value), { recursive: true });
			const staged = `${value}.${randomUUID()}.tmp`;
			try {
				await writeFile(staged, text, { mode: 0o600, flag: "wx" });
				await rename(staged, value);
			} finally {
				await rm(staged, { force: true }).catch(() => undefined);
			}
		},
		async remove(value) {
			await rm(value, { force: false });
		},
		symlink,
		rename,
		async canWrite(value) {
			try {
				await access(value, constants.W_OK);
				return true;
			} catch {
				return false;
			}
		},
		async canExecute(value) {
			try {
				await access(value, constants.X_OK);
				return true;
			} catch {
				return false;
			}
		},
		realpath: async (value) => {
			try {
				return await realpath(value);
			} catch {
				return null;
			}
		},
		version: (executable) => readCliVersion(executable),
	};
}

export function readCliVersion(executable: string): Promise<string> {
	return promisify(execFile)(executable, ["version"], {
		env: { PATH: path.dirname(executable) },
		timeout: 5000,
		maxBuffer: 1024 * 1024,
	}).then(({ stdout }) => {
		const line = stdout.trim().split("\n")[0]?.trim();
		if (!line) throw new Error(`The CLI at ${executable} did not report a version.`);
		return line;
	});
}
