import { randomUUID } from "node:crypto";
import * as fs from "node:fs/promises";
import { tmpdir } from "node:os";
import nativePath from "node:path";
import { nodeCliLinkIo } from "../main/cli-link-fs";
import type { CliLinkIo } from "./cli-link";

const windows = process.platform === "win32";
export const path = windows ? nativePath.posix : nativePath;
const roots: string[] = [];
const files = new Map<string, { text?: string; target?: string; directory?: boolean; mode: number }>();
const versions = new Map<string, string>();
const missing = (file: string) => Object.assign(new Error(`Missing fixture: ${file}`), { code: "ENOENT" });
const target = (file: string) => files.get(file)?.target ?? file;

// Windows 仅模拟已有 POSIX IO 契约；Unix 仍使用真实文件、符号链接与隔离版本进程。
const posixIo: CliLinkIo = {
	resolve: path.resolve,
	parent: path.dirname,
	join: path.join,
	/** 区分链接和普通文件，保留未注册用户文件的保护判断。 */
	lstat: async (file) => files.has(file) ? { symbolicLink: Boolean(files.get(file)?.target) } : null,
	/** 读取链接自身的目标，悬空链接也保留目标字符串。 */
	readlink: async (file) => {
		const link = files.get(file)?.target;
		if (!link) throw missing(file);
		return link;
	},
	readText: async (file) => files.get(target(file))?.text ?? null,
	writeText: async (file, text) => {
		await mkdir(path.dirname(file));
		files.set(file, { text, mode: 0o600 });
	},
	/** 删除单个对象，找不到对象仍返回 IO 失败。 */
	remove: async (file) => {
		if (!files.delete(file)) throw missing(file);
		versions.delete(file);
	},
	/** 原子创建链接，已有用户对象时保留 EEXIST 失败。 */
	symlink: async (destination, file) => {
		if (files.has(file)) throw Object.assign(new Error("File exists"), { code: "EEXIST" });
		files.set(file, { target: destination, mode: 0o777 });
	},
	/** 仅提交已创建的暂存对象，支持旧链接替换与回滚验证。 */
	rename: async (from, to) => {
		const entry = files.get(from);
		if (!entry) throw missing(from);
		files.set(to, entry);
		files.delete(from);
	},
	canWrite: async (file) => Boolean((files.get(file)?.mode ?? 0) & 0o222),
	/** POSIX 执行位由夹具提供，目录不能充当命令。 */
	canExecute: async (file) => {
		const entry = files.get(target(file));
		return Boolean(entry && !entry.directory && (entry.mode & 0o111));
	},
	/** 真实目标缺失时返回 null，覆盖悬空链接的恢复路径。 */
	realpath: async (file) => files.has(target(file)) ? target(file) : null,
	/** 版本只来自该用例登记的命令，不为未知候选伪造成功。 */
	version: async (file) => {
		const version = versions.get(target(file));
		if (!version) throw missing(file);
		return version;
	},
};

/** Windows 注入 POSIX 策略边界，支持的平台继续走生产文件适配器。 */
export function fixtureCliLinkIo(): CliLinkIo {
	return windows ? posixIo : nodeCliLinkIo();
}

export const lstat = (file: string) => fixtureCliLinkIo().lstat(file);
export const readlink = (file: string) => fixtureCliLinkIo().readlink(file);
export const writeFile = (file: string, text: string) => fixtureCliLinkIo().writeText(file, text);
export const rm = (file: string) => fixtureCliLinkIo().remove(file);
export const symlink = (destination: string, file: string) => fixtureCliLinkIo().symlink(destination, file);

/** 保留文件不存在时的异常，让原有保护与回滚断言继续检查落盘结果。 */
export async function readFile(file: string, _encoding?: "utf8"): Promise<string> {
	const text = await fixtureCliLinkIo().readText(file);
	if (text === null) throw missing(file);
	return text;
}

/** 只在夹具中创建目录，不接触 Windows 全局命令目录。 */
export async function mkdir(directory: string): Promise<void> {
	if (!windows) { await fs.mkdir(directory, { recursive: true }); return; }
	for (let current = directory; current !== path.dirname(current); current = path.dirname(current)) {
		if (!files.has(current)) files.set(current, { directory: true, mode: 0o755 });
	}
}

/** 执行位仅属于 POSIX 夹具语义，Windows 不借此声明实际 ACL 权限。 */
export async function chmod(file: string, mode: number): Promise<void> {
	if (!windows) { await fs.chmod(file, mode); return; }
	const entry = files.get(file);
	if (!entry) throw missing(file);
	entry.mode = mode;
}

/** 每个用例使用独立目录；Windows 的 POSIX 目录只存在于内存中。 */
export async function layout() {
	const root = windows ? `/tmp/ao-cli-link-${randomUUID()}` : await fs.mkdtemp(path.join(tmpdir(), "ao-cli-link-"));
	roots.push(root);
	const linkDirectory = path.join(root, "bin");
	const dataDir = path.join(root, "data");
	const apps = path.join(root, "apps");
	await mkdir(linkDirectory);
	await mkdir(apps);
	return { root, linkDirectory, dataDir, apps };
}

/** Unix 脚本实际拒绝 HOME 泄露与错误参数；Windows 只注入版本 IO 的结果。 */
export async function executable(directory: string, name: string, version: string): Promise<string> {
	const file = path.join(directory, name);
	await mkdir(directory);
	await writeFile(file, `#!/bin/sh\nif [ -n "$HOME" ]; then exit 4; fi\nif [ "$1" != version ]; then exit 2; fi\nprintf '%s\\n' '${version}'\n`);
	await chmod(file, 0o755);
	versions.set(file, version);
	return file;
}

/** 清理仅涉及本次生成的夹具目录或内存，不清理用户数据。 */
export async function cleanupCliLinkFixtures(): Promise<void> {
	if (!windows) await Promise.all(roots.map((root) => fs.rm(root, { recursive: true, force: true })));
	roots.length = 0;
	files.clear();
	versions.clear();
}
