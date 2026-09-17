package main

import (
	"context"
	"flag"
	"io"

	"github.com/korallis/letmecook/internal/backup"
	"github.com/korallis/letmecook/internal/store"
)

func init() { commands["restore"] = restoreCommand }

// Offline means no owner client, listener or credentials are ever opened here.
func restoreCommand(ctx context.Context, g globals, args []string) int {
	fs := flag.NewFlagSet("restore", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	source := fs.String("backup", "", "verified backup directory")
	state := fs.String("state-dir", "", "new empty private state directory")
	artifacts := fs.String("artifacts-dir", "", "new empty private artifact directory")
	if fs.Parse(args) != nil || fs.NArg() != 0 || *source == "" || *state == "" || *artifacts == "" {
		return failLocal(g, 2, "invalid_arguments", "restore requires --backup DIR --state-dir NEW --artifacts-dir NEW")
	}
	entry, err := backup.Restore(ctx, *source, *state, *artifacts)
	if err != nil {
		return failLocal(g, 1, "restore_refused", "backup must be complete and verified; targets must be distinct empty private directories; a partial restore is never resumed in place")
	}
	return emit(g, "restore", struct {
		Paused  bool               `json:"paused"`
		Restore store.RestoreEntry `json:"restore"`
	}{true, entry})
}
