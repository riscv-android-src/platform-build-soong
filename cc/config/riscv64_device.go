// Copyright 2015 Google Inc. All rights reserved.
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
	riscv64Cflags = []string{
		// Help catch common 32/64-bit errors.
		"-Werror=implicit-function-declaration",
		"-Wno-implicit-int-float-conversion",
		"-Wno-deprecated-copy",
		"-Wno-implicit-fallthrough",
	}

	riscv64ClangCflags = append(riscv64Cflags, []string{
		"-fintegrated-as",
		"-Wno-implicit-fallthrough",
	}...)

	riscv64Cppflags = []string{
		"-Wno-implicit-int-float-conversion",
		"-Wno-deprecated-copy",
		"-Wno-implicit-fallthrough",
	}

	riscv64Ldflags = []string{
		"-Wl,--allow-shlib-undefined",
	}

	riscv64ArchVariantCflags = map[string][]string{
		"imafdc": []string{
			"-march=rv64imafdc",
		},
	}
)

const (
	riscv64GccVersion = "8.1"
)

func init() {
	pctx.StaticVariable("riscv64GccVersion", riscv64GccVersion)

	pctx.SourcePathVariable("Riscv64GccRoot",
		"prebuilts/gcc/${HostPrebuiltTag}/riscv64/riscv64-linux-android-${riscv64GccVersion}")

	pctx.StaticVariable("Riscv64IncludeFlags", bionicHeaders("riscv"))

	// Clang cflags
	pctx.StaticVariable("Riscv64ClangCflags", strings.Join(ClangFilterUnknownCflags(riscv64ClangCflags), " "))
	pctx.StaticVariable("Riscv64ClangLdflags", strings.Join(ClangFilterUnknownCflags(riscv64Ldflags), " "))
	pctx.StaticVariable("Riscv64ClangCppflags", strings.Join(ClangFilterUnknownCflags(riscv64Cppflags), " "))

	// Extended cflags

	// Architecture variant cflags
	pctx.StaticVariable("Riscv64VariantClangCflags",
			strings.Join(ClangFilterUnknownCflags(riscv64ClangCflags), " "))
	for variant, cflags := range riscv64ArchVariantCflags {
		pctx.StaticVariable("Riscv64"+variant+"VariantClangCflags",
			strings.Join(ClangFilterUnknownCflags(cflags), " "))
	}
}

type toolchainRiscv64 struct {
	toolchain64Bit
	clangCflags          string
	toolchainClangCflags string
}

func (t *toolchainRiscv64) Name() string {
	return "riscv64"
}

func (t *toolchainRiscv64) GccRoot() string {
	return "${config.Riscv64GccRoot}"
}

func (t *toolchainRiscv64) GccTriple() string {
	return "riscv64-linux-android"
}

func (t *toolchainRiscv64) GccVersion() string {
	return riscv64GccVersion
}

func (t *toolchainRiscv64) IncludeFlags() string {
	return "${config.Riscv64IncludeFlags}"
}

func (t *toolchainRiscv64) ClangTriple() string {
	return t.GccTriple()
}

func (t *toolchainRiscv64) ToolchainClangCflags() string {
	return t.toolchainClangCflags
}

func (t *toolchainRiscv64) ClangAsflags() string {
	return "-fno-integrated-as"
}

func (t *toolchainRiscv64) ClangCflags() string {
	return t.clangCflags
}

func (t *toolchainRiscv64) ClangCppflags() string {
	return "${config.Riscv64ClangCppflags}"
}

func (t *toolchainRiscv64) ClangLdflags() string {
	return "${config.Riscv64ClangLdflags}"
}

func (t *toolchainRiscv64) ClangLldflags() string {
	// TODO: define and use Riscv64ClangLldflags
	return "${config.Riscv64ClangLdflags}"
}

func (toolchainRiscv64) LibclangRuntimeLibraryArch() string {
	return "riscv64"
}

func riscv64ToolchainFactory(arch android.Arch) Toolchain {
	return &toolchainRiscv64{
		clangCflags:          "${config.Riscv64ClangCflags}",
		toolchainClangCflags: "${config.Riscv64" + arch.ArchVariant + "VariantClangCflags}",
	}
}

func init() {
	registerToolchainFactory(android.Android, android.Riscv64, riscv64ToolchainFactory)
}
