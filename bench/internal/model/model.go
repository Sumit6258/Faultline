// Package model is the file format results are stored in, and the rules for
// when two results can be compared.
package model

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
)

// Version changes when the file format does, so an old file is refused
// instead of being misread.
const Version = 1

type Environment struct {
	GOOS          string `json:"goos"`
	GOARCH        string `json:"goarch"`
	CPU           string `json:"cpu"`
	NumCPU        int    `json:"num_cpu"`
	GoVersion     string `json:"go_version"`
	PythonVersion string `json:"python_version,omitempty"`
	Commit        string `json:"commit,omitempty"`
	Dirty         bool   `json:"dirty,omitempty"`
	Date          string `json:"date"`
	Count         int    `json:"count"`
	BenchTime     string `json:"benchtime"`
}

// Series is every measurement of one benchmark: one value per run, per metric.
type Series struct {
	Module  string               `json:"module"`
	Package string               `json:"package"`
	Name    string               `json:"name"`
	Procs   int                  `json:"procs"`
	Samples map[string][]float64 `json:"samples"`
}

func (s Series) Key() string { return s.Module + "|" + s.Package + "|" + s.Name }

type Failure struct {
	Module  string `json:"module"`
	Message string `json:"message"`
}

type File struct {
	Version  int         `json:"version"`
	Env      Environment `json:"environment"`
	Series   []Series    `json:"series"`
	Failures []Failure   `json:"failures,omitempty"`
}

func Load(path string) (File, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return File{}, err
	}
	var f File
	if err := json.Unmarshal(raw, &f); err != nil {
		return File{}, fmt.Errorf("%s: %w", path, err)
	}
	if f.Version != Version {
		return File{}, fmt.Errorf("%s is format version %d, this tool reads version %d", path, f.Version, Version)
	}
	return f, nil
}

func (f File) Save(path string) error {
	f.Version = Version
	raw, err := json.MarshalIndent(f, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(raw, '\n'), 0o644)
}

// MinorGoVersion is "go1.22" for "go1.22.2". Patch releases rarely change what
// a benchmark does, minor ones sometimes do.
func MinorGoVersion(v string) string {
	parts := strings.Split(v, ".")
	if len(parts) >= 2 {
		return parts[0] + "." + parts[1]
	}
	return v
}

// Comparable says whether timings from a and b can fairly be compared, and if
// not, why. A time is a property of a program on a machine, so a number from
// a laptop says nothing about a number from a server, and comparing them
// produces confident nonsense.
func Comparable(a, b Environment) (bool, string) {
	var why []string
	if a.GOOS != b.GOOS || a.GOARCH != b.GOARCH {
		why = append(why, fmt.Sprintf("platform %s/%s vs %s/%s", a.GOOS, a.GOARCH, b.GOOS, b.GOARCH))
	}
	if a.CPU != b.CPU {
		why = append(why, fmt.Sprintf("CPU %q vs %q", a.CPU, b.CPU))
	}
	if MinorGoVersion(a.GoVersion) != MinorGoVersion(b.GoVersion) {
		why = append(why, fmt.Sprintf("Go %s vs %s", a.GoVersion, b.GoVersion))
	}
	if len(why) == 0 {
		return true, ""
	}
	return false, strings.Join(why, "; ")
}
