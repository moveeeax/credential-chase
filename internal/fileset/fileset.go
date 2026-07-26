// Package fileset resolves a mix of file and directory arguments into a
// sorted list of YAML files.
package fileset

import (
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// YAML walks the given paths and returns every *.yaml / *.yml file below
// them, sorted. A path that is a file is taken as-is, whatever its
// extension, so `credchase lint one-facility.txt` still does something
// useful instead of silently checking nothing.
func YAML(paths []string) ([]string, error) {
	var files []string
	for _, p := range paths {
		info, err := os.Stat(p)
		if err != nil {
			return nil, err
		}
		if !info.IsDir() {
			files = append(files, p)
			continue
		}
		err = filepath.WalkDir(p, func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() {
				if d.Name() != "." && strings.HasPrefix(d.Name(), ".") {
					return filepath.SkipDir
				}
				return nil
			}
			switch strings.ToLower(filepath.Ext(path)) {
			case ".yaml", ".yml":
				files = append(files, path)
			}
			return nil
		})
		if err != nil {
			return nil, err
		}
	}
	sort.Strings(files)
	return files, nil
}
