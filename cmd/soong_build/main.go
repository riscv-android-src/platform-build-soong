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

package main

import (
	"flag"
	"fmt"
	"io/ioutil"
	"os"
	"path/filepath"
	"strings"
	"time"

	"android/soong/bp2build"
	"android/soong/shared"
	"github.com/google/blueprint/bootstrap"
	"github.com/google/blueprint/deptools"
	"github.com/google/blueprint/pathtools"

	"android/soong/android"
)

var (
	topDir           string
	outDir           string
	availableEnvFile string
	usedEnvFile      string

	globFile    string
	globListDir string
	delveListen string
	delvePath   string

	docFile           string
	bazelQueryViewDir string
	bp2buildMarker    string

	cmdlineArgs bootstrap.Args
)

func init() {
	// Flags that make sense in every mode
	flag.StringVar(&topDir, "top", "", "Top directory of the Android source tree")
	flag.StringVar(&outDir, "out", "", "Soong output directory (usually $TOP/out/soong)")
	flag.StringVar(&availableEnvFile, "available_env", "", "File containing available environment variables")
	flag.StringVar(&usedEnvFile, "used_env", "", "File containing used environment variables")

	// Debug flags
	flag.StringVar(&delveListen, "delve_listen", "", "Delve port to listen on for debugging")
	flag.StringVar(&delvePath, "delve_path", "", "Path to Delve. Only used if --delve_listen is set")

	// Flags representing various modes soong_build can run in
	flag.StringVar(&docFile, "soong_docs", "", "build documentation file to output")
	flag.StringVar(&bazelQueryViewDir, "bazel_queryview_dir", "", "path to the bazel queryview directory relative to --top")
	flag.StringVar(&bp2buildMarker, "bp2build_marker", "", "If set, run bp2build, touch the specified marker file then exit")

	flag.StringVar(&cmdlineArgs.OutFile, "o", "build.ninja", "the Ninja file to output")
	flag.StringVar(&globFile, "globFile", "build-globs.ninja", "the Ninja file of globs to output")
	flag.StringVar(&globListDir, "globListDir", "", "the directory containing the glob list files")
	flag.StringVar(&cmdlineArgs.BuildDir, "b", ".", "the build output directory")
	flag.StringVar(&cmdlineArgs.NinjaBuildDir, "n", "", "the ninja builddir directory")
	flag.StringVar(&cmdlineArgs.DepFile, "d", "", "the dependency file to output")
	flag.StringVar(&cmdlineArgs.Cpuprofile, "cpuprofile", "", "write cpu profile to file")
	flag.StringVar(&cmdlineArgs.TraceFile, "trace", "", "write trace to file")
	flag.StringVar(&cmdlineArgs.Memprofile, "memprofile", "", "write memory profile to file")
	flag.BoolVar(&cmdlineArgs.NoGC, "nogc", false, "turn off GC for debugging")
	flag.BoolVar(&cmdlineArgs.RunGoTests, "t", false, "build and run go tests during bootstrap")
	flag.BoolVar(&cmdlineArgs.UseValidations, "use-validations", false, "use validations to depend on go tests")
	flag.StringVar(&cmdlineArgs.ModuleListFile, "l", "", "file that lists filepaths to parse")
	flag.BoolVar(&cmdlineArgs.EmptyNinjaFile, "empty-ninja-file", false, "write out a 0-byte ninja file")
}

func newNameResolver(config android.Config) *android.NameResolver {
	namespacePathsToExport := make(map[string]bool)

	for _, namespaceName := range config.ExportedNamespaces() {
		namespacePathsToExport[namespaceName] = true
	}

	namespacePathsToExport["."] = true // always export the root namespace

	exportFilter := func(namespace *android.Namespace) bool {
		return namespacePathsToExport[namespace.Path]
	}

	return android.NewNameResolver(exportFilter)
}

