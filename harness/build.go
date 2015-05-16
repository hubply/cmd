package harness

import (
	"fmt"
	"go/build"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"text/template"

	"github.com/hubply/gospf"
)

var importErrorPattern = regexp.MustCompile("cannot find package \"([^\"]+)\"")

// Build the app:
// 1. Generate the the main.go file.
// 2. Run the appropriate "go build" command.
// Requires that gospf.Init has been called previously.
// Returns the path to the built binary, and an error if there was a problem building it.
func Build(buildFlags ...string) (app *App, compileError *gospf.Error) {
	// First, clear the generated files (to avoid them messing with ProcessSource).
	cleanSource("tmp", "routes")

	sourceInfo, compileError := ProcessSource(gospf.CodePaths)
	if compileError != nil {
		return nil, compileError
	}

	// Add the db.import to the import paths.
	if dbImportPath, found := gospf.Config.String("db.import"); found {
		sourceInfo.InitImportPaths = append(sourceInfo.InitImportPaths, dbImportPath)
	}

	// Generate two source files.
	templateArgs := map[string]interface{}{
		"Controllers":    sourceInfo.ControllerSpecs(),
		"ValidationKeys": sourceInfo.ValidationKeys,
		"ImportPaths":    calcImportAliases(sourceInfo),
		"TestSuites":     sourceInfo.TestSuites(),
	}
	genSource("tmp", "main.go", MAIN, templateArgs)
	genSource("routes", "routes.go", ROUTES, templateArgs)

	// Read build config.
	buildTags := gospf.Config.StringDefault("build.tags", "")

	// Build the user program (all code under app).
	// It relies on the user having "go" installed.
	goPath, err := exec.LookPath("go")
	if err != nil {
		gospf.ERROR.Fatalf("Go executable not found in PATH.")
	}

	pkg, err := build.Default.Import(gospf.ImportPath, "", build.FindOnly)
	if err != nil {
		gospf.ERROR.Fatalln("Failure importing", gospf.ImportPath)
	}

	// Binary path is a combination of $GOBIN/gospf.d directory, app's import path and its name.
	binName := path.Join(pkg.BinDir, "gospf.d", gospf.ImportPath, path.Base(gospf.BasePath))

	// Change binary path for Windows build
	goos := runtime.GOOS
	if goosEnv := os.Getenv("GOOS"); goosEnv != "" {
		goos = goosEnv
	}
	if goos == "windows" {
		binName += ".exe"
	}

	gotten := make(map[string]struct{})
	for {
		appVersion := getAppVersion()
		versionLinkerFlags := fmt.Sprintf("-X %s/app.APP_VERSION \"%s\"", gospf.ImportPath, appVersion)
		flags := []string{
			"build",
			"-ldflags", versionLinkerFlags,
			"-tags", buildTags,
			"-o", binName}

		// Add in build flags
		flags = append(flags, buildFlags...)

		// The main path
		flags = append(flags, path.Join(gospf.ImportPath, "app", "tmp"))

		buildCmd := exec.Command(goPath, flags...)
		gospf.TRACE.Println("Exec:", buildCmd.Args)
		output, err := buildCmd.CombinedOutput()

		// If the build succeeded, we're done.
		if err == nil {
			return NewApp(binName), nil
		}
		gospf.ERROR.Println(string(output))

		// See if it was an import error that we can go get.
		matches := importErrorPattern.FindStringSubmatch(string(output))
		if matches == nil {
			return nil, newCompileError(output)
		}

		// Ensure we haven't already tried to go get it.
		pkgName := matches[1]
		if _, alreadyTried := gotten[pkgName]; alreadyTried {
			return nil, newCompileError(output)
		}
		gotten[pkgName] = struct{}{}

		// Execute "go get <pkg>"
		getCmd := exec.Command(goPath, "get", pkgName)
		gospf.TRACE.Println("Exec:", getCmd.Args)
		getOutput, err := getCmd.CombinedOutput()
		if err != nil {
			gospf.ERROR.Println(string(getOutput))
			return nil, newCompileError(output)
		}

		// Success getting the import, attempt to build again.
	}
	gospf.ERROR.Fatalf("Not reachable")
	return nil, nil
}

