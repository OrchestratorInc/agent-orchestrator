import path from "node:path";
import { MakerBase, type MakerOptions } from "@electron-forge/maker-base";
import type { ForgePlatform } from "@electron-forge/shared-types";

export type MakerAppImageConfig = {
	appId?: string;
	productName?: string;
	icon?: string;
	protocols?: Array<{ name: string; schemes: string[] }>;
	appImage?: Record<string, unknown>;
};

export default class MakerAppImage extends MakerBase<MakerAppImageConfig> {
	name = "appimage";
	defaultPlatforms: ForgePlatform[] = ["linux"];

	isSupportedOnCurrentPlatform(): boolean {
		return true;
	}

	async make({ dir, targetArch, appName }: MakerOptions): Promise<string[]> {
		const { buildForge } = await import("app-builder-lib");
		const cfg = this.config ?? {};
		const output = path.join(path.dirname(path.resolve(dir)), "make");
		return buildForge(
			{ dir },
			{
				linux: [`appImage:${targetArch}`],
				config: {
					appId: cfg.appId,
					productName: cfg.productName ?? appName,
					protocols: cfg.protocols,
					directories: { output },
					toolsets: { appimage: "1.0.3" },
					publish: null,
					linux: {
						...(cfg.icon ? { icon: cfg.icon } : {}),
					},
					appImage: {
						...cfg.appImage,
					},
				},
			},
		);
	}
}

export { MakerAppImage };