func newContext(configuration android.Config, prepareBuildActions bool) *android.Context {
	ctx := android.NewContext(configuration)
	ctx.Register()
	if !prepareBuildActions {
		configuration.SetStopBefore(bootstrap.StopBeforePrepareBuildActions)
	}
	ctx.SetNameInterface(newNameResolver(configuration))
	ctx.SetAllowMissingDependencies(configuration.AllowMissingDependencies())
	return ctx
}

func newConfig(outDir string, availableEnv map[string]string) android.Config {
	configuration, err := android.NewConfig(outDir, cmdlineArgs.ModuleListFile, availableEnv)
	if err != nil {
		fmt.Fprintf(os.Stderr, "%s", err)
		os.Exit(1)
	}
	return configuration
}

// Bazel-enabled mode. Soong runs in two passes.
// First pass: Analyze the build tree, but only store all bazel commands
// needed to correctly evaluate the tree in the second pass.
// TODO(cparsons): Don't output any ninja file, as the second pass will overwrite
// the incorrect results from the first pass, and file I/O is expensive.
func runMixedModeBuild(configuration android.Config, firstCtx *android.Context, extraNinjaDeps []string) {
	var firstArgs, secondArgs bootstrap.Args

	firstArgs = cmdlineArgs
	configuration.SetStopBefore(bootstrap.StopBeforeWriteNinja)
	bootstrap.RunBlueprint(firstArgs, firstCtx.Context, configuration)

	// Invoke bazel commands and save results for second pass.
	if err := configuration.BazelContext.InvokeBazel(); err != nil {
		fmt.Fprintf(os.Stderr, "%s", err)
		os.Exit(1)
	}
	// Second pass: Full analysis, using the bazel command results. Output ninja file.
	secondConfig, err := android.ConfigForAdditionalRun(configuration)
	if err != nil {
		fmt.Fprintf(os.Stderr, "%s", err)
		os.Exit(1)
	}
	secondCtx := newContext(secondConfig, true)
	secondArgs = cmdlineArgs
	ninjaDeps := bootstrap.RunBlueprint(secondArgs, secondCtx.Context, secondConfig)
	ninjaDeps = append(ninjaDeps, extraNinjaDeps...)

	globListFiles := writeBuildGlobsNinjaFile(secondCtx.SrcDir(), configuration.BuildDir(), secondCtx.Globs, configuration)
	ninjaDeps = append(ninjaDeps, globListFiles...)

	err = deptools.WriteDepFile(shared.JoinPath(topDir, secondArgs.DepFile), secondArgs.OutFile, ninjaDeps)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error writing depfile '%s': %s\n", secondArgs.DepFile, err)
		os.Exit(1)
	}
}

// Run the code-generation phase to convert BazelTargetModules to BUILD files.
func runQueryView(configuration android.Config, ctx *android.Context) {
	codegenContext := bp2build.NewCodegenContext(configuration, *ctx, bp2build.QueryView)
	absoluteQueryViewDir := shared.JoinPath(topDir, bazelQueryViewDir)
	if err := createBazelQueryView(codegenContext, absoluteQueryViewDir); err != nil {
		fmt.Fprintf(os.Stderr, "%s", err)
		os.Exit(1)
	}
}

func runSoongDocs(configuration android.Config) {
	ctx := newContext(configuration, false)
	soongDocsArgs := cmdlineArgs
	bootstrap.RunBlueprint(soongDocsArgs, ctx.Context, configuration)
	if err := writeDocs(ctx, configuration, docFile); err != nil {
		fmt.Fprintf(os.Stderr, "%s", err)
		os.Exit(1)
	}
}

func writeMetrics(configuration android.Config) {
	metricsFile := filepath.Join(configuration.BuildDir(), "soong_build_metrics.pb")
	err := android.WriteMetrics(configuration, metricsFile)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error writing soong_build metrics %s: %s", metricsFile, err)
		os.Exit(1)
	}
}

