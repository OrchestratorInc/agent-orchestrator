// Static image imports (Metro resolves these to an asset reference at runtime,
// which React Native's <Image source> accepts as a number).
declare module "*.png" {
	const content: number;
	export default content;
}

// A script the app hands to a WebView, shipped as an asset (see metro.config.js).
declare module "*.webjs" {
	const content: number;
	export default content;
}
