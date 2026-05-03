package httpapi_test

import (
	"bytes"
	"encoding/json"
	"io"
	"mime/multipart"
	"net/http"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/cadenlund/wakeup/apps/backend/internal/apierror"
	"github.com/cadenlund/wakeup/apps/backend/internal/testutil"
)

// minimalPDF is the PDF magic + minimal trailer so http.DetectContentType
// returns application/pdf without needing a real document.
var minimalPDF = []byte("%PDF-1.4\n%EOF\n")

// uploadAttachment posts a multipart `file` part and returns the response.
func uploadAttachment(t *testing.T, c *http.Client, urlStr, filename string, data []byte) *http.Response {
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

// requireUploadAttachment uploads a PDF and returns the created
// attachment id as a string. Asserts 201.
func requireUploadAttachment(t *testing.T, h *testutil.Harness, c *http.Client) string {
	t.Helper()
	resp := uploadAttachment(t, c, h.Server.URL+"/v1/attachments", "report.pdf", minimalPDF)
	rb, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("upload status=%d body=%s", resp.StatusCode, rb)
	}
	var got map[string]any
	if err := json.Unmarshal(rb, &got); err != nil {
		t.Fatalf("decode: %v\nbody=%s", err, rb)
	}
	id, _ := got["id"].(string)
	if id == "" {
		t.Fatalf("missing id: %s", rb)
	}
	return id
}

// --- POST /v1/attachments ---------------------------------------------

func TestUploadAttachment_PDFSuccess(t *testing.T) {
	t.Parallel()
	h := testutil.New(t)
	c, _ := h.AuthClient(t)

	resp := uploadAttachment(t, c, h.Server.URL+"/v1/attachments", "report.pdf", minimalPDF)
	t.Cleanup(func() { _ = resp.Body.Close() })
	if resp.StatusCode != http.StatusCreated {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("status=%d body=%s", resp.StatusCode, body)
	}
	got := mustDecode(t, resp.Body)
	if got["content_type"] != "application/pdf" {
		t.Errorf("content_type = %v, want application/pdf", got["content_type"])
	}
	if got["filename"] != "report.pdf" {
		t.Errorf("filename = %v, want report.pdf", got["filename"])
	}
	url, _ := got["url"].(string)
	if url == "" || !strings.Contains(url, "response-content-disposition") {
		t.Errorf("url should contain presigned response-content-disposition: %q", url)
	}
}

func TestUploadAttachment_PNGSuccess(t *testing.T) {
	t.Parallel()
	h := testutil.New(t)
	c, _ := h.AuthClient(t)

	resp := uploadAttachment(t, c, h.Server.URL+"/v1/attachments", "screenshot.png", minimalPNG)
	t.Cleanup(func() { _ = resp.Body.Close() })
	if resp.StatusCode != http.StatusCreated {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("status=%d body=%s", resp.StatusCode, body)
	}
	got := mustDecode(t, resp.Body)
	if got["content_type"] != "image/png" {
		t.Errorf("content_type = %v, want image/png", got["content_type"])
	}
}

// MIME-detection lie: client sends a PDF body but names it .png. Server
// must record application/pdf (the detected MIME), not anything from
// the filename.
func TestUploadAttachment_DetectedMIMENotFilename(t *testing.T) {
	t.Parallel()
	h := testutil.New(t)
	c, _ := h.AuthClient(t)

	resp := uploadAttachment(t, c, h.Server.URL+"/v1/attachments", "fake.png", minimalPDF)
	t.Cleanup(func() { _ = resp.Body.Close() })
	if resp.StatusCode != http.StatusCreated {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("status=%d body=%s", resp.StatusCode, body)
	}
	got := mustDecode(t, resp.Body)
	if got["content_type"] != "application/pdf" {
		t.Errorf("content_type = %v, want application/pdf (server detection)", got["content_type"])
	}
}

func TestUploadAttachment_RejectsBadMIME(t *testing.T) {
	t.Parallel()
	h := testutil.New(t)
	c, _ := h.AuthClient(t)
	// PE/EXE-like magic — http.DetectContentType returns octet-stream.
	body := []byte{0x4D, 0x5A, 0x90, 0x00, 0x03, 0x00, 0x00, 0x00, 0x04}
	resp := uploadAttachment(t, c, h.Server.URL+"/v1/attachments", "evil.bin", body)
	t.Cleanup(func() { _ = resp.Body.Close() })
	assertCode(t, resp, http.StatusUnprocessableEntity, apierror.CodeValidation)
}

func TestUploadAttachment_RejectsTooLarge(t *testing.T) {
	t.Parallel()
	h := testutil.New(t)
	c, _ := h.AuthClient(t)

	// 51 MiB — past the 50 MiB cap. The MaxBytesReader catches this in
	// ParseMultipartForm before the service ever sees a stream.
	big := make([]byte, 51<<20)
	copy(big, minimalPDF)
	resp := uploadAttachment(t, c, h.Server.URL+"/v1/attachments", "big.pdf", big)
	t.Cleanup(func() { _ = resp.Body.Close() })
	assertCode(t, resp, http.StatusRequestEntityTooLarge, apierror.CodePayloadTooLarge)
}

