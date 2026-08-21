{
  pkgs,
  lib,
  config,
  ...
}:
{
  # https://devenv.sh/languages/
  languages = {
    go.enable = true;
    go.version = "1.26.5";
  };

  # Useful Go command-line development tools.
  packages = [
    pkgs.git
    pkgs.gomod2nix
    pkgs.gopls
    pkgs.goreleaser
  ];

  git-hooks.hooks = {
    # lint shell scripts
    shellcheck.enable = true;
    # lint go
    govet = {
      enable = true;
      pass_filenames = false;
    };
    gotest.enable = true;
    golangci-lint = {
      enable = true;
      pass_filenames = false;
    };
  };

  outputs =
    let
      name = "pipefitter";
      version = "1.0.0";
    in
    {
      app = import ./default.nix { inherit pkgs name version; };
    };
  # See full reference at https://devenv.sh/reference/options/
}
