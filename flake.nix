{
  description = "K6 builder";

  inputs = {
    nixpkgs.url = "github:nixos/nixpkgs?ref=nixos-unstable";
    flake-utils.url = "github:numtide/flake-utils";
  };

  outputs =
    {
      self,
      nixpkgs,
      flake-utils,
      ...
    }:
    flake-utils.lib.eachDefaultSystem (
      system:
      let
        pkgs = import nixpkgs {
          inherit system;
        };
        xk6 = pkgs.buildGoModule rec {
          pname = "xk6";
          version = "0.20.1";

          vendorHash = null;
          CGO_ENABLED = 0;
          GOFLAGS = [
            "-ldflags=-w"
            "-ldflags=-s"
          ];

          doCheck = false;

          nativeBuildInputs = [
            pkgs.makeWrapper
            pkgs.go
          ];

          disallowedRequisites = [ ];
          allowGoReference = true;

          postInstall = ''
            wrapProgram $out/bin/xk6 --prefix PATH : ${pkgs.lib.makeBinPath [ pkgs.go ]}
          '';

          src = pkgs.fetchFromGitHub {
            owner = "grafana";
            repo = "xk6";
            rev = "v${version}";
            sha256 = "sha256-RVuHGvoXQ9tAUEYTGK03Qjkgl3WCbFqbqt2rkPP6MYs=";
          };

          subPackages = [ "./cmd/xk6" ];
        };
      in
      {
        packages = {
          inherit xk6;
          default = xk6;
        };
        devShells.default =
          with pkgs;
          mkShell {
            buildInputs = [
              self.packages.${system}.xk6
              go
              gzip
            ];
            GOPRIVATE = "github.com/jeffutter/xk6-mutlipart-http";
          };
      }
    );
}
