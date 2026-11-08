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

package packer

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewDefaultsToErofs(t *testing.T) {
	p, err := New("")
	require.NoError(t, err)
	assert.IsType(t, MkfsErofsPacker{}, p)
}

func TestNewErofs(t *testing.T) {
	p, err := New(TypeErofs)
	require.NoError(t, err)
	assert.IsType(t, MkfsErofsPacker{}, p)
}

func TestNewExt4(t *testing.T) {
	p, err := New(TypeExt4)
	require.NoError(t, err)
	assert.IsType(t, MkfsExt4Packer{}, p)
}

func TestNewUnknownType(t *testing.T) {
	_, err := New(Type("zfs"))
	assert.Error(t, err)
}
