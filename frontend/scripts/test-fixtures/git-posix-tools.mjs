import { execFileSync } from "node:child_process";
import { isAbsolute, join, resolve } from "node:path";

const windows = process.platform === "win32";
// 从 Git 安装定位 POSIX 工具，避免 Windows PATH 命中 WSL 启动器。
export const gitPosixBin = windows
	? resolve(execFileSync("git", ["--exec-path"], { encoding: "utf8" }).trim(), "../../..", "usr", "bin")
	: "";
export const bash = windows ? join(gitPosixBin, "bash.exe") : "bash";
export const bashSearchPath = windows
	? `/usr/bin:/bin:${execFileSync(join(gitPosixBin, "cygpath.exe"), ["-u", "-p", process.env.PATH ?? ""], { encoding: "utf8" }).trim()}`
	: process.env.PATH;

/** Windows 绝对路径由 cygpath 转换；参数标记和相对路径原样传递。 */
export function bashPath(value) {
	if (!windows || !isAbsolute(value)) return value;
	return execFileSync(join(gitPosixBin, "cygpath.exe"), ["-u", "--", value], { encoding: "utf8" }).trim();
}

/** 统一 PATH 大小写和 POSIX 分隔符，供真实 Bash 与 GNU tar 共用。 */
export function bashEnv(overrides = {}) {
	const environment = Object.fromEntries(
		Object.entries({ ...process.env, ...overrides }).filter(([key]) => key.toUpperCase() !== "PATH"),
	);
	return { ...environment, PATH: overrides.PATH ?? bashSearchPath };
}
