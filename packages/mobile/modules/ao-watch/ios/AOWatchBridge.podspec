Pod::Spec.new do |s|
  s.name = 'AOWatchBridge'
  s.version = '1.0.0'
  s.summary = 'Phone-mediated AO Watch connectivity'
  s.description = s.summary
  s.license = { :type => 'MIT' }
  s.author = 'AO'
  s.homepage = 'https://github.com/ComposioHQ/agent-orchestrator'
  s.source = { :git => s.homepage }
  s.platforms = { :ios => '16.4' }
  s.swift_version = '5.9'
  s.static_framework = true
  s.dependency 'ExpoModulesCore'
  s.frameworks = 'WatchConnectivity'
  s.source_files = '**/*.swift'
end
