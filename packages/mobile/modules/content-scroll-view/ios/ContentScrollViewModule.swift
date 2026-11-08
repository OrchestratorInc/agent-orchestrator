import ExpoModulesCore
import UIKit

public final class ContentScrollViewModule: Module {
  public func definition() -> ModuleDefinition {
    Name("AOContentScrollView")

    View(ContentScrollView.self) {
      Prop("enabled", false) { (view, enabled: Bool) in
        view.enabled = enabled
      }
    }
  }
}

/// Makes the scroll view it wraps the one its screen's navigation bar observes
/// at the top edge (`UIViewController.setContentScrollView(_:for:)`).
///
/// iOS 26 blurs content under a clear glass bar only on the scroll view the bar
/// observes. Left to itself, UIKit picks that view from the hierarchy when the
/// page appears. The chat mounts its list only once the conversation has
/// loaded, so on iOS 26 the bar found nothing to observe: the blur covered the
/// status bar alone, and the title sat over sharp text. iOS 27 finds the list
/// later on. Naming it here works on both.
final class ContentScrollView: ExpoView {
  /// Off, nothing is registered and any earlier registration is withdrawn.
  var enabled = false {
    didSet { sync() }
  }

  private weak var observed: UIScrollView?
  private weak var observer: UIViewController?

  override func didMoveToWindow() {
    super.didMoveToWindow()
    sync()
  }

  override func didAddSubview(_ subview: UIView) {
    super.didAddSubview(subview)
    sync()
  }

  override func layoutSubviews() {
    super.layoutSubviews()
    sync()
  }

  override func willRemoveSubview(_ subview: UIView) {
    super.willRemoveSubview(subview)
    if let observed, observed.isDescendant(of: subview) {
      release()
    }
  }

  override func didMoveToSuperview() {
    super.didMoveToSuperview()
    // Unmounted (and possibly recycled into another screen): give the
    // controller back to UIKit's own choice.
    if superview == nil {
      release()
    }
  }

  private func sync() {
    // A page pushed over this one takes it out of the window. The registration
    // stands until it is back, so it is not withdrawn and redone on every push.
    guard window != nil else { return }
    guard enabled, let scrollView = ContentScrollView.firstScrollView(in: self), let controller = owningController() else {
      release()
      return
    }
    if observed === scrollView && observer === controller {
      return
    }
    release()
    controller.setContentScrollView(scrollView, for: .top)
    observed = scrollView
    observer = controller
  }

  private func release() {
    // Only withdraw what this view set; someone else may have named another.
    if let controller = observer, let scrollView = observed, controller.contentScrollView(for: .top) === scrollView {
      controller.setContentScrollView(nil, for: .top)
    }
    observed = nil
    observer = nil
  }

  /// The screen's view controller: the nearest one up the responder chain.
  private func owningController() -> UIViewController? {
    var responder = next
    while let current = responder {
      if let controller = current as? UIViewController {
        return controller
      }
      responder = current.next
    }
    return nil
  }

  /// React Native's scroll view is a plain view that holds the `UIScrollView`;
  /// the first one found, depth first, is the list's own, not one inside a row.
  private static func firstScrollView(in view: UIView) -> UIScrollView? {
    for subview in view.subviews {
      if let scrollView = subview as? UIScrollView {
        return scrollView
      }
      if let found = firstScrollView(in: subview) {
        return found
      }
    }
    return nil
  }
}
