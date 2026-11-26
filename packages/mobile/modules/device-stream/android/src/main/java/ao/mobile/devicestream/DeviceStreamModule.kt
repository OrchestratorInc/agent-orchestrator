package ao.mobile.devicestream

import expo.modules.kotlin.modules.Module
import expo.modules.kotlin.modules.ModuleDefinition

class DeviceStreamModule : Module() {
  override fun definition() = ModuleDefinition {
    Name("AODeviceStream")

    View(DeviceStreamView::class) {
      Events("onState", "onSize", "onStats")

      Prop("mode") { view: DeviceStreamView, value: String? -> view.mode = value ?: "h264" }
      Prop("url") { view: DeviceStreamView, value: String? -> view.url = value }
      Prop("headers") { view: DeviceStreamView, value: Map<String, String>? -> view.headers = value ?: emptyMap() }
    }
  }
}
