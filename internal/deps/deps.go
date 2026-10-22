//go:build unio_deps

// Package deps pins the third-party modules approved for Unio v0.1 so that
// `go mod tidy` keeps them before every workstream imports them. Adding a
// dependency is a reviewed change: update this list and go.mod together.
package deps

import (
	_ "github.com/anishathalye/porcupine"
	_ "github.com/aws/aws-sdk-go-v2/aws"
	_ "github.com/aws/aws-sdk-go-v2/config"
	_ "github.com/aws/aws-sdk-go-v2/credentials"
	_ "github.com/aws/aws-sdk-go-v2/service/s3"
	_ "github.com/bytedance/sonic"
	_ "github.com/cloudwego/hertz/pkg/app/server"
	_ "github.com/cockroachdb/pebble/v2"
	_ "github.com/google/go-cmp/cmp"
	_ "github.com/hashicorp/raft"
	_ "github.com/klauspost/reedsolomon"
	_ "github.com/minio/crc64nvme"
	_ "github.com/planetscale/vtprotobuf/protohelpers"
	_ "github.com/prometheus/client_golang/prometheus"
	_ "github.com/spf13/cobra"
	_ "github.com/zeebo/xxh3"
	_ "go.uber.org/goleak"
	_ "go.yaml.in/yaml/v3"
	_ "golang.org/x/sync/errgroup"
	_ "golang.org/x/sys/unix"
	_ "google.golang.org/protobuf/proto"
)
