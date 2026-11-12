package repodocs

import (
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// The README film is re-rendered only when the hash of film/inputs.sh changes (D1068). A file the film reaches,
// directly or through another file's imports, that is not in that list would let a change to it break the render
// with no render triggered; a listed path that no longer exists makes film-needed.sh fail on every run.
func TestFilmImportsStayInsideTheHashedInputs(t *testing.T) {
	root := repoRoot(t)
	list := regexp.MustCompile(`(?s)FILM_INPUTS=\((.*?)\)`).FindStringSubmatch(readFile(t, root, "film/inputs.sh"))
	if list == nil {
		t.Fatal("film/inputs.sh: no FILM_INPUTS array")
	}
	inputs := strings.Fields(list[1])
	for _, in := range inputs {
		if _, err := os.Stat(filepath.Join(root, filepath.FromSlash(in))); err != nil {
			t.Errorf("film/inputs.sh lists %s, which does not exist: %v", in, err)
		}
	}

	// Walk every code file under film/ and follow relative imports transitively: each file reached must be
	// an input, and each code file reached is read in turn.
	var queue []string
	err := filepath.WalkDir(filepath.Join(root, "film"), func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() && (d.Name() == "out" || d.Name() == "node_modules") {
			return filepath.SkipDir
		}
		if !d.IsDir() && isFilmCode(p) {
			rel, _ := filepath.Rel(root, p)
			queue = append(queue, filepath.ToSlash(rel))
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	for len(queue) > 0 {
		file := queue[0]
		queue = queue[1:]
		if seen[file] {
			continue
		}
		seen[file] = true
		for _, spec := range importTargets(readFile(t, root, file)) {
			if !strings.HasPrefix(spec, ".") {
				continue // a package: the lockfile is an input
			}
			target := resolveImport(root, path.Join(path.Dir(file), spec))
			if target == "" {
				t.Errorf("%s imports %s, which resolves to no file", file, spec)
				continue
			}
			if !inFilmInputs(inputs, target) {
				t.Errorf("%s imports %s (%s), outside film/inputs.sh: add it there, or a change to it will not re-render the film", file, spec, target)
			}
			if isFilmCode(target) {
				queue = append(queue, target)
			}
		}
	}
}

// The films are the lines of film/films.txt, the one list render.sh, record.mjs and the Vite config read. A film's
// page is film/<name>.html, except the launch film's, which is film/index.html (its URL is the server's root and
// its file keeps the name the README embeds). A page nobody lists is never rendered; a listed film with no page fails
// the render on the release candidate, so both are caught here, with no browser.
func TestFilmPagesAreTheFilmList(t *testing.T) {
	root := repoRoot(t)
	var films []string
	seen := map[string]bool{}
	for _, name := range strings.Fields(readFile(t, root, "film/films.txt")) {
		if !regexp.MustCompile(`^[a-z][a-z0-9]*(-[a-z0-9]+)*$`).MatchString(name) {
			t.Errorf("film/films.txt: %q is not a film name (lower-case words joined by -)", name)
		}
		if seen[name] {
			t.Errorf("film/films.txt lists %s twice", name)
		}
		seen[name] = true
		films = append(films, name)
	}
	if !seen["launch"] {
		t.Error("film/films.txt does not list launch: the README embeds its film")
	}

	pages := map[string]bool{}
	for _, name := range films {
		page := name + ".html"
		if name == "launch" {
			page = "index.html"
		}
		pages[page] = true
		src := readFile(t, root, "film/"+page)
		if !strings.Contains(src, `id="stage"`) {
			t.Errorf("film/%s has no #stage element, which film/src/kit.ts scales and the films append to", page)
		}
		entry := regexp.MustCompile(`<script type="module" src="/src/([^"]+)"`).FindStringSubmatch(src)
		if entry == nil {
			t.Errorf("film/%s loads no module script from /src", page)
			continue
		}
		if _, err := os.Stat(filepath.Join(root, "film", "src", filepath.FromSlash(entry[1]))); err != nil {
			t.Errorf("film/%s loads /src/%s, which does not exist: %v", page, entry[1], err)
		}
	}
	html, err := filepath.Glob(filepath.Join(root, "film", "*.html"))
	if err != nil {
		t.Fatal(err)
	}
	for _, h := range html {
		if name := filepath.Base(h); !pages[name] {
			t.Errorf("film/%s is a page of no film in film/films.txt: list the film there or remove the page", name)
		}
	}

	// The three programs that act on the list read the file instead of repeating it.
	for _, f := range []string{"film/vite.config.ts", "film/render.sh", "film/record.mjs"} {
		if !strings.Contains(readFile(t, root, f), "films.txt") {
			t.Errorf("%s does not read film/films.txt: a film added there would be skipped by it", f)
		}
	}
}

func isFilmCode(p string) bool {
	switch filepath.Ext(p) {
	case ".ts", ".tsx", ".js", ".mjs":
		return true
	}
	return false
}

// resolveImport maps an import path without its query to the repository file it names, as a bundler would:
// the exact file, then with an extension, then as a directory's index. "" when none exists.
func resolveImport(root, base string) string {
	for _, c := range []string{base, base + ".ts", base + ".tsx", base + ".js", base + ".mjs", base + "/index.ts", base + "/index.js"} {
		if st, err := os.Stat(filepath.Join(root, filepath.FromSlash(c))); err == nil && !st.IsDir() {
			return c
		}
	}
	return ""
}

var (
	// from "x" after any import/export clause, over lines (a clause often spans several); `import x = ` is not TS ESM.
	importFrom = regexp.MustCompile(`\bfrom\s*["']([^"'\n]+)["']`)
	// import "x": the side-effect form, which has no `from`.
	importBare = regexp.MustCompile(`\bimport\s*["']([^"'\n]+)["']`)
	// import("x"): the dynamic form.
	importDynamic = regexp.MustCompile(`\bimport\s*\(\s*["']([^"'\n]+)["']\s*\)`)
)

// importTargets lists the module specifiers a TS/JS source imports (static, side-effect, dynamic, re-export),
// with any ?query (?raw, ?url) removed.
func importTargets(src string) []string {
	var out []string
	for _, re := range []*regexp.Regexp{importFrom, importBare, importDynamic} {
		for _, m := range re.FindAllStringSubmatch(src, -1) {
			spec := m[1]
			if i := strings.IndexAny(spec, "?#"); i >= 0 {
				spec = spec[:i]
			}
			out = append(out, spec)
		}
	}
	return out
}

func TestImportTargets(t *testing.T) {
	for name, tc := range map[string]struct {
		src  string
		want []string
	}{
		"named":      {`import { a } from "../x";`, []string{"../x"}},
		"multi-line": {"import {\n  a,\n  b,\n} from '../x/y'", []string{"../x/y"}},
		"type":       {`import type { T } from "./t"`, []string{"./t"}},
		"re-export":  {"export {\n a\n} from \"./r\"", []string{"./r"}},
		"side":       {`import "./side.css";`, []string{"./side.css"}},
		"dynamic":    {`const m = await import( "./lazy" );`, []string{"./lazy"}},
		"raw":        {`import s from "./a.glsl?raw";`, []string{"./a.glsl"}},
		"url":        {`import u from "../l.svg?url"`, []string{"../l.svg"}},
		"package":    {`import * as THREE from "three";`, []string{"three"}},
		"none":       {`const from = "x"; // import is a word`, nil},
	} {
		got := importTargets(tc.src)
		if strings.Join(got, ",") != strings.Join(tc.want, ",") {
			t.Errorf("%s: importTargets = %v, want %v", name, got, tc.want)
		}
	}
}

func TestInFilmInputs(t *testing.T) {
	inputs := []string{"film", "web/src/lib/graph3d", "web/src/lib/palette.ts", "docs/brand/coordinate-pine.svg"}
	for target, want := range map[string]bool{
		"film/src/data":                   true,
		"web/src/lib/graph3d/scene":       true,
		"web/src/lib/palette":             true,
		"docs/brand/coordinate-pine.svg":  true,
		"web/src/lib/graph3d-extra/other": false,
		"web/src/lib/api":                 false,
		"web/src/lib/palette-other":       false,
		"docs/brand/cartographer.css":     false,
		"film-other/main":                 false,
	} {
		if got := inFilmInputs(inputs, target); got != want {
			t.Errorf("inFilmInputs(%q) = %v, want %v", target, got, want)
		}
	}
}

// inFilmInputs reports whether an import target, as written (no extension), is one of the inputs or under one.
func inFilmInputs(inputs []string, target string) bool {
	for _, in := range inputs {
		if target == in || strings.HasPrefix(target, in+"/") || strings.HasPrefix(in, target+".") {
			return true
		}
	}
	return false
}
