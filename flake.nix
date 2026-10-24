{
  description = "rlm-agent: sandboxed multi-agent factory host";

  inputs.nixpkgs.url = "github:NixOS/nixpkgs/nixos-unstable";

  outputs = { self, nixpkgs }: {
    # Reusable module so the sandbox can be pulled into any host config.
    nixosModules.factory = ./nix/containers/factory.nix;

    # Minimal evaluation target: lets us assert on container options without
    # rebuilding an entire system closure.
    nixosConfigurations.factory-host = nixpkgs.lib.nixosSystem {
      system = "x86_64-linux";
      modules = [ self.nixosModules.factory ];
    };
  };
}
