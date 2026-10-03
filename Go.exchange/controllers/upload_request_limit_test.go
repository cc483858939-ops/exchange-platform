package controllers

import (
	"bytes"
	"context"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"Go.exchange/models"
	"Go.exchange/profilecoverimage"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

var uploadLimitEndpoints = []struct {
	name    string
	handler gin.HandlerFunc
	limit   int
}{
	{"post-media", UploadPostMedia, maxPostMediaImageSize},
	{"profile-avatar", UploadProfileAvatar, maxProfileAvatarImageSize},
	{"profile-cover", UploadProfileCover, profilecoverimage.MaxSourceBytes},
}

type uploadCountingBody struct {
	io.ReadCloser
	readBytes int
	onRead    func()
}

func (b *uploadCountingBody) Read(p []byte) (int, error) {
	n, err := b.ReadCloser.Read(p)
	b.readBytes += n
	if b.onRead != nil {
		b.onRead()
	}
	return n, err
}

func stubUploadLimitDependencies(t *testing.T) (*postMediaUploadLifecycleSpy, *int) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	lifecycle := stubPostMediaUploadLifecycle(t)
	originalLoader, originalPut := loadActiveProfileViewer, putStoredObject
	originalAvatarStat, originalCoverStat := statProfileAvatarObject, statProfileCoverObject
	t.Cleanup(func() {
		loadActiveProfileViewer, putStoredObject = originalLoader, originalPut
		statProfileAvatarObject, statProfileCoverObject = originalAvatarStat, originalCoverStat
	})
	loadActiveProfileViewer = func(context.Context, uint) (models.User, error) {
		return models.User{Model: gorm.Model{ID: 42}}, nil
	}
	storageCalls := new(int)
	putStoredObject = func(context.Context, string, io.Reader, int64, string) error {
		*storageCalls++
		return nil
	}
	stat := func(context.Context, string) (storedObjectInfo, bool, error) {
		*storageCalls++
		return storedObjectInfo{}, false, nil
	}
	statProfileAvatarObject, statProfileCoverObject = stat, stat
	return lifecycle, storageCalls
}

