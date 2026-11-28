import ExpoModulesCore

public final class DeviceStreamModule: Module {
  public func definition() -> ModuleDefinition {
    Name("AODeviceStream")

    View(DeviceStreamView.self) {
      Events("onState", "onSize", "onStats")

      Prop("mode") { (view: DeviceStreamView, value: String?) in
        view.mode = value ?? "h264"
      }
      Prop("url") { (view: DeviceStreamView, value: String?) in
        view.url = value
      }
      Prop("headers") { (view: DeviceStreamView, value: [String: String]?) in
        view.headers = value ?? [:]
      }
    }
  }
}
