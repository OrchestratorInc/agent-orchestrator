{
  description = "agent-orchestrator development shell";

  inputs = {
    nixpkgs.url = "github:NixOS/nixpkgs/nixpkgs-unstable";
    flake-utils.url = "github:numtide/flake-utils";
  };

  outputs =
    {
      nixpkgs,
      flake-utils,
      ...
    }:
    flake-utils.lib.eachDefaultSystem (
      system:
      let
        pkgs = import nixpkgs { inherit system; };
        go = pkgs.go_1_25;
      in
      {
        devShells.default = pkgs.mkShell {
          buildInputs = [
            go
            pkgs.gotools
            pkgs.nodejs_24
            pkgs.pnpm_10
            pkgs.just
          ];

          shellHook = ''
            export GOROOT="${go}/share/go"
            export GOPATH="$PWD/.go"
            export GOBIN="$GOPATH/bin"
            export PNPM_HOME="$PWD/.pnpm"
            export PATH="$GOBIN:$PNPM_HOME:$PATH"
          '';
        };
      }
    );
}
