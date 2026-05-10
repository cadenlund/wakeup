package main

import (
	"bytes"
	"encoding/json"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// minimalPDFSmoke is the PDF magic + minimal trailer. http.DetectContentType
// reads the magic and returns application/pdf without needing a parser.
var minimalPDFSmoke = []byte("%PDF-1.4\n%EOF\n")

// TestSmoke_AttachmentsGoldenPath drives every step listed in §16
// milestone 7.5 in the documented order:
//
//	register alice + bob + carol
//	→ alice uploads 51 MiB PDF → 413
//	→ alice uploads PE/EXE-like body → 422
//	→ alice uploads normal PDF → 201 with presigned URL
//	→ alice GETs the orphan → 200 with presigned URL
//	→ carol GETs as non-member → 404 (no enumeration)
//	→ alice creates a direct with bob, sends a message linking the
//	  attachment → bob can GET → 200; carol still 404
//
// This is the docs-equivalent of clicking through the §6.2 attachment
// endpoints in Swagger UI by hand.
func TestSmoke_AttachmentsGoldenPath(t *testing.T) {
	t.Parallel()
	srv, _, _ := productionLikeServer(t)

	// 1) Register alice + bob + carol.
	aliceUsername := "alice" + uniqueSuffix(t)
	bobUsername := "bob" + uniqueSuffix(t)
	carolUsername := "carol" + uniqueSuffix(t)

	alice := registerSmoke(t, srv, aliceUsername)
	bob := registerSmoke(t, srv, bobUsername)
	carol := registerSmoke(t, srv, carolUsername)

	bobID := whoami(t, srv, bob)
	_ = carol

	// 2) 51 MiB upload → 413.
	tooBig := make([]byte, 51<<20)
	copy(tooBig, minimalPDFSmoke)
	resp := smokeUpload(t, alice, srv.URL+"/v1/attachments", "huge.pdf", tooBig)
	t.Cleanup(func() { _ = resp.Body.Close() })
	if resp.StatusCode != http.StatusRequestEntityTooLarge {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("51 MiB status=%d body=%s, want 413", resp.StatusCode, body)
	}
	_ = resp.Body.Close()

	// 3) Disallowed MIME (PE/EXE-like magic) → 422. Demonstrates the
	// server-side MIME detection catching a body that doesn't match the
	// §9.2 allowlist regardless of what the filename claims.
	respMIME := smokeUpload(t, alice, srv.URL+"/v1/attachments", "evil.png",
		[]byte{0x4D, 0x5A, 0x90, 0x00, 0x03, 0x00, 0x00, 0x00, 0x04})
	if respMIME.StatusCode != http.StatusUnprocessableEntity {
		body, _ := io.ReadAll(respMIME.Body)
		_ = respMIME.Body.Close()
		t.Fatalf("disallowed MIME status=%d body=%s, want 422", respMIME.StatusCode, body)
	}
	_ = respMIME.Body.Close()

	// 4) Normal PDF upload → 201 with the §9.7 DTO including a presigned URL.
	resp201 := smokeUpload(t, alice, srv.URL+"/v1/attachments", "report.pdf", minimalPDFSmoke)
	defer func() { _ = resp201.Body.Close() }()
	if resp201.StatusCode != http.StatusCreated {
		body, _ := io.ReadAll(resp201.Body)
		t.Fatalf("PDF upload status=%d body=%s, want 201", resp201.StatusCode, body)
	}
	created := smokeMustDecode(t, resp201.Body)
	attID := smokeAsString(t, created, "id")
	if smokeAsString(t, created, "content_type") != "application/pdf" {
		t.Errorf("content_type = %v, want application/pdf", created["content_type"])
	}
	if smokeAsString(t, created, "filename") != "report.pdf" {
		t.Errorf("filename = %v, want report.pdf", created["filename"])
	}
	uploadedURL := smokeAsString(t, created, "url")
	if !strings.Contains(uploadedURL, "response-content-disposition") {
		t.Errorf("upload URL missing response-content-disposition: %s", uploadedURL)
	}

	// 5) Alice GETs the orphan (uploader-before-link case) → 200.
	getResp, err := alice.Get(srv.URL + "/v1/attachments/" + attID)
	if err != nil {
		t.Fatalf("alice GET: %v", err)
	}
	t.Cleanup(func() { _ = getResp.Body.Close() })
	if getResp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(getResp.Body)
		t.Fatalf("alice GET orphan status=%d body=%s", getResp.StatusCode, body)
	}
	got := smokeMustDecode(t, getResp.Body)
	if smokeAsString(t, got, "id") != attID {
		t.Errorf("orphan GET id mismatch")
	}

	// 6) Carol (non-member, non-uploader) GETs the orphan → 404, no leak.
	carolPeek, err := carol.Get(srv.URL + "/v1/attachments/" + attID)
	if err != nil {
		t.Fatalf("carol GET: %v", err)
	}
	t.Cleanup(func() { _ = carolPeek.Body.Close() })
	if carolPeek.StatusCode != http.StatusNotFound {
		body, _ := io.ReadAll(carolPeek.Body)
		t.Fatalf("carol orphan GET status=%d body=%s, want 404", carolPeek.StatusCode, body)
	}

	// 7) Alice creates a direct with bob, sends a message linking the
	// attachment → bob can now GET, carol still 404. Friend the pair
	// first or the create returns 403 (friends-only DM enforcement).
	makeFriendsSmoke(t, srv, alice, bob, bobUsername)
	directRow := mustPostJSON(t, alice, srv.URL+"/v1/conversations", map[string]any{
		"type": "direct", "member_ids": []string{bobID},
	}, http.StatusCreated)
	directID := smokeAsString(t, directRow, "id")
	_ = mustPostJSON(t, alice, srv.URL+"/v1/conversations/"+directID+"/messages",
		map[string]any{"body": "see attached", "attachment_ids": []string{attID}},
		http.StatusCreated)

	bobResp, err := bob.Get(srv.URL + "/v1/attachments/" + attID)
	if err != nil {
		t.Fatalf("bob GET: %v", err)
	}
	t.Cleanup(func() { _ = bobResp.Body.Close() })
	if bobResp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(bobResp.Body)
		t.Fatalf("bob GET linked status=%d body=%s", bobResp.StatusCode, body)
	}

	carolPeekAfter, err := carol.Get(srv.URL + "/v1/attachments/" + attID)
	if err != nil {
		t.Fatalf("carol GET after link: %v", err)
	}
	t.Cleanup(func() { _ = carolPeekAfter.Body.Close() })
	if carolPeekAfter.StatusCode != http.StatusNotFound {
		body, _ := io.ReadAll(carolPeekAfter.Body)
		t.Fatalf("carol GET after-link status=%d body=%s, want 404", carolPeekAfter.StatusCode, body)
	}
}