// Try to define a version string for the compiled app
// The following is tried (first match returns):
// - Read a version explicitly specified in the APP_VERSION environment
//   variable
// - Read the output of "git describe" if the source is in a git repository
// If no version can be determined, an empty string is returned.
func getAppVersion() string {
	if version := os.Getenv("APP_VERSION"); version != "" {
		return version
	}

	// Check for the git binary
	if gitPath, err := exec.LookPath("git"); err == nil {
		// Check for the .git directory
		gitDir := path.Join(gospf.BasePath, ".git")
		info, err := os.Stat(gitDir)
		if (err != nil && os.IsNotExist(err)) || !info.IsDir() {
			return ""
		}
		gitCmd := exec.Command(gitPath, "--git-dir="+gitDir, "describe", "--always", "--dirty")
		gospf.TRACE.Println("Exec:", gitCmd.Args)
		output, err := gitCmd.Output()

		if err != nil {
			gospf.WARN.Println("Cannot determine git repository version:", err)
			return ""
		}

		return "git-" + strings.TrimSpace(string(output))
	}

	return ""
}

func cleanSource(dirs ...string) {
	for _, dir := range dirs {
		cleanDir(dir)
	}
}

func cleanDir(dir string) {
	gospf.INFO.Println("Cleaning dir " + dir)
	tmpPath := path.Join(gospf.AppPath, dir)
	f, err := os.Open(tmpPath)
	if err != nil {
		gospf.ERROR.Println("Failed to clean dir:", err)
	} else {
		defer f.Close()
		infos, err := f.Readdir(0)
		if err != nil {
			gospf.ERROR.Println("Failed to clean dir:", err)
		} else {
			for _, info := range infos {
				path := path.Join(tmpPath, info.Name())
				if info.IsDir() {
					err := os.RemoveAll(path)
					if err != nil {
						gospf.ERROR.Println("Failed to remove dir:", err)
					}
				} else {
					err := os.Remove(path)
					if err != nil {
						gospf.ERROR.Println("Failed to remove file:", err)
					}
				}
			}
		}
	}
}

// genSource renders the given template to produce source code, which it writes
// to the given directory and file.
func genSource(dir, filename, templateSource string, args map[string]interface{}) {
	sourceCode := gospf.ExecuteTemplate(
		template.Must(template.New("").Parse(templateSource)),
		args)

	// Create a fresh dir.
	cleanSource(dir)
	tmpPath := path.Join(gospf.AppPath, dir)
	err := os.Mkdir(tmpPath, 0777)
	if err != nil && !os.IsExist(err) {
		gospf.ERROR.Fatalf("Failed to make '%v' directory: %v", dir, err)
	}

	// Create the file
	file, err := os.Create(path.Join(tmpPath, filename))
	defer file.Close()
	if err != nil {
		gospf.ERROR.Fatalf("Failed to create file: %v", err)
	}
	_, err = file.WriteString(sourceCode)
	if err != nil {
		gospf.ERROR.Fatalf("Failed to write to file: %v", err)
	}
}

// Looks through all the method args and returns a set of unique import paths
// that cover all the method arg types.
// Additionally, assign package aliases when necessary to resolve ambiguity.
func calcImportAliases(src *SourceInfo) map[string]string {
	aliases := make(map[string]string)
	typeArrays := [][]*TypeInfo{src.ControllerSpecs(), src.TestSuites()}
	for _, specs := range typeArrays {
		for _, spec := range specs {
			addAlias(aliases, spec.ImportPath, spec.PackageName)

			for _, methSpec := range spec.MethodSpecs {
				for _, methArg := range methSpec.Args {
					if methArg.ImportPath == "" {
						continue
					}

					addAlias(aliases, methArg.ImportPath, methArg.TypeExpr.PkgName)
				}
			}
		}
	}

	// Add the "InitImportPaths", with alias "_"
	for _, importPath := range src.InitImportPaths {
		if _, ok := aliases[importPath]; !ok {
			aliases[importPath] = "_"
		}
	}

	return aliases
}

func addAlias(aliases map[string]string, importPath, pkgName string) {
	alias, ok := aliases[importPath]
	if ok {
		return
	}
	alias = makePackageAlias(aliases, pkgName)
	aliases[importPath] = alias
}

func makePackageAlias(aliases map[string]string, pkgName string) string {
	i := 0
	alias := pkgName
	for containsValue(aliases, alias) {
		alias = fmt.Sprintf("%s%d", pkgName, i)
		i++
	}
	return alias
}

func containsValue(m map[string]string, val string) bool {
	for _, v := range m {
		if v == val {
			return true
		}
	}
	return false
}

