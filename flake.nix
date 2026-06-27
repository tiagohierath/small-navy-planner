{
  description = "planner — a terminal weekly planner / journal";

  inputs = {
    nixpkgs.url = "github:NixOS/nixpkgs/nixos-unstable";
    flake-utils.url = "github:numtide/flake-utils";
  };

  outputs = { self, nixpkgs, flake-utils }:
    flake-utils.lib.eachDefaultSystem (system:
      let
        pkgs = import nixpkgs { inherit system; };
      in
      {
        devShells.default = pkgs.mkShell {
          packages = with pkgs; [
            go
            gopls
            gotools
            go-tools
            tmux
          ];
        };

        packages.default = pkgs.buildGoModule {
          pname = "planner";
          version = "0.1.0";
          src = ./.;
          vendorHash = null;
        };
      });
}
