Pod::Spec.new do |s|
  s.name           = 'AOHardwareReturn'
  s.version        = '1.0.0'
  s.summary        = 'Return-to-send from a hardware keyboard for the AO chat composer'
  s.description    = s.summary
  s.license        = 'UNLICENSED'
  s.authors        = 'AO'
  s.homepage       = 'https://github.com/OrchestratorInc/agent-orchestrator'
  s.platforms      = { :ios => '16.4' }
  s.source         = { git: '' }

  s.dependency 'ExpoModulesCore'

  s.static_framework = true
  s.source_files = "**/*.{h,m,swift}"
end
