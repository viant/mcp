package server

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestProtocolRequestBodyUsesBoundedHostLimit(t *testing.T) {
	for _, tc := range []struct {
		name  string
		size  int
		limit int64
		want  int
	}{{"default-rejects", (4 << 20) + 1, 0, http.StatusRequestEntityTooLarge}, {"authoring-allows", 6 << 20, 17 << 20, http.StatusOK}, {"custom-rejects", 1025, 1024, http.StatusRequestEntityTooLarge}} {
		t.Run(tc.name, func(t *testing.T) {
			body := bytes.Repeat([]byte("x"), tc.size)
			request := httptest.NewRequest(http.MethodPost, "/mcp", bytes.NewReader(body))
			response := httptest.NewRecorder()
			got, err := readProtocolRequestBody(response, request, tc.limit)
			if response.Code != tc.want {
				t.Fatalf("status=%d want=%d", response.Code, tc.want)
			}
			if tc.want == http.StatusOK {
				if err != nil || !bytes.Equal(got, body) {
					t.Fatal("allowed body changed", err)
				}
				replayed, err := io.ReadAll(request.Body)
				if err != nil || !bytes.Equal(replayed, body) {
					t.Fatal("downstream body changed", err)
				}
			} else if err == nil || got != nil {
				t.Fatal("oversized request accepted")
			}
		})
	}
}
func TestProtocolRequestBodyOptionCannotDisableBound(t *testing.T) {
	for _, limit := range []int64{-1, 0, (64 << 20) + 1} {
		if err := WithMaxRequestBodyBytes(limit)(&Server{}); err == nil {
			t.Fatal("invalid limit accepted", limit)
		}
	}
	server := &Server{}
	if err := WithMaxRequestBodyBytes(17 << 20)(server); err != nil || server.maxRequestBodyBytes != 17<<20 {
		t.Fatal("host limit not installed", err)
	}
}
