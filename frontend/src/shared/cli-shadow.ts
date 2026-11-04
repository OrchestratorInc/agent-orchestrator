// Answers one question for the desktop app: which ao does a shell outside AO
// run? AO pins its own ao inside the sessions and terminals it starts, but it
// cannot reach Terminal.app or an IDE terminal, and a stale ao cannot warn
// about itself. Pure and dependency-injected so it stays testable under vitest.

export type CliShadowStatus =
	| { kind: "match" }
	| { kind: "absent" }
	// The answer is not knowable: Windows, a custom daemon command, or a login
	// shell probe that failed. Never warn on a guess: a fallback PATH is not the
	// PATH the user's shell has.
	| { kind: "unknown" }
	| { kind: "shadowed"; shadowPath: string; shadowVersion: string | null; bundledPath: string };

export type CliShadowIo = {
	isExecutableFile(path: string): Promise<boolean>;
	realpath(path: string): Promise<string | null>;
	join(directory: string, name: string): string;
	version(executable: string): Promise<string>;
};

export type CliShadowRequest = {
	platform: NodeJS.Platform;
	// undefined when the login-shell probe failed.
	shellPath: string | undefined;
	// undefined when the app does not run a CLI binary of its own.
	bundledPath: string | undefined;
};

const CLI_NAME = "ao";

export async function inspectCliShadow(io: CliShadowIo, request: CliShadowRequest): Promise<CliShadowStatus> {
	const { platform, shellPath, bundledPath } = request;
	if (platform === "win32" || !shellPath || !bundledPath) return { kind: "unknown" };
	const bundledReal = await io.realpath(bundledPath);
	if (!bundledReal) return { kind: "unknown" };
	const directories = shellPath.split(":").filter((directory) => directory.startsWith("/"));
	for (const directory of directories) {
		const candidate = io.join(directory, CLI_NAME);
		if (!(await io.isExecutableFile(candidate))) continue;
		// Resolving both sides keeps a symlink to the app's binary, such as the
		// Developer Mode CLI link, from reading as a shadow.
		if ((await io.realpath(candidate)) === bundledReal) return { kind: "match" };
		return {
			kind: "shadowed",
			shadowPath: candidate,
			shadowVersion: await io.version(candidate).catch(() => null),
			bundledPath,
		};
	}
	return { kind: "absent" };
}
