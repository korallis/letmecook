package main

import (
	"encoding/json"
	"os"
	"reflect"
	"testing"

	p "github.com/korallis/letmecook/schemas/execution"
)

func TestSharedFixtures(t *testing.T) {
	data, err := os.ReadFile("data/cases.json")
	if err != nil {
		t.Fatal(err)
	}
	var suite Suite
	if err := json.Unmarshal(data, &suite); err != nil {
		t.Fatal(err)
	}
	for _, c := range suite.Cases {
		t.Run(c.Name, func(t *testing.T) {
			actual, err := evaluate(suite, c)
			if err != nil {
				t.Fatal(err)
			}
			data, err := json.Marshal(actual)
			if err != nil {
				t.Fatal(err)
			}
			var got, want any
			if err := json.Unmarshal(data, &got); err != nil {
				t.Fatal(err)
			}
			if err := json.Unmarshal(c.Expected, &want); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("got %s, want %s", data, c.Expected)
			}
		})
	}
}

func TestTypedBoundaries(t *testing.T) {
	if got := p.CheckCurrent(p.Message{}, p.Identity{}); got != p.Malformed {
		t.Fatal(got)
	}
	if got := p.CheckReplay(p.Message{}, p.Message{}, p.Identity{}); got != p.Malformed {
		t.Fatal(got)
	}
	if got := p.CheckTransition(p.Message{}, p.Identity{}, p.Assigned, 1); got != p.Malformed {
		t.Fatal(got)
	}
	if got := p.CheckLease(p.Message{}, p.Message{}, p.Identity{}, p.Timing{}); got.Reason != p.Malformed {
		t.Fatal(got)
	}
	if got := p.CheckAck(p.Message{}, p.Message{}, p.Identity{}, p.Receipt{}); got != p.Malformed {
		t.Fatal(got)
	}
	if _, err := p.Observe(p.Observation{}, "partition"); err != p.Malformed {
		t.Fatal(err)
	}
}