func writeJsonModuleGraph(configuration android.Config, ctx *android.Context, path string, extraNinjaDeps []string) {
	f, err := os.Create(path)
	if err != nil {
		fmt.Fprintf(os.Stderr, "%s", err)
		os.Exit(1)
	}

	defer f.Close()
	ctx.Context.PrintJSONGraph(f)
	writeFakeNinjaFile(extraNinjaDeps, configuration.BuildDir())
}

func writeBuildGlobsNinjaFile(srcDir, buildDir string, globs func() pathtools.MultipleGlobResults, config interface{}) []string {
	globDir := bootstrap.GlobDirectory(buildDir, globListDir)
	bootstrap.WriteBuildGlobsNinjaFile(&bootstrap.GlobSingleton{
		GlobLister: globs,
		GlobFile:   globFile,
		GlobDir:    globDir,
		SrcDir:     srcDir,
	}, config)
	return bootstrap.GlobFileListFiles(globDir)
}

// doChosenActivity runs Soong for a specific activity, like bp2build, queryview
// or the actual Soong build for the build.ninja file. Returns the top level
// output file of the specific activity.
func doChosenActivity(configuration android.Config, extraNinjaDeps []string) string {
	bazelConversionRequested := bp2buildMarker != ""
	mixedModeBuild := configuration.BazelContext.BazelEnabled()
	generateQueryView := bazelQueryViewDir != ""
	jsonModuleFile := configuration.Getenv("SOONG_DUMP_JSON_MODULE_GRAPH")

	blueprintArgs := cmdlineArgs
	prepareBuildActions := !generateQueryView && jsonModuleFile == ""
	if bazelConversionRequested {
		// Run the alternate pipeline of bp2build mutators and singleton to convert
		// Blueprint to BUILD files before everything else.
		runBp2Build(configuration, extraNinjaDeps)
		return bp2buildMarker
	}

	ctx := newContext(configuration, prepareBuildActions)
	if mixedModeBuild {
		runMixedModeBuild(configuration, ctx, extraNinjaDeps)
	} else {
		ninjaDeps := bootstrap.RunBlueprint(blueprintArgs, ctx.Context, configuration)
		ninjaDeps = append(ninjaDeps, extraNinjaDeps...)

		globListFiles := writeBuildGlobsNinjaFile(ctx.SrcDir(), configuration.BuildDir(), ctx.Globs, configuration)
		ninjaDeps = append(ninjaDeps, globListFiles...)

		err := deptools.WriteDepFile(shared.JoinPath(topDir, blueprintArgs.DepFile), blueprintArgs.OutFile, ninjaDeps)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error writing depfile '%s': %s\n", blueprintArgs.DepFile, err)
			os.Exit(1)
		}
	}

	// Convert the Soong module graph into Bazel BUILD files.
	if generateQueryView {
		runQueryView(configuration, ctx)
		return cmdlineArgs.OutFile // TODO: This is a lie
	}

	if jsonModuleFile != "" {
		writeJsonModuleGraph(configuration, ctx, jsonModuleFile, extraNinjaDeps)
		return cmdlineArgs.OutFile // TODO: This is a lie
	}

	writeMetrics(configuration)
	return cmdlineArgs.OutFile
}

// soong_ui dumps the available environment variables to
// soong.environment.available . Then soong_build itself is run with an empty
// environment so that the only way environment variables can be accessed is
// using Config, which tracks access to them.

// At the end of the build, a file called soong.environment.used is written
// containing the current value of all used environment variables. The next
// time soong_ui is run, it checks whether any environment variables that was
// used had changed and if so, it deletes soong.environment.used to cause a
// rebuild.
//
// The dependency of build.ninja on soong.environment.used is declared in
// build.ninja.d
func parseAvailableEnv() map[string]string {
	if availableEnvFile == "" {
		fmt.Fprintf(os.Stderr, "--available_env not set\n")
		os.Exit(1)
	}

	result, err := shared.EnvFromFile(shared.JoinPath(topDir, availableEnvFile))
	if err != nil {
		fmt.Fprintf(os.Stderr, "error reading available environment file '%s': %s\n", availableEnvFile, err)
		os.Exit(1)
	}

	return result
}

