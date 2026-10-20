import { execFileSync } from "node:child_process";
import { expect, it } from "vitest";

it("forwards scoped authentication through the real Android Image wrapper for both thumbnail and viewer", () => {
	const output = execFileSync(process.execPath, ["-e", String.raw`
const fs = require("node:fs"), path = require("node:path"), Module = require("node:module");
const React = require("react"), Renderer = require("react-test-renderer");
const ts = require("typescript"), babel = require("@babel/core");
global.IS_REACT_ACT_ENVIRONMENT = true;
const imageFile = require.resolve("react-native/Libraries/Image/Image.android.js");
const timelineFile = path.resolve("lib/chat/ChatTimeline.tsx");
const attachmentsFile = path.resolve("lib/chat/messageAttachments.ts");
const originalLoad = Module._load;
let AndroidImage, attachmentHelpers;
const styles = { create: value => value };
const nativeModule = value => ({ __esModule: true, default: value });
Module._load = function(name, parent, main) {
 if (parent?.filename === imageFile) {
  if (name === "../StyleSheet/flattenStyle") return nativeModule(() => ({}));
  if (name === "../StyleSheet/StyleSheet") return nativeModule(styles);
  if (name === "./ImageAnalyticsTagContext") return nativeModule(React.createContext(null));
  if (name === "./ImageInjection") return { unstable_getImageComponentDecorator: () => null, useWrapRefWithImageAttachedCallbacks: value => value };
  if (name === "./ImageSourceUtils") return { getImageSourcesFromImageProps: props => props.source };
  if (name === "./ImageUtils") return { convertObjectFitToResizeMode: () => undefined };
  if (name === "./ImageViewNativeComponent") return nativeModule("NativeImage");
  if (name === "./NativeImageLoaderAndroid") return nativeModule({});
  if (name === "./resolveAssetSource") return nativeModule(value => value);
 }
 if (parent?.filename === timelineFile) {
  if (name === "react-native") return {
   Image: AndroidImage, Platform: { OS: "android" }, StyleSheet: styles,
   View: "View", Pressable: "Pressable", ScrollView: "ScrollView", Text: "Text",
   Modal: ({ visible, children }) => visible ? children : null,
   useWindowDimensions: () => ({ width: 390, height: 844 }),
  };
  if (name === "react-native-safe-area-context") return { useSafeAreaInsets: () => ({ top: 0 }) };
  if (name === "../ThemeProvider") return { useTheme: () => ({}), useThemedStyles: () => ({}) };
  if (name === "../haptics") return { haptics: { tap: () => {} } };
  if (name === "../icons") return { Feather: "Feather" };
  if (name === "./messageAttachments") return attachmentHelpers;
  if (name !== "react" && name !== "react/jsx-runtime") return new Proxy({}, { get: (_, key) => key === "__esModule" ? true : () => null });
 }
 return originalLoad.call(this, name, parent, main);
};
function compile(file, code) {
 const loaded = new Module(file, module); loaded.filename = file; loaded.paths = Module._nodeModulePaths(path.dirname(file));
 loaded._compile(code, file); return loaded.exports;
}
AndroidImage = compile(imageFile, babel.transformSync(fs.readFileSync(imageFile, "utf8"), {
 filename: imageFile, configFile: false, babelrc: false,
 plugins: ["babel-plugin-syntax-hermes-parser", ["@babel/plugin-transform-flow-strip-types", { all: true }], "@babel/plugin-transform-react-jsx", "@babel/plugin-transform-modules-commonjs"],
}).code).default;
attachmentHelpers = compile(attachmentsFile, ts.transpileModule(fs.readFileSync(attachmentsFile, "utf8"), { compilerOptions: { module: ts.ModuleKind.CommonJS } }).outputText);
const timeline = compile(timelineFile, ts.transpileModule(fs.readFileSync(timelineFile, "utf8"), {
 compilerOptions: { module: ts.ModuleKind.CommonJS, jsx: ts.JsxEmit.ReactJSX },
}).outputText + "\nexports.TestAttachment = StagedAttachment;");
const source = { uri: "http://qa:58928/api/v1/sessions/standalone-2/preview/files/.ao/attachments/attachment-test.png", headers: { Authorization: "Bearer qa-test", "X-AO-Expected-Host-ID": "qa-host-a" } };
let renderer;
React.act(() => { renderer = Renderer.create(React.createElement(timeline.TestAttachment, { name: "attachment-test.png", source, tileSize: 160 })); });
const thumbnail = renderer.root.findByType("NativeImage").props;
React.act(() => renderer.root.findByProps({ accessibilityLabel: "Open attachment-test.png" }).props.onPress());
const viewer = renderer.root.findAllByType("NativeImage")[1].props;
console.log(JSON.stringify([thumbnail, viewer].map(props => ({ headers: props.headers, uri: props.source[0].uri }))));
React.act(() => renderer.unmount());
`], { cwd: process.cwd(), encoding: "utf8", stdio: ["ignore", "pipe", "pipe"] });
	const expected = {
		headers: { Authorization: "Bearer qa-test", "X-AO-Expected-Host-ID": "qa-host-a" },
		uri: "http://qa:58928/api/v1/sessions/standalone-2/preview/files/.ao/attachments/attachment-test.png",
	};
	expect(JSON.parse(output)).toEqual([expected, expected]);
});
