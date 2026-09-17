package main

import (
	"context"
	"errors"
	"flag"
	"io"
	"os"
	"os/signal"
	"syscall"

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
	// Offline filesystem recovery is not an HTTP request. Remove the global
	// request deadline, but retain explicit operator interruption via signals.
	ctx, stop := signal.NotifyContext(context.WithoutCancel(ctx), os.Interrupt, syscall.SIGTERM)
	defer stop()
	entry, err := backup.Restore(ctx, *source, *state, *artifacts)
	if err != nil {
		return failLocal(g, 1, "restore_refused", restoreErrorClass(err)+": backup must be complete and verified; targets must be distinct empty private directories; a partial restore is never resumed in place")
	}
	return emit(g, "restore", struct {
		Paused  bool               `json:"paused"`
		Restore store.RestoreEntry `json:"restore"`
	}{true, entry})
}

// Return only bounded known classes, never raw paths, SQLite text or payloads.
func restoreErrorClass(err error) string {
	switch {
	case errors.Is(err, context.Canceled):
		return "interrupted"
	case errors.Is(err, context.DeadlineExceeded):
		return "deadline_exceeded"
	case errors.Is(err, syscall.ENOSPC):
		return "no_space"
	case errors.Is(err, os.ErrPermission):
		return "permission_denied"
	case errors.Is(err, os.ErrNotExist):
		return "missing_file"
	}
	for err != nil {
		switch err.Error() {
		case "digest_mismatch", "target_not_empty", "invalid_destination", "overlapping_paths", "target_not_private", "unsafe_directory", "unsafe_file", "source_changed", "invalid_manifest", "invalid_manifest_shape", "invalid_inventory", "invalid_inventory_path", "invalid_inventory_kind", "incompatible_database", "database_integrity", "database_foreign_keys", "invalid_snapshot_state", "snapshot_identity_mismatch", "snapshot_attempt_mismatch", "invalid_artifact_manifest", "artifact_reference_mismatch", "artifact_reference_missing", "invalid_stream", "stream_identity_mismatch", "stream_watermark_missing", "restore_wal_not_checkpointed", "restore_history_missing":
			return err.Error()
		}
		if joined, ok := err.(interface{ Unwrap() []error }); ok {
			for _, child := range joined.Unwrap() {
				if class := restoreErrorClass(child); class != "restore_failed" {
					return class
				}
			}
			break
		}
		err = errors.Unwrap(err)
	}
	return "restore_failed"
}