func main() {
	flag.Parse()

	shared.ReexecWithDelveMaybe(delveListen, delvePath)
	android.InitSandbox(topDir)

	availableEnv := parseAvailableEnv()

	configuration := newConfig(outDir, availableEnv)
	extraNinjaDeps := []string{
		configuration.ProductVariablesFileName,
		usedEnvFile,
	}

	if configuration.Getenv("ALLOW_MISSING_DEPENDENCIES") == "true" {
		configuration.SetAllowMissingDependencies()
	}

	if shared.IsDebugging() {
		// Add a non-existent file to the dependencies so that soong_build will rerun when the debugger is
		// enabled even if it completed successfully.
		extraNinjaDeps = append(extraNinjaDeps, filepath.Join(configuration.BuildDir(), "always_rerun_for_delve"))
	}

	if docFile != "" {
		// We don't write an used variables file when generating documentation
		// because that is done from within the actual builds as a Ninja action and
		// thus it would overwrite the actual used variables file so this is
		// special-cased.
		// TODO: Fix this by not passing --used_env to the soong_docs invocation
		runSoongDocs(configuration)
		return
	}

	finalOutputFile := doChosenActivity(configuration, extraNinjaDeps)
	writeUsedEnvironmentFile(configuration, finalOutputFile)
}

func writeUsedEnvironmentFile(configuration android.Config, finalOutputFile string) {
	if usedEnvFile == "" {
		return
	}

	path := shared.JoinPath(topDir, usedEnvFile)
	data, err := shared.EnvFileContents(configuration.EnvDeps())
	if err != nil {
		fmt.Fprintf(os.Stderr, "error writing used environment file '%s': %s\n", usedEnvFile, err)
		os.Exit(1)
	}

	err = ioutil.WriteFile(path, data, 0666)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error writing used environment file '%s': %s\n", usedEnvFile, err)
		os.Exit(1)
	}

	// Touch the output file so that it's not older than the file we just
	// wrote. We can't write the environment file earlier because one an access
	// new environment variables while writing it.
	touch(shared.JoinPath(topDir, finalOutputFile))
}

// Workarounds to support running bp2build in a clean AOSP checkout with no
// prior builds, and exiting early as soon as the BUILD files get generated,
// therefore not creating build.ninja files that soong_ui and callers of
// soong_build expects.
//
// These files are: build.ninja and build.ninja.d. Since Kati hasn't been
// ran as well, and `nothing` is defined in a .mk file, there isn't a ninja
// target called `nothing`, so we manually create it here.
func writeFakeNinjaFile(extraNinjaDeps []string, buildDir string) {
	extraNinjaDepsString := strings.Join(extraNinjaDeps, " \\\n ")

	ninjaFileName := "build.ninja"
	ninjaFile := shared.JoinPath(topDir, buildDir, ninjaFileName)
	ninjaFileD := shared.JoinPath(topDir, buildDir, ninjaFileName+".d")
	// A workaround to create the 'nothing' ninja target so `m nothing` works,
	// since bp2build runs without Kati, and the 'nothing' target is declared in
	// a Makefile.
	ioutil.WriteFile(ninjaFile, []byte("build nothing: phony\n  phony_output = true\n"), 0666)
	ioutil.WriteFile(ninjaFileD,
		[]byte(fmt.Sprintf("%s: \\\n %s\n", ninjaFile, extraNinjaDepsString)),
		0666)
}

func touch(path string) {
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0666)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error touching '%s': %s\n", path, err)
		os.Exit(1)
	}

	err = f.Close()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error touching '%s': %s\n", path, err)
		os.Exit(1)
	}

	currentTime := time.Now().Local()
	err = os.Chtimes(path, currentTime, currentTime)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error touching '%s': %s\n", path, err)
		os.Exit(1)
	}
}

