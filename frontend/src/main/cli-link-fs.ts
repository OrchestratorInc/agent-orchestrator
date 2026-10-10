import { spawn } from "node:child_process";
import { access, lstat, mkdir, readFile, readlink, realpath, rm, symlink, writeFile } from "node:fs/promises";
import path from "node:path";
import { constants } from "node:fs";
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
			await writeFile(value, text, { mode: 0o600 });
		},
		async remove(value) {
			await rm(value, { force: false });
		},
		symlink,
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
	return new Promise((resolve, reject) => {
		const child = spawn(executable, ["version"], {
			shell: false,
			env: { PATH: path.dirname(executable) },
			stdio: ["ignore", "pipe", "pipe"],
			timeout: 5000,
		});
		let stdout = "";
		let stderr = "";
		child.stdout.setEncoding("utf8");
		child.stderr.setEncoding("utf8");
		child.stdout.on("data", (chunk: string) => {
			stdout += chunk;
		});
		child.stderr.on("data", (chunk: string) => {
			stderr += chunk;
		});
		child.on("error", reject);
		child.on("close", (code) => {
			const line = stdout.trim().split("\n")[0]?.trim() ?? "";
			if (code !== 0 || !line) {
				reject(new Error(stderr.trim() || `The CLI at ${executable} did not report a version.`));
				return;
			}
			resolve(line);
		});
	});
}
