package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
)

type module struct {
	Path    string
	Version string
	Dir     string
	Main    bool
}

type packageInfo struct {
	Module *module
}

type targetList []string

func (targets *targetList) String() string {
	return strings.Join(*targets, ",")
}

func (targets *targetList) Set(value string) error {
	parts := strings.Split(value, "/")
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return fmt.Errorf("target must use GOOS/GOARCH")
	}
	*targets = append(*targets, value)
	return nil
}

type noticeFile struct {
	modulePath string
	version    string
	name       string
	path       string
}

func main() {
	version := flag.String("version", "", "validated release version")
	output := flag.String("output", "", "notice output path")
	var targets targetList
	flag.Var(&targets, "target", "release target GOOS/GOARCH; repeat for each target")
	flag.Parse()
	if flag.NArg() != 0 || *version == "" || *output == "" {
		fatalf("usage: release-notices --version VERSION --output PATH")
	}

	if len(targets) == 0 {
		targets = append(targets, runtime.GOOS+"/"+runtime.GOARCH)
	}
	files, err := collectNoticeFiles(targets)
	if err != nil {
		fatalf("collect release notices: %v", err)
	}
	if err := writeNotices(*output, *version, targets, files); err != nil {
		fatalf("write release notices: %v", err)
	}
}

func collectNoticeFiles(targets []string) ([]noticeFile, error) {
	modulesByPath := make(map[string]module)
	for _, target := range targets {
		parts := strings.Split(target, "/")
		command := exec.Command("go", "list", "-deps", "-json", ".")
		command.Env = append(os.Environ(),
			"CGO_ENABLED=0",
			"GOOS="+parts[0],
			"GOARCH="+parts[1],
			"GOTOOLCHAIN=local",
		)
		output, err := command.Output()
		if err != nil {
			return nil, fmt.Errorf("list build packages for %s: %w", target, err)
		}
		decoder := json.NewDecoder(strings.NewReader(string(output)))
		for {
			var item packageInfo
			if err := decoder.Decode(&item); err != nil {
				if err == io.EOF {
					break
				}
				return nil, fmt.Errorf("decode build package list for %s: %w", target, err)
			}
			if item.Module != nil {
				modulesByPath[item.Module.Path] = *item.Module
			}
		}
	}
	modules := make([]module, 0, len(modulesByPath))
	for _, item := range modulesByPath {
		modules = append(modules, item)
	}
	sort.Slice(modules, func(i, j int) bool { return modules[i].Path < modules[j].Path })

	var files []noticeFile
	for _, item := range modules {
		if item.Dir == "" {
			return nil, fmt.Errorf("module %s has no local directory", item.Path)
		}
		matches, err := moduleNoticeFiles(item.Dir)
		if err != nil {
			return nil, fmt.Errorf("read module %s: %w", item.Path, err)
		}
		if len(matches) == 0 {
			return nil, fmt.Errorf("module %s has no root license or notice file", item.Path)
		}
		if item.Main {
			projectNotice := filepath.Join(item.Dir, "THIRD_PARTY_NOTICES.md")
			if _, err := os.Stat(projectNotice); err != nil {
				return nil, fmt.Errorf("read project third-party notice: %w", err)
			}
			matches = append(matches, projectNotice)
			sort.Strings(matches)
		}
		for _, path := range matches {
			version := item.Version
			if item.Main {
				version = "source"
			}
			files = append(files, noticeFile{
				modulePath: item.Path,
				version:    version,
				name:       filepath.Base(path),
				path:       path,
			})
		}
	}

	goRoot := runtime.GOROOT()
	for _, relative := range []string{"LICENSE", filepath.Join("lib", "time", "README")} {
		path := filepath.Join(goRoot, relative)
		if _, err := os.Stat(path); err != nil {
			return nil, fmt.Errorf("read Go toolchain notice %s: %w", relative, err)
		}
		files = append(files, noticeFile{
			modulePath: "Go toolchain and standard library",
			version:    runtime.Version(),
			name:       filepath.ToSlash(relative),
			path:       path,
		})
	}
	return files, nil
}

func moduleNoticeFiles(directory string) ([]string, error) {
	entries, err := os.ReadDir(directory)
	if err != nil {
		return nil, err
	}
	var paths []string
	for _, entry := range entries {
		if !entry.Type().IsRegular() {
			continue
		}
		upper := strings.ToUpper(entry.Name())
		if !strings.Contains(upper, "LICENSE") &&
			!strings.HasPrefix(upper, "COPYING") &&
			!strings.HasPrefix(upper, "NOTICE") &&
			!strings.HasPrefix(upper, "COPYRIGHT") {
			continue
		}
		paths = append(paths, filepath.Join(directory, entry.Name()))
	}
	sort.Strings(paths)
	return paths, nil
}

func writeNotices(output, version string, targets []string, files []noticeFile) error {
	file, err := os.OpenFile(output, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if err != nil {
		return err
	}
	defer file.Close()

	if _, err := fmt.Fprintf(file, "Index 01 Hook %s - Third-Party License Texts\n\n", version); err != nil {
		return err
	}
	if _, err := fmt.Fprintf(file, "Targets: %s\n\n", strings.Join(targets, ", ")); err != nil {
		return err
	}
	if _, err := io.WriteString(file, "This generated report includes root license and notice files for every Go module linked by these targets.\n"); err != nil {
		return err
	}
	if _, err := io.WriteString(file, "The container image also embeds its Certificate Authority bundle copyright file and Go time-zone data notice.\n"); err != nil {
		return err
	}

	for _, item := range files {
		content, err := os.ReadFile(item.path)
		if err != nil {
			return err
		}
		if len(content) > 4<<20 {
			return fmt.Errorf("notice file is too large: %s", item.name)
		}
		if _, err := fmt.Fprintf(file, "\n===== %s %s / %s =====\n", item.modulePath, item.version, item.name); err != nil {
			return err
		}
		if _, err := file.Write(content); err != nil {
			return err
		}
		if len(content) == 0 || content[len(content)-1] != '\n' {
			if _, err := io.WriteString(file, "\n"); err != nil {
				return err
			}
		}
	}
	return file.Sync()
}

func fatalf(format string, arguments ...any) {
	fmt.Fprintf(os.Stderr, format+"\n", arguments...)
	os.Exit(1)
}
