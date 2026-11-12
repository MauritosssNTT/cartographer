package recipetest

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/BeppeTemp/cartographer/internal/surveyor/connector"
)

// truncatedMark is what the connector set appends to a result it cut.
const truncatedMark = "\n[truncated]"

// ConnectorInput is one connector to test: the Config the Surveyor's own plugin
// loader built, and the scenario whose calls are made through it.
type ConnectorInput struct {
	Name     string // the local name, the one tests/<name>.yaml is named after
	Config   connector.Config
	Env      map[string]string // NAME the plugin declares -> the variable the Config reads it from
	Scenario *Scenario
}

// RunConnector opens the connector the way a Surveyor does (connector.Open, the
// descriptor's allowlist, the call timeout and the result cap), makes each call of
// the scenario and judges what the server offered. A stdio connector is the
// author's process; an http one is a live server, which a test never reaches.
func RunConnector(ctx context.Context, in ConnectorInput) []Result {
	x := &run{in: Input{Name: in.Name, Scenario: in.Scenario}}
	cfg := in.Config
	if cfg.Transport != connector.TransportStdio {
		x.add("connector", nil, "an http connector is a live server, which plugin test never reaches")
		return x.out
	}
	defer x.setEnv(in.Env)()
	var logs []string
	set := connector.Open(ctx, []connector.Config{cfg}, func(f string, a ...any) { logs = append(logs, fmt.Sprintf(f, a...)) })
	defer set.Close()

	var probs []string
	for i, c := range in.Scenario.Calls {
		res, err := set.Call(ctx, connector.Alias(cfg.Name)+connector.Separator+c.Tool, c.Args)
		x.add(fmt.Sprintf("call %d %s", i+1, c.Tool), judgeCall(c, res, err), "")
		x.note("call "+c.Tool, res.Text)
		if len(res.Text) > cfg.MaxResultBytes+len(truncatedMark) {
			probs = append(probs, fmt.Sprintf("call %d: the result is over max_result_bytes", i+1))
		}
	}
	tools, err := set.Tools(ctx, cfg.Name)
	if err != nil {
		probs = append(probs, err.Error())
	}
	offered := map[string]bool{}
	for _, t := range tools {
		offered[strings.TrimPrefix(t.Name, connector.Alias(cfg.Name)+connector.Separator)] = true
	}
	for _, name := range cfg.Tools {
		if err != nil || offered[name] {
			continue
		}
		// The set drops a tool with an oversize schema or a name over the cap and
		// says why on its log; a tool the server does not have is simply absent.
		why := "the server does not offer it"
		if i := slices.IndexFunc(logs, func(l string) bool { return strings.Contains(l, "tool "+name+" not offered") }); i >= 0 {
			why = logs[i]
		}
		probs = append(probs, fmt.Sprintf("tool %s is in the allowlist but is not offered: %s", name, why))
	}
	for n, v := range in.Scenario.Env {
		if len(v) >= 4 && slices.ContainsFunc(x.seen, func(s [2]string) bool { return strings.Contains(s[1], v) }) {
			probs = append(probs, "the value of "+n+" appears in a call result")
		}
	}
	slices.Sort(probs)
	x.verdict("connector", probs)
	return x.out
}

func judgeCall(c Call, res connector.Result, err error) error {
	switch {
	case err != nil && c.ExpectError:
		return nil
	case err != nil:
		return err
	case res.IsError != c.ExpectError:
		return fmt.Errorf("the result is an error: %t, expected %t", res.IsError, c.ExpectError)
	case c.ExpectTextContains != "" && !strings.Contains(res.Text, c.ExpectTextContains):
		return errors.New("the result does not contain " + fmt.Sprintf("%q", c.ExpectTextContains))
	}
	return nil
}
