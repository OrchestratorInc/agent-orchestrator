require 'json'

package = JSON.parse(File.read(File.join(__dir__, 'package.json')))

Pod::Spec.new do |s|
  s.name         = 'AOImagePaste'
  s.version      = package['version']
  s.summary      = 'Paste images into the AO mobile chat composer.'
  s.license      = 'UNLICENSED'
  s.authors      = 'AO'
  s.homepage     = 'https://github.com/OrchestratorInc/agent-orchestrator'
  s.platforms    = { :ios => '15.1' }
  s.source       = { :path => '.' }
  s.source_files = 'ios/**/*.{h,m}'
  s.frameworks   = 'UniformTypeIdentifiers'
  s.dependency 'React-Core'
end
