package store

// Upload seam (docs/decisions/0002 §4, routes /x/v1/attempts/{id}/uploads,
// /x/v1/uploads/{id}/blobs/{sha256} and /x/v1/uploads/{id}/commit). S1 (#115)
// replaces the bodies in this file only; until then every method returns
// ErrNotImplemented. Blobs stage under <artifacts-dir>/upload/<upload_id>/<sha256>;
// CommitUpload hands the staged files to CustodyResult, which is the only custody
// path. Custody never terminalizes an attempt; FinalizeAttempt does.

import (
	"context"
	"io"

	p "github.com/korallis/letmecook/schemas/execution"
)

// UploadBegin is the POST /x/v1/attempts/{id}/uploads body. Manifest is the raw
// canonical manifest; its JSON form is the wire's manifest_base64 string.
type UploadBegin struct {
	Version   string    `json:"version"`
	MessageID string    `json:"message_id"`
	Result    p.Message `json:"result"`
	Manifest  []byte    `json:"manifest_base64"`
}

// MissingBlob names one manifest blob the daemon does not hold yet.
type MissingBlob struct {
	SHA256 string `json:"sha256"`
	Bytes  int64  `json:"bytes"`
}

// UploadSession is the reply to UploadBegin and mirrors the upload_sessions row.
// Missing is the exact inventory still to PUT; BytesAllowed is the remaining
// per-attempt allowance (256 MiB aggregate).
type UploadSession struct {
	UploadID     string        `json:"upload_id"`
	Missing      []MissingBlob `json:"missing,omitempty"`
	BytesAllowed int64         `json:"bytes_allowed"`
}

// UploadedBlob is the reply to one blob PUT. Duplicate reports 200 instead of 201.
type UploadedBlob struct {
	SHA256    string `json:"sha256"`
	Bytes     int64  `json:"bytes"`
	Duplicate bool   `json:"duplicate"`
}

// CommitReply is the POST /x/v1/uploads/{id}/commit reply: the custody
// acknowledgement and receipt, byte-identical on replay.
type CommitReply struct {
	Ack         p.Message `json:"ack"`
	Receipt     p.Receipt `json:"receipt"`
	Quarantined bool      `json:"quarantined"`
}

// BeginUpload binds result.identity, the manifest hash and the blob inventory
// before any bytes arrive; the attempt must be result_pending and owned by the
// runner. Durable point: the upload_sessions and upload_blobs rows committed
// before the reply; the same message_id replays the same session.
func (s *Store) BeginUpload(ctx context.Context, fingerprint, session, attemptID string, begin UploadBegin) (UploadSession, error) {
	return UploadSession{}, ErrNotImplemented
}

// RecordUploadedBlob stores one blob of exactly length bytes (0..64 MiB) from
// body. Durable point: the temp file is hashed while written, fsynced, renamed to
// its digest name and the directory fsynced, then the upload_blobs row is marked
// staged. A digest mismatch removes the temp file (422 digest_mismatch); a
// duplicate returns Duplicate; exceeding 256 MiB per attempt is refused (413).
func (s *Store) RecordUploadedBlob(ctx context.Context, fingerprint, session, uploadID, sha256 string, body io.Reader, length int64) (UploadedBlob, error) {
	return UploadedBlob{}, ErrNotImplemented
}

// CommitUpload verifies the inventory is complete (else upload_incomplete listing
// the missing digests), builds CustodyRequest{Sources: staged, Fingerprint} and
// calls CustodyResult. Durable point: custody promotion and the metadata commit;
// a lost reply returns the byte-identical acknowledgement (result_ack message_id
// is dispatchID(receipt_id, "ack")). The upload session becomes committed.
func (s *Store) CommitUpload(ctx context.Context, fingerprint, session, uploadID, messageID string) (CommitReply, error) {
	return CommitReply{}, ErrNotImplemented
}
