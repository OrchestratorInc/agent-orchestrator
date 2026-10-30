// Metro config for the mobile app inside this npm workspace.
//
// Standard Expo monorepo setup (watch the workspace root, resolve from both
// node_modules trees) - with one extra rule: force `react` and `react-native`
// to resolve to a single copy from the workspace root.
//
// Why: the root hoists react/react-native, and this package also has its own
// (older, incompatible) copies. If app code and react-native each load a
// different React, the app crashes at startup ("main has not been registered").
// Redirecting ONLY these two module trees guarantees a single instance while
// leaving every other package to resolve normally (a blanket
// disableHierarchicalLookup instead breaks nested deps -> runtime errors).
const { getDefaultConfig } = require("expo/metro-config");
const { getSentryExpoConfig } = require("@sentry/react-native/metro");
const path = require("path");

const projectRoot = __dirname;
const workspaceRoot = path.resolve(projectRoot, "../..");
const localModules = path.resolve(projectRoot, "node_modules");

const config = getSentryExpoConfig(projectRoot, { getDefaultConfig });

config.watchFolders = [workspaceRoot];
config.resolver.nodeModulesPaths = [localModules, path.resolve(workspaceRoot, "node_modules")];

const pinned = config.resolver.resolveRequest;
config.resolver.resolveRequest = (context, moduleName, platform) => {
	if (
		moduleName === "react" ||
		moduleName.startsWith("react/") ||
		moduleName === "react-native" ||
		moduleName.startsWith("react-native/")
	) {
		return context.resolveRequest(
			{ ...context, nodeModulesPaths: [localModules], disableHierarchicalLookup: true },
			moduleName,
			platform,
		);
	}
	return pinned ? pinned(context, moduleName, platform) : context.resolveRequest(context, moduleName, platform);
};

module.exports = config;
