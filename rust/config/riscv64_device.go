// Copyright 2019 The Android Open Source Project
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

package config

import (
	"strings"

	"android/soong/android"
)

var (
	Riscv64RustFlags            = []string{}
	Riscv64ArchFeatureRustFlags = map[string][]string{}
	Riscv64LinkFlags            = []string{
		"-Wl,--icf=safe",
		"-Wl,-z,max-page-size=4096",

		"-Wl,-z,separate-code",
	}

	Riscv64ArchVariantRustFlags = map[string][]string{
		"c910": []string{},
	}
)

func init() {
	registerToolchainFactory(android.Android, android.Riscv64, Riscv64ToolchainFactory)

	pctx.StaticVariable("Riscv64ToolchainRustFlags", strings.Join(Riscv64RustFlags, " "))
	pctx.StaticVariable("Riscv64ToolchainLinkFlags", strings.Join(Riscv64LinkFlags, " "))

	for variant, rustFlags := range Riscv64ArchVariantRustFlags {
		pctx.StaticVariable("Riscv64"+variant+"VariantRustFlags",
			strings.Join(rustFlags, " "))
	}

}

type toolchainRiscv64 struct {
	toolchain64Bit
	toolchainRustFlags string
}

func (t *toolchainRiscv64) RustTriple() string {
	return "riscv64-linux-android"
}

func (t *toolchainRiscv64) ToolchainLinkFlags() string {
	return "${config.DeviceGlobalLinkFlags} ${config.Riscv64ToolchainLinkFlags}"
}

func (t *toolchainRiscv64) ToolchainRustFlags() string {
	return t.toolchainRustFlags
}

func (t *toolchainRiscv64) RustFlags() string {
	return "${config.Riscv64ToolchainRustFlags}"
}

func (t *toolchainRiscv64) Supported() bool {
	return true
}

func Riscv64ToolchainFactory(arch android.Arch) Toolchain {
	toolchainRustFlags := []string{
		"${config.Riscv64ToolchainRustFlags}",
		"${config.Riscv64" + arch.ArchVariant + "VariantRustFlags}",
	}

	toolchainRustFlags = append(toolchainRustFlags, deviceGlobalRustFlags...)

	for _, feature := range arch.ArchFeatures {
		toolchainRustFlags = append(toolchainRustFlags, Riscv64ArchFeatureRustFlags[feature]...)
	}

	return &toolchainRiscv64{
		toolchainRustFlags: strings.Join(toolchainRustFlags, " "),
	}
}
