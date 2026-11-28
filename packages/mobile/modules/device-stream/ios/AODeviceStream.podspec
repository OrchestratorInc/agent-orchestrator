Pod::Spec.new do |s|
  s.name           = 'AODeviceStream'
  s.version        = '1.0.0'
  s.summary        = 'Native view of a simulator or emulator stream for the AO device screen'
  s.description    = s.summary
  s.license        = 'UNLICENSED'
  s.authors        = 'AO'
  s.homepage       = 'https://github.com/OrchestratorInc/agent-orchestrator'
  s.platforms      = { :ios => '16.4' }
  s.source         = { git: '' }

  s.dependency 'ExpoModulesCore'
  s.frameworks = 'AVFoundation', 'CoreMedia', 'VideoToolbox'

  s.static_framework = true
  s.source_files = "**/*.{h,m,swift}"
end
