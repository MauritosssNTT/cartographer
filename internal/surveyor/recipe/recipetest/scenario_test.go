package recipetest

import (
	"os"
	"path/filepath"
	"testing"
)

func TestReadFileHoldsTheScenarioToItsShape(t *testing.T) {
	dir := t.TempDir()
	write := func(name, s string) {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(s), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write("page.json", `["a"]`)
	write("ok.yaml", "env:\n  ROOT: \"{tests}/x\"\nhttp:\n  - method: GET\n    url: https://wiki.example.com/\n    response:\n      body_file: page.json\nsteps:\n  - verb: list\n")
	sc, err := ReadFile(dir, "ok")
	abs, _ := filepath.Abs(dir)
	if err != nil || sc.HTTP[0].Response.Body != `["a"]` || sc.Env["ROOT"] != abs+"/x" {
		t.Fatalf("%+v %v", sc, err)
	}
	for name, doc := range map[string]string{
		"nolist":  "steps:\n  - verb: check\n",
		"verb":    "steps:\n  - verb: sync\n",
		"unknown": "stepz: []\n",
		"escape":  "http:\n  - method: GET\n    url: https://wiki.example.com/\n    response:\n      body_file: ../x\nsteps:\n  - verb: list\n",
	} {
		write(name+".yaml", doc)
		if _, err := ReadFile(dir, name); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}
