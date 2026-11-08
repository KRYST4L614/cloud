#!/usr/bin/env bash
set -euo pipefail
cd "$(dirname "$0")/.."

# Generated files are committed; these tools are only needed after editing the API contract.
command -v protoc >/dev/null
tools_dir=$(mktemp -d)
trap 'rm -rf "$tools_dir"' EXIT
export GOBIN="$tools_dir"
go install google.golang.org/protobuf/cmd/protoc-gen-go@v1.36.12
go install google.golang.org/grpc/cmd/protoc-gen-go-grpc@v1.6.2
go install github.com/grpc-ecosystem/grpc-gateway/v2/protoc-gen-grpc-gateway@v2.27.3
go install github.com/grpc-ecosystem/grpc-gateway/v2/protoc-gen-openapiv2@v2.27.3
go install github.com/mikefarah/yq/v4@v4.45.1
export PATH="$tools_dir:$PATH"
mkdir -p gen/go
protoc -I proto \
  --go_out=gen/go --go_opt=paths=source_relative \
  --go-grpc_out=gen/go --go-grpc_opt=paths=source_relative \
  --grpc-gateway_out=gen/go --grpc-gateway_opt=paths=source_relative \
  --openapiv2_out="$tools_dir" \
  --openapiv2_opt=allow_merge=true,merge_file_name=openapi,json_names_for_fields=false,output_format=yaml,disable_default_errors=true,disable_default_responses=true \
  proto/cloud/v1/storage.proto

# Merge only HTTP adapter descriptions and schema shapes unsupported by proto options.
yq eval-all '. as $document ireduce ({}; . * $document)' \
  "$tools_dir/openapi.swagger.yaml" api/http.yaml > "$tools_dir/openapi.yaml"
mv "$tools_dir/openapi.yaml" api/openapi.yaml
