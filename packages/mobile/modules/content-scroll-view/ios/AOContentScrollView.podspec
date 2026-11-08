Pod::Spec.new do |s|
  s.name           = 'AOContentScrollView'
  s.version        = '1.0.0'
  s.summary        = 'Names the scroll view a screen\'s navigation bar observes'
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
