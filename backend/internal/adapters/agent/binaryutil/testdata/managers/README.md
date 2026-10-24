# Version-manager regression fixtures

The entry scripts are copied from upstream without functional changes:

- pyenv: https://github.com/pyenv/pyenv/blob/0effba62c4bbf8f8ca2ad8f0eabbfd8221cc7bc6/libexec/pyenv
- nodenv: https://github.com/nodenv/nodenv/blob/093570626794abb172ad3a3a897efb4fca3f5b93/libexec/nodenv

Their licenses are in LICENSE.pyenv and LICENSE.nodenv. The sibling shim fixtures
are output from the corresponding libexec/*-rehash generators with only the local
installation prefix replaced by @ROOT@. Tests substitute an isolated temporary
prefix. The asdf 0.16 dispatch is generated inline from its upstream format:
https://github.com/asdf-vm/asdf/blob/v0.16.0/internal/shims/shims.go

Offline tests inspect these real script bodies. Native integration tests also
use pinned full manager checkouts and an actual asdf 0.16 binary to generate and
execute shims against harmless version-command fixtures. Real npm/bun packages
are installed into isolated CI prefixes rather than copying application bundles
or native executables into this repository.