// --- helpers ---------------------------------------------------------

// smokeUpload posts a multipart `file` part. Same shape as the
// handler-test uploadAttachment, kept local to cmd/server so the smoke
// suite stays self-contained.
func smokeUpload(t *testing.T, c *http.Client, urlStr, filename string, data []byte) *http.Response {
	t.Helper()
	buf := &bytes.Buffer{}
	mw := multipart.NewWriter(buf)
	part, err := mw.CreateFormFile("file", filename)
	if err != nil {
		t.Fatalf("create form file: %v", err)
	}
	if _, err := part.Write(data); err != nil {
		t.Fatalf("write part: %v", err)
	}
	if err := mw.Close(); err != nil {
		t.Fatalf("close mw: %v", err)
	}
	req, _ := http.NewRequest(http.MethodPost, urlStr, buf)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	resp, err := c.Do(req)
	if err != nil {
		t.Fatalf("upload: %v", err)
	}
	return resp
}

// smokeMustDecode + smokeAs* mirror the JSON-safe-cast helpers used in
// message_smoke_test.go (CodeRabbit PR #41) so a wire-shape change
// surfaces as t.Fatalf with the offending type instead of a runtime
// panic from .(map[string]any).
func smokeMustDecode(t *testing.T, body io.Reader) map[string]any {
	t.Helper()
	var got map[string]any
	if err := json.NewDecoder(body).Decode(&got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	return got
}

func smokeAsString(t *testing.T, m map[string]any, key string) string {
	t.Helper()
	v, ok := m[key]
	if !ok {
		t.Fatalf("missing %q in %#v", key, m)
	}
	s, ok := v.(string)
	if !ok {
		t.Fatalf("%q: expected string, got %T (%#v)", key, v, v)
	}
	return s
}

// (httptest.Server is imported transitively via productionLikeServer's
// signature; keep the explicit import so future smoke helpers can take
// it as a parameter without churn.)
var _ = httptest.Server{}
