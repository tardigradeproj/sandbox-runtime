// Copyright 2026 The Tardigrade Authors.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

//go:build !linux

package network

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/tardigradeproj/sandbox-runtime/internal/cfg"
	v1 "github.com/tardigradeproj/sandbox-runtime/pkg/api/runtime/v1"
)

func TestAttachDetachNetworkUnimplementedOffLinux(t *testing.T) {
	n, err := New(&cfg.Settings{})
	require.NoError(t, err)

	_, err = n.AttachNetwork(context.Background(), &v1.AttachNetworkRequest{SandboxId: "sandbox-1"})
	assert.Equal(t, codes.Unimplemented, status.Code(err))

	_, err = n.DetachNetwork(context.Background(), &v1.DetachNetworkRequest{SandboxId: "sandbox-1"})
	assert.Equal(t, codes.Unimplemented, status.Code(err))
}
