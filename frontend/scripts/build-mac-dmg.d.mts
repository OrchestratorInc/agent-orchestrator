// Type declarations for build-mac-dmg.mjs — a plain ESM module run directly by
// node (the release conductor calls it on a stapled app). makers/maker-dmg.ts
// imports it, and without this it fails typecheck (TS7016).
export declare const APP_NAME: string;
export declare const VOLUME_NAME: string;
export declare const APP_BUNDLE_NAME: string;
export declare const DMG_LAYOUT: {
	readonly window: { readonly width: number; readonly height: number };
	readonly iconSize: number;
	readonly iconTextSize: number;
	readonly app: { readonly x: number; readonly y: number };
	readonly applications: { readonly x: number; readonly y: number };
};
export declare const BACKGROUND_IMAGE: string;
export declare const VOLUME_ICON: string;
export declare function macDmgLayout(): Record<string, unknown>;
export declare function checkMacDmgLayout(dmg: string): Promise<void>;