// Parse the output of the "go build" command.
// Return a detailed Error.
func newCompileError(output []byte) *gospf.Error {
	errorMatch := regexp.MustCompile(`(?m)^([^:#]+):(\d+):(\d+:)? (.*)$`).
		FindSubmatch(output)
	if errorMatch == nil {
		errorMatch = regexp.MustCompile(`(?m)^(.*?)\:(\d+)\:\s(.*?)$`).FindSubmatch(output)

		if errorMatch == nil {
			gospf.ERROR.Println("Failed to parse build errors:\n", string(output))
			return &gospf.Error{
				SourceType:  "Go code",
				Title:       "Go Compilation Error",
				Description: "See console for build error.",
			}
		}

		errorMatch = append(errorMatch, errorMatch[3])

		gospf.ERROR.Println("Build errors:\n", string(output))
	}

	// Read the source for the offending file.
	var (
		relFilename    = string(errorMatch[1]) // e.g. "src/gospf/sample/app/controllers/app.go"
		absFilename, _ = filepath.Abs(relFilename)
		line, _        = strconv.Atoi(string(errorMatch[2]))
		description    = string(errorMatch[4])
		compileError   = &gospf.Error{
			SourceType:  "Go code",
			Title:       "Go Compilation Error",
			Path:        relFilename,
			Description: description,
			Line:        line,
		}
	)

	errorLink := gospf.Config.StringDefault("error.link", "")

	if errorLink != "" {
		compileError.SetLink(errorLink)
	}

	fileStr, err := gospf.ReadLines(absFilename)
	if err != nil {
		compileError.MetaError = absFilename + ": " + err.Error()
		gospf.ERROR.Println(compileError.MetaError)
		return compileError
	}

	compileError.SourceLines = fileStr
	return compileError
}

const MAIN = `// GENERATED CODE - DO NOT EDIT
package main

import (
	"flag"
	"reflect"
	"github.com/gospf/gospf"{{range $k, $v := $.ImportPaths}}
	{{$v}} "{{$k}}"{{end}}
	"github.com/gospf/gospf/testing"
)

var (
	runMode    *string = flag.String("runMode", "", "Run mode.")
	port       *int    = flag.Int("port", 0, "By default, read from app.conf")
	importPath *string = flag.String("importPath", "", "Go Import Path for the app.")
	srcPath    *string = flag.String("srcPath", "", "Path to the source root.")

	// So compiler won't complain if the generated code doesn't reference reflect package...
	_ = reflect.Invalid
)

func main() {
	flag.Parse()
	gospf.Init(*runMode, *importPath, *srcPath)
	gospf.INFO.Println("Running gospf server")
	{{range $i, $c := .Controllers}}
	gospf.RegisterController((*{{index $.ImportPaths .ImportPath}}.{{.StructName}})(nil),
		[]*gospf.MethodType{
			{{range .MethodSpecs}}&gospf.MethodType{
				Name: "{{.Name}}",
				Args: []*gospf.MethodArg{ {{range .Args}}
					&gospf.MethodArg{Name: "{{.Name}}", Type: reflect.TypeOf((*{{index $.ImportPaths .ImportPath | .TypeExpr.TypeName}})(nil)) },{{end}}
				},
				RenderArgNames: map[int][]string{ {{range .RenderCalls}}
					{{.Line}}: []string{ {{range .Names}}
						"{{.}}",{{end}}
					},{{end}}
				},
			},
			{{end}}
		})
	{{end}}
	gospf.DefaultValidationKeys = map[string]map[int]string{ {{range $path, $lines := .ValidationKeys}}
		"{{$path}}": { {{range $line, $key := $lines}}
			{{$line}}: "{{$key}}",{{end}}
		},{{end}}
	}
	testing.TestSuites = []interface{}{ {{range .TestSuites}}
		(*{{index $.ImportPaths .ImportPath}}.{{.StructName}})(nil),{{end}}
	}

	gospf.Run(*port)
}
`
const ROUTES = `// GENERATED CODE - DO NOT EDIT
package routes

import "github.com/gospf/gospf"

{{range $i, $c := .Controllers}}
type t{{.StructName}} struct {}
var {{.StructName}} t{{.StructName}}

{{range .MethodSpecs}}
func (_ t{{$c.StructName}}) {{.Name}}({{range .Args}}
		{{.Name}} {{if .ImportPath}}interface{}{{else}}{{.TypeExpr.TypeName ""}}{{end}},{{end}}
		) string {
	args := make(map[string]string)
	{{range .Args}}
	gospf.Unbind(args, "{{.Name}}", {{.Name}}){{end}}
	return gospf.MainRouter.Reverse("{{$c.StructName}}.{{.Name}}", args).Url
}
{{end}}
{{end}}
`