// Find BUILD files in the srcDir which...
//
// - are not on the allow list (android/bazel.go#ShouldKeepExistingBuildFileForDir())
//
// - won't be overwritten by corresponding bp2build generated files
//
// And return their paths so they can be left out of the Bazel workspace dir (i.e. ignored)
func getPathsToIgnoredBuildFiles(topDir string, generatedRoot string, srcDirBazelFiles []string) []string {
	paths := make([]string, 0)

	for _, srcDirBazelFileRelativePath := range srcDirBazelFiles {
		srcDirBazelFileFullPath := shared.JoinPath(topDir, srcDirBazelFileRelativePath)
		fileInfo, err := os.Stat(srcDirBazelFileFullPath)
		if err != nil {
			// Warn about error, but continue trying to check files
			fmt.Fprintf(os.Stderr, "WARNING: Error accessing path '%s', err: %s\n", srcDirBazelFileFullPath, err)
			continue
		}
		if fileInfo.IsDir() {
			// Don't ignore entire directories
			continue
		}
		if !(fileInfo.Name() == "BUILD" || fileInfo.Name() == "BUILD.bazel") {
			// Don't ignore this file - it is not a build file
			continue
		}
		srcDirBazelFileDir := filepath.Dir(srcDirBazelFileRelativePath)
		if android.ShouldKeepExistingBuildFileForDir(srcDirBazelFileDir) {
			// Don't ignore this existing build file
			continue
		}
		correspondingBp2BuildFile := shared.JoinPath(topDir, generatedRoot, srcDirBazelFileRelativePath)
		if _, err := os.Stat(correspondingBp2BuildFile); err == nil {
			// If bp2build generated an alternate BUILD file, don't exclude this workspace path
			// BUILD file clash resolution happens later in the symlink forest creation
			continue
		}
		fmt.Fprintf(os.Stderr, "Ignoring existing BUILD file: %s\n", srcDirBazelFileRelativePath)
		paths = append(paths, srcDirBazelFileRelativePath)
	}

	return paths
}

// Returns temporary symlink forest excludes necessary for bazel build //external/... (and bazel build //frameworks/...) to work
func getTemporaryExcludes() []string {
	excludes := make([]string, 0)

	// FIXME: 'autotest_lib' is a symlink back to external/autotest, and this causes an infinite symlink expansion error for Bazel
	excludes = append(excludes, "external/autotest/venv/autotest_lib")

	// FIXME: The external/google-fruit/extras/bazel_root/third_party/fruit dir is poison
	// It contains several symlinks back to real source dirs, and those source dirs contain BUILD files we want to ignore
	excludes = append(excludes, "external/google-fruit/extras/bazel_root/third_party/fruit")

	// FIXME: 'frameworks/compile/slang' has a filegroup error due to an escaping issue
	excludes = append(excludes, "frameworks/compile/slang")

	return excludes
}

// Read the bazel.list file that the Soong Finder already dumped earlier (hopefully)
// It contains the locations of BUILD files, BUILD.bazel files, etc. in the source dir
func getExistingBazelRelatedFiles(topDir string) ([]string, error) {
	bazelFinderFile := filepath.Join(filepath.Dir(cmdlineArgs.ModuleListFile), "bazel.list")
	if !filepath.IsAbs(bazelFinderFile) {
		// Assume this was a relative path under topDir
		bazelFinderFile = filepath.Join(topDir, bazelFinderFile)
	}
	data, err := ioutil.ReadFile(bazelFinderFile)
	if err != nil {
		return nil, err
	}
	files := strings.Split(strings.TrimSpace(string(data)), "\n")
	return files, nil
}

