const fs = require("node:fs");
const path = require("node:path");
const { withDangerousMod } = require("@expo/config-plugins");

// The harness marks SwiftUI menus draw by asset name, sized and scaled for
// them (scripts/generate-harness-menu-icons.mjs writes these image sets).
const source = path.join(__dirname, "..", "assets", "icons", "harness");

function copyHarnessMenuIcons(platformProjectRoot, projectName) {
	const catalog = path.join(platformProjectRoot, projectName, "Images.xcassets");
	for (const set of fs.readdirSync(source).filter((name) => name.endsWith(".imageset"))) {
		const target = path.join(catalog, set);
		fs.mkdirSync(target, { recursive: true });
		for (const filename of fs.readdirSync(path.join(source, set))) {
			fs.copyFileSync(path.join(source, set, filename), path.join(target, filename));
		}
	}
}

function withHarnessMenuIcons(config) {
	return withDangerousMod(config, ["ios", (config) => {
		copyHarnessMenuIcons(config.modRequest.platformProjectRoot, config.modRequest.projectName);
		return config;
	}]);
}

module.exports = withHarnessMenuIcons;
module.exports.copyHarnessMenuIcons = copyHarnessMenuIcons;
