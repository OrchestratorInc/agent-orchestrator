import { mkdtemp, readdir, writeFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import path from "node:path";
import { beforeEach, describe, expect, it, vi } from "vitest";

const buildForge = vi.fn<(forge: { dir: string }, options: any) => Promise<string[]>>(
	async () => ["/out/make/Agent-Orchestrator.AppImage"],
);
vi.mock("app-builder-lib", () => ({ buildForge }));

import MakerAppImage from "./maker-appimage";

const makeOptions = {
	dir: "/tmp/app/Agent Orchestrator-linux-x64",
	makeDir: "/tmp/app/make",
	appName: "Agent Orchestrator",
	targetPlatform: "linux" as const,
	targetArch: "x64" as const,
	forgeConfig: {} as never,
	packageJSON: {},
};

beforeEach(() => {
	buildForge.mockClear();
});

describe("MakerAppImage", () => {
	it("targets Linux and is supported for cross-builds", () => {
		const maker = new MakerAppImage();
		expect(maker.name).toBe("appimage");
		expect(maker.defaultPlatforms).toEqual(["linux"]);
		expect(maker.isSupportedOnCurrentPlatform()).toBe(true);
	});

	it("writes callback protocols into the AppImage desktop entry", async () => {
		const protocols = [
			{
				name: "Agent Orchestrator authentication callback",
				schemes: ["ao-app"],
			},
		];
		const maker = new MakerAppImage(
			{ appId: "dev.agent-orchestrator.desktop", protocols },
			["linux"],
		);
		await maker.prepareConfig(makeOptions.targetArch);
		await maker.make(makeOptions);

		const [, options] = buildForge.mock.calls[0];
		expect(options.linux).toEqual(["appImage:x64"]);
		expect(options.config.protocols).toEqual(protocols);
		expect(options.config.publish).toBeNull();
	});

	// Pinned exactly rather than asserted "not legacy": app-builder-lib treats null
	// the same as "0.0.0", so a looser check would stay green on a regression. See
	// the rationale in maker-appimage.ts.
	it("pins the appimage toolset so the artifact does not need libfuse2 (#4006)", async () => {
		const maker = new MakerAppImage({ appId: "dev.agent-orchestrator.desktop" }, ["linux"]);
		await maker.prepareConfig(makeOptions.targetArch);
		await maker.make(makeOptions);

		const [, options] = buildForge.mock.calls[0];
		expect(options.config.toolsets?.appimage).toBe("1.0.3");
	});

	it("wraps a single PNG so electron-builder emits the hicolor size set (#8836)", async () => {
		const root = await mkdtemp(path.join(tmpdir(), "ao-icon-"));
		const png = path.join(root, "icon.png");
		await writeFile(png, "png");
		const maker = new MakerAppImage(
			{ appId: "dev.agent-orchestrator.desktop", icon: png },
			["linux"],
		);
		await maker.prepareConfig(makeOptions.targetArch);
		await maker.make(makeOptions);

		const [, options] = buildForge.mock.calls[0];
		const iconDir = options.config.linux.icon as string;
		expect(iconDir).not.toBe(png);
		expect(await readdir(iconDir)).toEqual(["icon.png"]);
	});

	it("passes an icon directory through unchanged", async () => {
		const dir = await mkdtemp(path.join(tmpdir(), "ao-icons-"));
		const maker = new MakerAppImage(
			{ appId: "dev.agent-orchestrator.desktop", icon: dir },
			["linux"],
		);
		await maker.prepareConfig(makeOptions.targetArch);
		await maker.make(makeOptions);

		const [, options] = buildForge.mock.calls[0];
		expect(options.config.linux.icon).toBe(path.resolve(dir));
	});
});
