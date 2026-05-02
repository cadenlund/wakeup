package domain

import (
	"time"

	"github.com/google/uuid"
)

// Attachment mirrors a row in the `attachments` table (migration 0006).
//
// `storage_key` is the opaque key used by the object store (§9.1 layout
// is `attachments/<uuid>/<sanitized_filename>`); `content_type` is the
// MIME the SERVER detected from the first 512 bytes (§9.2) — never what
// the client claimed in the upload form.
type Attachment struct {
	ID          uuid.UUID
	UploaderID  uuid.UUID
	StorageKey  string
	Filename    string
	ContentType string
	SizeBytes   int64
	CreatedAt   time.Time
}
