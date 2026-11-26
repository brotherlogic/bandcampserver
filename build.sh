#!/bin/sh
set -e

PROTO_IMPORT_PATH="../../../"
TMP_PROTO_DIR=""

if [ ! -f "${PROTO_IMPORT_PATH}github.com/brotherlogic/bandcamplib/proto/bandcamp.proto" ]; then
  BANDCAMPLIB_DIR=$(go list -m -f '{{.Dir}}' github.com/brotherlogic/bandcamplib)
  TMP_PROTO_DIR=$(mktemp -d)
  trap 'rm -rf "$TMP_PROTO_DIR"' EXIT
  mkdir -p "$TMP_PROTO_DIR/github.com/brotherlogic"
  ln -s "$BANDCAMPLIB_DIR" "$TMP_PROTO_DIR/github.com/brotherlogic/bandcamplib"
  PROTO_IMPORT_PATH="$TMP_PROTO_DIR"
fi

protoc --proto_path "$PROTO_IMPORT_PATH" -I=./proto --go_out ./proto --go_opt=paths=source_relative --go-grpc_out ./proto --go-grpc_opt paths=source_relative --go-grpc_opt=require_unimplemented_servers=false proto/bandcampserver.proto
