package httpapi

import (
	"time"

	"github.com/google/uuid"
)

// AttachmentResponse matches the §9.7 wire shape. `url` and
// `expires_at` are filled on GET (after presigning); they're also
// filled on POST so the uploading client can immediately render the
// new attachment without a follow-up GET.
type AttachmentResponse struct {
	ID          uuid.UUID `json:"id"            example:"0192f5a3-7c1b-7a3f-9b1c-2d3e4f5a6b7c"`
	URL         string    `json:"url"           example:"https://wakeup-prod-media.s3.amazonaws.com/attachments/0192f5a3-7c1b-7a3f-9b1c-2d3e4f5a6b7c?X-Amz-..."`
	ExpiresAt   time.Time `json:"expires_at"    example:"2026-05-02T10:47:55.412Z"`
	Filename    string    `json:"filename"      example:"report.pdf"`
	ContentType string    `json:"content_type"  example:"application/pdf"`
	SizeBytes   int64     `json:"size_bytes"    example:"42313"`
}