func uploadLimitBody(t *testing.T, image []byte, extraName string, extraBytes int, field bool) ([]byte, string) {
	t.Helper()
	body := new(bytes.Buffer)
	writer := multipart.NewWriter(body)
	part, err := writer.CreateFormFile("image", "image.png")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := part.Write(image); err != nil {
		t.Fatal(err)
	}
	if extraName != "" {
		if field {
			part, err = writer.CreateFormField(extraName)
		} else {
			part, err = writer.CreateFormFile(extraName, "extra.bin")
		}
		if err != nil {
			t.Fatal(err)
		}
		if _, err := part.Write(make([]byte, extraBytes)); err != nil {
			t.Fatal(err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	return body.Bytes(), writer.FormDataContentType()
}

func callUploadLimitHandler(t *testing.T, handler gin.HandlerFunc, body []byte, contentType string, knownLength bool) (*httptest.ResponseRecorder, int) {
	t.Helper()
	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest(http.MethodPost, "/api/uploads/image", bytes.NewReader(body))
	if !knownLength {
		ctx.Request.ContentLength = -1
		ctx.Request.TransferEncoding = []string{"chunked"}
	}
	counted := &uploadCountingBody{ReadCloser: ctx.Request.Body}
	ctx.Request.Body = counted
	ctx.Request.Header.Set("Content-Type", contentType)
	ctx.Set("user_id", uint(42))
	handler(ctx)
	if ctx.Request.MultipartForm != nil {
		t.Cleanup(func() { ctx.Request.MultipartForm.RemoveAll() })
	}
	return recorder, counted.readBytes
}

func TestUploadRequestRejectsOversizeBeforeStorage(t *testing.T) {
	lifecycle, storageCalls := stubUploadLimitDependencies(t)
	image := profilePNGFixture(t)
	for _, endpoint := range uploadLimitEndpoints {
		t.Run(endpoint.name, func(t *testing.T) {
			maxRequestBytes := endpoint.limit + uploadMultipartOverhead
			for _, variant := range []string{"large-image", "extra-file", "duplicate-image", "extra-field", "epilogue"} {
				t.Run(variant, func(t *testing.T) {
					var body []byte
					var contentType string
					switch variant {
					case "large-image":
						body, contentType = uploadLimitBody(t, make([]byte, maxRequestBytes+4096), "", 0, false)
					case "extra-file":
						body, contentType = uploadLimitBody(t, image, "unused", maxRequestBytes+4096, false)
					case "duplicate-image":
						body, contentType = uploadLimitBody(t, image, "image", maxRequestBytes+4096, false)
					case "extra-field":
						body, contentType = uploadLimitBody(t, image, "unused", maxRequestBytes+4096, true)
					case "epilogue":
						body, contentType = uploadLimitBody(t, image, "", 0, false)
						body = append(body, make([]byte, maxRequestBytes+4096)...)
					}
					for _, knownLength := range []bool{true, false} {
						lifecycle.reset()
						*storageCalls = 0
						response, readBytes := callUploadLimitHandler(t, endpoint.handler, body, contentType, knownLength)
						if response.Code != http.StatusRequestEntityTooLarge {
							t.Fatalf("knownLength=%v status=%d body=%s", knownLength, response.Code, response.Body.String())
						}
						wantRead := maxRequestBytes + 1
						if knownLength {
							wantRead = 0
						}
						if readBytes != wantRead {
							t.Fatalf("knownLength=%v read=%d want=%d", knownLength, readBytes, wantRead)
						}
						if *storageCalls != 0 || lifecycle.createCalls != 0 || lifecycle.finalizeCalls != 0 || lifecycle.deleteCalls != 0 {
							t.Fatalf("rejected upload touched storage=%d lifecycle=%+v", *storageCalls, lifecycle)
						}
					}
				})
			}
		})
	}
}

func TestUploadRequestBoundariesAndExistingValidation(t *testing.T) {
	lifecycle, storageCalls := stubUploadLimitDependencies(t)
	for _, endpoint := range uploadLimitEndpoints {
		t.Run(endpoint.name, func(t *testing.T) {
			// A valid PNG padded to the image limit exercises both independent
			// budgets. PNG decoding ignores bytes after the end of the image.
			image := append(profilePNGFixture(t), make([]byte, endpoint.limit-len(profilePNGFixture(t)))...)
			emptyExtra, _ := uploadLimitBody(t, image, "metadata", 0, true)
			maxRequestBytes := endpoint.limit + uploadMultipartOverhead
			for _, knownLength := range []bool{true, false} {
				for _, delta := range []int{0, 1} {
					body, contentType := uploadLimitBody(t, image, "metadata", maxRequestBytes-len(emptyExtra)+delta, true)
					if len(body) != maxRequestBytes+delta {
						t.Fatalf("test body size=%d want=%d", len(body), maxRequestBytes+delta)
					}
					lifecycle.reset()
					*storageCalls = 0
					response, _ := callUploadLimitHandler(t, endpoint.handler, body, contentType, knownLength)
					wantStatus := http.StatusOK
					if delta > 0 {
						wantStatus = http.StatusRequestEntityTooLarge
					}
					if response.Code != wantStatus {
						t.Fatalf("knownLength=%v delta=%d status=%d body=%s", knownLength, delta, response.Code, response.Body.String())
					}
					if delta > 0 && (*storageCalls != 0 || lifecycle.createCalls != 0) {
						t.Fatal("oversized boundary request reached storage")
					}
				}
				// A selected image one byte over its own limit is still a 400
				// even though the entire request fits within the multipart budget.
				body, contentType := uploadLimitBody(t, append(image, 0), "", 0, false)
				response, _ := callUploadLimitHandler(t, endpoint.handler, body, contentType, knownLength)
				if response.Code != http.StatusBadRequest {
					t.Fatalf("single-file limit status=%d body=%s", response.Code, response.Body.String())
				}
			}
			for _, body := range [][]byte{[]byte("malformed multipart"), []byte("--empty--\r\n")} {
				response, _ := callUploadLimitHandler(t, endpoint.handler, body, "multipart/form-data; boundary=empty", false)
				if response.Code != http.StatusBadRequest {
					t.Fatalf("invalid multipart status=%d body=%s", response.Code, response.Body.String())
				}
			}
		})
	}
}

func TestUploadChunkedRequestLimitCleansTemporaryFiles(t *testing.T) {
	stubUploadLimitDependencies(t)
	for _, endpoint := range uploadLimitEndpoints {
		for _, epilogue := range []bool{false, true} {
			t.Run(endpoint.name+map[bool]string{false: "/extra-file", true: "/epilogue"}[epilogue], func(t *testing.T) {
				tempDir := t.TempDir()
				t.Setenv("TMP", tempDir)
				t.Setenv("TEMP", tempDir)
				t.Setenv("TMPDIR", tempDir)
				image := append(profilePNGFixture(t), make([]byte, 128<<10)...)
				body, contentType := uploadLimitBody(t, image, "unused", endpoint.limit+uploadMultipartOverhead, false)
				if epilogue {
					body, contentType = uploadLimitBody(t, image, "", 0, false)
					body = append(body, make([]byte, endpoint.limit+uploadMultipartOverhead)...)
				}
				router := gin.New()
				router.MaxMultipartMemory = 1024
				type observation struct {
					chunked bool
					spilled bool
					read    int
				}
				observed := make(chan observation, 1)
				router.POST("/upload", func(ctx *gin.Context) {
					result := observation{chunked: ctx.Request.ContentLength == -1 && len(ctx.Request.TransferEncoding) == 1 && ctx.Request.TransferEncoding[0] == "chunked"}
					counted := &uploadCountingBody{ReadCloser: ctx.Request.Body, onRead: func() {
						entries, _ := os.ReadDir(tempDir)
						result.spilled = result.spilled || len(entries) > 0
					}}
					ctx.Request.Body = counted
					ctx.Set("user_id", uint(42))
					endpoint.handler(ctx)
					result.read = counted.readBytes
					observed <- result
				})
				server := httptest.NewServer(router)
				t.Cleanup(server.Close)
				// Hide the buffer's size from the client so it uses real HTTP/1.1
				// chunked transfer encoding, not just a synthetic request flag.
				request, err := http.NewRequest(http.MethodPost, server.URL+"/upload", io.NopCloser(bytes.NewReader(body)))
				if err != nil {
					t.Fatal(err)
				}
				request.Header.Set("Content-Type", contentType)
				response, err := server.Client().Do(request)
				if err != nil {
					t.Fatal(err)
				}
				io.Copy(io.Discard, response.Body)
				response.Body.Close()
				server.Close() // Wait for net/http's request cleanup to finish.
				result := <-observed
				if response.StatusCode != http.StatusRequestEntityTooLarge || !result.chunked || !result.spilled || result.read != endpoint.limit+uploadMultipartOverhead+1 {
					t.Fatalf("status=%d observation=%+v", response.StatusCode, result)
				}
				entries, err := os.ReadDir(tempDir)
				if err != nil || len(entries) != 0 {
					t.Fatalf("temporary files remain=%v err=%v", entries, err)
				}
			})
		}
	}
}
