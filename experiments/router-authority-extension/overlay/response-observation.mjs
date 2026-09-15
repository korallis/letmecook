// Diagnostics only: neither MIME classification nor body-stream presence is
// original terminal evidence. Never persist or reflect header/body strings.
export function observeResponse(response) {
  const value = response.headers.get('content-type');
  const mime = value?.split(';', 1)[0].trim().toLowerCase();
  const mediaType = value === null ? 'missing' : mime === 'text/event-stream' ? 'sse' : mime === 'application/json' ? 'json' : mime === 'text/html' ? 'html' : 'other';
  if (!Number.isSafeInteger(response.status) || response.status < 100 || response.status > 599) throw Error('unsupported_response_status');
  return {status: response.status, mediaType, bodyPresent: response.body !== null};
}

export function responseObservations(db) {
  // A separate table leaves existing operation/receipt bytes and terminal
  // semantics intact. An absent row is unavailable, including on old receipts.
  db.exec(`CREATE TABLE IF NOT EXISTS gaffer_response_observations (
    request_id TEXT NOT NULL, ordinal INTEGER NOT NULL,
    status INTEGER NOT NULL CHECK(typeof(status)='integer' AND status BETWEEN 100 AND 599),
    media_type TEXT NOT NULL CHECK(media_type IN ('sse','json','html','missing','other')),
    body_present INTEGER NOT NULL CHECK(body_present IN (0,1)),
    PRIMARY KEY(request_id, ordinal),
    FOREIGN KEY(request_id, ordinal) REFERENCES gaffer_operations(request_id, ordinal));`);
  return Object.freeze({
    record(requestId, ordinal, response) {
      const observation = observeResponse(response);
      db.transaction(() => {
        // No upsert: a second observation cannot overwrite the first response.
        db.run('INSERT INTO gaffer_response_observations VALUES(?, ?, ?, ?, ?)', [requestId, ordinal, observation.status, observation.mediaType, Number(observation.bodyPresent)]);
      });
    },
    read(requestId, ordinal) {
      const row = db.get('SELECT status,media_type,body_present FROM gaffer_response_observations WHERE request_id=? AND ordinal=?', [requestId, ordinal]);
      return row ? {status: row.status, mediaType: row.media_type, bodyPresent: row.body_present === 1} : null;
    }
  });
}