func TestUploadAttachment_MissingFile(t *testing.T) {
	t.Parallel()
	h := testutil.New(t)
	c, _ := h.AuthClient(t)

	buf := &bytes.Buffer{}
	mw := multipart.NewWriter(buf)
	_ = mw.Close()
	req, _ := http.NewRequest(http.MethodPost, h.Server.URL+"/v1/attachments", buf)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	resp, err := c.Do(req)
	if err != nil {
		t.Fatalf("upload: %v", err)
	}
	t.Cleanup(func() { _ = resp.Body.Close() })
	assertCode(t, resp, http.StatusBadRequest, apierror.CodeBadRequest)
}

func TestUploadAttachment_Unauthenticated(t *testing.T) {
	t.Parallel()
	h := testutil.New(t)
	c := h.HTTPClient(t)
	resp := uploadAttachment(t, c, h.Server.URL+"/v1/attachments", "x.pdf", minimalPDF)
	t.Cleanup(func() { _ = resp.Body.Close() })
	assertCode(t, resp, http.StatusUnauthorized, apierror.CodeUnauthorized)
}

// --- GET /v1/attachments/{id} -----------------------------------------

func TestGetAttachment_OrphanByUploader(t *testing.T) {
	t.Parallel()
	h := testutil.New(t)
	c, _ := h.AuthClient(t)
	id := requireUploadAttachment(t, h, c)

	resp, err := c.Get(h.Server.URL + "/v1/attachments/" + id)
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	t.Cleanup(func() { _ = resp.Body.Close() })
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("status=%d body=%s", resp.StatusCode, body)
	}
	got := mustDecode(t, resp.Body)
	if got["id"] != id {
		t.Errorf("id mismatch: got %v want %v", got["id"], id)
	}
	url, _ := got["url"].(string)
	if url == "" {
		t.Errorf("url empty")
	}
}

func TestGetAttachment_OrphanByStrangerSeesNotFound(t *testing.T) {
	t.Parallel()
	h := testutil.New(t)
	uploader, _ := h.AuthClient(t)
	stranger, _ := h.AuthClient(t)
	id := requireUploadAttachment(t, h, uploader)

	resp, err := stranger.Get(h.Server.URL + "/v1/attachments/" + id)
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	t.Cleanup(func() { _ = resp.Body.Close() })
	assertCode(t, resp, http.StatusNotFound, apierror.CodeNotFound)
}

func TestGetAttachment_NotFound(t *testing.T) {
	t.Parallel()
	h := testutil.New(t)
	c, _ := h.AuthClient(t)
	resp, err := c.Get(h.Server.URL + "/v1/attachments/" + uuid.New().String())
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	t.Cleanup(func() { _ = resp.Body.Close() })
	assertCode(t, resp, http.StatusNotFound, apierror.CodeNotFound)
}

func TestGetAttachment_BadUUID(t *testing.T) {
	t.Parallel()
	h := testutil.New(t)
	c, _ := h.AuthClient(t)
	resp, err := c.Get(h.Server.URL + "/v1/attachments/not-a-uuid")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	t.Cleanup(func() { _ = resp.Body.Close() })
	assertCode(t, resp, http.StatusBadRequest, apierror.CodeBadRequest)
}

// Linked-via-message: uploader sends the attachment in a direct, the
// other member can GET, a stranger still gets 404.
func TestGetAttachment_LinkedMemberCanRead(t *testing.T) {
	t.Parallel()
	h := testutil.New(t)
	uploaderClient, _ := h.AuthClient(t)
	memberClient, member := h.AuthClient(t)
	stranger, _ := h.AuthClient(t)

	// Upload (still orphan).
	attID := requireUploadAttachment(t, h, uploaderClient)

	// Create direct + send a message that links the attachment.
	cid := requireCreateConversation(t, h, uploaderClient, map[string]any{
		"type": "direct", "member_ids": []uuid.UUID{member.ID},
	})
	sendResp := post(t, uploaderClient, h.Server.URL+"/v1/conversations/"+cid+"/messages",
		map[string]any{"body": "see attached", "attachment_ids": []string{attID}})
	if sendResp.StatusCode != http.StatusCreated {
		body, _ := io.ReadAll(sendResp.Body)
		_ = sendResp.Body.Close()
		t.Fatalf("send status=%d body=%s", sendResp.StatusCode, body)
	}
	_ = sendResp.Body.Close()

	// Member can now GET the attachment.
	memberResp, err := memberClient.Get(h.Server.URL + "/v1/attachments/" + attID)
	if err != nil {
		t.Fatalf("member GET: %v", err)
	}
	t.Cleanup(func() { _ = memberResp.Body.Close() })
	if memberResp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(memberResp.Body)
		t.Fatalf("member status=%d body=%s", memberResp.StatusCode, body)
	}

	// Stranger still gets 404.
	strangerResp, err := stranger.Get(h.Server.URL + "/v1/attachments/" + attID)
	if err != nil {
		t.Fatalf("stranger GET: %v", err)
	}
	t.Cleanup(func() { _ = strangerResp.Body.Close() })
	assertCode(t, strangerResp, http.StatusNotFound, apierror.CodeNotFound)
}

func TestGetAttachment_Unauthenticated(t *testing.T) {
	t.Parallel()
	h := testutil.New(t)
	c := h.HTTPClient(t)
	resp, err := c.Get(h.Server.URL + "/v1/attachments/" + uuid.New().String())
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	t.Cleanup(func() { _ = resp.Body.Close() })
	assertCode(t, resp, http.StatusUnauthorized, apierror.CodeUnauthorized)
}
