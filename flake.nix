{
  description = "jcode-web: monitor and steer jcode sessions from the browser";

  inputs = {
    nixpkgs.url = "github:NixOS/nixpkgs/nixos-unstable";
    flake-utils.url = "github:numtide/flake-utils";
  };

  outputs = { self, nixpkgs, flake-utils }:
    flake-utils.lib.eachDefaultSystem (system:
      let
        pkgs = nixpkgs.legacyPackages.${system};
        version = self.shortRev or self.dirtyShortRev or "dev";

        web = pkgs.stdenvNoCC.mkDerivation (finalAttrs: {
          pname = "jcode-web-ui";
          inherit version;
          src = ./web;
          nativeBuildInputs = [ pkgs.nodejs pkgs.pnpm_10 pkgs.pnpmConfigHook ];
          pnpmDeps = pkgs.fetchPnpmDeps {
            inherit (finalAttrs) pname version src;
            pnpm = pkgs.pnpm_10;
            fetcherVersion = 4;
            hash = "sha256-D3EO/U9Hnf27FKREsrjUcbZhzTAdCwXX7LYLMoTiIFQ=";
          };
          buildPhase = ''
            runHook preBuild
            pnpm build
            runHook postBuild
          '';
          installPhase = ''
            runHook preInstall
            cp -r dist $out
            runHook postInstall
          '';
        });

        jcode-web = pkgs.buildGoModule {
          pname = "jcode-web";
          inherit version;
          src = pkgs.lib.cleanSourceWith {
            src = ./.;
            filter = path: _: !(pkgs.lib.hasInfix "/web/node_modules" path);
          };
          vendorHash = "sha256-zqR6bV5ECJNqgD91X2G14wOK4T9YybZoYiEgplP0UGI=";
          subPackages = [ "cmd/jcode-web" ];
          env.CGO_ENABLED = 0;
          ldflags = [ "-s" "-w" "-X main.version=${version}" ];
          preBuild = ''
            rm -rf web/dist
            cp -r ${web} web/dist
            chmod -R u+w web/dist
          '';
          meta = {
            description = "Web UI to monitor and control jcode sessions";
            mainProgram = "jcode-web";
          };
        };
      in
      {
        packages.default = jcode-web;
        packages.web = web;

        devShells.default = pkgs.mkShell {
          packages = with pkgs; [ go nodejs pnpm buf protoc-gen-go gnumake ];
        };
      });
}
