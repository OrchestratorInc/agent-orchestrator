import ExpoModulesCore
import UIKit

public final class HardwareReturnModule: Module {
  public func definition() -> ModuleDefinition {
    Name("AOHardwareReturn")

    View(HardwareReturnView.self) {
      Events("onReturnKey")

      Prop("enabled", true) { (view, enabled: Bool) in
        view.enabled = enabled
      }
    }
  }
}

/// Wraps the chat composer's field so Return on a hardware keyboard sends.
///
/// A multiline `TextInput` only reports `"Enter"` from `onKeyPress`: no modifiers,
/// and no way to tell a hardware Return from the on-screen keyboard's, which must
/// keep inserting a newline. Key commands come only from hardware keyboards, and
/// UIKit collects them from every responder between the focused field and the
/// window, so this view sees them while the field it wraps has focus.
///
/// Shift+Return is deliberately not registered: it falls through to the field
/// and inserts a newline, as it does in the desktop composer.
final class HardwareReturnView: ExpoView {
  let onReturnKey = EventDispatcher()
  /// Whether Return is taken. When it is not, Return stays the field's and
  /// inserts a newline. The keyboard click stays off either way (see below).
  var enabled = true

  private lazy var returnCommands: [UIKeyCommand] = {
    let modifiers: [UIKeyModifierFlags] = [[], .alternate, .control, .command]
    return modifiers.map { flags in
      let command = UIKeyCommand(input: "\r", modifierFlags: flags, action: #selector(handleReturn(_:)))
      // Without this the focused text view may take Return first and insert a
      // newline before the command is consulted.
      command.wantsPriorityOverSystemBehavior = true
      return command
    }
  }()

  override var keyCommands: [UIKeyCommand]? {
    enabled ? returnCommands : nil
  }

  override func canPerformAction(_ action: Selector, withSender sender: Any?) -> Bool {
    guard action == #selector(handleReturn(_:)) else {
      return super.canPerformAction(action, withSender: sender)
    }
    // Declining here lets UIKit deliver Return normally. While an input method is
    // composing (Japanese, Chinese, Korean…), Return commits the composition; it
    // must not send a half-typed message.
    return enabled && !isComposingText()
  }

  @objc private func handleReturn(_ command: UIKeyCommand) {
    let flags = command.modifierFlags
    onReturnKey([
      "command": flags.contains(.command),
      "control": flags.contains(.control),
      "option": flags.contains(.alternate),
    ])
  }

  // MARK: Ctrl+Return is also iPadOS's keyboard click

  /// Keyboard-click recognizers this view has turned off, to hand back later.
  private var disabledClickRecognizers: [UIGestureRecognizer] = []

  /// iPadOS treats Ctrl+Return as a click on the focused view: the text view's
  /// context-menu interaction has a press recognizer that opens the edit menu as
  /// soon as the key goes down — before any key command runs — and the open menu
  /// then takes the keyboard, so whatever is typed next goes nowhere. In this
  /// field Ctrl+Return sends or steers, so that recognizer stays off for as long
  /// as the field is on screen here — whether or not Return is being taken at
  /// the moment, since the field can still have focus and keystrokes while the
  /// composer cannot send. Tapping and long-pressing the field use other recognizers
  /// and still open the menu.
  ///
  /// It is found by name, using public API only. If a later iPadOS renames it,
  /// nothing is turned off and Ctrl+Return opens the menu after sending again.
  private func syncKeyboardClick() {
    let wanted: [UIGestureRecognizer] = window != nil
      ? HardwareReturnView.textViews(in: self).flatMap { field in
        (field.gestureRecognizers ?? []).filter { recognizer in
          // Only ones this view turned off itself, or that are on now.
          (recognizer.isEnabled || disabledClickRecognizers.contains(recognizer))
            && (recognizer.name?.contains("keyPressClickDriver") ?? false)
        }
      }
      : []
    for recognizer in disabledClickRecognizers where !wanted.contains(recognizer) {
      recognizer.isEnabled = true
    }
    for recognizer in wanted where recognizer.isEnabled {
      recognizer.isEnabled = false
    }
    disabledClickRecognizers = wanted
  }

  // The field is mounted, and can be remounted, after this view; layout follows both.
  override func layoutSubviews() {
    super.layoutSubviews()
    syncKeyboardClick()
  }

  override func didMoveToWindow() {
    super.didMoveToWindow()
    syncKeyboardClick()
  }

  private static func textViews(in view: UIView) -> [UITextView] {
    if let field = view as? UITextView {
      return [field]
    }
    return view.subviews.flatMap { textViews(in: $0) }
  }

  private func isComposingText() -> Bool {
    guard let input = HardwareReturnView.firstResponder(in: self) as? UITextInput else {
      return false
    }
    return input.markedTextRange != nil
  }

  private static func firstResponder(in view: UIView) -> UIView? {
    if view.isFirstResponder {
      return view
    }
    for subview in view.subviews {
      if let found = firstResponder(in: subview) {
        return found
      }
    }
    return nil
  }
}
