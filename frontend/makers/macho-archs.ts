import { closeSync, fstatSync, openSync, readSync } from "node:fs";

const FAT_MAGIC = 0xcafebabe;
const FAT_MAGIC_64 = 0xcafebabf;
const MH_MAGIC = 0xfeedface;
const MH_MAGIC_64 = 0xfeedfacf;
const CPU_TYPE_X86_64 = 0x01000007;
const CPU_TYPE_ARM64 = 0x0100000c;
const MAX_FAT_ARCHES = 16;
const MAX_HEADER_BYTES = 8 + MAX_FAT_ARCHES * 32;

export type MachOArch = "x86_64" | "arm64";

export class MachOParseError extends Error {
	constructor(
		message: string,
		readonly filePath: string,
	) {
		super(`${message}: ${filePath}`);
		this.name = "MachOParseError";
	}
}

export function machoArchs(filePath: string): MachOArch[] {
	let fd: number;
	try {
		fd = openSync(filePath, "r");
	} catch (error) {
		throw new MachOParseError(`cannot open (${(error as NodeJS.ErrnoException)?.code ?? error})`, filePath);
	}
	try {
		const stats = fstatSync(fd);
		if (!stats.isFile()) {
			throw new MachOParseError("not a regular file", filePath);
		}
		const header = Buffer.alloc(MAX_HEADER_BYTES);
		let total = 0;
		while (total < header.length) {
			const read = readSync(fd, header, total, header.length - total, total);
			if (read === 0) break;
			total += read;
		}
		if (total < 8) {
			throw new MachOParseError("truncated Mach-O header", filePath);
		}
		const magicBE = header.readUInt32BE(0);
		const magicLE = header.readUInt32LE(0);
		if (magicBE === FAT_MAGIC || magicBE === FAT_MAGIC_64) {
			const stride = magicBE === FAT_MAGIC_64 ? 32 : 20;
			const nfatArch = header.readUInt32BE(4);
			if (nfatArch === 0 || nfatArch > MAX_FAT_ARCHES) {
				throw new MachOParseError(`invalid fat header (nfat_arch ${nfatArch})`, filePath);
			}
			if (8 + nfatArch * stride > total) {
				throw new MachOParseError("truncated fat header", filePath);
			}
			const archs: MachOArch[] = [];
			for (let index = 0; index < nfatArch; index += 1) {
				const arch = classify(header.readUInt32BE(8 + index * stride), filePath);
				if (!archs.includes(arch)) archs.push(arch);
			}
			return archs;
		}
		if (magicLE === MH_MAGIC_64 || magicLE === MH_MAGIC) {
			return [classify(header.readUInt32LE(4), filePath)];
		}
		if (magicBE === MH_MAGIC_64 || magicBE === MH_MAGIC) {
			return [classify(header.readUInt32BE(4), filePath)];
		}
		throw new MachOParseError(`not a Mach-O image (magic 0x${magicBE.toString(16).padStart(8, "0")})`, filePath);
	} finally {
		closeSync(fd);
	}
}

export function machoHasX86_64Slice(filePath: string): boolean {
	return machoArchs(filePath).includes("x86_64");
}

function classify(cputype: number, filePath: string): MachOArch {
	if (cputype === CPU_TYPE_X86_64) return "x86_64";
	if (cputype === CPU_TYPE_ARM64) return "arm64";
	throw new MachOParseError(`unsupported cputype 0x${cputype.toString(16)}`, filePath);
}
