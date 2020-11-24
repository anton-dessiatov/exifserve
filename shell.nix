let pkgs = import <nixpkgs> {};
in with pkgs; stdenv.mkDerivation {
  name = "exifserve-dev";

  hardeningDisable = [ "stackprotector" "fortify" ];

  buildInputs = [ go_1_15 ag exiftool ];
  shellHook = ''
  if [ -z "$EXIFSERVE_GOPATH_SET" ]; then
    export GOPATH="$(pwd)/.go"
    export GOBIN="$GOPATH/bin"
    mkdir -p "$GOBIN"
    export PATH="$GOPATH/bin":$PATH
    export GO111MODULE=on
    export EXIFSERVE_GOPATH_SET=1
  fi
  '';
}

