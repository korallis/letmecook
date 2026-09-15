// Command protocol executes public synthetic consistency fixtures, not a runner.
package main

import (
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"

	p "github.com/korallis/letmecook/schemas/execution"
)

type Suite struct {
	Current  p.Identity                 `json:"current"`
	Messages map[string]json.RawMessage `json:"messages"`
	Cases    []Case                     `json:"cases"`
}
type Case struct {
	Name        string            `json:"name"`
	Op          string            `json:"op"`
	Message     string            `json:"message"`
	Previous    string            `json:"previous"`
	Selected    string            `json:"selected"`
	Wire        *string           `json:"wire"`
	Hex         string            `json:"hex"`
	Padding     int               `json:"padding"`
	Current     *p.Identity       `json:"current"`
	State       p.AttemptState    `json:"state"`
	Revision    int64             `json:"revision"`
	Request     string            `json:"request"`
	Reply       string            `json:"reply"`
	Timing      p.Timing          `json:"timing"`
	Receipt     p.Receipt         `json:"receipt"`
	Observation p.Observation     `json:"observation"`
	Events      []p.RecoveryEvent `json:"events"`
	Expected    json.RawMessage   `json:"expected"`
	Steps       []Case            `json:"steps"`
}

func evaluate(s Suite, c Case) (any, error) {
	current := s.Current
	if c.Current != nil {
		current = *c.Current
	}
	load := func(name string) (p.Message, error) {
		data, ok := s.Messages[name]
		if !ok {
			return p.Message{}, fmt.Errorf("missing fixture message %s", name)
		}
		return p.Decode(data)
	}
	switch c.Op {
	case "trace":
		out := []any{}
		for _, step := range c.Steps {
			v, err := evaluate(s, step)
			if err != nil {
				return nil, err
			}
			out = append(out, v)
		}
		return out, nil
	case "decode":
		data := []byte(s.Messages[c.Message])
		if c.Wire != nil {
			data = []byte(*c.Wire)
		}
		if c.Hex != "" {
			var err error
			data, err = hex.DecodeString(c.Hex)
			if err != nil {
				return nil, err
			}
		}
		if c.Padding < 0 || c.Padding > p.MaxBytes+1 {
			return nil, fmt.Errorf("invalid fixture padding")
		}
		data = append(data, strings.Repeat(" ", c.Padding)...)
		_, err := p.Decode(data)
		if err != nil {
			return err.Error(), nil
		}
		return p.OK, nil
	case "current", "session", "replay", "transition", "ack":
		m, err := load(c.Message)
		if err != nil {
			return err.Error(), nil
		}
		switch c.Op {
		case "current":
			return p.CheckCurrent(m, current), nil
		case "session":
			return p.CheckSession(m, current, c.Selected), nil
		case "replay":
			previous, err := load(c.Previous)
			if err != nil {
				return err.Error(), nil
			}
			return p.CheckReplay(m, previous, current), nil
		case "transition":
			return p.CheckTransition(m, current, c.State, c.Revision), nil
		case "ack":
			ack, err := load(c.Reply)
			if err != nil {
				return err.Error(), nil
			}
			return p.CheckAck(m, ack, current, c.Receipt), nil
		}
	case "lease":
		request, err := load(c.Request)
		if err != nil {
			return err.Error(), nil
		}
		reply, err := load(c.Reply)
		if err != nil {
			return err.Error(), nil
		}
		return p.CheckLease(request, reply, current, c.Timing), nil
	case "observe":
		state := c.Observation
		trace := []p.Observation{}
		for _, event := range c.Events {
			var err error
			state, err = p.Observe(state, event)
			if err != nil {
				return err.Error(), nil
			}
			trace = append(trace, state)
		}
		return trace, nil
	}
	return nil, fmt.Errorf("unknown fixture op %q", c.Op)
}
func execute(s Suite) ([]any, error) {
	out := []any{}
	for _, c := range s.Cases {
		value, err := evaluate(s, c)
		if err != nil {
			return nil, err
		}
		out = append(out, value)
	}
	return out, nil
}
func main() {
	data, err := io.ReadAll(io.LimitReader(os.Stdin, 1048577))
	if err != nil || len(data) > 1048576 {
		fmt.Fprintln(os.Stderr, "invalid fixture input")
		os.Exit(1)
	}
	var suite Suite
	if err := json.Unmarshal(data, &suite); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	out, err := execute(suite)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	if err := json.NewEncoder(os.Stdout).Encode(out); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
