const { withAppDelegate } = require("@expo/config-plugins");

// iOS 26 blurs the content scrolling under a clear glass bar with the soft
// scroll-edge effect. iOS 27 resolves a scroll view's `automatic` top edge to
// the hard one instead: a pale slab with a line under the bar. Every scroll
// view the app shows, React Native's and SwiftUI's alike, takes the soft edge
// when it reaches a window, and an edge later set back to `automatic` (as
// react-native-screens does for the scroll view it finds in a screen) is soft
// too. Hard and hidden edges are left as chosen.
//
// Installed on iOS 27 only: iOS 26 already resolves `automatic` to soft, so
// there the app keeps UIKit's own methods.

const MARKER = "// withIosSoftScrollEdge";

const EXTENSION = `
${MARKER}
private func ao_swizzle(_ cls: AnyClass, _ original: Selector, _ replacement: Selector) {
  guard let originalMethod = class_getInstanceMethod(cls, original),
    let replacementMethod = class_getInstanceMethod(cls, replacement) else { return }
  // The class may only inherit the original; add it rather than swap the superclass's.
  if class_addMethod(cls, original, method_getImplementation(replacementMethod), method_getTypeEncoding(replacementMethod)) {
    class_replaceMethod(cls, replacement, method_getImplementation(originalMethod), method_getTypeEncoding(originalMethod))
  } else {
    method_exchangeImplementations(originalMethod, replacementMethod)
  }
}

extension UIScrollView {
  static func adoptSoftTopEdge() {
    guard #available(iOS 27, *) else { return }
    ao_swizzle(UIScrollView.self, #selector(UIView.didMoveToWindow), #selector(UIScrollView.ao_softTopEdgeDidMoveToWindow))
    ao_swizzle(UIScrollEdgeEffect.self, #selector(setter: UIScrollEdgeEffect.style), #selector(UIScrollEdgeEffect.ao_setSoftStyle(_:)))
  }

  @objc func ao_softTopEdgeDidMoveToWindow() {
    ao_softTopEdgeDidMoveToWindow()
    if #available(iOS 26, *), window != nil, topEdgeEffect.style == .automatic {
      topEdgeEffect.style = .soft
    }
  }
}

@available(iOS 26, *)
extension UIScrollEdgeEffect {
  @objc func ao_setSoftStyle(_ style: UIScrollEdgeEffect.Style) {
    ao_setSoftStyle(style == .automatic ? .soft : style)
  }
}
`;

const RETURN_SUPER = "    return super.application(application, didFinishLaunchingWithOptions: launchOptions)";

function adoptSoftScrollEdge(contents) {
	if (contents.includes(MARKER)) return contents;
	if (!contents.includes(RETURN_SUPER)) {
		throw new Error("withIosSoftScrollEdge: AppDelegate.swift no longer matches Expo's template; update the plugin.");
	}
	return contents.replace(RETURN_SUPER, `    UIScrollView.adoptSoftTopEdge()\n\n${RETURN_SUPER}`) + EXTENSION;
}

function withIosSoftScrollEdge(config) {
	return withAppDelegate(config, (config) => {
		if (config.modResults.language !== "swift") {
			throw new Error("withIosSoftScrollEdge: expected a Swift AppDelegate.");
		}
		config.modResults.contents = adoptSoftScrollEdge(config.modResults.contents);
		return config;
	});
}

module.exports = withIosSoftScrollEdge;
module.exports.adoptSoftScrollEdge = adoptSoftScrollEdge;