// Run Soong in the bp2build mode. This creates a standalone context that registers
// an alternate pipeline of mutators and singletons specifically for generating
// Bazel BUILD files instead of Ninja files.
func runBp2Build(configuration android.Config, extraNinjaDeps []string) {
	// Register an alternate set of singletons and mutators for bazel
	// conversion for Bazel conversion.
	bp2buildCtx := android.NewContext(configuration)

	// Propagate "allow misssing dependencies" bit. This is normally set in
	// newContext(), but we create bp2buildCtx without calling that method.
	bp2buildCtx.SetAllowMissingDependencies(configuration.AllowMissingDependencies())
	bp2buildCtx.SetNameInterface(newNameResolver(configuration))
	bp2buildCtx.RegisterForBazelConversion()

	// The bp2build process is a purely functional process that only depends on
	// Android.bp files. It must not depend on the values of per-build product
	// configurations or variables, since those will generate different BUILD
	// files based on how the user has configured their tree.
	bp2buildCtx.SetModuleListFile(cmdlineArgs.ModuleListFile)
	modulePaths, err := bp2buildCtx.ListModulePaths(".")
	if err != nil {
		panic(err)
	}

	extraNinjaDeps = append(extraNinjaDeps, modulePaths...)

	// No need to generate Ninja build rules/statements from Modules and Singletons.
	configuration.SetStopBefore(bootstrap.StopBeforePrepareBuildActions)

	// Run the loading and analysis pipeline to prepare the graph of regular
	// Modules parsed from Android.bp files, and the BazelTargetModules mapped
	// from the regular Modules.
	blueprintArgs := cmdlineArgs
	ninjaDeps := bootstrap.RunBlueprint(blueprintArgs, bp2buildCtx.Context, configuration)
	ninjaDeps = append(ninjaDeps, extraNinjaDeps...)

	globListFiles := writeBuildGlobsNinjaFile(bp2buildCtx.SrcDir(), configuration.BuildDir(), bp2buildCtx.Globs, configuration)
	ninjaDeps = append(ninjaDeps, globListFiles...)

	// Run the code-generation phase to convert BazelTargetModules to BUILD files
	// and print conversion metrics to the user.
	codegenContext := bp2build.NewCodegenContext(configuration, *bp2buildCtx, bp2build.Bp2Build)
	metrics := bp2build.Codegen(codegenContext)

	generatedRoot := shared.JoinPath(configuration.BuildDir(), "bp2build")
	workspaceRoot := shared.JoinPath(configuration.BuildDir(), "workspace")

	excludes := []string{
		"bazel-bin",
		"bazel-genfiles",
		"bazel-out",
		"bazel-testlogs",
		"bazel-" + filepath.Base(topDir),
	}

	if cmdlineArgs.NinjaBuildDir[0] != '/' {
		excludes = append(excludes, cmdlineArgs.NinjaBuildDir)
	}

	existingBazelRelatedFiles, err := getExistingBazelRelatedFiles(topDir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error determining existing Bazel-related files: %s\n", err)
		os.Exit(1)
	}

	pathsToIgnoredBuildFiles := getPathsToIgnoredBuildFiles(topDir, generatedRoot, existingBazelRelatedFiles)
	excludes = append(excludes, pathsToIgnoredBuildFiles...)

	excludes = append(excludes, getTemporaryExcludes()...)

	symlinkForestDeps := bp2build.PlantSymlinkForest(
		topDir, workspaceRoot, generatedRoot, ".", excludes)

	// Only report metrics when in bp2build mode. The metrics aren't relevant
	// for queryview, since that's a total repo-wide conversion and there's a
	// 1:1 mapping for each module.
	metrics.Print()

	ninjaDeps = append(ninjaDeps, codegenContext.AdditionalNinjaDeps()...)
	ninjaDeps = append(ninjaDeps, symlinkForestDeps...)

	depFile := bp2buildMarker + ".d"
	err = deptools.WriteDepFile(shared.JoinPath(topDir, depFile), bp2buildMarker, ninjaDeps)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Cannot write depfile '%s': %s\n", depFile, err)
		os.Exit(1)
	}

	// Create an empty bp2build marker file.
	touch(shared.JoinPath(topDir, bp2buildMarker))
}
