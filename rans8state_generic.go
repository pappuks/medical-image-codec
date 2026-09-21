//go:build !amd64 && !arm64

// Copyright 2024 Kuldeep Singh
// This source code is licensed under a MIT-style
// license that can be found in the LICENSE file.

package mic

import "unsafe"

// rans8StateDecompNative is a no-op on non-amd64/arm64 platforms.
// The pure-Go 8-state loop in rans8state.go handles all decoding.
func rans8StateDecompNative(_, _, _, _ unsafe.Pointer, _ int) int { return 0 }
